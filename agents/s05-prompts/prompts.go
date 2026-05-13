package main

// Prompts are **user-controlled** templates. Tools are model-controlled
// ("the LLM decides when to call"), resources are app-controlled ("the
// app decides what context to attach"), prompts are user-controlled
// ("the user invokes a slash command"). Same wire shape, different
// consumer. See server/prompts.mdx:28-174.
//
// The four types here mirror schema.ts:923-1081 directly:
//
//   - Prompt           (schema.ts:986-1001)  — listed by prompts/list
//   - PromptArgument   (schema.ts:1008-1017) — argument declaration
//   - GetPromptResult  (schema.ts:973-979)   — body of prompts/get reply
//   - PromptMessage    (schema.ts:1034-1037) — one message in the result

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ContentBlock is re-declared from s03 (schema.ts:1742-1839). In s05 we
// only use the `text` variant for the demo, but the struct stays "fat"
// so a future demo can carry a `resource_link` or an embedded resource
// without changing the type.
type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Data     string          `json:"data,omitempty"`
	MIMEType string          `json:"mimeType,omitempty"`
	URI      string          `json:"uri,omitempty"`
	Resource json.RawMessage `json:"resource,omitempty"`
}

// TextBlock is the s05 constructor of choice; the demo prompt only ever
// returns text content.
func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

// PromptArgument declares one argument of a prompt template (schema.ts:1008).
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Prompt is the metadata advertised by prompts/list (schema.ts:986).
// We intentionally drop `_meta` and `icons` — the lesson is the
// substitution mechanic, not metadata plumbing.
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// GetPromptRequestParams matches schema.ts:947-956. Note that `arguments`
// is `map[string]string` — every value arrives as a JSON string, even if
// the underlying intent is numeric. The substitution layer must accept
// strings only.
type GetPromptRequestParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// GetPromptResult is the body of the prompts/get reply (schema.ts:973).
type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// PromptMessage carries a role and a content block (schema.ts:1034).
type PromptMessage struct {
	Role    string       `json:"role"` // "user" | "assistant"
	Content ContentBlock `json:"content"`
}

// ListPromptsResult is the body of the prompts/list reply (schema.ts:938).
// We don't implement pagination — the cursor is always omitted.
type ListPromptsResult struct {
	Prompts []Prompt `json:"prompts"`
}

// PromptHandler is the per-prompt callback. The registry holds one of
// these per prompt name. Returning a non-nil error becomes a JSON-RPC
// error response — typically -32602 InvalidParams.
//
// Why an interface (not a func): two reasons. (1) Future prompts may
// want to carry state (a file watcher, a DB handle). (2) Mocking in
// tests reads more naturally as a struct.
type PromptHandler interface {
	Get(args map[string]string) (GetPromptResult, error)
}

// PromptRegistry holds the (name → metadata, handler) pairs.
// The order of registration determines listing order — Go maps are
// unordered, so we keep a parallel slice of names.
type PromptRegistry struct {
	order    []string
	prompts  map[string]Prompt
	handlers map[string]PromptHandler
}

func NewPromptRegistry() *PromptRegistry {
	return &PromptRegistry{
		prompts:  map[string]Prompt{},
		handlers: map[string]PromptHandler{},
	}
}

func (r *PromptRegistry) Register(p Prompt, h PromptHandler) {
	if _, ok := r.prompts[p.Name]; !ok {
		r.order = append(r.order, p.Name)
	}
	r.prompts[p.Name] = p
	r.handlers[p.Name] = h
}

func (r *PromptRegistry) List() []Prompt {
	out := make([]Prompt, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.prompts[n])
	}
	return out
}

func (r *PromptRegistry) Lookup(name string) (Prompt, PromptHandler, bool) {
	p, ok := r.prompts[name]
	if !ok {
		return Prompt{}, nil, false
	}
	return p, r.handlers[name], true
}

