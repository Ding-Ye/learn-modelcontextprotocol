package main

// Envelope re-declared, seventh time. By now the shape is muscle memory.
// The interesting thing in s08 is that the *transport* changes (HTTP, not
// stdio) but the envelope does not — that's the whole point of "MCP is
// JSON-RPC + a transport". See plan.md "Shared types catalog" for the
// rationale on not factoring this into a shared module.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// JSONRPCVersion is the only valid value for the `jsonrpc` field per
// schema/2025-11-25/schema.ts:14.
const JSONRPCVersion = "2.0"

// Standard JSON-RPC 2.0 error codes (schema.ts:175-180). s08 adds an
// HTTP layer on top, but JSON-RPC error codes still travel inside the
// body — HTTP status codes are a separate, complementary signal.
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603
)

// ID wraps a raw request id. JSON-RPC permits string or number; MCP
// additionally forbids null (schema.ts:124).
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

// Message is the JSON-RPC envelope (request / notification / response /
// error response). Unchanged from s01.
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
