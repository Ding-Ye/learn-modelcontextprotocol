package main

// Minimal init handshake — same shape as s02, but capability declarations
// are tuned for sampling:
//
//   - **Client-side capability**: `sampling: {}` (the client *can* run an LLM
//     on the server's behalf). The server consults this before emitting
//     `sampling/createMessage` — no `sampling` capability ⇒ method not
//     available. We record what the client sent but don't enforce — the
//     demo wires a StubSampler that always works.
//   - **Server-side capability**: `tools` (the demo `summarize` tool is the
//     trigger that fires `sampling/createMessage`). Sampling itself is a
//     server-initiated *direction*, not a server-declared capability.
//
// We don't re-implement the full state machine here (that was s02's
// lesson); we keep just enough to gate post-initialize methods.

import (
	"encoding/json"
)

// LatestProtocolVersion matches schema.ts:15.
const LatestProtocolVersion = "2025-11-25"

type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientCapabilities — only fields s06 cares about. `Sampling` is the
// presence-only marker (schema.ts:368-372): if the client advertises it,
// the server is allowed to call `sampling/createMessage` back.
type ClientCapabilities struct {
	Roots        *struct{ ListChanged bool `json:"listChanged,omitempty"` } `json:"roots,omitempty"`
	Sampling     *struct{}                                                  `json:"sampling,omitempty"`
	Experimental map[string]json.RawMessage                                 `json:"experimental,omitempty"`
}

// ToolsCapability is server-declared. listChanged=true means we may emit
// notifications/tools/list_changed when the registry mutates. The s06 demo
// keeps the registry static, but we still declare the bit so the wire
// shape matches what s03 taught.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerCapabilities only declares the surface this chapter implements:
// `tools` — the demo `summarize` tool is what triggers a server→client
// `sampling/createMessage`. Sampling itself isn't a server capability.
type ServerCapabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
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
// trimmed: we only care about "have we seen notifications/initialized?"
// and "did the client advertise sampling?".
type LifecycleState int

const (
	stateUninitialized LifecycleState = iota
	stateInitializing
	stateReady
)

type Lifecycle struct {
	state          LifecycleState
	clientSampling bool
}

func NewLifecycle() *Lifecycle { return &Lifecycle{state: stateUninitialized} }

func (l *Lifecycle) Ready() bool                { return l.state == stateReady }
func (l *Lifecycle) ClientSupportsSampling() bool { return l.clientSampling }

// HandleInitialize parses params, records client-side sampling support,
// and builds the canonical s06 init result.
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
	l.clientSampling = p.Capabilities.Sampling != nil
	l.state = stateInitializing
	return InitializeResult{
		ProtocolVersion: pv,
		Capabilities: ServerCapabilities{
			Tools: &ToolsCapability{ListChanged: true},
		},
		ServerInfo: Implementation{Name: "learn-mcp-s06-sampling", Version: "0.1.0"},
		Instructions: "s06 wires `tools/call` to a server-initiated `sampling/createMessage`. " +
			"Try `tools/call` with name=summarize and arguments.text=...; the server will ask the client " +
			"to run the LLM and stream back text.",
	}, nil
}

// HandleInitialized flips the state to ready.
func (l *Lifecycle) HandleInitialized() { l.state = stateReady }
