package main

// Minimal init handshake — same shape as s02..s06, but the client side now
// advertises **roots** and **elicitation** capabilities. s07's server has
// no new capability of its own; it consumes capabilities the client
// declares. So most of this file mirrors s05, but `ClientCapabilities`
// gains the `Elicitation` block, and we document the rule from
// `client/elicitation.mdx`: an empty `elicitation: {}` is back-compat for
// "form only"; declaring both modes requires `{form: {}, url: {}}`.

import (
	"encoding/json"
)

// LatestProtocolVersion matches schema.ts:15.
const LatestProtocolVersion = "2025-11-25"

type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// RootsCapability is client-declared (schema.ts:367-371,
// client/roots.mdx#capabilities). `listChanged: true` means the client
// will emit `notifications/roots/list_changed` when its root set
// mutates; the server then re-issues `roots/list`.
type RootsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ElicitationCapability is client-declared (client/elicitation.mdx#capabilities).
// Spec rule: clients declaring `elicitation` MUST support at least one
// mode (form or url). An empty `elicitation: {}` is back-compat sugar
// for "form only". We declare both explicitly so the s07 server has
// permission to send either kind.
type ElicitationCapability struct {
	Form *struct{} `json:"form,omitempty"`
	URL  *struct{} `json:"url,omitempty"`
}

type ClientCapabilities struct {
	Roots        *RootsCapability                     `json:"roots,omitempty"`
	Sampling     *struct{}                            `json:"sampling,omitempty"`
	Elicitation  *ElicitationCapability               `json:"elicitation,omitempty"`
	Experimental map[string]json.RawMessage           `json:"experimental,omitempty"`
}

// ServerCapabilities for s07 advertises `tools` only — the demo exposes a
// single `who-are-you` tool that triggers an elicitation. roots and
// elicitation are *client* capabilities; the server only declares its
// own surface.
type ServerCapabilities struct {
	Tools *struct {
		ListChanged bool `json:"listChanged,omitempty"`
	} `json:"tools,omitempty"`
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

// LifecycleState gates post-initialize methods. Trimmed from s02.
type LifecycleState int

const (
	stateUninitialized LifecycleState = iota
	stateInitializing
	stateReady
)

type Lifecycle struct {
	state                  LifecycleState
	rootsSupported         bool
	elicitFormSupported    bool
	elicitURLSupported     bool
}

func NewLifecycle() *Lifecycle { return &Lifecycle{state: stateUninitialized} }

func (l *Lifecycle) Ready() bool             { return l.state == stateReady }
func (l *Lifecycle) RootsSupported() bool    { return l.rootsSupported }
func (l *Lifecycle) FormElicitOK() bool      { return l.elicitFormSupported }
func (l *Lifecycle) URLElicitOK() bool       { return l.elicitURLSupported }

// HandleInitialize parses params, records which client capabilities are
// present (so server-initiated requests can refuse to go out for modes
// the client never declared), and replies. We do NOT validate
// protocolVersion strictly; that was s02's lesson.
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
	if p.Capabilities.Roots != nil {
		l.rootsSupported = true
	}
	if p.Capabilities.Elicitation != nil {
		// Per the elicitation spec: empty object means form-only.
		if p.Capabilities.Elicitation.Form == nil && p.Capabilities.Elicitation.URL == nil {
			l.elicitFormSupported = true
		} else {
			l.elicitFormSupported = p.Capabilities.Elicitation.Form != nil
			l.elicitURLSupported = p.Capabilities.Elicitation.URL != nil
		}
	}
	l.state = stateInitializing
	return InitializeResult{
		ProtocolVersion: pv,
		Capabilities: ServerCapabilities{
			Tools: &struct {
				ListChanged bool `json:"listChanged,omitempty"`
			}{ListChanged: false},
		},
		ServerInfo: Implementation{Name: "learn-mcp-s07-roots-elicitation", Version: "0.1.0"},
		Instructions: "s07 demonstrates client capabilities roots/list and elicitation/create. " +
			"Call tools/list then tools/call name=who-are-you to trigger an elicitation form.",
	}, nil
}

// HandleInitialized flips state to ready.
func (l *Lifecycle) HandleInitialized() { l.state = stateReady }
