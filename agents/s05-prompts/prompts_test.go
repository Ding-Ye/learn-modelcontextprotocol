package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// helper: build a router with the demo prompt and walk it through
// initialize + notifications/initialized so subsequent calls clear the
// lifecycle gate.
func newReadyRouter(t *testing.T) *Router {
	t.Helper()
	lc := NewLifecycle()
	reg := NewPromptRegistry()
	RegisterDemoPrompt(reg)
	rt := NewRouter(lc, reg)

	initID := NumberID(1)
	rt.Dispatch(Message{
		JSONRPC: JSONRPCVersion,
		ID:      &initID,
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}`),
	})
	rt.Dispatch(Message{
		JSONRPC: JSONRPCVersion,
		Method:  "notifications/initialized",
	})
	if !lc.Ready() {
		t.Fatalf("lifecycle did not reach ready")
	}
	return rt
}

func dispatchAndDecode(t *testing.T, rt *Router, method string, params string, into any) Message {
	t.Helper()
	id := NumberID(42)
	reply, ok := rt.Dispatch(Message{
		JSONRPC: JSONRPCVersion,
		ID:      &id,
		Method:  method,
		Params:  json.RawMessage(params),
	})
	if !ok {
		t.Fatalf("dispatch returned no reply for %s", method)
	}
	if into != nil && reply.Error == nil {
		if err := json.Unmarshal(reply.Result, into); err != nil {
			t.Fatalf("decode %s result: %v", method, err)
		}
	}
	return reply
}

// Test 1: prompts/list returns the demo prompt.
func TestListPromptsReturnsDemo(t *testing.T) {
	rt := newReadyRouter(t)
	var res ListPromptsResult
	reply := dispatchAndDecode(t, rt, "prompts/list", `{}`, &res)
	if reply.Error != nil {
		t.Fatalf("unexpected error: %+v", reply.Error)
	}
	if len(res.Prompts) != 1 {
		t.Fatalf("want 1 prompt, got %d (%+v)", len(res.Prompts), res.Prompts)
	}
	if res.Prompts[0].Name != "summarize-file" {
		t.Fatalf("want name summarize-file, got %q", res.Prompts[0].Name)
	}
	// At least one required argument must be declared.
	foundRequired := false
	for _, a := range res.Prompts[0].Arguments {
		if a.Required {
			foundRequired = true
		}
	}
	if !foundRequired {
		t.Fatalf("expected at least one required argument in %+v", res.Prompts[0].Arguments)
	}
}

// Test 2: prompts/get with all args substitutes correctly.
func TestGetPromptSubstitutes(t *testing.T) {
	rt := newReadyRouter(t)
	var res GetPromptResult
	params := `{"name":"summarize-file","arguments":{"path":"go.mod","style":"bullets"}}`
	reply := dispatchAndDecode(t, rt, "prompts/get", params, &res)
	if reply.Error != nil {
		t.Fatalf("unexpected error: %+v", reply.Error)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("want 1 message, got %d", len(res.Messages))
	}
	m := res.Messages[0]
	if m.Role != "user" {
		t.Fatalf("want role user, got %q", m.Role)
	}
	if m.Content.Type != "text" {
		t.Fatalf("want content.type=text, got %q", m.Content.Type)
	}
	if !strings.Contains(m.Content.Text, "go.mod") {
		t.Fatalf("substitution missed `{{path}}`: %q", m.Content.Text)
	}
	if !strings.Contains(m.Content.Text, "bullets") {
		t.Fatalf("substitution missed `{{style}}`: %q", m.Content.Text)
	}
	if strings.Contains(m.Content.Text, "{{") {
		t.Fatalf("unsubstituted placeholder left in output: %q", m.Content.Text)
	}
}

// Test 3: prompts/get with missing required arg returns -32602.
func TestGetPromptMissingRequiredArg(t *testing.T) {
	rt := newReadyRouter(t)
	reply := dispatchAndDecode(t, rt, "prompts/get", `{"name":"summarize-file","arguments":{}}`, nil)
	if reply.Error == nil {
		t.Fatalf("expected error, got success: %s", string(reply.Result))
	}
	if reply.Error.Code != InvalidParams {
		t.Fatalf("want code %d, got %d (%s)", InvalidParams, reply.Error.Code, reply.Error.Message)
	}
	if !strings.Contains(reply.Error.Message, "path") {
		t.Fatalf("error message should mention `path`: %q", reply.Error.Message)
	}
}

// Test 4: completion/complete for `path` returns candidates.
func TestCompletePathReturnsCandidates(t *testing.T) {
	rt := newReadyRouter(t)
	var res CompleteResult
	params := `{"ref":{"type":"ref/prompt","name":"summarize-file"},"argument":{"name":"path","value":"R"}}`
	reply := dispatchAndDecode(t, rt, "completion/complete", params, &res)
	if reply.Error != nil {
		t.Fatalf("unexpected error: %+v", reply.Error)
	}
	if len(res.Completion.Values) == 0 {
		t.Fatalf("want at least one candidate, got 0")
	}
	for _, v := range res.Completion.Values {
		if !strings.HasPrefix(strings.ToLower(v), "r") {
			t.Fatalf("candidate %q does not match prefix %q", v, "R")
		}
	}
	if res.Completion.Total == nil || *res.Completion.Total != len(res.Completion.Values) {
		t.Fatalf("total mismatch: total=%v len=%d", res.Completion.Total, len(res.Completion.Values))
	}
}

// Test 5: completion/complete with unknown ref.type returns -32602.
func TestCompleteUnknownRefType(t *testing.T) {
	rt := newReadyRouter(t)
	params := `{"ref":{"type":"ref/banana","name":"summarize-file"},"argument":{"name":"path","value":""}}`
	reply := dispatchAndDecode(t, rt, "completion/complete", params, nil)
	if reply.Error == nil {
		t.Fatalf("expected error, got success")
	}
	if reply.Error.Code != InvalidParams {
		t.Fatalf("want code %d, got %d (%s)", InvalidParams, reply.Error.Code, reply.Error.Message)
	}
	if !strings.Contains(reply.Error.Message, "ref/banana") {
		t.Fatalf("error should echo the bad ref.type: %q", reply.Error.Message)
	}
}
