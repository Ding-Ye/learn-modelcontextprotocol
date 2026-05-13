package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// LatestProtocolVersion is the schema date this chapter implements
// (schema/2025-11-25/schema.ts:13).
const LatestProtocolVersion = "2025-11-25"

// Implementation mirrors schema.ts:546-558 Implementation block: a
// loosely-typed name+version pair used for both clientInfo and
// serverInfo. We intentionally keep only the two MUST-have fields; the
// schema also defines title/description/icons/websiteUrl as optional
// extras (chapter s05 visits BaseMetadata more thoroughly).
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientCapabilities mirrors schema.ts:308-381. The interesting design
// move here: optional capability *sub-objects* are represented as
// pointer-to-struct so we can distinguish "absent" from "present but
// empty". A `*struct{}` that is nil serializes to nothing; a non-nil
// `&struct{}{}` serializes to `{}` and signals "feature on".
type ClientCapabilities struct {
	Experimental map[string]json.RawMessage `json:"experimental,omitempty"`
	Roots        *RootsClientCapability     `json:"roots,omitempty"`
	Sampling     *SamplingClientCapability  `json:"sampling,omitempty"`
	Elicitation  *ElicitationCapability     `json:"elicitation,omitempty"`
}

type RootsClientCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type SamplingClientCapability struct {
	// Per schema.ts:332-339 the body is open: presence of any non-nil
	// field signals the corresponding feature. We carry it as raw JSON
	// so s06 can read it without re-touching this file.
	Context json.RawMessage `json:"context,omitempty"`
	Tools   json.RawMessage `json:"tools,omitempty"`
}

type ElicitationCapability struct {
	Form json.RawMessage `json:"form,omitempty"`
	URL  json.RawMessage `json:"url,omitempty"`
}

// ServerCapabilities mirrors schema.ts:388-459. Same pointer-sub-object
// pattern as ClientCapabilities; nil means "the server does not advertise
// this feature at all" (clients then know not to send those methods).
type ServerCapabilities struct {
	Experimental map[string]json.RawMessage `json:"experimental,omitempty"`
	Logging      *LoggingCapability         `json:"logging,omitempty"`
	Completions  *CompletionsCapability     `json:"completions,omitempty"`
	Prompts      *PromptsCapability         `json:"prompts,omitempty"`
	Resources    *ResourcesCapability       `json:"resources,omitempty"`
	Tools        *ToolsCapability           `json:"tools,omitempty"`
}

type LoggingCapability struct{}
type CompletionsCapability struct{}

type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// InitializeRequestParams mirrors schema.ts:257-264.
type InitializeRequestParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      Implementation     `json:"clientInfo"`
}

// InitializeResult mirrors schema.ts:281-295. `instructions` is the
// only optional field; we let omitempty hide it on the wire.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

// serverState is the lifecycle state machine (basic/lifecycle.mdx:36-205).
// The four states are not arbitrary: they encode the spec's MUST/SHOULD
// language about what methods are legal when.
type serverState int

const (
	stateNew          serverState = iota // pre-handshake; only `initialize` (and ping) allowed
	stateInitializing                    // server replied; awaiting `notifications/initialized`
	stateOperating                       // ready for normal traffic
	stateClosed                          // shutdown
)

