package main

// Minimal init handshake — same shape as s02 but with **prompts** and
// **completions** capabilities declared. We don't re-implement the full
// state machine here (that was s02's lesson); we keep just enough to
// pass `initialize` → `notifications/initialized` and gate every other
// method behind it.

import (
	"encoding/json"
)

// LatestProtocolVersion matches schema.ts:15.
const LatestProtocolVersion = "2025-11-25"

type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ClientCapabilities struct {
	Roots        *struct{ ListChanged bool `json:"listChanged,omitempty"` } `json:"roots,omitempty"`
	Sampling     *struct{}                                                  `json:"sampling,omitempty"`
	Experimental map[string]json.RawMessage                                 `json:"experimental,omitempty"`
}

// PromptsCapability is server-declared. listChanged=true means we may emit
// notifications/prompts/list_changed when the registry mutates (s05 itself
// keeps the registry static, but the capability is real because the wire
// shape demands a stable contract).
type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerCapabilities only declares the surface this chapter implements:
// `prompts` (with listChanged) and `completions`. Resources / tools /
// sampling are left nil so they marshal to nothing.
type ServerCapabilities struct {
	Prompts     *PromptsCapability `json:"prompts,omitempty"`
	Completions *struct{}          `json:"completions,omitempty"`
}

type InitializeRequestParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      Implementation     `json:"clientInfo"`
}

type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

// LifecycleState gates the post-initialize methods. Lifted from s02 but
// trimmed: we only care about "have we seen notifications/initialized?".
type LifecycleState int

const (
	stateUninitialized LifecycleState = iota
	stateInitializing
	stateReady
)

type Lifecycle struct {
	state LifecycleState
}

func NewLifecycle() *Lifecycle { return &Lifecycle{state: stateUninitialized} }

func (l *Lifecycle) Ready() bool { return l.state == stateReady }

// HandleInitialize parses params, builds the canonical s05 init result.
// We do **not** validate protocolVersion strictly — that's s02's job;
// here we just echo what the client sent if non-empty, otherwise default.
func (l *Lifecycle) HandleInitialize(raw json.RawMessage) (InitializeResult, *Error) {
	var p InitializeRequestParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return InitializeResult{}, &Error{Code: InvalidParams, Message: "initialize: " + err.Error()}
		}
	}
	pv := p.ProtocolVersion
	if pv == "" {
		pv = LatestProtocolVersion
	}
	l.state = stateInitializing
	return InitializeResult{
		ProtocolVersion: pv,
		Capabilities: ServerCapabilities{
			Prompts:     &PromptsCapability{ListChanged: true},
			Completions: &struct{}{},
		},
		ServerInfo: Implementation{Name: "learn-mcp-s05-prompts", Version: "0.1.0"},
		Instructions: "s05 exposes `prompts/list`, `prompts/get` and `completion/complete`. " +
			"Try `prompts/get` with name=summarize-file and arguments.path=README.md.",
	}, nil
}

// HandleInitialized flips the state to ready.
func (l *Lifecycle) HandleInitialized() { l.state = stateReady }
