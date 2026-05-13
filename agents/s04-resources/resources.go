package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// ---- Type surface (schema.ts:651-921) ------------------------------------

// Resource is one known, listable resource (schema.ts:804-845).
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

// ResourceTemplate exposes a parameterised resource URI per RFC-6570
// (schema.ts:847-885).
type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// ResourceContents is the union written on the wire as
// TextResourceContents | BlobResourceContents (schema.ts:887-921). We use a
// single fat struct + a Validate() that enforces "exactly one of text/blob".
type ResourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

func TextContents(uri, mime, text string) ResourceContents {
	return ResourceContents{URI: uri, MIMEType: mime, Text: text}
}

func BlobContents(uri, mime string, raw []byte) ResourceContents {
	return ResourceContents{URI: uri, MIMEType: mime, Blob: base64.StdEncoding.EncodeToString(raw)}
}

// ListResourcesRequest/Result (schema.ts:657-668).
type ListResourcesRequest struct {
	Cursor string `json:"cursor,omitempty"`
}
type ListResourcesResult struct {
	Resources  []Resource `json:"resources"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

// ListResourceTemplatesRequest/Result (schema.ts:675-687).
type ListResourceTemplatesRequest struct {
	Cursor string `json:"cursor,omitempty"`
}
type ListResourceTemplatesResult struct {
	ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
	NextCursor        string             `json:"nextCursor,omitempty"`
}

// ReadResourceRequest/Result (schema.ts:715-731).
type ReadResourceRequest struct {
	URI string `json:"uri"`
}
type ReadResourceResult struct {
	Contents []ResourceContents `json:"contents"`
}

// SubscribeRequest / UnsubscribeRequest (schema.ts:738-771).
type SubscribeRequest struct {
	URI string `json:"uri"`
}
type UnsubscribeRequest struct {
	URI string `json:"uri"`
}

// ResourceUpdatedNotificationParams (schema.ts:778-803).
type ResourceUpdatedNotificationParams struct {
	URI string `json:"uri"`
}

// ---- Store interface and an in-memory implementation --------------------

// Store is the s04 backing-storage abstraction. Real servers would back this
// with a filesystem, a database, or fsnotify; s04 ships a map.
type Store interface {
	List() []Resource
	Templates() []ResourceTemplate
	Read(uri string) ([]ResourceContents, error)
	Set(uri, mime, text string) // mutate triggers subscriptions
}

var ErrNotFound = errors.New("resource not found")

// memStore is an in-memory Store backed by a map keyed on URI. It supports
// the "mem://" scheme: static resources are stored verbatim; template
// resources expand the path against a registered RFC-6570 template.
type memStore struct {
	mu        sync.RWMutex
	resources map[string]storedResource
	templates []ResourceTemplate
	// templateData[name] is the per-template instance map keyed by the
	// single template variable (level-1 only). Demo: templateData["users"]
	// = {"alice": "Alice Liddell", "bob": "Bob Builder"}.
	templateData map[string]map[string]string
	templateMIME map[string]string

	notifier *notifier // installed in newMemStore so Set can fire updates
}

type storedResource struct {
	res     Resource
	mime    string
	payload string
}

func newMemStore(notifier *notifier) *memStore {
	return &memStore{
		resources:    map[string]storedResource{},
		templateData: map[string]map[string]string{},
		templateMIME: map[string]string{},
		notifier:     notifier,
	}
}

// Register stores a static, listable resource at uri.
func (s *memStore) Register(uri, name, description, mime, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources[uri] = storedResource{
		res: Resource{
			URI: uri, Name: name, Description: description,
			MIMEType: mime, Size: int64(len(text)),
		},
		mime:    mime,
		payload: text,
	}
}

// RegisterTemplate adds a listable RFC-6570 level-1 template plus the data
// it expands against. `template` must be e.g. `mem://users/{id}`.
func (s *memStore) RegisterTemplate(template, name, description, mime string, data map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.templates = append(s.templates, ResourceTemplate{
		URITemplate: template,
		Name:        name,
		Description: description,
		MIMEType:    mime,
	})
	// Extract the variable name so we can key templateData by it.
	varName := singleVarName(template)
	if varName == "" {
		return
	}
	s.templateData[varName] = data
	s.templateMIME[varName] = mime
}

func (s *memStore) List() []Resource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Resource, 0, len(s.resources))
	for _, sr := range s.resources {
		out = append(out, sr.res)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URI < out[j].URI })
	return out
}

func (s *memStore) Templates() []ResourceTemplate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ResourceTemplate, len(s.templates))
	copy(out, s.templates)
	return out
}

