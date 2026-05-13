package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Tool — schema.ts:1251-1299. The TS interface extends BaseMetadata + Icons
// via mixin; Go has no real mixins so we inline only the fields s03 needs.
type Tool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
}

// ContentBlock — schema.ts:1742-1839. The TS source is a tagged union
// (`TextContent | ImageContent | AudioContent | ResourceLink |
// EmbeddedResource`); Go has no real sum type so we use a "fat struct"
// discriminated by `Type`. Variant constructors (TextBlock, ImageBlock)
// guarantee a valid combination.
type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Data     string          `json:"data,omitempty"`
	MIMEType string          `json:"mimeType,omitempty"`
	URI      string          `json:"uri,omitempty"`
	Resource json.RawMessage `json:"resource,omitempty"`
}

// TextBlock — TextContent variant (schema.ts:1753-1781).
func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

// ImageBlock — ImageContent variant (schema.ts:1783-1810).
// `data` MUST be base64-encoded by the caller.
func ImageBlock(b64data, mimeType string) ContentBlock {
	return ContentBlock{Type: "image", Data: b64data, MIMEType: mimeType}
}

// CallToolRequestParams — schema.ts:1141-1158.
type CallToolRequestParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallToolResult — schema.ts:1106-1138. The key field is IsError:
// per the spec, tool *execution* failures live here (so the LLM sees
// the error and can self-correct); only protocol-level failures
// (unknown tool, args fail schema) become JSON-RPC errors.
type CallToolResult struct {
	Content           []ContentBlock  `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// ListToolsResult — schema.ts:1097-1100. Pagination (nextCursor) is
// supported by the spec but s03 returns one page.
type ListToolsResult struct {
	Tools []Tool `json:"tools"`
}

// ToolHandler is implemented by each registered tool. The handler is
// free to return an error, but per spec semantics that error gets
// wrapped into a CallToolResult{IsError: true, Content: [text]} — it
// is NOT propagated to the JSON-RPC layer.
type ToolHandler interface {
	Call(args json.RawMessage) (CallToolResult, error)
}

// HandlerFunc adapts a plain function to ToolHandler.
type HandlerFunc func(args json.RawMessage) (CallToolResult, error)

func (f HandlerFunc) Call(args json.RawMessage) (CallToolResult, error) { return f(args) }

// registryEntry pairs a Tool descriptor with its runtime handler and
// pre-compiled input-schema validator.
type registryEntry struct {
	tool      Tool
	handler   ToolHandler
	validator *jsonschema.Schema // nil if InputSchema couldn't be compiled
}

// ToolRegistry is the in-memory tool table. Concurrent reads (list/call)
// are safe; writes only happen at startup.
type ToolRegistry struct {
	mu     sync.RWMutex
	tools  map[string]*registryEntry
	order  []string // preserves registration order for tools/list
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]*registryEntry)}
}

// Register adds a tool. The input schema is compiled once at
// registration time; a malformed schema is a programmer error and is
// returned synchronously.
func (r *ToolRegistry) Register(t Tool, h ToolHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name]; exists {
		return fmt.Errorf("tool already registered: %s", t.Name)
	}
	var v *jsonschema.Schema
	if len(t.InputSchema) > 0 {
		c := jsonschema.NewCompiler()
		// Synthetic URL because we feed raw bytes, not a URL.
		if err := c.AddResource(t.Name+"-input.json", bytes.NewReader(t.InputSchema)); err != nil {
			return fmt.Errorf("tool %s: add schema: %w", t.Name, err)
		}
		s, err := c.Compile(t.Name + "-input.json")
		if err != nil {
			return fmt.Errorf("tool %s: compile schema: %w", t.Name, err)
		}
		v = s
	}
	r.tools[t.Name] = &registryEntry{tool: t, handler: h, validator: v}
	r.order = append(r.order, t.Name)
	return nil
}

// List returns all registered tools in registration order.
func (r *ToolRegistry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name].tool)
	}
	return out
}

// Lookup returns the entry for name (or nil).
func (r *ToolRegistry) Lookup(name string) *registryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// handleListTools serves `tools/list`. Params (cursor) are accepted but
// ignored — we always return the single-page result.
//
// Source: server/tools.mdx:55-109; schema.ts:1086-1100.
func handleListTools(reg *ToolRegistry, _ json.RawMessage) (any, *Error) {
	return ListToolsResult{Tools: reg.List()}, nil
}

// handleCallTool serves `tools/call`. The protocol contract:
//   - Unknown tool name      → JSON-RPC error -32601 MethodNotFound.
//   - Args fail input schema → JSON-RPC error -32602 InvalidParams.
//   - Tool execution failure → CallToolResult{IsError:true, Content:[text]}.
//
// Source: server/tools.mdx:114-148; schema.ts:1106-1138.
func handleCallTool(reg *ToolRegistry, params json.RawMessage) (any, *Error) {
	var p CallToolRequestParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &Error{Code: InvalidParams, Message: "decode params: " + err.Error()}
	}
	if p.Name == "" {
		return nil, &Error{Code: InvalidParams, Message: "missing tool name"}
	}
	entry := reg.Lookup(p.Name)
	if entry == nil {
		return nil, &Error{Code: MethodNotFound, Message: "unknown tool: " + p.Name}
	}
	if entry.validator != nil {
		// jsonschema.Validate wants any-typed input; round-trip through
		// json.Unmarshal to get a generic shape.
		var any any
		args := p.Arguments
		if len(args) == 0 {
			args = []byte("{}")
		}
		if err := json.Unmarshal(args, &any); err != nil {
			return nil, &Error{Code: InvalidParams, Message: "decode arguments: " + err.Error()}
		}
		if err := entry.validator.Validate(any); err != nil {
			return nil, &Error{Code: InvalidParams, Message: "arguments fail schema: " + err.Error()}
		}
	}
	// Per spec: a panic or returned Go error becomes IsError:true, NOT
	// a JSON-RPC error. That's what gives the model a chance to self-correct.
	result, err := safeCall(entry.handler, p.Arguments)
	if err != nil {
		return CallToolResult{
			Content: []ContentBlock{TextBlock("tool error: " + err.Error())},
			IsError: true,
		}, nil
	}
	return result, nil
}

// safeCall wraps the handler in a panic recovery so a buggy tool can't
// crash the server. Panics surface as Go errors which the caller then
// turns into IsError:true results.
func safeCall(h ToolHandler, args json.RawMessage) (out CallToolResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return h.Call(args)
}

// ---- Demo tools registered by main.go ------------------------------

// echoInputSchema requires a `text` string.
var echoInputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "text": {"type": "string", "description": "Text to echo back."}
  },
  "required": ["text"]
}`)

