package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// helper: build a ready-to-serve router with the two demo tools.
func newTestRouter(t *testing.T) *Router {
	t.Helper()
	lc := NewLifecycle()
	reg := NewToolRegistry()
	if err := RegisterDemoTools(reg); err != nil {
		t.Fatalf("register demo tools: %v", err)
	}
	r := NewRouter(lc, reg)
	// Advance to Ready so tests can call tools/list and tools/call directly.
	lc.HandleInitialize(InitializeRequestParams{ProtocolVersion: LatestProtocolVersion})
	lc.HandleInitialized()
	return r
}

// requestMsg helps build a Message{request} with numeric id n.
func requestMsg(n int64, method string, params any) Message {
	id := NumberID(n)
	var raw json.RawMessage
	if params != nil {
		raw, _ = json.Marshal(params)
	}
	return Message{JSONRPC: JSONRPCVersion, ID: &id, Method: method, Params: raw}
}

func TestListToolsReturnsDemos(t *testing.T) {
	r := newTestRouter(t)
	reply, err := r.Dispatch(requestMsg(1, "tools/list", map[string]any{}))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if reply.Error != nil {
		t.Fatalf("unexpected rpc error: %+v", reply.Error)
	}
	var res ListToolsResult
	if err := json.Unmarshal(reply.Result, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(res.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(res.Tools))
	}
	got := map[string]bool{}
	for _, tl := range res.Tools {
		got[tl.Name] = true
	}
	if !got["echo"] || !got["get_time"] {
		t.Fatalf("missing demo tools: %+v", got)
	}
}

func TestCallEchoReturnsTextBlock(t *testing.T) {
	r := newTestRouter(t)
	reply, err := r.Dispatch(requestMsg(2, "tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"text": "hi"},
	}))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if reply.Error != nil {
		t.Fatalf("unexpected rpc error: %+v", reply.Error)
	}
	var res CallToolResult
	if err := json.Unmarshal(reply.Result, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got IsError=true: %+v", res)
	}
	if len(res.Content) != 1 || res.Content[0].Type != "text" || res.Content[0].Text != "hi" {
		t.Fatalf("unexpected content: %+v", res.Content)
	}
}

// Per spec: missing required arg → JSON-RPC -32602, NOT IsError:true.
// Schema validation is "protocol-level" because the tool never ran.
func TestCallEchoMissingTextReturnsInvalidParams(t *testing.T) {
	r := newTestRouter(t)
	reply, err := r.Dispatch(requestMsg(3, "tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{},
	}))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if reply.Error == nil {
		t.Fatalf("expected rpc error, got result: %s", string(reply.Result))
	}
	if reply.Error.Code != InvalidParams {
		t.Fatalf("expected code %d, got %d (%s)", InvalidParams, reply.Error.Code, reply.Error.Message)
	}
}

// Per spec: unknown tool → JSON-RPC -32601 MethodNotFound.
func TestCallUnknownToolReturnsMethodNotFound(t *testing.T) {
	r := newTestRouter(t)
	reply, err := r.Dispatch(requestMsg(4, "tools/call", map[string]any{
		"name":      "alert",
		"arguments": map[string]any{"x": 1},
	}))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if reply.Error == nil || reply.Error.Code != MethodNotFound {
		t.Fatalf("expected MethodNotFound, got %+v / result=%s", reply.Error, string(reply.Result))
	}
}

// Per spec: tool execution failure (panic or returned Go error) becomes
// CallToolResult{IsError: true}, NOT JSON-RPC -32603 InternalError.
// This is what lets the model see and correct its mistake.
func TestPanicingToolReturnsIsErrorNotRpcError(t *testing.T) {
	lc := NewLifecycle()
	reg := NewToolRegistry()
	bombSchema := json.RawMessage(`{"type":"object"}`)
	err := reg.Register(Tool{Name: "bomb", InputSchema: bombSchema},
		HandlerFunc(func(args json.RawMessage) (CallToolResult, error) {
			panic("kaboom")
		}))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	r := NewRouter(lc, reg)
	lc.HandleInitialize(InitializeRequestParams{ProtocolVersion: LatestProtocolVersion})
	lc.HandleInitialized()

	reply, dispatchErr := r.Dispatch(requestMsg(5, "tools/call", map[string]any{
		"name":      "bomb",
		"arguments": map[string]any{},
	}))
	if dispatchErr != nil {
		t.Fatalf("dispatch: %v", dispatchErr)
	}
	if reply.Error != nil {
		t.Fatalf("expected no rpc error (tool failure must NOT escape to JSON-RPC), got %+v", reply.Error)
	}
	var res CallToolResult
	if err := json.Unmarshal(reply.Result, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true, got %+v", res)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "kaboom") {
		t.Fatalf("expected error text to mention panic reason, got %+v", res.Content)
	}
}

// Bonus: server in StateUninit must reject tools/list with -32002.
func TestCallBeforeInitializedReturnsNotInitialized(t *testing.T) {
	lc := NewLifecycle()
	reg := NewToolRegistry()
	_ = RegisterDemoTools(reg)
	r := NewRouter(lc, reg)
	// Skip the handshake — lifecycle is still Uninit.

	reply, _ := r.Dispatch(requestMsg(6, "tools/list", map[string]any{}))
	if reply.Error == nil || reply.Error.Code != ServerNotInitialized {
		t.Fatalf("expected %d ServerNotInitialized, got %+v", ServerNotInitialized, reply.Error)
	}
}
