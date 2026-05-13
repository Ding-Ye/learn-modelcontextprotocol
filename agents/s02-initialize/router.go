package main

import (
	"context"
	"encoding/json"
)

// Handler is the per-method signature the Router dispatches to. It
// mirrors the canonical shared-types catalog in the curriculum plan:
// the request body lives in `params` as raw JSON, the handler returns
// either a marshalable value or a typed Error.
type Handler func(ctx context.Context, method string, params json.RawMessage) (any, *Error)

// Router is the smallest piece of plumbing every chapter from s02
// onward needs: a method -> Handler map plus a fallback. It's
// deliberately not generic over transport — Process takes and returns
// the wire-level Message envelope so the same Router can sit behind
// stdio (this chapter) or HTTP (s08) unchanged.
type Router struct {
	handlers map[string]Handler
	fallback Handler
}

// NewRouter constructs an empty Router. If fallback is nil, unknown
// methods get a standard MethodNotFound error response.
func NewRouter(fallback Handler) *Router {
	return &Router{handlers: map[string]Handler{}, fallback: fallback}
}

// Handle registers (or overwrites) the handler for `method`.
func (r *Router) Handle(method string, h Handler) {
	r.handlers[method] = h
}

// Process turns one inbound Message into at most one outbound Message.
//
// Behavior matrix:
//   - Request   -> exactly one reply (result or error)
//   - Notification -> no reply, fan out via fallback for side effects
//     (the second return is the zero Message and `false` for the bool)
//   - Response  -> ignored at this layer; s02 only acts as a server.
//
// The `ok` return is true iff the caller should write `reply` back.
func (r *Router) Process(ctx context.Context, msg Message) (reply Message, ok bool) {
	switch {
	case msg.IsNotification():
		// Run the handler for side effects (e.g. flipping
		// lifecycle state), but never write a reply.
		h := r.lookup(msg.Method)
		_, _ = h(ctx, msg.Method, msg.Params)
		return Message{}, false

	case msg.IsRequest():
		h := r.lookup(msg.Method)
		result, herr := h(ctx, msg.Method, msg.Params)
		if herr != nil {
			return NewErrorResponse(*msg.ID, herr.Code, herr.Message), true
		}
		out, err := NewResultResponse(*msg.ID, result)
		if err != nil {
			return NewErrorResponse(*msg.ID, InternalError, err.Error()), true
		}
		return out, true

	default:
		return Message{}, false
	}
}

// lookup returns the registered handler for `method`, or the fallback,
// or the built-in "method not found" closure.
func (r *Router) lookup(method string) Handler {
	if h, ok := r.handlers[method]; ok {
		return h
	}
	if r.fallback != nil {
		return r.fallback
	}
	return notFoundHandler
}

func notFoundHandler(_ context.Context, method string, _ json.RawMessage) (any, *Error) {
	return nil, &Error{Code: MethodNotFound, Message: "method not found: " + method}
}
