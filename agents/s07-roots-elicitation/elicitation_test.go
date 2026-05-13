package main

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// pipeFramer is a tiny in-process Framer that lets the test drive the
// "client" side of the conversation. Server writes go into `out`; the
// test reads them and answers by writing into `in`. Closing `in` makes
// Read return io.EOF.
type pipeFramer struct {
	in     chan Message
	out    chan Message
	closed bool
	mu     sync.Mutex
}

func newPipeFramer() *pipeFramer {
	return &pipeFramer{in: make(chan Message, 16), out: make(chan Message, 16)}
}

func (p *pipeFramer) Read() (Message, error) {
	m, ok := <-p.in
	if !ok {
		return Message{}, errors.New("pipe closed")
	}
	return m, nil
}

func (p *pipeFramer) Write(m Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("pipe closed")
	}
	p.out <- m
	return nil
}

func (p *pipeFramer) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		close(p.in)
		close(p.out)
	}
}

// readReply pulls the next message from the server-side out channel
// with a generous timeout so a hung test doesn't deadlock the suite.
func (p *pipeFramer) readReply(t *testing.T) Message {
	t.Helper()
	select {
	case m := <-p.out:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server reply")
		return Message{}
	}
}

// initReady walks a fresh router through `initialize` + `notifications/initialized`
// so post-init methods clear the lifecycle gate.
func initReady(t *testing.T, rt *Router) {
	t.Helper()
	initID := NumberID(1)
	rt.Dispatch(Message{
		JSONRPC: JSONRPCVersion,
		ID:      &initID,
		Method:  "initialize",
		Params: json.RawMessage(`{
			"protocolVersion":"2025-11-25",
			"capabilities":{
				"roots":{"listChanged":true},
				"elicitation":{"form":{},"url":{}}
			},
			"clientInfo":{"name":"test","version":"0"}
		}`),
	})
	rt.Dispatch(Message{JSONRPC: JSONRPCVersion, Method: "notifications/initialized"})
	if !rt.lifecycle.Ready() {
		t.Fatal("lifecycle did not reach ready")
	}
}

// Test 1: roots/list round-trips two file:// URIs through the client.
// We model the "client" with `handleListRoots` + an OutboundRouter
// driven by a pipeFramer. The server fires roots/list; the test
// reads the outbound request from the pipe and writes back the result.
func TestRootsListRoundTrip(t *testing.T) {
	p := newPipeFramer()
	defer p.close()
	lc := NewLifecycle()
	ob := NewOutboundRouter(p, 1*time.Second)

	// Pretend we already completed init with roots support.
	lc.rootsSupported = true
	lc.state = stateReady

	want := []Root{
		{URI: "file:///home/user/frontend", Name: "Frontend"},
		{URI: "file:///home/user/backend", Name: "Backend"},
	}
	clientResult, e := handleListRoots(want)
	if e != nil {
		t.Fatalf("client handler failed: %+v", e)
	}

	// Server goroutine: fire the outbound request.
	type result struct {
		res ListRootsResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		raw, err := ob.SendRequest("roots/list", ListRootsRequest{})
		if err != nil {
			done <- result{err: err}
			return
		}
		got, err := decodeListRootsResult(raw)
		done <- result{res: got, err: err}
	}()

	// Read the outbound request the server just wrote, then deliver the
	// canned client reply directly via Dispatch (which is what the
	// real inbound loop would do).
	out := p.readReply(t)
	if out.Method != "roots/list" {
		t.Fatalf("want method roots/list, got %q", out.Method)
	}
	if out.ID == nil {
		t.Fatal("outbound request missing id")
	}
	raw, _ := json.Marshal(clientResult)
	ob.Dispatch(Message{JSONRPC: JSONRPCVersion, ID: out.ID, Result: raw})

	r := <-done
	if r.err != nil {
		t.Fatalf("server send failed: %v", r.err)
	}
	if len(r.res.Roots) != 2 {
		t.Fatalf("want 2 roots, got %d", len(r.res.Roots))
	}
	if r.res.Roots[0].URI != want[0].URI || r.res.Roots[1].URI != want[1].URI {
		t.Fatalf("roots mismatch: %+v vs %+v", r.res.Roots, want)
	}
	if ob.pendingCount() != 0 {
		t.Fatalf("pending table not cleaned: %d", ob.pendingCount())
	}
}

