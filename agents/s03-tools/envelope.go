package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// JSON-RPC version literal pinned by the MCP spec.
// Source: schema/2025-11-25/schema.ts:14.
const JSONRPCVersion = "2.0"

// JSON-RPC error codes. Standard codes (-32700..-32603) come from
// schema.ts:175-180; the MCP-reserved band [-32000, -32099] holds
// protocol-specific codes — schema.ts:181-201 defines
// `SERVER_NOT_INITIALIZED = -32002`. We also re-declare
// `InvalidParams = -32602` because s03 uses it for schema-failing
// tool arguments.
const (
	ParseError            = -32700
	InvalidRequest        = -32600
	MethodNotFound        = -32601
	InvalidParams         = -32602
	InternalError         = -32603
	ServerNotInitialized  = -32002 // schema.ts:185-188
)

// ID is the JSON-RPC request id. Standard JSON-RPC permits null;
// MCP forbids it (schema.ts:124, `RequestId = string | number`).
// We wrap a raw JSON token and reject `null` at decode time.
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

// Message is the single wire-envelope used for requests, notifications,
// success responses, and error responses. The four shapes are distinguished
// by which optional fields are populated.
//
// Source: schema.ts:1-180.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *ID             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// UnmarshalJSON intercepts `"id": null` before the standard decoder
// silently sets *ID to nil. See s01 docs for the rationale.
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

func (e *Error) Error() string { return fmt.Sprintf("mcp: rpc error %d: %s", e.Code, e.Message) }

// IsRequest reports whether m is a JSON-RPC request (id + method).
func (m *Message) IsRequest() bool { return m.ID != nil && m.Method != "" }

// IsNotification reports whether m is a JSON-RPC notification (method, no id).
func (m *Message) IsNotification() bool { return m.ID == nil && m.Method != "" }

// IsResponse reports whether m is a response (id, no method).
func (m *Message) IsResponse() bool { return m.ID != nil && m.Method == "" }

// NewResultResponse builds a success response. Caller passes the result
// value already shaped as a Go struct; we serialize it here.
func NewResultResponse(id ID, result any) (Message, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return Message{}, fmt.Errorf("encode result: %w", err)
	}
	return Message{JSONRPC: JSONRPCVersion, ID: &id, Result: raw}, nil
}

// NewErrorResponse builds an error response.
func NewErrorResponse(id ID, code int, msg string) Message {
	return Message{JSONRPC: JSONRPCVersion, ID: &id, Error: &Error{Code: code, Message: msg}}
}

// Framer abstracts the transport. s01..s07 use stdioFramer; s08 swaps
// in an HTTP/SSE implementation.
type Framer interface {
	Read() (Message, error)
	Write(m Message) error
}

// stdioFramer is line-delimited JSON-RPC over arbitrary Reader/Writer
// pairs. Per basic/transports.mdx:1-51 frames MUST NOT contain embedded
// newlines and MUST be UTF-8.
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
