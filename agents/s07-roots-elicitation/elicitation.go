package main

// Elicitation — the SECOND server→client capability. Two modes:
//
//   - form mode: server hands the client a flat JSON Schema; the client
//     renders a form; the user fills it; the client returns a flat
//     map[string]any in `content`. This is for *non-sensitive* data —
//     the spec is emphatic that passwords, API keys, payment data MUST
//     go through URL mode instead. See `client/elicitation.mdx`
//     "Form Mode Security".
//
//   - URL mode: server returns a URL plus an `elicitationId`; the
//     client opens the URL in a secure browser surface and the user
//     completes the interaction *out of band*. The JSON-RPC reply
//     carries only `action`; data never crosses the MCP channel.
//
// The schema requires that elicitation requests with mode "form" carry
// a `requestedSchema` (a restricted, top-level-only JSON Schema), and
// requests with mode "url" carry `url` + `elicitationId` (no schema).
//
// Upstream: schema.ts:2161-2506 + docs/specification/2025-11-25/client/elicitation.mdx.

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ElicitMethod is the method name the server uses to send either
// mode. Per spec it is the same RPC for both — `params.mode`
// discriminates.
const ElicitMethod = "elicitation/create"

// PrimitiveSchema is the restricted top-level property schema allowed
// in form-mode `requestedSchema.properties` (schema.ts:2270-2360). We
// keep this as a map[string]any to dodge Go's lack of tagged unions;
// constructors below produce the canonical shapes.
type PrimitiveSchema map[string]any

// RequestedSchema is the value of form-mode `requestedSchema`. It is
// always `{type: "object", properties: {...}, required?: [...]}` per
// schema.ts:2249-2260.
type RequestedSchema struct {
	Type       string                     `json:"type"`
	Properties map[string]PrimitiveSchema `json:"properties"`
	Required   []string                   `json:"required,omitempty"`
}

// NewRequestedSchema builds the canonical {type: object, ...} wrapper
// so callers don't accidentally drop the discriminator.
func NewRequestedSchema(props map[string]PrimitiveSchema, required ...string) RequestedSchema {
	return RequestedSchema{Type: "object", Properties: props, Required: required}
}

// ElicitRequestFormParams is the params for a form-mode elicitation
// (schema.ts:2161-2189). Mode defaults to "form" when omitted; we
// emit it explicitly to be unambiguous on the wire.
type ElicitRequestFormParams struct {
	Mode            string          `json:"mode"`
	Message         string          `json:"message"`
	RequestedSchema RequestedSchema `json:"requestedSchema"`
}

// ElicitRequestURLParams is the params for a URL-mode elicitation
// (schema.ts:2191-2229).
type ElicitRequestURLParams struct {
	Mode          string `json:"mode"`
	Message       string `json:"message"`
	ElicitationID string `json:"elicitationId"`
	URL           string `json:"url"`
}

// ElicitResult is the client's response shape for *both* modes
// (schema.ts:2476-2506). For accepted form mode, `Content` carries the
// submitted values. For URL mode, `Content` is always omitted — the
// out-of-band channel returns no data on the JSON-RPC wire.
type ElicitResult struct {
	Action  string         `json:"action"`            // accept | decline | cancel
	Content map[string]any `json:"content,omitempty"` // only for form + accept
}

// validate is shared by both senders. It mirrors the structural rules
// in the spec (mode discriminator, required fields per mode).
func (p ElicitRequestFormParams) validate() error {
	if p.Message == "" {
		return errors.New("elicitation/create (form): message is required")
	}
	if p.RequestedSchema.Type != "object" {
		return errors.New(`elicitation/create (form): requestedSchema.type must be "object"`)
	}
	return nil
}

func (p ElicitRequestURLParams) validate() error {
	if p.Mode != "url" {
		return errors.New(`elicitation/create (url): mode must be "url"`)
	}
	if p.Message == "" {
		return errors.New("elicitation/create (url): message is required")
	}
	if p.ElicitationID == "" {
		return errors.New("elicitation/create (url): elicitationId is required")
	}
	if p.URL == "" {
		return errors.New("elicitation/create (url): url is required")
	}
	return nil
}

// sendFormElicitation issues an outbound `elicitation/create` request in
// form mode and decodes the typed reply. It refuses to send if the
// client did not declare form support during initialize — same
// rationale as the spec's "Servers MUST NOT send elicitation requests
// with modes that are not supported by the client".
func sendFormElicitation(rt *OutboundRouter, lc *Lifecycle, message string, schema RequestedSchema) (ElicitResult, error) {
	if !lc.FormElicitOK() {
		return ElicitResult{}, errors.New("client did not advertise form-mode elicitation")
	}
	p := ElicitRequestFormParams{Mode: "form", Message: message, RequestedSchema: schema}
	if err := p.validate(); err != nil {
		return ElicitResult{}, err
	}
	raw, err := rt.SendRequest(ElicitMethod, p)
	if err != nil {
		return ElicitResult{}, err
	}
	return decodeElicitResult(raw)
}

// sendURLElicitation issues an outbound `elicitation/create` request in
// URL mode.
func sendURLElicitation(rt *OutboundRouter, lc *Lifecycle, message, elicitationID, url string) (ElicitResult, error) {
	if !lc.URLElicitOK() {
		return ElicitResult{}, errors.New("client did not advertise url-mode elicitation")
	}
	p := ElicitRequestURLParams{Mode: "url", Message: message, ElicitationID: elicitationID, URL: url}
	if err := p.validate(); err != nil {
		return ElicitResult{}, err
	}
	raw, err := rt.SendRequest(ElicitMethod, p)
	if err != nil {
		return ElicitResult{}, err
	}
	return decodeElicitResult(raw)
}

func decodeElicitResult(raw json.RawMessage) (ElicitResult, error) {
	var res ElicitResult
	if len(raw) == 0 {
		return res, errors.New("empty elicitation/create result")
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return res, fmt.Errorf("decode elicit result: %w", err)
	}
	switch res.Action {
	case "accept", "decline", "cancel":
	default:
		return res, fmt.Errorf("invalid action %q (want accept|decline|cancel)", res.Action)
	}
	// Per spec: content is only valid when action=accept AND the mode
	// was "form". The decoder can't see the originating mode, so we
	// just trust the upstream. We DO scrub content when action != accept
	// so handlers can't accidentally consume cancel/decline payloads.
	if res.Action != "accept" {
		res.Content = nil
	}
	return res, nil
}

// URLElicitationRequiredError is a typed short-circuit. A tool handler
// that needs the user to complete a URL-mode elicitation before it
// can answer can `return nil, &URLElicitationRequiredError{...}` and
// the router will turn it into the -32042 error response with
// `data.elicitations` populated. Spec: `client/elicitation.mdx`
// "URL Elicitation Required Error".
type URLElicitationRequiredError struct {
	Message      string
	Elicitations []ElicitRequestURLParams
}

func (e *URLElicitationRequiredError) Error() string {
	if e.Message == "" {
		return "URL elicitation required"
	}
	return e.Message
}

// asRPCError serialises the typed error into a JSON-RPC `Error` with
// code -32042 and the elicitations[] payload tucked into `data`.
func (e *URLElicitationRequiredError) asRPCError() *Error {
	payload := struct {
		Elicitations []ElicitRequestURLParams `json:"elicitations"`
	}{Elicitations: e.Elicitations}
	data, _ := json.Marshal(payload)
	msg := e.Message
	if msg == "" {
		msg = "This request requires more information."
	}
	return &Error{Code: URLElicitationRequired, Message: msg, Data: data}
}
