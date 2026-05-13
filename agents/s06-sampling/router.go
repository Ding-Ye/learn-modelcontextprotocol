package main

// Router — method dispatcher for s06. Two surfaces:
//
//   - **Inbound**: `initialize`, `notifications/initialized`, `tools/list`,
//     `tools/call`. The lifecycle gate from s02..s05 carries over.
//   - **Outbound responses**: when an inbound frame is a *response*
//     (no method, has id) we route it to `OutboundRouter.HandleResponse`
//     so the matching SendRequest unblocks. This is new in s06.
//
// The demo tool `summarize` is the trigger — calling it makes the server
// emit `sampling/createMessage` back at the client and weave the LLM
// reply into the tool's own result. That's the agentic loop in miniature.

import (
	"context"
	"encoding/json"
	"fmt"
)

// HandlerFunc returns either a typed result or a *Error. The `ctx` lets
// `tools/call` reach the outbound path with its own timeout.
type HandlerFunc func(ctx context.Context, params json.RawMessage) (any, *Error)

type Router struct {
	lifecycle *Lifecycle
	outbound  *OutboundRouter
	sampler   Sampler
	handlers  map[string]HandlerFunc
}

func NewRouter(l *Lifecycle, out *OutboundRouter, s Sampler) *Router {
	rt := &Router{lifecycle: l, outbound: out, sampler: s, handlers: map[string]HandlerFunc{}}
	rt.handlers["initialize"] = func(_ context.Context, p json.RawMessage) (any, *Error) {
		return l.HandleInitialize(p)
	}
	rt.handlers["tools/list"] = rt.handleToolsList
	rt.handlers["tools/call"] = rt.handleToolsCall
	return rt
}

// DispatchInbound returns (reply, hasReply). Notifications and responses
// don't produce replies; requests do.
func (rt *Router) DispatchInbound(ctx context.Context, m Message) (Message, bool) {
	// Responses to outbound sampling: route to the pending table.
	if m.IsResponse() {
		rt.outbound.HandleResponse(m)
		return Message{}, false
	}
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
	if !rt.lifecycle.Ready() && m.Method != "initialize" {
		return NewErrorResponse(id, InvalidRequest, "lifecycle: not yet initialized"), true
	}
	h, ok := rt.handlers[m.Method]
	if !ok {
		return NewErrorResponse(id, MethodNotFound, "method not found: "+m.Method), true
	}
	res, err := h(ctx, m.Params)
	if err != nil {
		return Message{JSONRPC: JSONRPCVersion, ID: &id, Error: err}, true
	}
	out, mErr := NewResultResponse(id, res)
	if mErr != nil {
		return NewErrorResponse(id, InternalError, mErr.Error()), true
	}
	return out, true
}

// ----------------------------------------------------------------------------
// Demo tool: `summarize`
//
// Input:  { "text": string }
// Action: server emits sampling/createMessage with the user-role text
//         "Please summarize: {text}", waits for the LLM reply, returns
//         the reply as the tool result.
//
// This is the smallest end-to-end sampling demo — one outbound request,
// one inbound response, one tool result built from the response.
//
// A second tool `agentic-summarize` shows the two-turn loop: server emits
// sampling/createMessage with a `tools` array; if the LLM responds with a
// tool_use block, the server runs that tool *in-process* via the same
// tool registry, then issues a follow-up sampling/createMessage carrying
// a tool_result block; that second result is what the tool returns.
// ----------------------------------------------------------------------------

// ListToolsResult — minimal s03-shape (no pagination).
type ListToolsResult struct {
	Tools []Tool `json:"tools"`
}

// CallToolRequestParams matches schema.ts:1155-1182.
type CallToolRequestParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// CallToolResult matches schema.ts:1106-1153.
type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

var (
	summarizeTool = Tool{
		Name:        "summarize",
		Description: "Summarize the given text via server-initiated sampling/createMessage.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
	}
	echoTool = Tool{
		Name:        "echo",
		Description: "Echo the input back. Used by agentic-summarize's tool_use round-trip.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
	}
	agenticTool = Tool{
		Name:        "agentic-summarize",
		Description: "Two-turn agentic loop: ask the LLM with tools available; if it returns tool_use, run the tool and feed tool_result back.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
	}
)

func (rt *Router) handleToolsList(_ context.Context, _ json.RawMessage) (any, *Error) {
	return ListToolsResult{Tools: []Tool{summarizeTool, echoTool, agenticTool}}, nil
}

func (rt *Router) handleToolsCall(ctx context.Context, raw json.RawMessage) (any, *Error) {
	var p CallToolRequestParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &Error{Code: InvalidParams, Message: "tools/call: " + err.Error()}
	}
	switch p.Name {
	case "summarize":
		return rt.callSummarize(ctx, p.Arguments)
	case "echo":
		return rt.callEcho(p.Arguments)
	case "agentic-summarize":
		return rt.callAgentic(ctx, p.Arguments)
	default:
		return nil, &Error{Code: MethodNotFound, Message: "tool not found: " + p.Name}
	}
}

