package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// helper: send a Message through the Router and return the (typed) reply.
func process(t *testing.T, r *Router, raw string) (Message, bool) {
	t.Helper()
	var m Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode in-test message: %v", err)
	}
	reply, ok := r.Process(context.Background(), m)
	return reply, ok
}

// 1) Happy path: initialize returns the advertised ServerCapabilities and
//    the negotiated protocol version.
func TestInitializeHappyPath(t *testing.T) {
	s := buildServer()
	r := buildRouter(s)

	reply, ok := process(t, r, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}`)
	if !ok {
		t.Fatalf("expected a reply for initialize")
	}
	if reply.Error != nil {
		t.Fatalf("unexpected error: %+v", reply.Error)
	}
	var got InitializeResult
	if err := json.Unmarshal(reply.Result, &got); err != nil {
		t.Fatalf("decode InitializeResult: %v", err)
	}
	if got.ProtocolVersion != LatestProtocolVersion {
		t.Fatalf("protocolVersion=%q want %q", got.ProtocolVersion, LatestProtocolVersion)
	}
	if got.Capabilities.Tools == nil {
		t.Fatalf("expected tools capability advertised, got nil")
	}
	if got.Capabilities.Logging == nil || got.Capabilities.Resources == nil || got.Capabilities.Prompts == nil {
		t.Fatalf("missing advertised capability: %+v", got.Capabilities)
	}
	if got.ServerInfo.Name == "" {
		t.Fatalf("missing serverInfo.name")
	}
	if s.State() != stateInitializing {
		t.Fatalf("expected stateInitializing after initialize, got %s", s.State())
	}
}

// 2) Calling tools/list before `notifications/initialized` arrives must
//    return -32002 server-not-initialized.
func TestPreInitializedMethodRejected(t *testing.T) {
	s := buildServer()
	r := buildRouter(s)

	// Drive the handshake one step (we have a reply but no init notif yet).
	if _, ok := process(t, r, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}`); !ok {
		t.Fatalf("initialize had no reply")
	}
	if s.State() != stateInitializing {
		t.Fatalf("setup: state=%s want initializing", s.State())
	}

	reply, ok := process(t, r, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if !ok {
		t.Fatalf("expected an error reply for tools/list")
	}
	if reply.Error == nil {
		t.Fatalf("expected an Error, got result=%s", string(reply.Result))
	}
	if reply.Error.Code != ServerNotInitialized {
		t.Fatalf("code=%d want %d (ServerNotInitialized)", reply.Error.Code, ServerNotInitialized)
	}
	if !strings.Contains(reply.Error.Message, "tools/list") {
		t.Fatalf("error message should name the method, got %q", reply.Error.Message)
	}
}

// 3) Wrong protocolVersion → server responds with its own preferred
//    version. The spec says the server MUST still reply; the *client*
//    decides whether to disconnect.
func TestWrongProtocolVersionEchoesServerPreferred(t *testing.T) {
	s := buildServer()
	r := buildRouter(s)

	reply, ok := process(t, r, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}`)
	if !ok {
		t.Fatalf("expected a reply")
	}
	if reply.Error != nil {
		t.Fatalf("server should not error on unsupported version; got %+v", reply.Error)
	}
	var got InitializeResult
	if err := json.Unmarshal(reply.Result, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ProtocolVersion != LatestProtocolVersion {
		t.Fatalf("expected server preferred version %q, got %q", LatestProtocolVersion, got.ProtocolVersion)
	}
	// And the lifecycle did advance — the server didn't disconnect.
	if s.State() != stateInitializing {
		t.Fatalf("expected state=initializing after version fallback, got %s", s.State())
	}
}

// 4) Duplicate initialize is rejected with -32600 InvalidRequest.
func TestDuplicateInitializeRejected(t *testing.T) {
	s := buildServer()
	r := buildRouter(s)

	if _, ok := process(t, r, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}`); !ok {
		t.Fatalf("first initialize had no reply")
	}

	reply, ok := process(t, r, `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}`)
	if !ok {
		t.Fatalf("expected a reply for duplicate initialize")
	}
	if reply.Error == nil {
		t.Fatalf("expected error, got result=%s", string(reply.Result))
	}
	if reply.Error.Code != InvalidRequest {
		t.Fatalf("code=%d want %d (InvalidRequest)", reply.Error.Code, InvalidRequest)
	}
}

// 5) `notifications/initialized` flips state to stateOperating and
//    produces NO response (it's a notification, no `id`).
func TestInitializedNotificationFlipsStateNoReply(t *testing.T) {
	s := buildServer()
	r := buildRouter(s)

	if _, ok := process(t, r, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}`); !ok {
		t.Fatalf("initialize had no reply")
	}

	reply, ok := process(t, r, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if ok {
		t.Fatalf("notifications must produce no reply, got %+v", reply)
	}
	if s.State() != stateOperating {
		t.Fatalf("expected stateOperating after initialized notif, got %s", s.State())
	}

	// And after the flip, an arbitrary method passes the state gate
	// (it'll fall through to MethodNotFound in s02 because nothing's
	// registered yet — that's still progress, not -32002).
	r2, ok := process(t, r, `{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`)
	if !ok || r2.Error == nil {
		t.Fatalf("expected error reply; got ok=%v reply=%+v", ok, r2)
	}
	if r2.Error.Code != MethodNotFound {
		t.Fatalf("after initialized, expected MethodNotFound (s03 adds tools); got %d", r2.Error.Code)
	}
}

// Bonus: ping is allowed at every state, including stateNew. Confirms
// the lifecycle gate doesn't accidentally block liveness probes.
func TestPingAllowedPreHandshake(t *testing.T) {
	s := buildServer()
	r := buildRouter(s)

	reply, ok := process(t, r, `{"jsonrpc":"2.0","id":9,"method":"ping","params":{}}`)
	if !ok || reply.Error != nil {
		t.Fatalf("ping should succeed pre-handshake; ok=%v err=%+v", ok, reply.Error)
	}
	if s.State() != stateNew {
		t.Fatalf("ping must not advance lifecycle state; got %s", s.State())
	}
}
