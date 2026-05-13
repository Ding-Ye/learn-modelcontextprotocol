package main

import (
	"encoding/json"
	"sync"
)

// notifier tracks which URIs have active subscribers and pushes
// `notifications/resources/updated` frames through whatever sink is
// installed. The sink is a func, not a Framer, so tests can capture
// emitted messages without standing up stdio.
type notifier struct {
	mu     sync.Mutex
	subs   map[string]int           // uri -> subscriber count
	sink   func(Message) error      // installed in main.go / tests
	logger func(format string, args ...any)
}

func newNotifier(sink func(Message) error) *notifier {
	return &notifier{
		subs:   map[string]int{},
		sink:   sink,
		logger: func(string, ...any) {},
	}
}

func (n *notifier) subscribe(uri string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.subs[uri]++
}

// unsubscribe drops a single subscription and returns true if it removed an
// active subscription (i.e. it was actually subscribed). After dropping the
// last subscriber the entry is deleted so fire becomes a no-op.
func (n *notifier) unsubscribe(uri string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	count, ok := n.subs[uri]
	if !ok || count <= 0 {
		return false
	}
	count--
	if count == 0 {
		delete(n.subs, uri)
	} else {
		n.subs[uri] = count
	}
	return true
}

func (n *notifier) isSubscribed(uri string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, ok := n.subs[uri]
	return ok
}

// fire emits `notifications/resources/updated` to the sink iff at least one
// subscription is active for uri. The notification itself is a JSON-RPC
// notification (no id) per schema.ts:786-803.
func (n *notifier) fire(uri string) {
	n.mu.Lock()
	_, ok := n.subs[uri]
	sink := n.sink
	n.mu.Unlock()
	if !ok || sink == nil {
		return
	}
	params := ResourceUpdatedNotificationParams{URI: uri}
	raw, err := json.Marshal(params)
	if err != nil {
		n.logger("marshal updated params: %v", err)
		return
	}
	msg := Message{
		JSONRPC: JSONRPCVersion,
		Method:  "notifications/resources/updated",
		Params:  raw,
	}
	if err := sink(msg); err != nil {
		n.logger("emit updated: %v", err)
	}
}

// ---- Handlers ------------------------------------------------------------

func handleSubscribe(n *notifier, raw json.RawMessage) (any, *Error) {
	var req SubscribeRequest
	if e := decodeParams(raw, &req); e != nil {
		return nil, e
	}
	if req.URI == "" {
		return nil, &Error{Code: InvalidParams, Message: "uri required"}
	}
	n.subscribe(req.URI)
	// Empty result is fine — schema.ts treats this as `Result` (no fields
	// beyond _meta). We return `struct{}` which marshals to `{}`.
	return struct{}{}, nil
}

func handleUnsubscribe(n *notifier, raw json.RawMessage) (any, *Error) {
	var req UnsubscribeRequest
	if e := decodeParams(raw, &req); e != nil {
		return nil, e
	}
	if req.URI == "" {
		return nil, &Error{Code: InvalidParams, Message: "uri required"}
	}
	if !n.unsubscribe(req.URI) {
		return nil, &Error{Code: InvalidParams, Message: "not subscribed: " + req.URI}
	}
	return struct{}{}, nil
}
