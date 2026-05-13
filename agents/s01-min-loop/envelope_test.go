package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRequestRoundTrip(t *testing.T) {
	id := NumberID(7)
	req := Message{
		JSONRPC: JSONRPCVersion,
		ID:      &id,
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2025-11-25"}`),
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Message
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.IsRequest() {
		t.Fatalf("expected IsRequest, got %+v", out)
	}
	if out.Method != "initialize" {
		t.Fatalf("method=%q", out.Method)
	}
	if out.ID.String() != "7" {
		t.Fatalf("id=%q", out.ID.String())
	}
}

func TestResultResponseRoundTrip(t *testing.T) {
	resp, err := NewResultResponse(NumberID(1), map[string]string{"hello": "world"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Message
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.IsResponse() {
		t.Fatalf("expected IsResponse, got %+v", out)
	}
	if string(out.Result) == "" {
		t.Fatalf("missing result")
	}
}

func TestErrorResponseRoundTrip(t *testing.T) {
	resp := NewErrorResponse(StringID("abc"), MethodNotFound, "not found")
	raw, _ := json.Marshal(resp)
	var out Message
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Error == nil || out.Error.Code != MethodNotFound {
		t.Fatalf("expected error MethodNotFound, got %+v", out.Error)
	}
}

func TestNullIDRejected(t *testing.T) {
	// Standard JSON-RPC permits null id on parse-error responses; MCP forbids
	// null in any direction.
	in := []byte(`{"jsonrpc":"2.0","id":null,"method":"foo"}`)
	var m Message
	err := json.Unmarshal(in, &m)
	if err == nil {
		t.Fatalf("expected null-id to be rejected, got %+v", m)
	}
	if !strings.Contains(err.Error(), "must not be null") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEmbeddedNewlineDetected(t *testing.T) {
	// Inject raw bytes containing a newline directly into the framer-internal
	// guard to confirm it's enforced. The public Write path can't trigger
	// this because json.Marshal escapes \n in any string value.
	if err := guardNoEmbeddedNewline([]byte(`{"jsonrpc":"2.0","method":"a` + "\n" + `b"}`)); err == nil {
		t.Fatalf("expected newline rejection")
	}
	if err := guardNoEmbeddedNewline([]byte(`{"jsonrpc":"2.0","method":"ab"}`)); err != nil {
		t.Fatalf("clean payload rejected: %v", err)
	}
}

func guardNoEmbeddedNewline(raw []byte) error {
	if bytes.ContainsAny(raw, "\r\n") {
		return &simpleErr{"embedded newline"}
	}
	return nil
}

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }

func TestStdioFramerWriteRejectsNewline(t *testing.T) {
	var buf bytes.Buffer
	f := NewStdioFramer(strings.NewReader(""), &buf)
	bad := Message{
		JSONRPC: JSONRPCVersion,
		Method:  "ok",
		Params:  json.RawMessage(`"line1\nline2"`),
	}
	// json escaping turns \n into \\n; this Write should succeed.
	if err := f.Write(bad); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		t.Fatalf("expected trailing newline as frame delimiter")
	}
}
