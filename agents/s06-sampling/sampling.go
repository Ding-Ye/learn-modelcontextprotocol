package main

// Sampling — `sampling/createMessage`. The defining MCP capability:
// **the server asks the client to run an LLM**, reversing the usual
// client→server flow. The Go types here mirror schema.ts:1574-1998:
//
//   - CreateMessageRequestParams (schema.ts:1580-1624)
//   - CreateMessageResult        (schema.ts:1658-1681)
//   - SamplingMessage            (schema.ts:1683-1696)
//   - ContentBlock variants      (schema.ts:1742-1916) — including the
//     new-in-sampling `tool_use` and `tool_result` blocks at 1840-1898.
//   - ToolChoice                 (schema.ts:1628-1640)
//   - ModelPreferences           (schema.ts:1930-1979)
//   - Tool                       (schema.ts:1251-1299, re-declared)
//
// We make ContentBlock **fat-struct** (every field optional) like s03/s05
// did, but with `tool_use` / `tool_result` joining the variant set. The
// discriminator is the `type` field.

import (
	"encoding/json"
	"fmt"
)

// ----------------------------------------------------------------------------
// ContentBlock — discriminated union over `type`
//
// schema.ts:1742-1916. The s06 superset:
//   - text         (TextContent)
//   - image        (ImageContent)
//   - audio        (AudioContent)
//   - tool_use     (ToolUseContent)    NEW in sampling
//   - tool_result  (ToolResultContent) NEW in sampling
//
// Marshalling: a single struct with optional fields and a hand-rolled
// MarshalJSON that omits zero-valued ones per type. This is uglier than
// five separate Go types but keeps `[]ContentBlock` ergonomic.
// ----------------------------------------------------------------------------

type ContentBlock struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// image / audio
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`

	// tool_use
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`

	// tool_result
	ToolUseID string         `json:"toolUseId,omitempty"`
	Content   []ContentBlock `json:"content,omitempty"`
	IsError   bool           `json:"isError,omitempty"`
}

// TextBlock is the s06 constructor of choice for plain text content.
func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

// ToolUseBlock builds a `tool_use` content block (schema.ts:1840-1869).
// The LLM emits this when it wants the server to invoke a tool.
func ToolUseBlock(id, name string, input map[string]any) ContentBlock {
	return ContentBlock{Type: "tool_use", ID: id, Name: name, Input: input}
}

// ToolResultBlock builds a `tool_result` content block (schema.ts:1874-1898).
// The server feeds this back into a follow-up sampling/createMessage so
// the LLM can see what the tool returned.
func ToolResultBlock(toolUseID string, content []ContentBlock, isError bool) ContentBlock {
	return ContentBlock{Type: "tool_result", ToolUseID: toolUseID, Content: content, IsError: isError}
}

// ----------------------------------------------------------------------------
// Tool — re-declared from s03 (schema.ts:1251-1299). InputSchema is opaque
// JSON; we don't enforce JSONSchema validation here, that was s03's job.
// ----------------------------------------------------------------------------

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ----------------------------------------------------------------------------
// ToolChoice (schema.ts:1628-1640) and ModelPreferences (schema.ts:1930-1979).
// Both are advisory — the client MAY ignore them entirely.
// ----------------------------------------------------------------------------

type ToolChoice struct {
	// Mode: "auto" (default) | "required" | "none".
	Mode string `json:"mode,omitempty"`
}

type ModelHint struct {
	Name string `json:"name,omitempty"`
}

type ModelPreferences struct {
	Hints                []ModelHint `json:"hints,omitempty"`
	CostPriority         *float64    `json:"costPriority,omitempty"`
	SpeedPriority        *float64    `json:"speedPriority,omitempty"`
	IntelligencePriority *float64    `json:"intelligencePriority,omitempty"`
}

