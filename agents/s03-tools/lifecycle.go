package main

import (
	"encoding/json"
	"sync"
)

// LatestProtocolVersion is what this server speaks. s02 negotiates this
// properly; in s03 we just echo it back.
//
// Source: schema/2025-11-25/schema.ts:14.
const LatestProtocolVersion = "2025-11-25"

// Implementation identifies a client or server (schema.ts:464-470).
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientCapabilities — only the bits s03 demos need (schema.ts:308-340).
// Real implementations would also model roots/sampling/elicitation.
type ClientCapabilities struct {
	Experimental map[string]json.RawMessage `json:"experimental,omitempty"`
}

// ToolsCapability advertises whether the server supports `tools/list`
// and whether it emits `notifications/tools/list_changed`. s03 does NOT
// emit list-changed notifications, so we report `listChanged: false`.
//
// Source: schema.ts:388-399; server/tools.mdx:36-50.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

// ServerCapabilities — only the bits s03 needs (schema.ts:342-460).
type ServerCapabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

// InitializeRequestParams — schema.ts:251-273.
type InitializeRequestParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      Implementation     `json:"clientInfo"`
}

// InitializeResult — schema.ts:277-300.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

// LifecycleState is the three-phase server state machine
// (basic/lifecycle.mdx:36-205).
//
//   StateUninit  → server just started, only `initialize` is legal.
//   StateInit    → server replied to `initialize`, waiting for
//                  `notifications/initialized` from the client.
//   StateReady   → all other methods are now legal.
type LifecycleState int

const (
	StateUninit LifecycleState = iota
	StateInit
	StateReady
)

// Lifecycle owns the state machine. Embedded in Router so handlers can
// gate themselves on Ready (handleListTools, handleCallTool).
type Lifecycle struct {
	mu    sync.Mutex
	state LifecycleState
}

func NewLifecycle() *Lifecycle { return &Lifecycle{state: StateUninit} }

func (l *Lifecycle) State() LifecycleState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}

// HandleInitialize advances Uninit → Init and produces a result.
// s03 does not negotiate versions or merge capabilities — that's s02's
// job; we just echo the client's protocolVersion back and advertise the
// `tools` capability.
func (l *Lifecycle) HandleInitialize(params InitializeRequestParams) InitializeResult {
	l.mu.Lock()
	l.state = StateInit
	l.mu.Unlock()
	return InitializeResult{
		ProtocolVersion: params.ProtocolVersion,
		Capabilities: ServerCapabilities{
			Tools: &ToolsCapability{ListChanged: false},
		},
		ServerInfo:   Implementation{Name: "learn-mcp-s03-tools", Version: "0.1.0"},
		Instructions: "s03: a tools-capable MCP server. Try tools/list, then tools/call.",
	}
}

// HandleInitialized advances Init → Ready. The notification carries no
// payload and produces no response.
func (l *Lifecycle) HandleInitialized() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state == StateInit {
		l.state = StateReady
	}
}
