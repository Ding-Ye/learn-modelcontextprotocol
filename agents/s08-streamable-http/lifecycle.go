package main

// s08 declares the same initialize/initialized lifecycle as s02-s07; the
// only twist is which capabilities we advertise. To demonstrate that the
// HTTP transport is fully duplex, we advertise both **tools** (so the
// client can POST tools/call) and **sampling** (so the server can ride
// the SSE stream back to the client with a sampling/createMessage).
//
// See basic/lifecycle.mdx:36-205 for the prose. The state machine is
// identical to s02's; we trim it here to the bare gate.

import "encoding/json"

// LatestProtocolVersion matches schema.ts:15.
const LatestProtocolVersion = "2025-11-25"

type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientCapabilities — only the fields s08 looks at. `sampling` is the
// key one; if the client did not declare sampling, the server-initiated
// sampling/createMessage in `summarize` will fail at the transport layer.
type ClientCapabilities struct {
	Roots        *struct{ ListChanged bool `json:"listChanged,omitempty"` } `json:"roots,omitempty"`
	Sampling     *struct{}                                                  `json:"sampling,omitempty"`
	Experimental map[string]json.RawMessage                                 `json:"experimental,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerCapabilities — s08 advertises tools.listChanged so a client can
// trust that adds/removes will surface as a notification. (We don't
// emit one in the demo; the capability is honest about the wire shape.)
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

// LifecycleState is per-session in s08. The map of session -> state
// lives inside SessionStore (session.go); the Lifecycle struct itself
// is just the value type.
type LifecycleState int

const (
	stateUninitialized LifecycleState = iota
	stateInitializing
	stateReady
)

// HandleInitialize builds the s08 InitializeResult. We *do* mirror
// the protocolVersion sent by the client; lifecycle.mdx:120 says the
// server SHOULD respond with the same version if it supports it, and
// only fall back when it doesn't.
func handleInitialize(raw json.RawMessage) (InitializeResult, *Error) {
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
	return InitializeResult{
		ProtocolVersion: pv,
		Capabilities: ServerCapabilities{
			Tools: &ToolsCapability{ListChanged: true},
		},
		ServerInfo: Implementation{Name: "learn-mcp-s08-streamable-http", Version: "0.1.0"},
		Instructions: "s08 runs over Streamable HTTP. POST /mcp with " +
			"Mcp-Session-Id; the `summarize` tool will upgrade the response " +
			"to SSE and round-trip a sampling/createMessage before returning " +
			"its CallToolResult.",
	}, nil
}
