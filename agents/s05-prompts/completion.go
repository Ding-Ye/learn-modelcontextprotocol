package main

// completion/complete is a tiny but **structurally important** RPC: it
// shows the only "reference union" in the entire surface of s05. The
// client says "I'm typing the value of argument X on prompt Y, give me
// candidates", and the server returns up to 100 strings. See
// schema.ts:2006-2088 and server/prompts.mdx:101-102.
//
// Why a union: a completion can target a *prompt argument* (ref/prompt
// + name) or a *resource-template variable* (ref/resource + uri). In
// Go we model it as a single struct discriminated by the `Type` field;
// the same trick the TS spec uses internally before TypeScript's type
// system collapses it to a union at compile time.

import (
	"encoding/json"
	"sort"
	"strings"
)

// CompletionReference is the `ref` field of CompleteRequestParams
// (schema.ts:2070-2087). Exactly one of {Name, URI} is meaningful for
// each Type:
//
//	Type = "ref/prompt"    → Name carries the prompt name
//	Type = "ref/resource"  → URI carries the resource-template URI
type CompletionReference struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	URI  string `json:"uri,omitempty"`
}

// CompleteArgument is the inner `argument` object — schema.ts:2011-2020.
type CompleteArgument struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CompleteContext is the optional `context` object — schema.ts:2025-2030.
// We don't *use* the context dictionary for matching in s05 (the demo
// dictionary is one-dimensional), but we accept it so a real client's
// request decodes cleanly.
type CompleteContext struct {
	Arguments map[string]string `json:"arguments,omitempty"`
}

// CompleteRequest is the params block of completion/complete
// (schema.ts:2006-2031).
type CompleteRequest struct {
	Ref      CompletionReference `json:"ref"`
	Argument CompleteArgument    `json:"argument"`
	Context  *CompleteContext    `json:"context,omitempty"`
}

// CompletionPayload is the inner `completion` object on the reply
// (schema.ts:2048-2062). It's nested one level because the spec keeps
// it future-proofed for additional sibling fields.
type CompletionPayload struct {
	Values  []string `json:"values"`
	Total   *int     `json:"total,omitempty"`
	HasMore bool     `json:"hasMore,omitempty"`
}

// CompleteResult is the body of the completion/complete reply
// (schema.ts:2048).
type CompleteResult struct {
	Completion CompletionPayload `json:"completion"`
}

// completionDictionary is the canned candidate set per (prompt, argument).
// In a real server, this would defer to a filesystem walk, a database
// query, an LLM, etc. Hard-coding it keeps the lesson zero-dependency.
var completionDictionary = map[string]map[string][]string{
	"summarize-file": {
		"path": {
			"README.md",
			"README.en.md",
			"go.mod",
			"go.work",
			"agents/s01-min-loop/main.go",
			"agents/s05-prompts/main.go",
			"docs/en/s05-prompts.md",
			"docs/zh/s05-prompts.md",
		},
		"style": {"bullets", "prose", "tldr"},
	},
}

// handleComplete dispatches completion/complete. Unknown ref.type is
// the canonical -32602 path (one of the five required tests).
func handleComplete(r *PromptRegistry, raw json.RawMessage) (any, *Error) {
	var req CompleteRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &Error{Code: InvalidParams, Message: "completion/complete: " + err.Error()}
	}
	switch req.Ref.Type {
	case "ref/prompt":
		return completeForPrompt(r, req)
	case "ref/resource":
		// s05 doesn't ship resource templates (that's s04), but the wire
		// shape demands we accept the ref/resource type and either return
		// candidates or an empty list. We return an empty list so the
		// client behaves correctly.
		return CompleteResult{Completion: CompletionPayload{Values: []string{}}}, nil
	case "":
		return nil, &Error{Code: InvalidParams, Message: "completion/complete: ref.type is required"}
	default:
		return nil, &Error{Code: InvalidParams, Message: "completion/complete: unknown ref.type: " + req.Ref.Type}
	}
}

func completeForPrompt(r *PromptRegistry, req CompleteRequest) (CompleteResult, *Error) {
	if req.Ref.Name == "" {
		return CompleteResult{}, &Error{Code: InvalidParams, Message: "completion/complete: ref.name required for ref/prompt"}
	}
	if _, _, ok := r.Lookup(req.Ref.Name); !ok {
		return CompleteResult{}, &Error{Code: MethodNotFound, Message: "prompt not found: " + req.Ref.Name}
	}
	argSet, ok := completionDictionary[req.Ref.Name]
	if !ok {
		return CompleteResult{Completion: CompletionPayload{Values: []string{}}}, nil
	}
	candidates, ok := argSet[req.Argument.Name]
	if !ok {
		return CompleteResult{Completion: CompletionPayload{Values: []string{}}}, nil
	}
	// Prefix-match against the partial value the user has typed so far.
	// Case-insensitive because filenames on macOS/Windows are.
	prefix := strings.ToLower(req.Argument.Value)
	matches := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if prefix == "" || strings.HasPrefix(strings.ToLower(c), prefix) {
			matches = append(matches, c)
		}
	}
	sort.Strings(matches)
	total := len(matches)
	// Schema caps `values` at 100; trim if we ever exceed it (we won't
	// in the demo, but a learner reading this needs to see the rule).
	hasMore := false
	if len(matches) > 100 {
		matches = matches[:100]
		hasMore = true
	}
	return CompleteResult{
		Completion: CompletionPayload{
			Values:  matches,
			Total:   &total,
			HasMore: hasMore,
		},
	}, nil
}