// substitute replaces every `{{arg}}` occurrence in `tmpl` with
// `args[arg]`. Missing keys leave the placeholder untouched on purpose:
// in real life you'd rather see `{{unset}}` in the prompt than a silent
// empty string. Required-ness is enforced *before* substitution by
// validateArgs (called from the demo handler).
func substitute(tmpl string, args map[string]string) string {
	if len(args) == 0 {
		return tmpl
	}
	out := tmpl
	for k, v := range args {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

// validateArgs reports the first missing-required argument, if any.
// We return a *Error directly because the result wires straight into
// the router's error path.
func validateArgs(decl []PromptArgument, args map[string]string) *Error {
	for _, a := range decl {
		if !a.Required {
			continue
		}
		v, ok := args[a.Name]
		if !ok || v == "" {
			return &Error{
				Code:    InvalidParams,
				Message: fmt.Sprintf("missing required argument %q", a.Name),
			}
		}
	}
	return nil
}

// ----------------------------------------------------------------------------
// Demo prompt: summarize-file
//
// The demo is intentionally **stateless**: it doesn't read the file at
// `path`, it just embeds the path into a one-shot user message asking the
// LLM to summarize it. That keeps the lesson focused on the template
// substitution machinery (not filesystem I/O, which is s04's territory).
// ----------------------------------------------------------------------------

// SummarizeFilePrompt is the demo template advertised in prompts/list.
var SummarizeFilePrompt = Prompt{
	Name:        "summarize-file",
	Description: "Ask the LLM to summarize the file at the given path.",
	Arguments: []PromptArgument{
		{
			Name:        "path",
			Description: "Path to the file to summarize.",
			Required:    true,
		},
		{
			Name:        "style",
			Description: "Output style: bullets|prose|tldr (default: prose).",
			Required:    false,
		},
	},
}

type summarizeFileHandler struct{}

// Get builds the user-role message. The `{{style}}` placeholder is
// optional — if the caller omits it, the raw `{{style}}` token survives
// substitution; we collapse it to "prose" so the rendered prompt is
// always readable.
func (summarizeFileHandler) Get(args map[string]string) (GetPromptResult, error) {
	if err := validateArgs(SummarizeFilePrompt.Arguments, args); err != nil {
		return GetPromptResult{}, err
	}
	// Fill defaults before substitution.
	if _, ok := args["style"]; !ok {
		if args == nil {
			args = map[string]string{}
		}
		args["style"] = "prose"
	}
	const tmpl = "Please summarize the file at `{{path}}` in {{style}} form. " +
		"Focus on what the code or document is *for*, not a line-by-line restatement."
	body := substitute(tmpl, args)
	return GetPromptResult{
		Description: "summarize the file at " + args["path"],
		Messages: []PromptMessage{
			{Role: "user", Content: TextBlock(body)},
		},
	}, nil
}

// RegisterDemoPrompt wires the summarize-file prompt into a registry.
// main.go and the tests both go through this constructor so the demo
// surface is defined in exactly one place.
func RegisterDemoPrompt(r *PromptRegistry) {
	r.Register(SummarizeFilePrompt, summarizeFileHandler{})
}

// ----------------------------------------------------------------------------
// Router-facing handlers
//
// Both return a (result, *Error) pair. The router serializes whichever
// is non-nil. We don't use Go's `error` here because *Error carries the
// JSON-RPC error code which a vanilla error doesn't.
// ----------------------------------------------------------------------------

func handleListPrompts(r *PromptRegistry, _ json.RawMessage) (any, *Error) {
	return ListPromptsResult{Prompts: r.List()}, nil
}

func handleGetPrompt(r *PromptRegistry, raw json.RawMessage) (any, *Error) {
	var p GetPromptRequestParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &Error{Code: InvalidParams, Message: "prompts/get: " + err.Error()}
	}
	if p.Name == "" {
		return nil, &Error{Code: InvalidParams, Message: "prompts/get: missing name"}
	}
	_, handler, ok := r.Lookup(p.Name)
	if !ok {
		return nil, &Error{Code: MethodNotFound, Message: "prompt not found: " + p.Name}
	}
	res, err := handler.Get(p.Arguments)
	if err != nil {
		// If the handler returned an *Error, preserve its code.
		if e, ok := err.(*Error); ok {
			return nil, e
		}
		return nil, &Error{Code: InternalError, Message: err.Error()}
	}
	return res, nil
}
