package main

// The s08 HTTP handler. One endpoint, two methods:
//
//   POST /mcp   — every client→server JSON-RPC message.
//                 Returns either a one-shot JSON body or, if the request
//                 triggers a server-initiated request (e.g. sampling),
//                 upgrades to Content-Type: text/event-stream and stays
//                 open until the response and any intermediate messages
//                 have been delivered.
//
//   GET  /mcp   — long-lived SSE for *unsolicited* server-to-client
//                 messages (notifications). Supports Last-Event-ID for
//                 resumption.
//
//   DELETE /mcp — client says "I'm done with this session".
//
// All three routes share one http.Handler whose Switch on r.Method does
// the dispatch. The session lookup happens once at the top.
//
// See basic/transports.mdx:52-330 for the prose; the file you're reading
// is the canonical s08 implementation it describes.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

const sessionHeader = "Mcp-Session-Id"
const protocolHeader = "MCP-Protocol-Version"
const lastEventIDHeader = "Last-Event-ID"

// Server is the top-level state. Everything is shared across requests;
// per-request state lives in Session.
type Server struct {
	store *SessionStore
	tools *ToolRegistry
}

func NewServer() *Server {
	s := &Server{
		store: NewSessionStore(),
		tools: NewToolRegistry(),
	}
	RegisterDemoTool(s.tools)
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", s)
	return mux
}

