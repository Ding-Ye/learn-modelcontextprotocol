package main

// OutboundRouter — the server-initiated request infrastructure.
//
// Until s05 every request was inbound (client→server) and the dispatch was
// one-shot: read frame → look up handler → write reply. s06 introduces
// the *other* direction: the server emits a request (`sampling/createMessage`)
// and must wait for the client's response on the same channel. Two new
// problems show up:
//
//  1. **Correlation.** The response will arrive *interleaved* with other
//     inbound traffic. We tag each outbound with a fresh id and keep a
//     `pending` map { id → channel-for-result }. When the read loop sees
//     a response (no `method`, present `id`), it routes to the channel.
//
//  2. **Timeouts.** A client that vanishes shouldn't leak a goroutine. We
//     register a context-with-timeout per outbound; on timeout we delete
//     the entry, close the channel, and return an error to the caller.
//
// This file owns the pending table and the SendRequest API. The router
// (router.go) hands inbound responses to OutboundRouter.HandleResponse.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultOutboundTimeout caps how long SendRequest waits. 30s matches the
// spec's "client SHOULD timeout sampling requests" guidance (sampling.mdx
// "Implementation Notes").
const DefaultOutboundTimeout = 30 * time.Second

// pendingEntry holds the one-shot reply channel and the cancel for the
// timeout goroutine.
type pendingEntry struct {
	ch     chan pendingResult
	cancel context.CancelFunc
}

// pendingResult is what flows back on the channel. Exactly one of Result
// or Err is set.
type pendingResult struct {
	Result json.RawMessage
	Err    error
}

// OutboundRouter is safe for concurrent use. The s06 demo only has one
// goroutine emitting outbound requests, but tests exercise the timeout
// path which involves the timer goroutine and the SendRequest caller.
type OutboundRouter struct {
	framer Framer
	mu     sync.Mutex
	next   atomic.Int64
	// pending is keyed by the *string form* of the ID, since `ID` itself
	// isn't comparable (it wraps a slice). See envelope.go: ID.Key().
	pending map[string]*pendingEntry
	timeout time.Duration
}

func NewOutboundRouter(f Framer) *OutboundRouter {
	return &OutboundRouter{
		framer:  f,
		pending: map[string]*pendingEntry{},
		timeout: DefaultOutboundTimeout,
	}
}

// SetTimeout overrides DefaultOutboundTimeout. Tests use this to force a
// quick timeout without sleeping for 30s.
func (o *OutboundRouter) SetTimeout(d time.Duration) { o.timeout = d }

// nextID mints a fresh integer id. We use a numeric id (NumberID) to keep
// the wire compact and to avoid ambiguity with inbound string ids.
func (o *OutboundRouter) nextID() ID {
	n := o.next.Add(1)
	return NumberID(n)
}

// SendRequest emits a request frame and blocks until the client responds
// or the timeout fires. The result is the raw `result` field; callers
// json.Unmarshal it into the typed result they expect.
//
// Why expose the raw bytes here, not a typed result: SendRequest is
// method-agnostic. The same scaffold can carry sampling/createMessage,
// elicitation/create (s07), roots/list (s07), etc. Typed parsing
// belongs in the method-specific caller.
func (o *OutboundRouter) SendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := o.nextID()
	key := id.Key()

	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("outbound %s: encode params: %w", method, err)
	}
	msg := Message{
		JSONRPC: JSONRPCVersion,
		ID:      &id,
		Method:  method,
		Params:  raw,
	}

	// Register pending BEFORE writing the frame — otherwise a fast
	// client could reply before we've added the channel.
	ch := make(chan pendingResult, 1)
	timeoutCtx, cancel := context.WithTimeout(ctx, o.timeout)
	o.mu.Lock()
	o.pending[key] = &pendingEntry{ch: ch, cancel: cancel}
	o.mu.Unlock()

	if err := o.framer.Write(msg); err != nil {
		o.removePending(key)
		cancel()
		return nil, fmt.Errorf("outbound %s: write: %w", method, err)
	}

	select {
	case res := <-ch:
		cancel()
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Result, nil
	case <-timeoutCtx.Done():
		// Clean the entry so the pending map doesn't leak.
		o.removePending(key)
		if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("outbound %s: timed out after %s", method, o.timeout)
		}
		return nil, fmt.Errorf("outbound %s: %w", method, timeoutCtx.Err())
	}
}

// HandleResponse routes an inbound response (`id` present, no `method`)
// to whichever pending entry is waiting. Unknown ids are dropped —
// JSON-RPC says responses with unknown ids "MAY be ignored".
func (o *OutboundRouter) HandleResponse(m Message) {
	if m.ID == nil {
		return
	}
	key := m.ID.Key()
	o.mu.Lock()
	entry, ok := o.pending[key]
	if ok {
		delete(o.pending, key)
	}
	o.mu.Unlock()
	if !ok {
		return
	}
	var res pendingResult
	if m.Error != nil {
		res.Err = m.Error
	} else {
		res.Result = m.Result
	}
	// Non-blocking send — the channel is buffered to 1, and the
	// receiver may have already taken the timeout path.
	select {
	case entry.ch <- res:
	default:
	}
}

// PendingCount returns the size of the pending map. Tests use it to
// assert "no goroutine leak" after timeout.
func (o *OutboundRouter) PendingCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.pending)
}

func (o *OutboundRouter) removePending(key string) {
	o.mu.Lock()
	delete(o.pending, key)
	o.mu.Unlock()
}
