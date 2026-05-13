package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// newTestServer wires up a router + memStore + notifier with a captured
// notification sink, mirroring main.go but without stdio. Returns the
// router, the store (so tests can mutate), and a fn that drains every
// notification frame emitted so far.
func newTestServer(t *testing.T) (*router, *memStore, func() []Message) {
	t.Helper()

	var mu sync.Mutex
	var emitted []Message
	sink := func(m Message) error {
		mu.Lock()
		defer mu.Unlock()
		emitted = append(emitted, m)
		return nil
	}

	n := newNotifier(sink)
	store := newMemStore(n)
	lc := &lifecycle{}
	lc.markInitialized() // skip handshake; tested in s02

	store.Register("mem://log.txt", "log.txt", "demo log",
		"text/plain", "hello\n")
	store.RegisterTemplate("mem://users/{id}", "user", "user record",
		"text/plain",
		map[string]string{
			"alice": "Alice Liddell",
			"bob":   "Bob Builder",
		})

	r := newRouter(lc, store, n)

	drain := func() []Message {
		mu.Lock()
		defer mu.Unlock()
		out := make([]Message, len(emitted))
		copy(out, emitted)
		return out
	}
	return r, store, drain
}

// invoke runs a single request through the router. id is always StringID("t").
func invoke(t *testing.T, r *router, method string, params any) Message {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	id := StringID("t")
	req := Message{JSONRPC: JSONRPCVersion, ID: &id, Method: method, Params: raw}
	reply, err := r.handle(req)
	if err != nil {
		t.Fatalf("router.handle(%s): %v", method, err)
	}
	return reply
}

func decodeResult[T any](t *testing.T, msg Message) T {
	t.Helper()
	if msg.Error != nil {
		t.Fatalf("unexpected error: %+v", msg.Error)
	}
	var v T
	if err := json.Unmarshal(msg.Result, &v); err != nil {
		t.Fatalf("decode result: %v (raw=%s)", err, msg.Result)
	}
	return v
}

func TestListReturnsExpectedURIs(t *testing.T) {
	r, _, _ := newTestServer(t)
	reply := invoke(t, r, "resources/list", ListResourcesRequest{})
	res := decodeResult[ListResourcesResult](t, reply)
	if len(res.Resources) != 1 {
		t.Fatalf("want 1 static resource, got %d (%+v)", len(res.Resources), res.Resources)
	}
	if res.Resources[0].URI != "mem://log.txt" {
		t.Errorf("uri=%q", res.Resources[0].URI)
	}
	if res.Resources[0].MIMEType != "text/plain" {
		t.Errorf("mime=%q", res.Resources[0].MIMEType)
	}
}

func TestReadMemLogTextContent(t *testing.T) {
	r, _, _ := newTestServer(t)
	reply := invoke(t, r, "resources/read", ReadResourceRequest{URI: "mem://log.txt"})
	res := decodeResult[ReadResourceResult](t, reply)
	if len(res.Contents) != 1 {
		t.Fatalf("want 1 contents, got %d", len(res.Contents))
	}
	if res.Contents[0].Text != "hello\n" {
		t.Errorf("text=%q", res.Contents[0].Text)
	}
	if res.Contents[0].Blob != "" {
		t.Errorf("blob unexpectedly set: %q", res.Contents[0].Blob)
	}
}

func TestReadTemplateSubstitutes(t *testing.T) {
	r, _, _ := newTestServer(t)

	// resources/templates/list should report mem://users/{id}.
	listReply := invoke(t, r, "resources/templates/list", ListResourceTemplatesRequest{})
	listRes := decodeResult[ListResourceTemplatesResult](t, listReply)
	if len(listRes.ResourceTemplates) != 1 || listRes.ResourceTemplates[0].URITemplate != "mem://users/{id}" {
		t.Fatalf("template list mismatch: %+v", listRes.ResourceTemplates)
	}

	// resources/read for an expanded template URI should return the right entry.
	reply := invoke(t, r, "resources/read", ReadResourceRequest{URI: "mem://users/alice"})
	res := decodeResult[ReadResourceResult](t, reply)
	if len(res.Contents) != 1 || res.Contents[0].Text != "Alice Liddell" {
		t.Fatalf("want Alice Liddell, got %+v", res.Contents)
	}

	// Unknown template instance => InvalidParams (not-found).
	missing := invoke(t, r, "resources/read", ReadResourceRequest{URI: "mem://users/eve"})
	if missing.Error == nil || missing.Error.Code != InvalidParams {
		t.Fatalf("expected InvalidParams for unknown user, got %+v", missing.Error)
	}
}

func TestSubscribeMutateEmitsUpdated(t *testing.T) {
	r, store, drain := newTestServer(t)

	// 1. Subscribe.
	subReply := invoke(t, r, "resources/subscribe", SubscribeRequest{URI: "mem://log.txt"})
	if subReply.Error != nil {
		t.Fatalf("subscribe failed: %+v", subReply.Error)
	}

	// 2. Mutate via the Store API (this is what an external watcher would do).
	store.Set("mem://log.txt", "text/plain", "hello\nworld\n")

	// 3. Confirm exactly one notification was emitted with the right URI.
	emitted := drain()
	if len(emitted) != 1 {
		t.Fatalf("want 1 notification, got %d: %+v", len(emitted), emitted)
	}
	got := emitted[0]
	if got.Method != "notifications/resources/updated" {
		t.Errorf("method=%q", got.Method)
	}
	if got.ID != nil {
		t.Errorf("notification carried an id: %s", got.ID.String())
	}
	var params ResourceUpdatedNotificationParams
	if err := json.Unmarshal(got.Params, &params); err != nil {
		t.Fatalf("decode notification params: %v", err)
	}
	if params.URI != "mem://log.txt" {
		t.Errorf("notification uri=%q", params.URI)
	}

	// 4. The notification must round-trip through JSON without "id":null
	//    leaking through. Marshal it and confirm no `"id"` key appears.
	wire, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	if strings.Contains(string(wire), `"id"`) {
		t.Errorf("notification frame must not carry an id key: %s", wire)
	}
}

func TestUnsubscribeStopsNotifications(t *testing.T) {
	r, store, drain := newTestServer(t)

	// Subscribe, then immediately unsubscribe.
	if reply := invoke(t, r, "resources/subscribe", SubscribeRequest{URI: "mem://log.txt"}); reply.Error != nil {
		t.Fatalf("subscribe: %+v", reply.Error)
	}
	if reply := invoke(t, r, "resources/unsubscribe", UnsubscribeRequest{URI: "mem://log.txt"}); reply.Error != nil {
		t.Fatalf("unsubscribe: %+v", reply.Error)
	}

	// Mutate — must NOT produce a notification.
	store.Set("mem://log.txt", "text/plain", "third revision\n")
	if got := drain(); len(got) != 0 {
		t.Fatalf("want 0 notifications post-unsubscribe, got %d: %+v", len(got), got)
	}

	// Unsubscribing again should error (not currently subscribed).
	reply := invoke(t, r, "resources/unsubscribe", UnsubscribeRequest{URI: "mem://log.txt"})
	if reply.Error == nil {
		t.Fatalf("expected error on double-unsubscribe")
	}
}