func (s serverState) String() string {
	switch s {
	case stateNew:
		return "new"
	case stateInitializing:
		return "initializing"
	case stateOperating:
		return "operating"
	case stateClosed:
		return "closed"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// Server bundles the lifecycle state, the negotiated protocol version
// and the capability advertisement. It's deliberately small: the router
// owns method dispatch, the Server only knows about the handshake.
type Server struct {
	mu sync.Mutex

	Info            Implementation
	Capabilities    ServerCapabilities
	ProtocolVersion string
	Instructions    string

	state serverState
	// negotiatedVersion is what the *client* asked for and we accepted.
	// It may differ from ProtocolVersion if we chose to fall back to our
	// preferred version (basic/lifecycle.mdx:170-175).
	negotiatedVersion string
	clientCaps        ClientCapabilities
	clientInfo        Implementation
}

// NewServer constructs a Server in stateNew with the given identity
// and capabilities pre-baked in.
func NewServer(info Implementation, caps ServerCapabilities) *Server {
	return &Server{
		Info:            info,
		Capabilities:    caps,
		ProtocolVersion: LatestProtocolVersion,
		state:           stateNew,
	}
}

// State returns the current lifecycle state under the lock.
func (s *Server) State() serverState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// handleInitialize parses the request params, advances the state
// machine and constructs the typed reply. Per the spec, a duplicate
// `initialize` is illegal: we reject it with InvalidRequest. Per the
// version-negotiation rules (basic/lifecycle.mdx:165-175), if the
// client asks for a version we don't support we MUST still respond,
// substituting our preferred version — the *client* then decides
// whether to disconnect.
func (s *Server) handleInitialize(_ context.Context, _ string, params json.RawMessage) (any, *Error) {
	var p InitializeRequestParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &Error{Code: InvalidParams, Message: "invalid initialize params: " + err.Error()}
	}
	if p.ProtocolVersion == "" {
		return nil, &Error{Code: InvalidParams, Message: "missing protocolVersion"}
	}
	if p.ClientInfo.Name == "" {
		return nil, &Error{Code: InvalidParams, Message: "missing clientInfo.name"}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state != stateNew {
		return nil, &Error{Code: InvalidRequest, Message: "initialize already received (state=" + s.state.String() + ")"}
	}

	// Version negotiation: echo the client's version if we support it,
	// otherwise return our preferred version. We DO NOT disconnect.
	chosen := p.ProtocolVersion
	if !s.supports(p.ProtocolVersion) {
		chosen = s.ProtocolVersion
	}

	s.negotiatedVersion = chosen
	s.clientCaps = p.Capabilities
	s.clientInfo = p.ClientInfo
	s.state = stateInitializing

	return InitializeResult{
		ProtocolVersion: chosen,
		Capabilities:    s.Capabilities,
		ServerInfo:      s.Info,
		Instructions:    s.Instructions,
	}, nil
}

// handleInitializedNotification flips us from stateInitializing to
// stateOperating. Notifications produce no reply, so the return value
// is always (nil, nil). Receiving the notification in any state other
// than stateInitializing is a protocol violation by the peer; we log
// it via the error path but don't send anything back (notifications
// have no id).
func (s *Server) handleInitializedNotification(_ context.Context, _ string, _ json.RawMessage) (any, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stateInitializing {
		// Cannot respond (notification); just stay where we are.
		return nil, nil
	}
	s.state = stateOperating
	return nil, nil
}

// supports decides whether `v` is a protocol version this server
// understands. The current build supports exactly one date; later
// chapters can extend this.
func (s *Server) supports(v string) bool {
	return v == s.ProtocolVersion
}

// handleRequest is the per-method dispatcher used by the Router. It
// gates non-handshake methods behind state checks. Per the spec, ping
// is always allowed; we accept it as a stub here so a client can probe
// liveness before/during the handshake (s04 implements the real
// resources subscribe story; ping is a one-liner).
func (s *Server) handleRequest(ctx context.Context, method string, params json.RawMessage) (any, *Error) {
	switch method {
	case "initialize":
		return s.handleInitialize(ctx, method, params)
	case "notifications/initialized":
		return s.handleInitializedNotification(ctx, method, params)
	case "ping":
		// ping is allowed at any time, including pre-handshake
		// (basic/lifecycle.mdx:157-163).
		return struct{}{}, nil
	}

	// All other methods require an operating state.
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	if state != stateOperating {
		return nil, &Error{
			Code:    ServerNotInitialized,
			Message: "server not initialized: method " + method + " rejected in state " + state.String(),
		}
	}

	// In s02 we don't actually serve tools/list etc. — those land in
	// s03+. Surface MethodNotFound so a future chapter only has to
	// register handlers on the Router.
	return nil, &Error{Code: MethodNotFound, Message: "method not found: " + method}
}
