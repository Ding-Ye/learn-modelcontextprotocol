package main

// Sessions are how Streamable HTTP recovers the "long-lived connection"
// guarantees that came for free in stdio. The session ID is minted at
// `initialize` time, returned in the `Mcp-Session-Id` response header,
// and echoed back by the client on every subsequent request.
//
// See basic/transports.mdx:195-230 for the prose. The spec is generous:
// IDs need only be ASCII 0x21-0x7E and "globally unique"; UUIDv4 satisfies
// both. Sessions are entirely server-state — the client never inspects them.
//
// Note SEP-2575 (Make MCP Stateless) and SEP-2567 (Sessionless MCP via
// Explicit State Handles) are working to remove sessions from the
// protocol. Until those land, the session header is normative; the
// curriculum docs cover the upcoming shift.

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"
)

// Session is the per-client state. Almost everything is set at init
// time and never changes; the parts that do mutate (subscriptions,
// pending outbound requests) live behind their own mutex.
type Session struct {
	ID              string
	Created         time.Time
	ProtocolVersion string

	state LifecycleState

	// Subscriptions kept for symmetry with s04; s08 never uses them
	// in the demo but the field is here so the shape is recognizable.
	subsMu        sync.Mutex
	subscriptions map[string]struct{}

	// pending tracks outbound server-initiated requests (e.g. a
	// sampling/createMessage we sent over an SSE-upgraded POST).
	// The map is keyed by request ID; the value is a channel the
	// dispatcher reads when the client's response comes back via
	// a subsequent POST. We don't actually wire up cross-request
	// reply routing in s08 (the demo does the round-trip inside
	// one POST stream), but the field is here so a learner can see
	// the shape of a real implementation.
	pendingMu sync.Mutex
	pending   map[string]chan Message

	// sseCounter is the per-session monotonic counter for SSE event
	// IDs. Used by both POST-upgraded streams and the long-lived
	// GET stream so a Last-Event-ID is unambiguous (transports.mdx:174).
	sseCounter atomic.Int64

	// buf holds the last 100 SSE events for replay.
	buf *SSEBuffer

	// notif is a fan-in channel used by the GET handler. Any code
	// that wants to push an unsolicited notification to the client
	// writes to this channel; the GET handler reads, serializes,
	// and emits via the SSE stream.
	notifMu sync.Mutex
	notif   chan Message
}

// SessionStore is a tiny in-memory map. Real implementations would back
// it with Redis or a database — the API stays the same.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewSessionStore() *SessionStore {
	return &SessionStore{sessions: map[string]*Session{}}
}

// New creates a session, assigns an ID, and stores it. The caller is
// responsible for putting the ID in the Mcp-Session-Id response header.
func (s *SessionStore) New(protocolVersion string) *Session {
	sess := &Session{
		ID:              newSessionID(),
		Created:         time.Now(),
		ProtocolVersion: protocolVersion,
		state:           stateInitializing,
		subscriptions:   map[string]struct{}{},
		pending:         map[string]chan Message{},
		buf:             NewSSEBuffer(100),
		notif:           make(chan Message, 32),
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	return sess
}

// Get retrieves a session by ID; returns nil if not found.
func (s *SessionStore) Get(id string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

// Delete removes a session. Used by DELETE /mcp.
func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// MarkReady flips the lifecycle state. Called when we see
// notifications/initialized.
func (sess *Session) MarkReady() { sess.state = stateReady }
func (sess *Session) Ready() bool { return sess.state == stateReady }

// Notify pushes a server-initiated notification toward the GET stream.
// If no GET stream is currently connected, the notification waits in
// the buffered channel (capacity 32) until one is.
func (sess *Session) Notify(m Message) {
	sess.notifMu.Lock()
	defer sess.notifMu.Unlock()
	select {
	case sess.notif <- m:
	default:
		// Drop on the floor if the buffer is full. A real impl would
		// either back-pressure or persist; we'd rather lose events
		// than block a request thread in a teaching repo.
	}
}

// newSessionID returns a 32-char hex string (16 random bytes). UUIDv4
// would also work; we avoid the dep by using `crypto/rand` directly.
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is unrecoverable; panicking here is fine
		// — main() will catch it on the next request anyway.
		panic("session: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
