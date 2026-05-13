package main

// OutboundRouter — the scaffolding for *server-initiated* requests. s06
// introduced this for `sampling/createMessage`; s07 re-implements it
// verbatim because (a) the plan forbids cross-module imports, and (b)
// rewriting the pending-request table is the lesson.
//
// Pattern: the server picks a fresh request id, writes the outbound
// `Message`, and parks the goroutine on a result channel registered in
// `pending`. When the dispatcher reads a `Response` (id matches, method
// empty), it looks the id up and fulfils the channel. A `timeout`
// guards against a stuck client.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// pendingEntry is one parked request waiting for its reply.
type pendingEntry struct {
	ch     chan outboundReply
	method string
	at     time.Time
}

// outboundReply is what the dispatcher delivers when it sees a matching
// response on the wire.
type outboundReply struct {
	result json.RawMessage
	err    *Error
}

// Writer is the minimal interface the OutboundRouter needs. Both the
// real `stdioFramer.Write` and the test harness satisfy it.
type Writer interface {
	Write(m Message) error
}

// OutboundRouter owns the pending-request table for one server.
type OutboundRouter struct {
	w        Writer
	timeout  time.Duration
	mu       sync.Mutex
	pending  map[string]*pendingEntry
	nextID   atomic.Int64
}

func NewOutboundRouter(w Writer, timeout time.Duration) *OutboundRouter {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	r := &OutboundRouter{
		w:       w,
		timeout: timeout,
		pending: map[string]*pendingEntry{},
	}
	// Seed at 1; ids must not be 0 (a zero number id is legal, but
	// stays distinct from our pre-existing inbound traffic).
	r.nextID.Store(1)
	return r
}

// SendRequest writes one server-initiated request and blocks for the
// reply or a timeout. Returns the raw `result` bytes; the caller
// decodes them into a typed shape.
func (r *OutboundRouter) SendRequest(method string, params any) (json.RawMessage, error) {
	rawParams, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode params: %w", err)
	}
	id := NumberID(r.nextID.Add(1))
	entry := &pendingEntry{
		ch:     make(chan outboundReply, 1),
		method: method,
		at:     time.Now(),
	}
	r.mu.Lock()
	r.pending[id.String()] = entry
	r.mu.Unlock()

	msg := Message{
		JSONRPC: JSONRPCVersion,
		ID:      &id,
		Method:  method,
		Params:  rawParams,
	}
	if err := r.w.Write(msg); err != nil {
		r.cancel(id.String())
		return nil, fmt.Errorf("write outbound: %w", err)
	}

	select {
	case reply := <-entry.ch:
		if reply.err != nil {
			return nil, reply.err
		}
		return reply.result, nil
	case <-time.After(r.timeout):
		r.cancel(id.String())
		return nil, fmt.Errorf("outbound %s timed out after %s", method, r.timeout)
	}
}

// Dispatch is called by the inbound loop when it sees a Response
// (id != nil, method == ""). It looks the id up and forwards the
// result/error to the waiting channel. Returns true if the message
// was claimed.
func (r *OutboundRouter) Dispatch(m Message) bool {
	if !m.IsResponse() || m.ID == nil {
		return false
	}
	key := m.ID.String()
	r.mu.Lock()
	entry, ok := r.pending[key]
	if ok {
		delete(r.pending, key)
	}
	r.mu.Unlock()
	if !ok {
		return false
	}
	reply := outboundReply{}
	if m.Error != nil {
		reply.err = m.Error
	} else {
		reply.result = m.Result
	}
	entry.ch <- reply
	return true
}

// cancel removes a pending entry (timeout or write failure).
func (r *OutboundRouter) cancel(key string) {
	r.mu.Lock()
	delete(r.pending, key)
	r.mu.Unlock()
}

// pendingCount is exposed for tests that assert the table empties after
// timeouts or successful dispatches.
func (r *OutboundRouter) pendingCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

// ErrNoPending is returned by helpers that try to deliver a reply for
// an id the router never issued. Exported for tests.
var ErrNoPending = errors.New("no pending entry for id")
