package main

// Router — same shape as every chapter since s02: method → handler. The
// new ingredient for s07 is that some inbound responses are *not*
// requests for us to handle but *replies* to outbound requests we
// previously sent. The `OutboundRouter` claims those.
//
// The demo tool `who-are-you` is the simplest possible end-to-end
// exercise of elicitation: when called, the server fires an
// `elicitation/create` form back to the client asking for the user's
// name, then folds the reply into a single text content block.

import (
	"encoding/json"
	"fmt"
)

// HandlerFunc is the s07-shape callback. Returning a typed
// *URLElicitationRequiredError is the short-circuit path that produces
// a -32042 reply with `data.elicitations`.
type HandlerFunc func(params json.RawMessage) (any, error)

type Router struct {
	lifecycle *Lifecycle
	outbound  *OutboundRouter
	handlers  map[string]HandlerFunc
}

func NewRouter(lc *Lifecycle, ob *OutboundRouter) *Router {
	rt := &Router{lifecycle: lc, outbound: ob, handlers: map[string]HandlerFunc{}}
	rt.handlers["initialize"] = func(p json.RawMessage) (any, error) {
		res, e := lc.HandleInitialize(p)
		if e != nil {
			return nil, e
		}
		return res, nil
	}
	rt.handlers["tools/list"] = func(p json.RawMessage) (any, error) {
		return rt.handleToolsList(p)
	}
	rt.handlers["tools/call"] = func(p json.RawMessage) (any, error) {
		return rt.handleToolsCall(p)
	}
	return rt
}

// Dispatch one inbound message. Returns (reply, hasReply).
func (rt *Router) Dispatch(m Message) (Message, bool) {
	// First: outbound-response path. The dispatcher claims any
	// response whose id matches a pending entry.
	if m.IsResponse() && rt.outbound != nil {
		if rt.outbound.Dispatch(m) {
			return Message{}, false
		}
	}
	// Notifications.
	if m.IsNotification() {
		if m.Method == "notifications/initialized" {
			rt.lifecycle.HandleInitialized()
		}
		return Message{}, false
	}
	if !m.IsRequest() {
		return Message{}, false
	}
	id := *m.ID
	if !rt.lifecycle.Ready() && m.Method != "initialize" && m.Method != "notifications/initialized" {
		return NewErrorResponse(id, InvalidRequest, "lifecycle: not yet initialized"), true
	}
	h, ok := rt.handlers[m.Method]
	if !ok {
		return NewErrorResponse(id, MethodNotFound, "method not found: "+m.Method), true
	}
	res, err := h(m.Params)
	if err != nil {
		// Typed short-circuit: URL elicitation required.
		if ue, ok := err.(*URLElicitationRequiredError); ok {
			return Message{JSONRPC: JSONRPCVersion, ID: &id, Error: ue.asRPCError()}, true
		}
		if rpcErr, ok := err.(*Error); ok {
			return Message{JSONRPC: JSONRPCVersion, ID: &id, Error: rpcErr}, true
		}
		return NewErrorResponse(id, InternalError, err.Error()), true
	}
	out, mErr := NewResultResponse(id, res)
	if mErr != nil {
		return NewErrorResponse(id, InternalError, mErr.Error()), true
	}
	return out, true
}

// ---- demo tools/list + tools/call ----

// Tool is the trimmed shape from schema.ts:1251 (we keep only what
// `who-are-you` needs). The `inputSchema` is required by the spec; we
// declare an empty object schema since the tool takes no args.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type ListToolsResult struct {
	Tools []Tool `json:"tools"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

func (rt *Router) handleToolsList(_ json.RawMessage) (any, error) {
	return ListToolsResult{
		Tools: []Tool{
			{
				Name:        "who-are-you",
				Description: "Asks the user their name via an elicitation/create form.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			},
		},
	}, nil
}

// CallToolRequestParams trimmed to what we need.
type CallToolRequestParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (rt *Router) handleToolsCall(raw json.RawMessage) (any, error) {
	var p CallToolRequestParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &Error{Code: InvalidParams, Message: "tools/call: " + err.Error()}
		}
	}
	switch p.Name {
	case "who-are-you":
		return rt.whoAreYou()
	default:
		return nil, &Error{Code: MethodNotFound, Message: "tool not found: " + p.Name}
	}
}

// whoAreYou is the demo: emit a form elicitation, fold the reply into a
// text content block. Note that *cancel* / *decline* are not errors —
// they are perfectly valid user actions, surfaced via `isError:false`
// content. Only a transport / protocol failure becomes an `error`.
func (rt *Router) whoAreYou() (CallToolResult, error) {
	schema := NewRequestedSchema(map[string]PrimitiveSchema{
		"name": {"type": "string", "title": "Your name", "description": "How should we address you?"},
	}, "name")
	res, err := sendFormElicitation(rt.outbound, rt.lifecycle, "Please tell us who you are.", schema)
	if err != nil {
		return CallToolResult{}, err
	}
	switch res.Action {
	case "accept":
		name, _ := res.Content["name"].(string)
		if name == "" {
			name = "<unset>"
		}
		return CallToolResult{Content: []ContentBlock{{Type: "text", Text: "Hello, " + name + "!"}}}, nil
	case "decline":
		return CallToolResult{Content: []ContentBlock{{Type: "text", Text: "User declined to share their name."}}}, nil
	case "cancel":
		return CallToolResult{Content: []ContentBlock{{Type: "text", Text: "User dismissed the prompt."}}}, nil
	default:
		return CallToolResult{}, fmt.Errorf("unexpected elicit action %q", res.Action)
	}
}
