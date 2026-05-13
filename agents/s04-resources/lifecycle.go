package main

import (
	"encoding/json"
	"errors"
	"sync"
)

// LatestProtocolVersion is the MCP version this chapter targets.
// (schema/2025-11-25/schema.ts)
const LatestProtocolVersion = "2025-11-25"

// Implementation identifies a client or server (schema.ts:275).
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ResourcesCapability is the capability flag block from
// docs/specification/2025-11-25/server/resources.mdx:30-79.
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

type ServerCapabilities struct {
	Resources *ResourcesCapability `json:"resources,omitempty"`
}

type ClientCapabilities struct {
	Roots        *struct{ ListChanged bool `json:"listChanged,omitempty"` } `json:"roots,omitempty"`
	Sampling     *struct{}                                                  `json:"sampling,omitempty"`
	Experimental map[string]json.RawMessage                                 `json:"experimental,omitempty"`
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

// lifecycle is the trivial state machine: a server must see `initialize` and
// then `notifications/initialized` before it processes any other method.
// (lifecycle.mdx). s04 uses the minimal version — the full state machine
// belongs to s02.
type lifecycle struct {
	mu          sync.Mutex
	initialized bool
}

var errNotInitialized = errors.New("server not initialized")

func (l *lifecycle) markInitialized() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initialized = true
}

func (l *lifecycle) ready() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.initialized
}

func handleInitialize(_ json.RawMessage) (any, *Error) {
	// We accept whatever protocolVersion the client sent — version
	// negotiation is the s02 chapter's job. s04 only needs the wire OK.
	return InitializeResult{
		ProtocolVersion: LatestProtocolVersion,
		Capabilities: ServerCapabilities{
			Resources: &ResourcesCapability{Subscribe: true, ListChanged: true},
		},
		ServerInfo:   Implementation{Name: "learn-mcp-s04-resources", Version: "0.1.0"},
		Instructions: "s04: resources. Try resources/list, resources/read, resources/templates/list, resources/subscribe.",
	}, nil
}