// EchoTool is the canonical zero-side-effects tool — useful for smoke
// tests of schema validation and content-block shaping.
var EchoTool = Tool{
	Name:        "echo",
	Description: "Echoes back the `text` argument verbatim.",
	InputSchema: echoInputSchema,
}

func echoHandler(args json.RawMessage) (CallToolResult, error) {
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return CallToolResult{}, err
	}
	return CallToolResult{Content: []ContentBlock{TextBlock(p.Text)}}, nil
}

// getTimeInputSchema accepts an optional `format` (currently informational).
var getTimeInputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "format": {"type": "string", "enum": ["rfc3339", "unix"]}
  }
}`)

// GetTimeTool is a tiny stateful demo — proves that tool execution can
// touch the outside world (here, the wall clock).
var GetTimeTool = Tool{
	Name:        "get_time",
	Description: "Returns the current server time.",
	InputSchema: getTimeInputSchema,
}

// nowFn lets tests inject a fixed time.
var nowFn = time.Now

func getTimeHandler(args json.RawMessage) (CallToolResult, error) {
	var p struct {
		Format string `json:"format"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &p)
	}
	t := nowFn()
	var out string
	switch strings.ToLower(p.Format) {
	case "unix":
		out = fmt.Sprintf("%d", t.Unix())
	default:
		out = t.Format(time.RFC3339)
	}
	return CallToolResult{Content: []ContentBlock{TextBlock(out)}}, nil
}

// RegisterDemoTools wires the two demo tools into reg.
func RegisterDemoTools(reg *ToolRegistry) error {
	if err := reg.Register(EchoTool, HandlerFunc(echoHandler)); err != nil {
		return err
	}
	if err := reg.Register(GetTimeTool, HandlerFunc(getTimeHandler)); err != nil {
		return err
	}
	return nil
}
