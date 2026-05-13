package main

// Sampler — the client-side LLM-call seam. In a real MCP client this would
// route to whichever provider the host has wired (Anthropic, OpenAI,
// llama.cpp, ...). In s06 the server *contains* a stub client so we can
// drive the whole loop in a single process and test deterministically.
//
// Two implementations ship:
//
//   - StubSampler   — canned response. Tests use this exclusively.
//   - AnthropicSampler — skeleton with TODO body. The real HTTP wiring is
//     deferred to Phase G of the curriculum (multi-model addendum); the
//     stub here just demonstrates the shape so learners can see where
//     a real provider would plug in.
//
// The interface intentionally takes the *parsed* CreateMessageRequestParams,
// not a raw `json.RawMessage`. Reason: a Sampler is the place where typed
// validation pays off — the wire-level checks happened in router/outbound
// code; here the implementer can rely on the params being well-formed.

import (
	"context"
	"errors"
	"fmt"
)

// Sampler is the LLM-call seam. CreateMessage is synchronous from the
// caller's POV; long-running providers should internally stream.
type Sampler interface {
	CreateMessage(ctx context.Context, req CreateMessageRequestParams) (CreateMessageResult, error)
}

// ----------------------------------------------------------------------------
// StubSampler — deterministic canned responses, configurable per call.
//
// Two knobs control behaviour:
//
//   - TextReply: when non-empty, return a single text block with this
//     content. Used by the "plain reply" test.
//   - ToolUseReply: when non-zero, return a tool_use block (one per call,
//     until the queue empties). Used by the agentic loop test where the
//     first call requests a tool, the second call (after the tool has
//     run) returns a plain text answer.
//
// If `ToolUseReply` queue is non-empty *and* TextReply is set, tool_use
// drains first; text comes after the queue is empty. That's how the
// two-turn agentic loop is encoded in the test.
// ----------------------------------------------------------------------------

type StubSampler struct {
	// Model is echoed verbatim in CreateMessageResult.Model.
	Model string
	// TextReply is the body of the plain-text response (when no
	// tool_use is queued).
	TextReply string
	// ToolUseQueue is consumed FIFO; each call pops one entry.
	ToolUseQueue []ContentBlock
	// LastRequest records the last params seen (lets tests assert
	// that round-tripped fields like includeContext were preserved).
	LastRequest CreateMessageRequestParams
	// CallCount counts CreateMessage invocations.
	CallCount int
}

func (s *StubSampler) CreateMessage(_ context.Context, req CreateMessageRequestParams) (CreateMessageResult, error) {
	s.CallCount++
	s.LastRequest = req

	model := s.Model
	if model == "" {
		model = "stub-model-v0"
	}
	// tool_use drains first.
	if len(s.ToolUseQueue) > 0 {
		blk := s.ToolUseQueue[0]
		s.ToolUseQueue = s.ToolUseQueue[1:]
		return CreateMessageResult{
			Role:       "assistant",
			Content:    []ContentBlock{blk},
			Model:      model,
			StopReason: "toolUse",
		}, nil
	}
	text := s.TextReply
	if text == "" {
		text = "(stub: no reply configured)"
	}
	return CreateMessageResult{
		Role:       "assistant",
		Content:    []ContentBlock{TextBlock(text)},
		Model:      model,
		StopReason: "endTurn",
	}, nil
}

// EnqueueToolUse pushes a tool_use response onto the queue. Convenience
// for tests so the call site reads top-down.
func (s *StubSampler) EnqueueToolUse(id, name string, input map[string]any) {
	s.ToolUseQueue = append(s.ToolUseQueue, ToolUseBlock(id, name, input))
}

// ----------------------------------------------------------------------------
// AnthropicSampler — skeleton.
//
// Real implementations need:
//
//  1. Translate `CreateMessageRequestParams` to the provider's messages API
//     payload. SystemPrompt → top-level system field; the SamplingMessage
//     array maps 1:1 (Anthropic uses the same role+content shape and even
//     the same tool_use / tool_result block names).
//  2. POST to the provider; collect streamed deltas if streaming.
//  3. Translate back to `CreateMessageResult`; preserve stopReason from
//     the provider's `stop_reason` field.
//
// We don't ship the HTTP body in s06 because Phase G of the curriculum
// covers multi-provider wiring; here we just keep the shape so the type
// check passes and a learner can see exactly which method to fill in.
// ----------------------------------------------------------------------------

type AnthropicSampler struct {
	APIKey  string
	BaseURL string // defaults to https://api.anthropic.com
	Model   string // e.g. "claude-3-5-sonnet-latest"
}

// CreateMessage is intentionally unimplemented. Phase G fills the body.
func (s *AnthropicSampler) CreateMessage(_ context.Context, _ CreateMessageRequestParams) (CreateMessageResult, error) {
	return CreateMessageResult{}, errors.New("AnthropicSampler: not implemented (deferred to Phase G; use StubSampler in s06 tests)")
}

// errSamplerUnavailable is the canonical error when the server tries to
// emit a sampling/createMessage but no Sampler is wired. Tests don't
// trigger this (we always wire StubSampler), but main.go references it.
var errSamplerUnavailable = fmt.Errorf("no sampler configured")