func (s *memStore) Read(uri string) ([]ResourceContents, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sr, ok := s.resources[uri]; ok {
		return []ResourceContents{TextContents(uri, sr.mime, sr.payload)}, nil
	}
	// Template path. Walk each registered template, try to match.
	for _, tmpl := range s.templates {
		varName := singleVarName(tmpl.URITemplate)
		if varName == "" {
			continue
		}
		matched, value := matchTemplate(tmpl.URITemplate, uri)
		if !matched {
			continue
		}
		data, ok := s.templateData[varName]
		if !ok {
			continue
		}
		payload, ok := data[value]
		if !ok {
			return nil, fmt.Errorf("%w: %s (unknown %s=%q)", ErrNotFound, uri, varName, value)
		}
		mime := s.templateMIME[varName]
		return []ResourceContents{TextContents(uri, mime, payload)}, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, uri)
}

// Set updates a static resource and triggers subscription notifications. It
// is used by the demo and by the subscribe→mutate→updated test.
func (s *memStore) Set(uri, mime, text string) {
	s.mu.Lock()
	if existing, ok := s.resources[uri]; ok {
		existing.mime = mime
		existing.payload = text
		existing.res.Size = int64(len(text))
		if mime != "" {
			existing.res.MIMEType = mime
		}
		s.resources[uri] = existing
	} else {
		s.resources[uri] = storedResource{
			res:     Resource{URI: uri, Name: uri, MIMEType: mime, Size: int64(len(text))},
			mime:    mime,
			payload: text,
		}
	}
	s.mu.Unlock()
	if s.notifier != nil {
		s.notifier.fire(uri)
	}
}

// ---- RFC-6570 level-1 template expander --------------------------------
//
// We only implement {name} substitution — no `{?q}`, `{+path}` etc.

var levelOneVar = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// singleVarName returns the first variable name in tmpl, or "" if none. For
// the s04 demo every template carries exactly one variable.
func singleVarName(tmpl string) string {
	m := levelOneVar.FindStringSubmatch(tmpl)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

// expandTemplate returns tmpl with {var} replaced by value.
func expandTemplate(tmpl string, vars map[string]string) string {
	return levelOneVar.ReplaceAllStringFunc(tmpl, func(match string) string {
		name := match[1 : len(match)-1]
		if v, ok := vars[name]; ok {
			return v
		}
		return match
	})
}

// matchTemplate is the inverse of expandTemplate: given a template like
// `mem://users/{id}` and a concrete uri like `mem://users/alice`, return
// (true, "alice").
func matchTemplate(tmpl, uri string) (bool, string) {
	// Split tmpl at the variable; only level-1, only one variable.
	loc := levelOneVar.FindStringIndex(tmpl)
	if loc == nil {
		return tmpl == uri, ""
	}
	prefix := tmpl[:loc[0]]
	suffix := tmpl[loc[1]:]
	if !strings.HasPrefix(uri, prefix) || !strings.HasSuffix(uri, suffix) {
		return false, ""
	}
	value := uri[len(prefix) : len(uri)-len(suffix)]
	if value == "" || strings.Contains(value, "/") {
		// level-1 vars do not match path separators; if you want that you
		// need `{+path}` which is level-2.
		return false, ""
	}
	return true, value
}

// ---- Handlers ------------------------------------------------------------

func decodeParams(raw json.RawMessage, dst any) *Error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return &Error{Code: InvalidParams, Message: err.Error()}
	}
	return nil
}

func handleListResources(store Store, raw json.RawMessage) (any, *Error) {
	var req ListResourcesRequest
	if e := decodeParams(raw, &req); e != nil {
		return nil, e
	}
	return ListResourcesResult{Resources: store.List()}, nil
}

func handleListTemplates(store Store, raw json.RawMessage) (any, *Error) {
	var req ListResourceTemplatesRequest
	if e := decodeParams(raw, &req); e != nil {
		return nil, e
	}
	return ListResourceTemplatesResult{ResourceTemplates: store.Templates()}, nil
}

func handleReadResource(store Store, raw json.RawMessage) (any, *Error) {
	var req ReadResourceRequest
	if e := decodeParams(raw, &req); e != nil {
		return nil, e
	}
	if req.URI == "" {
		return nil, &Error{Code: InvalidParams, Message: "uri required"}
	}
	contents, err := store.Read(req.URI)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, &Error{Code: InvalidParams, Message: err.Error()}
		}
		return nil, &Error{Code: InternalError, Message: err.Error()}
	}
	return ReadResourceResult{Contents: contents}, nil
}