// Test 2: form elicitation with action=accept returns content keys.
func TestFormElicitationAcceptReturnsContent(t *testing.T) {
	p := newPipeFramer()
	defer p.close()
	lc := NewLifecycle()
	lc.elicitFormSupported = true
	lc.state = stateReady
	ob := NewOutboundRouter(p, 1*time.Second)

	schema := NewRequestedSchema(map[string]PrimitiveSchema{
		"name": {"type": "string"},
	}, "name")

	type result struct {
		res ElicitResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := sendFormElicitation(ob, lc, "Who are you?", schema)
		done <- result{res: res, err: err}
	}()

	out := p.readReply(t)
	if out.Method != ElicitMethod {
		t.Fatalf("want method %s, got %q", ElicitMethod, out.Method)
	}
	// Confirm the params look like a form-mode request.
	var got ElicitRequestFormParams
	if err := json.Unmarshal(out.Params, &got); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if got.Mode != "form" {
		t.Fatalf("want mode=form, got %q", got.Mode)
	}
	if got.RequestedSchema.Type != "object" {
		t.Fatalf("want schema.type=object, got %q", got.RequestedSchema.Type)
	}

	// Client replies with action=accept + content.
	reply, _ := json.Marshal(ElicitResult{
		Action:  "accept",
		Content: map[string]any{"name": "Ada"},
	})
	ob.Dispatch(Message{JSONRPC: JSONRPCVersion, ID: out.ID, Result: reply})

	r := <-done
	if r.err != nil {
		t.Fatalf("send failed: %v", r.err)
	}
	if r.res.Action != "accept" {
		t.Fatalf("want action=accept, got %q", r.res.Action)
	}
	if name, _ := r.res.Content["name"].(string); name != "Ada" {
		t.Fatalf("want content.name=Ada, got %q (%+v)", name, r.res.Content)
	}
}

// Test 3: form elicitation with action=cancel returns no content (we
// scrub it even if the client erroneously sends some).
func TestFormElicitationCancelDropsContent(t *testing.T) {
	p := newPipeFramer()
	defer p.close()
	lc := NewLifecycle()
	lc.elicitFormSupported = true
	lc.state = stateReady
	ob := NewOutboundRouter(p, 1*time.Second)

	schema := NewRequestedSchema(map[string]PrimitiveSchema{
		"name": {"type": "string"},
	}, "name")

	type result struct {
		res ElicitResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := sendFormElicitation(ob, lc, "Who are you?", schema)
		done <- result{res: res, err: err}
	}()

	out := p.readReply(t)
	// Client sends action=cancel but accidentally also leaks content;
	// the decoder MUST drop it.
	reply, _ := json.Marshal(ElicitResult{
		Action:  "cancel",
		Content: map[string]any{"name": "should-be-stripped"},
	})
	ob.Dispatch(Message{JSONRPC: JSONRPCVersion, ID: out.ID, Result: reply})

	r := <-done
	if r.err != nil {
		t.Fatalf("send failed: %v", r.err)
	}
	if r.res.Action != "cancel" {
		t.Fatalf("want action=cancel, got %q", r.res.Action)
	}
	if r.res.Content != nil {
		t.Fatalf("want nil content on cancel, got %+v", r.res.Content)
	}
}