// ----------------------------------------------------------------------------
// SamplingMessage — schema.ts:1683-1696. Role is "user" or "assistant" only;
// "system" lives in `SystemPrompt`, not here.
//
// On the wire the spec allows `content` to be either a single block *or* an
// array. We always emit an array (always valid) and accept either on the
// reader path through a custom UnmarshalJSON.
// ----------------------------------------------------------------------------

type SamplingMessage struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

func (m *SamplingMessage) UnmarshalJSON(data []byte) error {
	var aux struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	m.Role = aux.Role
	if len(aux.Content) == 0 {
		m.Content = nil
		return nil
	}
	// Try array first; fall back to single object.
	var arr []ContentBlock
	if err := json.Unmarshal(aux.Content, &arr); err == nil {
		m.Content = arr
		return nil
	}
	var single ContentBlock
	if err := json.Unmarshal(aux.Content, &single); err != nil {
		return fmt.Errorf("samplingMessage.content: %w", err)
	}
	m.Content = []ContentBlock{single}
	return nil
}

// ----------------------------------------------------------------------------
// CreateMessageRequestParams — schema.ts:1580-1624.
//
// Two pointer-typed fields call out a non-obvious wire concern:
//
//   - `Temperature *float64` — 0 is a *valid, meaningful* temperature
//     (deterministic-ish). A bare `float64` would marshal `0` and we'd
//     never know whether the caller said "0" or "unset". Pointer-or-nil is
//     the only honest encoding.
//   - `MaxTokens int` is REQUIRED (no `omitempty`) but must be > 0; we
//     enforce in `validateCreateMessage` below, not via the type system.
// ----------------------------------------------------------------------------

type CreateMessageRequestParams struct {
	Messages         []SamplingMessage `json:"messages"`
	SystemPrompt     string            `json:"systemPrompt,omitempty"`
	IncludeContext   string            `json:"includeContext,omitempty"`
	Temperature      *float64          `json:"temperature,omitempty"`
	MaxTokens        int               `json:"maxTokens"`
	StopSequences    []string          `json:"stopSequences,omitempty"`
	Metadata         json.RawMessage   `json:"metadata,omitempty"`
	Tools            []Tool            `json:"tools,omitempty"`
	ToolChoice       *ToolChoice       `json:"toolChoice,omitempty"`
	ModelPreferences *ModelPreferences `json:"modelPreferences,omitempty"`
}

// CreateMessageResult — schema.ts:1658-1681. Embeds SamplingMessage's shape
// (role + content) plus `model` and `stopReason`.
//
// We *don't* embed via Go struct embedding because JSON marshalling of an
// embedded SamplingMessage with a custom UnmarshalJSON would surprise us;
// instead we restate the two fields. The duplication is small and explicit.
type CreateMessageResult struct {
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	Model      string         `json:"model"`
	StopReason string         `json:"stopReason,omitempty"`
}

// SingleContent is a convenience for the common case of "one text block,
// then assistant-role". Used by StubSampler.
func (r *CreateMessageResult) SingleContent() ContentBlock {
	if len(r.Content) == 0 {
		return ContentBlock{}
	}
	return r.Content[0]
}

// validateCreateMessage enforces the shape constraints we *can* check
// purely from the wire. The big ones:
//
//   - maxTokens > 0 (schema.ts:1606-1610 says "to prevent runaway
//     completions" — zero is nonsensical).
//   - At least one message must be present.
//   - includeContext, if set, must be one of the three enumerated values.
func validateCreateMessage(p CreateMessageRequestParams) *Error {
	if p.MaxTokens <= 0 {
		return &Error{Code: InvalidParams, Message: "sampling/createMessage: maxTokens must be > 0"}
	}
	if len(p.Messages) == 0 {
		return &Error{Code: InvalidParams, Message: "sampling/createMessage: messages must not be empty"}
	}
	switch p.IncludeContext {
	case "", "none", "thisServer", "allServers":
		// ok
	default:
		return &Error{Code: InvalidParams, Message: "sampling/createMessage: invalid includeContext: " + p.IncludeContext}
	}
	return nil
}