// callSummarize: one outbound sampling request, no tools.
func (rt *Router) callSummarize(ctx context.Context, args map[string]any) (any, *Error) {
	text, _ := args["text"].(string)
	if text == "" {
		return nil, &Error{Code: InvalidParams, Message: "summarize: missing text"}
	}
	req := CreateMessageRequestParams{
		Messages: []SamplingMessage{
			{Role: "user", Content: []ContentBlock{TextBlock("Please summarize: " + text)}},
		},
		MaxTokens:      512,
		IncludeContext: "thisServer",
	}
	result, err := rt.requestSampling(ctx, req)
	if err != nil {
		return CallToolResult{
			Content: []ContentBlock{TextBlock("sampling failed: " + err.Error())},
			IsError: true,
		}, nil
	}
	return CallToolResult{Content: result.Content}, nil
}

// callEcho is the trivial in-process tool the LLM "asks for" via tool_use.
func (rt *Router) callEcho(args map[string]any) (any, *Error) {
	text, _ := args["text"].(string)
	return CallToolResult{Content: []ContentBlock{TextBlock("echo: " + text)}}, nil
}

// callAgentic exercises the two-turn loop:
//
//	turn 1: server emits sampling/createMessage with tools=[echo].
//	turn 2: if reply has a tool_use block, run echo in-process, feed
//	        tool_result back via a second sampling/createMessage.
//
// The second sampling call's reply is what the tool returns.
func (rt *Router) callAgentic(ctx context.Context, args map[string]any) (any, *Error) {
	text, _ := args["text"].(string)
	if text == "" {
		return nil, &Error{Code: InvalidParams, Message: "agentic-summarize: missing text"}
	}
	// Turn 1.
	turn1 := CreateMessageRequestParams{
		Messages: []SamplingMessage{
			{Role: "user", Content: []ContentBlock{TextBlock("Summarize using the echo tool if needed: " + text)}},
		},
		MaxTokens: 512,
		Tools:     []Tool{echoTool},
		ToolChoice: &ToolChoice{Mode: "auto"},
	}
	r1, err := rt.requestSampling(ctx, turn1)
	if err != nil {
		return CallToolResult{Content: []ContentBlock{TextBlock("agentic turn1 failed: " + err.Error())}, IsError: true}, nil
	}

	// Find a tool_use block. If none, return whatever the LLM said.
	var toolUse *ContentBlock
	for i := range r1.Content {
		if r1.Content[i].Type == "tool_use" {
			toolUse = &r1.Content[i]
			break
		}
	}
	if toolUse == nil {
		return CallToolResult{Content: r1.Content}, nil
	}

	// Run the named tool in-process.
	if toolUse.Name != "echo" {
		return CallToolResult{
			Content: []ContentBlock{TextBlock("unsupported tool in agentic loop: " + toolUse.Name)},
			IsError: true,
		}, nil
	}
	toolResult, _ := rt.callEcho(toolUse.Input)
	tr, _ := toolResult.(CallToolResult)

	// Turn 2: feed tool_result back.
	turn2 := CreateMessageRequestParams{
		Messages: []SamplingMessage{
			{Role: "user", Content: []ContentBlock{TextBlock("Summarize using the echo tool if needed: " + text)}},
			{Role: "assistant", Content: r1.Content},
			{Role: "user", Content: []ContentBlock{ToolResultBlock(toolUse.ID, tr.Content, tr.IsError)}},
		},
		MaxTokens: 512,
		Tools:     []Tool{echoTool},
	}
	r2, err := rt.requestSampling(ctx, turn2)
	if err != nil {
		return CallToolResult{Content: []ContentBlock{TextBlock("agentic turn2 failed: " + err.Error())}, IsError: true}, nil
	}
	return CallToolResult{Content: r2.Content}, nil
}

// requestSampling is the typed wrapper around OutboundRouter.SendRequest
// for sampling/createMessage. Validation runs first; if the params pass,
// we either route to the in-process Sampler (the common test path) or,
// when no Sampler is wired, emit the request on the wire and wait for
// the response. Pick the path based on whether a Sampler is present —
// the demo always wires StubSampler so the stdio main never hits the
// "wait on the wire" branch.
func (rt *Router) requestSampling(ctx context.Context, req CreateMessageRequestParams) (CreateMessageResult, error) {
	if verr := validateCreateMessage(req); verr != nil {
		return CreateMessageResult{}, verr
	}
	if rt.sampler != nil {
		return rt.sampler.CreateMessage(ctx, req)
	}
	// No in-process sampler: emit on the wire.
	if rt.outbound == nil {
		return CreateMessageResult{}, errSamplerUnavailable
	}
	raw, err := rt.outbound.SendRequest(ctx, "sampling/createMessage", req)
	if err != nil {
		return CreateMessageResult{}, err
	}
	var res CreateMessageResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return CreateMessageResult{}, fmt.Errorf("sampling/createMessage: decode reply: %w", err)
	}
	return res, nil
}
