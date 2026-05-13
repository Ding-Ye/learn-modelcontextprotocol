package main

import (
	"encoding/json"
)

// router dispatches a parsed Message to the right handler. Each entry takes
// the raw params slice and returns (result, *Error). Following s02's
// pattern, but adding the resource-specific methods this chapter introduces.
type router struct {
	lc       *lifecycle
	store    *memStore
	notifier *notifier
}

func newRouter(lc *lifecycle, store *memStore, n *notifier) *router {
	return &router{lc: lc, store: store, notifier: n}
}

// handle resolves a single message. Notifications return (Message{}, errSkip)
// and produce no reply.
func (r *router) handle(m Message) (Message, error) {
	if m.IsNotification() {
		switch m.Method {
		case "notifications/initialized":
			r.lc.markInitialized()
		}
		return Message{}, errSkip
	}
	if !m.IsRequest() {
		return Message{}, errSkip
	}

	// `initialize` is always accepted, even before the initialized
	// notification has been received (it can't be received before it).
	if m.Method == "initialize" {
		result, rerr := handleInitialize(m.Params)
		if rerr != nil {
			return NewErrorResponse(*m.ID, rerr.Code, rerr.Message), nil
		}
		return NewResultResponse(*m.ID, result)
	}

	if !r.lc.ready() {
		return NewErrorResponse(*m.ID, InvalidRequest, "server not initialized"), nil
	}

	result, rerr := r.dispatch(m.Method, m.Params)
	if rerr != nil {
		return NewErrorResponse(*m.ID, rerr.Code, rerr.Message), nil
	}
	return NewResultResponse(*m.ID, result)
}

func (r *router) dispatch(method string, params json.RawMessage) (any, *Error) {
	switch method {
	case "resources/list":
		return handleListResources(r.store, params)
	case "resources/read":
		return handleReadResource(r.store, params)
	case "resources/templates/list":
		return handleListTemplates(r.store, params)
	case "resources/subscribe":
		return handleSubscribe(r.notifier, params)
	case "resources/unsubscribe":
		return handleUnsubscribe(r.notifier, params)
	default:
		return nil, &Error{Code: MethodNotFound, Message: "method not found: " + method}
	}
}