// ServeHTTP is the single entry point.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handlePOST(w, r)
	case http.MethodGet:
		s.handleGET(w, r)
	case http.MethodDelete:
		s.handleDELETE(w, r)
	default:
		w.Header().Set("Allow", "POST, GET, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ----------------------------------------------------------------------------
// POST /mcp
//
// Flow:
//   1. Read body, validate MCP-Protocol-Version header (unless this is
//      the *first* initialize, in which case the spec lets us infer
//      the version from the request body — basic/transports.mdx:264-272).
//   2. Resolve or create the session.
//   3. Dispatch the message:
//      - notifications/responses → 202 Accepted, no body
//      - initialize             → JSON body + new Mcp-Session-Id header
//      - request whose handler needs sampling → upgrade to SSE
//      - any other request      → JSON body
// ----------------------------------------------------------------------------

func (s *Server) handlePOST(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	r.Body.Close()

	var msg Message
	if err := json.Unmarshal(body, &msg); err != nil {
		writeJSONRPCError(w, http.StatusBadRequest, ParseError, "parse error: "+err.Error())
		return
	}
	if msg.JSONRPC != JSONRPCVersion {
		writeJSONRPCError(w, http.StatusBadRequest, InvalidRequest, "bad jsonrpc version")
		return
	}

	isInit := msg.Method == "initialize"

	// Header validation: MCP-Protocol-Version is required on every POST
	// *except* the initialize itself (transports.mdx:260-272 — "the client
	// MUST include the header on all subsequent requests").
	if !isInit {
		pv := r.Header.Get(protocolHeader)
		if pv == "" {
			http.Error(w, "missing MCP-Protocol-Version header", http.StatusBadRequest)
			return
		}
		if pv != LatestProtocolVersion {
			http.Error(w, "unsupported MCP-Protocol-Version: "+pv, http.StatusBadRequest)
			return
		}
	}

	// Session lookup / mint.
	sessID := r.Header.Get(sessionHeader)
	var sess *Session
	if isInit {
		// Initialize always mints a fresh session. We honour any header
		// the client sent (rare; usually first POST has none).
		var p InitializeRequestParams
		if len(msg.Params) > 0 {
			_ = json.Unmarshal(msg.Params, &p)
		}
		pv := p.ProtocolVersion
		if pv == "" {
			pv = LatestProtocolVersion
		}
		sess = s.store.New(pv)
	} else {
		if sessID == "" {
			http.Error(w, "missing Mcp-Session-Id header", http.StatusBadRequest)
			return
		}
		sess = s.store.Get(sessID)
		if sess == nil {
			// Spec: 404 Not Found tells the client to restart with a
			// fresh initialize (transports.mdx:214-217).
			http.Error(w, "unknown session", http.StatusNotFound)
			return
		}
	}

	// Notifications and responses receive 202 Accepted with no body
	// (transports.mdx:96-99).
	if msg.IsNotification() {
		if msg.Method == "notifications/initialized" {
			sess.MarkReady()
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if msg.IsResponse() {
		// A client-side response to a server-initiated request. We
		// look up the pending channel; if found, deliver it.
		sess.pendingMu.Lock()
		ch, ok := sess.pending[msg.ID.String()]
		if ok {
			delete(sess.pending, msg.ID.String())
		}
		sess.pendingMu.Unlock()
		if ok {
			ch <- msg
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Below: a request. Need a reply.
	// Pre-init gate: only `initialize` may run before notifications/initialized.
	if !isInit && !sess.Ready() {
		reply := NewErrorResponse(*msg.ID, InvalidRequest, "lifecycle: not yet initialized")
		writeJSONReply(w, sess.ID, isInit, reply)
		return
	}

	if isInit {
		res, rpcErr := handleInitialize(msg.Params)
		if rpcErr != nil {
			reply := Message{JSONRPC: JSONRPCVersion, ID: msg.ID, Error: rpcErr}
			writeJSONReply(w, sess.ID, true, reply)
			return
		}
		reply, mErr := NewResultResponse(*msg.ID, res)
		if mErr != nil {
			http.Error(w, mErr.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONReply(w, sess.ID, true, reply)
		return
	}

	// At this point we have a *post-init request*. The question is:
	// does this method need to round-trip back to the client (sampling,
	// elicitation, roots, etc.)? In s08 we hard-code "tools/call to
	// summarize" as the only sampling-driven path; everything else
	// resolves synchronously and returns JSON.
	if msg.Method == "tools/list" {
		res, rpcErr := handleListTools(s.tools)
		writeRPC(w, sess.ID, msg.ID, res, rpcErr)
		return
	}
	if msg.Method == "tools/call" {
		s.handleToolsCall(w, r, sess, msg)
		return
	}

	// Unknown method.
	reply := NewErrorResponse(*msg.ID, MethodNotFound, "method not found: "+msg.Method)
	writeJSONReply(w, sess.ID, false, reply)
}

// handleToolsCall is the "may upgrade to SSE" path. We peek at the tool
// to decide: if it's a sampling-driven tool, we go straight to SSE; else
// JSON. In a real impl you'd check whether the handler *actually* calls
// the Sampler, but predicting that statically is impossible — so the
// convention is "tools that *might* sample always run on SSE", which is
// what transports.mdx:81-86 endorses.
func (s *Server) handleToolsCall(w http.ResponseWriter, r *http.Request, sess *Session, msg Message) {
	var p callToolParams
	_ = json.Unmarshal(msg.Params, &p)

	// Decide: SSE or JSON. We only know one sampling-driven tool name
	// in this demo, but the predicate is pluggable.
	if needsSampling(p.Name) {
		s.runToolOverSSE(w, r, sess, msg, p)
		return
	}
	// Synchronous tool: run with a nil Sampler and return JSON.
	res, rpcErr := handleCallTool(r.Context(), s.tools, nil, msg.Params)
	if rpcErr != nil {
		reply := Message{JSONRPC: JSONRPCVersion, ID: msg.ID, Error: rpcErr}
		writeJSONReply(w, sess.ID, false, reply)
		return
	}
	reply, mErr := NewResultResponse(*msg.ID, res)
	if mErr != nil {
		http.Error(w, mErr.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONReply(w, sess.ID, false, reply)
}

// needsSampling is the pluggable predicate. In a real server you'd
// annotate tools with a "may_sample" flag; for the demo we just match
// the one known name.
func needsSampling(name string) bool { return name == "summarize" }

// runToolOverSSE drives the SSE-upgraded path:
//   1. Flush SSE headers.
//   2. Issue a sampling/createMessage *to the client* through the same
//      stream (with a fresh JSON-RPC id we mint).
//   3. Block on the next POST that carries the matching response.
//   4. Hand the response payload to the tool, get the CallToolResult.
//   5. Emit the CallToolResult as the final SSE event.
//
// Because the demo test client posts its response back on a separate
// connection, we wire it up through sess.pending[id] = chan Message.
func (s *Server) runToolOverSSE(w http.ResponseWriter, r *http.Request, sess *Session, msg Message, p callToolParams) {
	stream, err := NewSSEStream(w, &sess.sseCounter, sess.buf)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer stream.Close()

	sampler := &sseSampler{
		sess:    sess,
		stream:  stream,
		nextID:  &atomic.Int64{},
		timeout: r.Context(),
	}
	res, rpcErr := handleCallTool(r.Context(), s.tools, sampler, msg.Params)
	var reply Message
	if rpcErr != nil {
		reply = Message{JSONRPC: JSONRPCVersion, ID: msg.ID, Error: rpcErr}
	} else {
		raw, mErr := json.Marshal(res)
		if mErr != nil {
			reply = NewErrorResponse(*msg.ID, InternalError, mErr.Error())
		} else {
			reply = Message{JSONRPC: JSONRPCVersion, ID: msg.ID, Result: raw}
		}
	}
	if _, err := stream.Send(reply); err != nil {
		// Client disconnected mid-flight. transports.mdx:117-118
		// says "disconnection SHOULD NOT be interpreted as the client
		// cancelling its request" — we just stop here.
		return
	}
}

// sseSampler is the per-request Sampler that bridges tool code to the
// client. It uses the open SSE stream to push a sampling/createMessage
// request and registers a pending channel so the *next* POST carrying
// the client's response wakes us up. There's no real timeout — we tie
// the wait to r.Context() so a cancelled request unblocks the tool.
type sseSampler struct {
	sess    *Session
	stream  *SSEStream
	nextID  *atomic.Int64
	timeout context.Context
}

func (s *sseSampler) CreateMessage(ctx context.Context, req CreateMessageRequest) (CreateMessageResult, error) {
	// Mint a request id. We use "s8-<n>" to make it visually distinct
	// from any client-side ids in the same session.
	n := s.nextID.Add(1)
	idStr := "s8-" + strconv.FormatInt(n, 10)
	id := StringID(idStr)

	params, err := json.Marshal(req)
	if err != nil {
		return CreateMessageResult{}, err
	}
	out := Message{
		JSONRPC: JSONRPCVersion,
		ID:      &id,
		Method:  "sampling/createMessage",
		Params:  params,
	}

	// Register the pending channel BEFORE sending so we never miss a
	// fast reply.
	ch := make(chan Message, 1)
	s.sess.pendingMu.Lock()
	s.sess.pending[id.String()] = ch
	s.sess.pendingMu.Unlock()

	if _, err := s.stream.Send(out); err != nil {
		s.sess.pendingMu.Lock()
		delete(s.sess.pending, id.String())
		s.sess.pendingMu.Unlock()
		return CreateMessageResult{}, err
	}

	// Wait for the response. Either the test/client POSTs it back to
	// /mcp (delivered via ch) or the request context is cancelled.
	select {
	case reply := <-ch:
		if reply.Error != nil {
			return CreateMessageResult{}, errors.New(reply.Error.Message)
		}
		var result CreateMessageResult
		if err := json.Unmarshal(reply.Result, &result); err != nil {
			return CreateMessageResult{}, err
		}
		return result, nil
	case <-ctx.Done():
		s.sess.pendingMu.Lock()
		delete(s.sess.pending, id.String())
		s.sess.pendingMu.Unlock()
		return CreateMessageResult{}, ctx.Err()
	}
}

// ----------------------------------------------------------------------------
// GET /mcp
//
// Open a long-lived SSE stream. Honour Last-Event-ID for replay.
// Pump server-initiated notifications from sess.notif onto the stream
// until the client disconnects.
// ----------------------------------------------------------------------------

func (s *Server) handleGET(w http.ResponseWriter, r *http.Request) {
	pv := r.Header.Get(protocolHeader)
	if pv == "" {
		http.Error(w, "missing MCP-Protocol-Version header", http.StatusBadRequest)
		return
	}
	if pv != LatestProtocolVersion {
		http.Error(w, "unsupported MCP-Protocol-Version: "+pv, http.StatusBadRequest)
		return
	}
	sessID := r.Header.Get(sessionHeader)
	if sessID == "" {
		http.Error(w, "missing Mcp-Session-Id header", http.StatusBadRequest)
		return
	}
	sess := s.store.Get(sessID)
	if sess == nil {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}

	stream, err := NewSSEStream(w, &sess.sseCounter, sess.buf)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer stream.Close()

	// Replay missed events if requested. transports.mdx:179-194.
	if last := r.Header.Get(lastEventIDHeader); last != "" {
		if n, err := strconv.ParseInt(last, 10, 64); err == nil {
			if err := stream.Replay(n); err != nil {
				return
			}
		}
	}

	// Pump notifications until the client disconnects.
	for {
		select {
		case m := <-sess.notif:
			if _, err := stream.Send(m); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

// ----------------------------------------------------------------------------
// DELETE /mcp
// ----------------------------------------------------------------------------

func (s *Server) handleDELETE(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get(sessionHeader)
	if id == "" {
		http.Error(w, "missing Mcp-Session-Id header", http.StatusBadRequest)
		return
	}
	s.store.Delete(id)
	w.WriteHeader(http.StatusNoContent)
}

// ----------------------------------------------------------------------------
// Reply helpers
// ----------------------------------------------------------------------------

func writeJSONReply(w http.ResponseWriter, sessID string, includeSession bool, m Message) {
	if m.JSONRPC == "" {
		m.JSONRPC = JSONRPCVersion
	}
	raw, err := json.Marshal(m)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if includeSession && sessID != "" {
		w.Header().Set(sessionHeader, sessID)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func writeRPC(w http.ResponseWriter, sessID string, id *ID, res any, rpcErr *Error) {
	var reply Message
	if rpcErr != nil {
		reply = Message{JSONRPC: JSONRPCVersion, ID: id, Error: rpcErr}
	} else {
		raw, err := json.Marshal(res)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		reply = Message{JSONRPC: JSONRPCVersion, ID: id, Result: raw}
	}
	writeJSONReply(w, sessID, false, reply)
}

// writeJSONRPCError emits a JSON-RPC error response with no id (the
// shape the spec calls for when a request couldn't be parsed at all —
// see basic/transports.mdx:96-99 for the "input was a notification we
// couldn't accept" case which is structurally the same).
func writeJSONRPCError(w http.ResponseWriter, httpStatus, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	out := Message{
		JSONRPC: JSONRPCVersion,
		Error:   &Error{Code: code, Message: msg},
	}
	raw, _ := json.Marshal(out)
	_, _ = w.Write(raw)
}

// ----------------------------------------------------------------------------
// Misc helpers
// ----------------------------------------------------------------------------

// drainNotif drops queued notifications. Useful for tests that want a
// clean slate after the initialization round-trip.
func drainNotif(sess *Session) {
	for {
		select {
		case <-sess.notif:
		default:
			return
		}
	}
}

// Ensure unused imports don't trip the compiler if a refactor drops
// references. (Imports are used in the live code paths; this is just
// defensive plumbing for editing.)
var (
	_ = bytes.NewBufferString
	_ = strings.HasPrefix
	_ = sync.Mutex{}
)
