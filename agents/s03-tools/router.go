package main

import (
	"encoding/json"
	"errors"
)

// Handler is the canonical method-handler signature. It returns a Go
// value to serialize as `result`, or a *Error to send back as
// `error`. Returning (nil, nil) means "notification, no response".
type Handler func(params json.RawMessage) (any, *Error)

// Router is the dispatch table. It owns a Lifecycle so it can enforce
// the "no requests before initialized" rule with -32002.
type Router struct {
	lifecycle *Lifecycle
	registry  *ToolRegistry

	requestHandlers      map[string]Handler
	notificationHandlers map[string]func(params json.RawMessage)
}

// NewRouter wires the lifecycle and pre-registers the four methods
// s03 cares about: initialize, notifications/initialized, tools/list,
// tools/call.
func NewRouter(lc *Lifecycle, reg *ToolRegistry) *Router {
	r := &Router{
		lifecycle:            lc,
		registry:             reg,
		requestHandlers:      make(map[string]Handler),
		notificationHandlers: make(map[string]func(params json.RawMessage)),
	}
	r.requestHandlers["initialize"] = r.handleInitialize
	r.requestHandlers["tools/list"] = func(p json.RawMessage) (any, *Error) {
		return handleListTools(r.registry, p)
	}
	r.requestHandlers["tools/call"] = func(p json.RawMessage) (any, *Error) {
		return handleCallTool(r.registry, p)
	}
	r.notificationHandlers["notifications/initialized"] = func(_ json.RawMessage) {
		r.lifecycle.HandleInitialized()
	}
	return r
}

// errSkip signals the loop in main to skip writing a response (used
// for notifications and for malformed messages we silently drop).
var errSkip = errors.New("skip")

// Dispatch routes one decoded Message to a handler and produces the
// reply Message (if any). Notifications and dropped frames return errSkip.
func (r *Router) Dispatch(m Message) (Message, error) {
	switch {
	case m.IsNotification():
		if h, ok := r.notificationHandlers[m.Method]; ok {
			h(m.Params)
		}
		return Message{}, errSkip
	case m.IsRequest():
		// Lifecycle gate: only `initialize` is legal before
		// notifications/initialized arrives.
		if r.lifecycle.State() != StateReady && m.Method != "initialize" {
			return NewErrorResponse(*m.ID, ServerNotInitialized,
				"server not initialized; expected notifications/initialized before "+m.Method), nil
		}
		h, ok := r.requestHandlers[m.Method]
		if !ok {
			return NewErrorResponse(*m.ID, MethodNotFound, "method not found: "+m.Method), nil
		}
		result, rerr := h(m.Params)
		if rerr != nil {
			return Message{JSONRPC: JSONRPCVersion, ID: m.ID, Error: rerr}, nil
		}
		return NewResultResponse(*m.ID, result)
	default:
		return Message{}, errSkip
	}
}

// handleInitialize decodes params, advances the state machine, and
// builds the result.
func (r *Router) handleInitialize(params json.RawMessage) (any, *Error) {
	var p InitializeRequestParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &Error{Code: InvalidParams, Message: "decode initialize: " + err.Error()}
		}
	}
	if p.ProtocolVersion == "" {
		p.ProtocolVersion = LatestProtocolVersion
	}
	return r.lifecycle.HandleInitialize(p), nil
}
