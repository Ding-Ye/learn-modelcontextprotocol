package main

// The envelope code below is **re-declared verbatim** in every chapter from
// s01 onward. The repetition is the lesson — by the time a learner reaches
// s05 they have written `Message` and `ID` four times, and the wire shape
// stops feeling magical. See plan.md "Shared types catalog" for the
// canonical superset and the rationale for not sharing this code.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// JSONRPCVersion is the only valid value for the `jsonrpc` field per
// schema/2025-11-25/schema.ts:14.
const JSONRPCVersion = "2.0"

// Standard JSON-RPC 2.0 error codes (schema.ts:175-180). InvalidParams is
// the workhorse in this chapter: every "missing required argument" or
// "unknown ref.type" path returns -32602.
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603
)

// ID wraps a raw request id. JSON-RPC permits string or number; MCP
// additionally forbids null (schema.ts:124, RequestId = string | number).
type ID struct {
	raw json.RawMessage
}

func StringID(s string) ID {
	b, _ := json.Marshal(s)
	return ID{raw: b}
}

func NumberID(n int64) ID {
	b, _ := json.Marshal(n)
	return ID{raw: b}
}

func (id ID) MarshalJSON() ([]byte, error) {
	if len(id.raw) == 0 {
		return []byte("null"), nil
	}
	return id.raw, nil
}

func (id *ID) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		return errors.New("mcp: request id must not be null")
	}
	id.raw = append(id.raw[:0], trimmed...)
	return nil
}

func (id ID) String() string { return string(id.raw) }

// Message is the single envelope shape (request / notification / response /
// error response). See s01 for the long-form commentary.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *ID             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

func (m *Message) UnmarshalJSON(data []byte) error {
	type rawMessage Message
	var probe struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(data, &probe); err == nil {
		if len(probe.ID) > 0 && bytes.Equal(bytes.TrimSpace(probe.ID), []byte("null")) {
			return errors.New("mcp: request id must not be null")
		}
	}
	return json.Unmarshal(data, (*rawMessage)(m))
}

// Error is the JSON-RPC error object (schema.ts:140-156).
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("mcp: rpc error %d: %s", e.Code, e.Message)
}

func (m *Message) IsRequest() bool      { return m.ID != nil && m.Method != "" }
func (m *Message) IsNotification() bool { return m.ID == nil && m.Method != "" }
func (m *Message) IsResponse() bool     { return m.ID != nil && m.Method == "" }

func NewResultResponse(id ID, result any) (Message, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return Message{}, fmt.Errorf("encode result: %w", err)
	}
	return Message{JSONRPC: JSONRPCVersion, ID: &id, Result: raw}, nil
}

func NewErrorResponse(id ID, code int, msg string) Message {
	return Message{JSONRPC: JSONRPCVersion, ID: &id, Error: &Error{Code: code, Message: msg}}
}

// Framer abstracts the transport. s05 uses stdio; s08 swaps in HTTP behind
// this same interface.
type Framer interface {
	Read() (Message, error)
	Write(m Message) error
}

type stdioFramer struct {
	in  *bufio.Reader
	out io.Writer
}

func NewStdioFramer(r io.Reader, w io.Writer) *stdioFramer {
	return &stdioFramer{in: bufio.NewReader(r), out: w}
}

func (f *stdioFramer) Read() (Message, error) {
	line, err := f.in.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return Message{}, err
	}
	line = bytes.TrimRight(line, "\r\n")
	if len(line) == 0 {
		return Message{}, errors.New("mcp: empty frame")
	}
	var m Message
	if err := json.Unmarshal(line, &m); err != nil {
		return Message{}, fmt.Errorf("decode frame: %w", err)
	}
	if m.JSONRPC != JSONRPCVersion {
		return Message{}, fmt.Errorf("mcp: bad jsonrpc version %q", m.JSONRPC)
	}
	return m, nil
}

func (f *stdioFramer) Write(m Message) error {
	if m.JSONRPC == "" {
		m.JSONRPC = JSONRPCVersion
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}
	if bytes.ContainsAny(raw, "\r\n") {
		return errors.New("mcp: serialized frame contains forbidden newline")
	}
	raw = append(raw, '\n')
	_, err = f.out.Write(raw)
	return err
}
