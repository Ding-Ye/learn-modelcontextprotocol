package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// ----------------------------------------------------------------------------
// Test scaffolding
// ----------------------------------------------------------------------------

// mockFramer captures outbound writes in a slice and serves inbound reads
// from a channel. The outbound table needs *some* Framer to call Write on
// even when we route to the in-process StubSampler — we never wait on it
// in those tests, but the s06 Router constructor takes the OutboundRouter.
type mockFramer struct {
	writes chan Message
	reads  chan Message
}

func newMockFramer() *mockFramer {
	return &mockFramer{
		writes: make(chan Message, 16),
		reads:  make(chan Message, 16),
	}
}
func (m *mockFramer) Read() (Message, error)   { return <-m.reads, nil }
func (m *mockFramer) Write(msg Message) error  { m.writes <- msg; return nil }

// newReadyRouter wires Lifecycle → OutboundRouter → Router with the given
// sampler, then runs the init handshake so the lifecycle gate is open.
func newReadyRouter(t *testing.T, sampler Sampler) (*Router, *mockFramer) {
	t.Helper()
	lc := NewLifecycle()
	mf := newMockFramer()
	out := NewOutboundRouter(mf)
	rt := NewRouter(lc, out, sampler)

	ctx := context.Background()
	initID := NumberID(1)
	rt.DispatchInbound(ctx, Message{
		JSONRPC: JSONRPCVersion,
		ID:      &initID,
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{"sampling":{}},"clientInfo":{"name":"t","version":"0"}}`),
	})
	rt.DispatchInbound(ctx, Message{
		JSONRPC: JSONRPCVersion,
		Method:  "notifications/initialized",
	})
	if !lc.Ready() {
		t.Fatalf("lifecycle not ready after init")
	}
	if !lc.ClientSupportsSampling() {
		t.Fatalf("client sampling capability not recorded")
	}
	return rt, mf
}

func callTool(t *testing.T, rt *Router, name string, args map[string]any) CallToolResult {
	t.Helper()
	params, err := json.Marshal(CallToolRequestParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	id := NumberID(99)
	reply, ok := rt.DispatchInbound(context.Background(), Message{
		JSONRPC: JSONRPCVersion,
		ID:      &id,
		Method:  "tools/call",
		Params:  params,
	})
	if !ok {
		t.Fatalf("no reply for tools/call %s", name)
	}
	if reply.Error != nil {
		t.Fatalf("tools/call %s: rpc error %d %s", name, reply.Error.Code, reply.Error.Message)
	}
	var res CallToolResult
	if err := json.Unmarshal(reply.Result, &res); err != nil {
		t.Fatalf("decode CallToolResult: %v", err)
	}
	return res
}

// ----------------------------------------------------------------------------
// Test 1: stub Sampler replies with a single text block; the demo tool
// returns that text as the tool result.
// ----------------------------------------------------------------------------

func TestSamplingTextReply(t *testing.T) {
	stub := &StubSampler{Model: "m1", TextReply: "summary-of-hello"}
	rt, _ := newReadyRouter(t, stub)

	res := callTool(t, rt, "summarize", map[string]any{"text": "hello"})
	if res.IsError {
		t.Fatalf("unexpected isError=true: %+v", res)
	}
	if len(res.Content) != 1 || res.Content[0].Type != "text" {
		t.Fatalf("want single text block, got %+v", res.Content)
	}
	if res.Content[0].Text != "summary-of-hello" {
		t.Fatalf("want summary-of-hello, got %q", res.Content[0].Text)
	}
	if stub.CallCount != 1 {
		t.Fatalf("want 1 sampler call, got %d", stub.CallCount)
	}
	// And the request the server emitted should mention the input text.
	gotMsg := stub.LastRequest.Messages
	if len(gotMsg) != 1 || gotMsg[0].Role != "user" {
		t.Fatalf("want one user-role message, got %+v", gotMsg)
	}
	if !strings.Contains(gotMsg[0].Content[0].Text, "hello") {
		t.Fatalf("user message should mention input text, got %q", gotMsg[0].Content[0].Text)
	}
}

// ----------------------------------------------------------------------------
// Test 2: agentic two-turn loop. Stub returns tool_use on turn 1; server
// runs the named tool in-process; turn 2 returns plain text.
// ----------------------------------------------------------------------------

func TestSamplingAgenticToolLoop(t *testing.T) {
	stub := &StubSampler{
		Model:     "m1",
		TextReply: "final-summary-after-tool-result",
	}
	stub.EnqueueToolUse("call_1", "echo", map[string]any{"text": "hello"})

	rt, _ := newReadyRouter(t, stub)
	res := callTool(t, rt, "agentic-summarize", map[string]any{"text": "hello"})
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res)
	}
	if stub.CallCount != 2 {
		t.Fatalf("want 2 sampler calls (turn1+turn2), got %d", stub.CallCount)
	}
	// Final tool result should reflect the second sampling reply.
	if len(res.Content) != 1 || res.Content[0].Type != "text" {
		t.Fatalf("want one text block as final, got %+v", res.Content)
	}
	if res.Content[0].Text != "final-summary-after-tool-result" {
		t.Fatalf("want final-summary-after-tool-result, got %q", res.Content[0].Text)
	}
	// Turn 2's messages must include a tool_result block referencing the
	// call_1 toolUseId from the stub.
	turn2Msgs := stub.LastRequest.Messages
	found := false
	for _, m := range turn2Msgs {
		for _, c := range m.Content {
			if c.Type == "tool_result" && c.ToolUseID == "call_1" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("turn-2 messages missing tool_result for call_1: %+v", turn2Msgs)
	}
}

// ----------------------------------------------------------------------------
// Test 3: maxTokens: 0 is rejected with -32602 InvalidParams. We call
// validateCreateMessage directly — it's the same path the outbound code
// takes, and exposing the error code matters here more than the route.
// ----------------------------------------------------------------------------

func TestSamplingMaxTokensZeroRejected(t *testing.T) {
	req := CreateMessageRequestParams{
		Messages:  []SamplingMessage{{Role: "user", Content: []ContentBlock{TextBlock("x")}}},
		MaxTokens: 0,
	}
	if err := validateCreateMessage(req); err == nil {
		t.Fatalf("expected validation error for maxTokens=0")
	} else if err.Code != InvalidParams {
		t.Fatalf("want code %d, got %d (%s)", InvalidParams, err.Code, err.Message)
	} else if !strings.Contains(err.Message, "maxTokens") {
		t.Fatalf("error message should mention maxTokens: %q", err.Message)
	}

	// And via the router: a tool call that ends up emitting a bad request
	// should still respond to the *outer* JSON-RPC call without crashing.
	// We construct a bespoke router whose Sampler is nil and the outbound
	// path is exercised — but the validation gate fires first.
	lc := NewLifecycle()
	mf := newMockFramer()
	out := NewOutboundRouter(mf)
	rt := NewRouter(lc, out, nil)
	initID := NumberID(1)
	rt.DispatchInbound(context.Background(), Message{
		JSONRPC: JSONRPCVersion, ID: &initID, Method: "initialize",
		Params: json.RawMessage(`{"capabilities":{"sampling":{}}}`),
	})
	rt.DispatchInbound(context.Background(), Message{JSONRPC: JSONRPCVersion, Method: "notifications/initialized"})

	// requestSampling directly — bypass the demo tools to exercise the
	// validation surface end-to-end.
	_, err := rt.requestSampling(context.Background(), req)
	if err == nil {
		t.Fatalf("expected requestSampling to fail")
	}
	var rpcErr *Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("want *Error, got %T (%v)", err, err)
	}
	if rpcErr.Code != InvalidParams {
		t.Fatalf("want code %d, got %d", InvalidParams, rpcErr.Code)
	}
}

// ----------------------------------------------------------------------------
// Test 4: outbound request times out; pending entry is cleaned up so the
// goroutine doesn't leak.
// ----------------------------------------------------------------------------

func TestOutboundTimeoutCleansPending(t *testing.T) {
	mf := newMockFramer()
	out := NewOutboundRouter(mf)
	out.SetTimeout(20 * time.Millisecond)

	start := time.Now()
	_, err := out.SendRequest(context.Background(), "sampling/createMessage", CreateMessageRequestParams{
		Messages:  []SamplingMessage{{Role: "user", Content: []ContentBlock{TextBlock("x")}}},
		MaxTokens: 1,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("timeout took too long: %s", elapsed)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want a timeout error, got %v", err)
	}
	if pc := out.PendingCount(); pc != 0 {
		t.Fatalf("pending table leak: %d entries left", pc)
	}
	// The framer should have received exactly one outbound write.
	select {
	case <-mf.writes:
	default:
		t.Fatalf("expected one outbound write")
	}
}

// ----------------------------------------------------------------------------
// Test 5: includeContext: "thisServer" is round-tripped to the Sampler.
// ----------------------------------------------------------------------------

func TestSamplingIncludeContextRoundTrip(t *testing.T) {
	stub := &StubSampler{TextReply: "ok"}
	rt, _ := newReadyRouter(t, stub)
	_ = callTool(t, rt, "summarize", map[string]any{"text": "hello"})
	if got := stub.LastRequest.IncludeContext; got != "thisServer" {
		t.Fatalf("want includeContext=thisServer, got %q", got)
	}
}
