package main

// Roots — the FIRST of two server→client capabilities exercised in s07.
// A root is a `file://` URI plus an optional display name that the
// client exposes to the server as a workspace boundary. The server
// asks via `roots/list`; the client answers. The notification
// `notifications/roots/list_changed` flows the other way but we leave
// it as an exercise (see `client/roots.mdx`).
//
// Upstream: schema.ts:2089-2160 + docs/specification/2025-11-25/client/roots.mdx.

import (
	"encoding/json"
	"errors"
	"strings"
)

// Root represents one workspace boundary the client grants to the
// server. Per schema.ts:2127-2131 the URI MUST start with `file://` in
// the 2025-11-25 spec; future revisions may relax this.
type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// validate enforces the file:// constraint. We split this off so test
// harnesses and the client handler can call it without dragging the
// outbound machinery in.
func (r Root) validate() error {
	if r.URI == "" {
		return errors.New("root.uri is required")
	}
	if !strings.HasPrefix(r.URI, "file://") {
		return errors.New("root.uri must start with file:// (got " + r.URI + ")")
	}
	return nil
}

// ListRootsRequest is the **server→client** request body. JSON-RPC
// params is empty in the spec, but we keep the struct around so the
// outbound router can type-check what it serializes.
type ListRootsRequest struct{}

// ListRootsResult is the client's reply (schema.ts:2110-2116).
type ListRootsResult struct {
	Roots []Root `json:"roots"`
}

// handleListRoots is a **client-side** helper. In a real MCP client this
// would live in the client process; here we wire it up to a test
// harness so we can drive a full server↔client round-trip from a single
// Go test. It validates every entry and returns -32603 on internal
// failure to mirror `client/roots.mdx#error-handling`.
func handleListRoots(roots []Root) (ListRootsResult, *Error) {
	for _, r := range roots {
		if err := r.validate(); err != nil {
			return ListRootsResult{}, &Error{Code: InternalError, Message: err.Error()}
		}
	}
	// Per spec the field is non-null even when empty. Pre-allocate so
	// the JSON encoder emits [] instead of null.
	if roots == nil {
		roots = []Root{}
	}
	return ListRootsResult{Roots: roots}, nil
}

// decodeListRootsResult is used by the server-side outbound router to
// parse the reply that came back across the wire. Kept here because the
// outbound code is generic and shouldn't know about roots-specific
// shapes.
func decodeListRootsResult(raw json.RawMessage) (ListRootsResult, error) {
	var res ListRootsResult
	if len(raw) == 0 {
		return res, errors.New("empty roots/list result")
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return res, err
	}
	return res, nil
}
