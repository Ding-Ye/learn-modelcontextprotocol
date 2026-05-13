package main

// Router is a tiny method dispatcher. s02 introduced the pattern in
// full; here we keep the trimmed version: a map of `method` →
// (params → (result, *Error)). Pre-initialize requests for anything
// other than `initialize` itself return -32600 InvalidRequest, matching
// the lifecycle gate described in basic/lifecycle.mdx.

import (
	"encoding/json"
)

// HandlerFunc is the s05-shape callback. We keep `context.Context` out
// of the signature on purpose — none of the prompt or completion paths
// do I/O, so the extra type parameter would be noise.
type HandlerFunc func(params json.RawMessage) (any, *Error)

type Router struct {
	lifecycle *Lifecycle
	registry  *PromptRegistry
	handlers  map[string]HandlerFunc
}

func NewRouter(l *Lifecycle, r *PromptRegistry) *Router {
	rt := &Router{lifecycle: l, registry: r, handlers: map[string]HandlerFunc{}}
	rt.handlers["initialize"] = func(p json.RawMessage) (any, *Error) {
		return l.HandleInitialize(p)
	}
	rt.handlers["prompts/list"] = func(p json.RawMessage) (any, *Error) {
		return handleListPrompts(r, p)
	}
	rt.handlers["prompts/get"] = func(p json.RawMessage) (any, *Error) {
		return handleGetPrompt(r, p)
	}
	rt.handlers["completion/complete"] = func(p json.RawMessage) (any, *Error) {
		return handleComplete(r, p)
	}
	return rt
}

// Dispatch turns one inbound message into one outbound message (or none,
// if it's a notification). The lifecycle gate lives here, in one place.
func (rt *Router) Dispatch(m Message) (Message, bool) {
	// Handle notifications first — they have no id and produce no reply.
	if m.IsNotification() {
		if m.Method == "notifications/initialized" {
			rt.lifecycle.HandleInitialized()
		}
		return Message{}, false
	}
	if !m.IsRequest() {
		// Responses to outbound requests aren't supported in s05 (no
		// server-initiated requests). Quietly drop them.
		return Message{}, false
	}
	id := *m.ID
	// Pre-init gate: only `initialize` may run before the handshake
	// completes (lifecycle.mdx).
	if !rt.lifecycle.Ready() && m.Method != "initialize" && m.Method != "notifications/initialized" {
		return NewErrorResponse(id, InvalidRequest, "lifecycle: not yet initialized"), true
	}
	h, ok := rt.handlers[m.Method]
	if !ok {
		return NewErrorResponse(id, MethodNotFound, "method not found: "+m.Method), true
	}
	res, err := h(m.Params)
	if err != nil {
		return Message{
			JSONRPC: JSONRPCVersion,
			ID:      &id,
			Error:   err,
		}, true
	}
	out, mErr := NewResultResponse(id, res)
	if mErr != nil {
		return NewErrorResponse(id, InternalError, mErr.Error()), true
	}
	return out, true
}
