package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// JSONRPCVersion is the wire-level version every MCP message carries
// (schema/2025-11-25/schema.ts:14-16).
const JSONRPCVersion = "2.0"

// Standard JSON-RPC 2.0 error codes (schema.ts:175-180).
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603

	// MCP-reserved code returned when a peer issues a non-ping/non-init
	// method before the handshake has completed. The spec text
	// (basic/lifecycle.mdx:157-163) doesn't dictate a numeric code, but
	// the TS SDK and most servers in the wild use -32002 by convention.
	ServerNotInitialized = -32002
)

// ID is a JSON-RPC request id. Standard JSON-RPC allows numeric, string
// or null; MCP forbids null (schema.ts:124 RequestId = string | number).
// We wrap json.RawMessage and reject "null" in UnmarshalJSON.
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

func (id ID) String() string {
	return string(id.raw)
}

// Message is the JSON-RPC envelope. Request, notification, response and
// error response all share one struct (schema.ts:1-180).
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *ID             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// UnmarshalJSON enforces the MCP rule: id MUST be string or number, never
// null. encoding/json sees `"id": null` on a *ID field and sets the
// pointer to nil silently, so we intercept at the Message level.
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

// IsRequest reports whether m carries a request id AND a method.
func (m *Message) IsRequest() bool { return m.ID != nil && m.Method != "" }

// IsNotification reports whether m is a method invocation without id.
func (m *Message) IsNotification() bool { return m.ID == nil && m.Method != "" }

// IsResponse reports whether m is a response (success or error).
func (m *Message) IsResponse() bool { return m.ID != nil && m.Method == "" }

// NewResultResponse builds a success response for id with the given
// result value (serialized as JSON).
func NewResultResponse(id ID, result any) (Message, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return Message{}, fmt.Errorf("encode result: %w", err)
	}
	return Message{JSONRPC: JSONRPCVersion, ID: &id, Result: raw}, nil
}

// NewErrorResponse builds an error response for id.
func NewErrorResponse(id ID, code int, msg string) Message {
	return Message{JSONRPC: JSONRPCVersion, ID: &id, Error: &Error{Code: code, Message: msg}}
}

// Framer abstracts the transport. s01..s07 use a stdioFramer; s08 swaps
// in an HTTP implementation.
type Framer interface {
	Read() (Message, error)
	Write(m Message) error
}

// stdioFramer implements line-delimited JSON-RPC over arbitrary
// Reader/Writer pairs (basic/transports.mdx:1-51).
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
