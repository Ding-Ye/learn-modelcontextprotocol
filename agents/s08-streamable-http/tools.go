package main

// Minimal tool surface to drive the s08 demo. Re-declared from s03 with
// the s06-style sampling-aware Call signature: a tool's Call receives a
// Sampler interface it can use to reach back into the client via
// sampling/createMessage. The Sampler is supplied by the server's per-
// request context, *not* baked into the registry — so the same tool can
// run with a real sampling round-trip in production or a stub in tests.

import (
	"context"
	"encoding/json"
	"fmt"
)

// ContentBlock is the s03 shape re-declared a seventh time.
type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Data     string          `json:"data,omitempty"`
	MIMEType string          `json:"mimeType,omitempty"`
	URI      string          `json:"uri,omitempty"`
	Resource json.RawMessage `json:"resource,omitempty"`
}

func TextBlock(text string) ContentBlock { return ContentBlock{Type: "text", Text: text} }

type Tool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
}

type CallToolResult struct {
	Content           []ContentBlock  `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

type ListToolsResult struct {
	Tools []Tool `json:"tools"`
}

// SamplingMessage / CreateMessageRequest / Result — s06 shapes, trimmed
// to what the `summarize` tool actually uses.
type SamplingMessage struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

type CreateMessageRequest struct {
	Messages     []SamplingMessage `json:"messages"`
	SystemPrompt string            `json:"systemPrompt,omitempty"`
	MaxTokens    int               `json:"maxTokens"`
}

type CreateMessageResult struct {
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	Model      string         `json:"model"`
	StopReason string         `json:"stopReason,omitempty"`
}

// Sampler is the per-request injection point. ToolHandler.Call receives
// one, and either uses it (sampling-driven tool) or ignores it (pure tool).
type Sampler interface {
	CreateMessage(ctx context.Context, req CreateMessageRequest) (CreateMessageResult, error)
}

type ToolHandler interface {
	Call(ctx context.Context, sampler Sampler, args json.RawMessage) (CallToolResult, error)
}

type ToolRegistry struct {
	order    []string
	tools    map[string]Tool
	handlers map[string]ToolHandler
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: map[string]Tool{}, handlers: map[string]ToolHandler{}}
}

func (r *ToolRegistry) Register(t Tool, h ToolHandler) {
	if _, ok := r.tools[t.Name]; !ok {
		r.order = append(r.order, t.Name)
	}
	r.tools[t.Name] = t
	r.handlers[t.Name] = h
}

func (r *ToolRegistry) List() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.tools[n])
	}
	return out
}

func (r *ToolRegistry) Lookup(name string) (ToolHandler, bool) {
	h, ok := r.handlers[name]
	return h, ok
}

// ----------------------------------------------------------------------------
// Demo tool: summarize
//
// `summarize` takes a `text` arg and asks the *client's* LLM to produce a
// summary by calling sampling/createMessage back through the SSE stream.
// The tool returns the LLM's reply (or the literal text, if no Sampler was
// supplied — useful in tests that don't want to fake a sampling client).
// ----------------------------------------------------------------------------

var SummarizeTool = Tool{
	Name:        "summarize",
	Description: "Summarize the input text by asking the host LLM via sampling.",
	InputSchema: json.RawMessage(`{
        "type":"object",
        "properties":{
            "text":{"type":"string","description":"Text to summarize."}
        },
        "required":["text"]
    }`),
}

type summarizeArgs struct {
	Text string `json:"text"`
}

type summarizeHandler struct{}

func (summarizeHandler) Call(ctx context.Context, sampler Sampler, args json.RawMessage) (CallToolResult, error) {
	var a summarizeArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return CallToolResult{IsError: true, Content: []ContentBlock{TextBlock("invalid args: " + err.Error())}}, nil
		}
	}
	if a.Text == "" {
		return CallToolResult{IsError: true, Content: []ContentBlock{TextBlock("`text` is required")}}, nil
	}
	if sampler == nil {
		// No Sampler — return the original text, prefixed so a test can
		// distinguish "we ran" from "sampling worked".
		return CallToolResult{Content: []ContentBlock{TextBlock("(no sampler) " + a.Text)}}, nil
	}
	req := CreateMessageRequest{
		Messages: []SamplingMessage{
			{Role: "user", Content: []ContentBlock{TextBlock("Summarize the following:\n\n" + a.Text)}},
		},
		SystemPrompt: "You are a concise summarizer. Return one sentence.",
		MaxTokens:    256,
	}
	res, err := sampler.CreateMessage(ctx, req)
	if err != nil {
		return CallToolResult{IsError: true, Content: []ContentBlock{TextBlock("sampling failed: " + err.Error())}}, nil
	}
	if len(res.Content) == 0 {
		return CallToolResult{IsError: true, Content: []ContentBlock{TextBlock("sampling returned no content")}}, nil
	}
	return CallToolResult{Content: res.Content}, nil
}

func RegisterDemoTool(r *ToolRegistry) {
	r.Register(SummarizeTool, summarizeHandler{})
}

// callToolParams is the request body for tools/call (schema.ts:1155-1165).
type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// handleListTools / handleCallTool are the router-facing entry points.
// handleCallTool is unusual: it returns a `(needsSampling, ...)` tuple
// because the server handler decides at call time whether to upgrade
// the response to SSE. We model that with a callback: the server passes
// in a Sampler that, if invoked, will cause the upgrade.
func handleListTools(reg *ToolRegistry) (any, *Error) {
	return ListToolsResult{Tools: reg.List()}, nil
}

func handleCallTool(ctx context.Context, reg *ToolRegistry, sampler Sampler, raw json.RawMessage) (CallToolResult, *Error) {
	var p callToolParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return CallToolResult{}, &Error{Code: InvalidParams, Message: "tools/call: " + err.Error()}
	}
	if p.Name == "" {
		return CallToolResult{}, &Error{Code: InvalidParams, Message: "tools/call: missing name"}
	}
	h, ok := reg.Lookup(p.Name)
	if !ok {
		return CallToolResult{}, &Error{Code: MethodNotFound, Message: "tool not found: " + p.Name}
	}
	res, err := h.Call(ctx, sampler, p.Arguments)
	if err != nil {
		return CallToolResult{}, &Error{Code: InternalError, Message: fmt.Sprintf("tool %s: %s", p.Name, err.Error())}
	}
	return res, nil
}