// Test 4: URL elicitation builds the right params shape (no schema, with
// elicitationId + url). We capture the outbound message and inspect it.
func TestURLElicitationParamsShape(t *testing.T) {
	p := newPipeFramer()
	defer p.close()
	lc := NewLifecycle()
	lc.elicitURLSupported = true
	lc.state = stateReady
	ob := NewOutboundRouter(p, 1*time.Second)

	type result struct {
		res ElicitResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := sendURLElicitation(ob, lc, "Please connect.", "elic-123", "https://mcp.example.com/connect")
		done <- result{res: res, err: err}
	}()

	out := p.readReply(t)
	if out.Method != ElicitMethod {
		t.Fatalf("want %s, got %q", ElicitMethod, out.Method)
	}
	// Must decode cleanly as URL params.
	var url ElicitRequestURLParams
	if err := json.Unmarshal(out.Params, &url); err != nil {
		t.Fatalf("decode url params: %v", err)
	}
	if url.Mode != "url" {
		t.Fatalf("want mode=url, got %q", url.Mode)
	}
	if url.ElicitationID != "elic-123" {
		t.Fatalf("elicitationId mismatch: %q", url.ElicitationID)
	}
	if url.URL != "https://mcp.example.com/connect" {
		t.Fatalf("url mismatch: %q", url.URL)
	}
	// MUST NOT carry a requestedSchema.
	var raw map[string]any
	if err := json.Unmarshal(out.Params, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["requestedSchema"]; ok {
		t.Fatalf("URL params must not carry requestedSchema (%+v)", raw)
	}

	// Reply with action=accept (URL mode never carries content).
	reply, _ := json.Marshal(ElicitResult{Action: "accept"})
	ob.Dispatch(Message{JSONRPC: JSONRPCVersion, ID: out.ID, Result: reply})

	r := <-done
	if r.err != nil {
		t.Fatalf("send failed: %v", r.err)
	}
	if r.res.Content != nil {
		t.Fatalf("URL-mode accept must have nil content, got %+v", r.res.Content)
	}
}

// Test 5: declined elicitation propagates action: "decline" to the
// caller (and consequently to the upstream tool result).
func TestDeclinedElicitationPropagates(t *testing.T) {
	p := newPipeFramer()
	defer p.close()
	lc := NewLifecycle()
	lc.elicitFormSupported = true
	lc.state = stateReady
	ob := NewOutboundRouter(p, 1*time.Second)
	rt := NewRouter(lc, ob)

	// Drive the tools/call from a goroutine so we can answer the
	// nested elicitation/create outbound request inline.
	callID := NumberID(99)
	type result struct {
		reply Message
		ok    bool
	}
	done := make(chan result, 1)
	go func() {
		reply, ok := rt.Dispatch(Message{
			JSONRPC: JSONRPCVersion,
			ID:      &callID,
			Method:  "tools/call",
			Params:  json.RawMessage(`{"name":"who-are-you"}`),
		})
		done <- result{reply: reply, ok: ok}
	}()

	// Server emits elicitation/create. We reply with decline.
	out := p.readReply(t)
	if out.Method != ElicitMethod {
		t.Fatalf("want %s, got %q", ElicitMethod, out.Method)
	}
	reply, _ := json.Marshal(ElicitResult{Action: "decline"})
	ob.Dispatch(Message{JSONRPC: JSONRPCVersion, ID: out.ID, Result: reply})

	r := <-done
	if !r.ok {
		t.Fatal("router produced no reply for tools/call")
	}
	if r.reply.Error != nil {
		t.Fatalf("decline must not surface as RPC error: %+v", r.reply.Error)
	}
	var ctr CallToolResult
	if err := json.Unmarshal(r.reply.Result, &ctr); err != nil {
		t.Fatalf("decode CallToolResult: %v", err)
	}
	if ctr.IsError {
		t.Fatal("decline should not set isError")
	}
	if len(ctr.Content) != 1 || ctr.Content[0].Type != "text" {
		t.Fatalf("want one text content, got %+v", ctr.Content)
	}
	if !contains(ctr.Content[0].Text, "declined") {
		t.Fatalf("want text mentioning 'declined', got %q", ctr.Content[0].Text)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
