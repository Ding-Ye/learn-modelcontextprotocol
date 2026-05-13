package main

// In s01..s07 the Framer was *stream-based*: each call to Read() blocked
// on the next newline-delimited line. HTTP is fundamentally different —
// each request is a one-shot envelope and each response is either one
// envelope or an SSE stream. So s08 has two transport shapes:
//
//   1. HTTPFramer  — per-request, single envelope in / single envelope out.
//                    Used for the "simple POST" path.
//
//   2. SSEStream   — server-to-client event stream over an already-open
//                    HTTP response. Used both when a POST upgrades to SSE
//                    (because the request triggered a server-initiated
//                    request, e.g. sampling) and on GET /mcp.
//
// The Framer interface signature itself (Read/Write) is reused from the
// stdio chapters so the routing logic feels familiar, but Read on the
// HTTPFramer can only be called once per request — it returns io.EOF
// afterwards. That asymmetry is exactly what makes the HTTP transport
// trickier than stdio, and naming it explicitly is the lesson.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Framer is kept for shape continuity with s01..s07 even though the
// HTTP transport uses it differently (one-shot vs. stream-of-frames).
type Framer interface {
	Read() (Message, error)
	Write(m Message) error
}

// ----------------------------------------------------------------------------
// HTTPFramer — one-shot POST body in, one-shot JSON body out.
//
// Read() returns the request body decoded as Message once; subsequent
// calls return io.EOF. Write() buffers the reply message; Finish()
// writes it to the http.ResponseWriter with Content-Type: application/json.
// ----------------------------------------------------------------------------

type HTTPFramer struct {
	body    []byte
	read    bool
	pending *Message
}

func NewHTTPFramer(body []byte) *HTTPFramer { return &HTTPFramer{body: body} }

func (f *HTTPFramer) Read() (Message, error) {
	if f.read {
		return Message{}, io.EOF
	}
	f.read = true
	var m Message
	if err := json.Unmarshal(f.body, &m); err != nil {
		return Message{}, fmt.Errorf("decode body: %w", err)
	}
	if m.JSONRPC != JSONRPCVersion {
		return Message{}, fmt.Errorf("mcp: bad jsonrpc version %q", m.JSONRPC)
	}
	return m, nil
}

func (f *HTTPFramer) Write(m Message) error {
	if m.JSONRPC == "" {
		m.JSONRPC = JSONRPCVersion
	}
	// Single-shot: just stash the reply for the caller to flush.
	f.pending = &m
	return nil
}

// Pending returns the buffered reply (or nil if none was written).
func (f *HTTPFramer) Pending() *Message { return f.pending }

// ----------------------------------------------------------------------------
// SSEStream — server-to-client over an already-flushed HTTP response.
//
// Each event is `id: <n>\ndata: <json>\n\n` (basic/transports.mdx:171-178).
// The `n` is monotonic per session — we reuse the same counter for both
// POST-upgraded streams and the GET stream, because resumption via
// Last-Event-ID is *also* per-session (transports.mdx:174-178).
//
// We hold the last 100 events in a ring buffer so a reconnecting client
// can replay missed events via Last-Event-ID. A real server would push
// this to a durable store; in-memory is enough for the lesson.
// ----------------------------------------------------------------------------

type sseEvent struct {
	ID   int64
	Data []byte
}

// SSEBuffer is a tiny ring buffer for replay. 100 events per session
// is hard-coded; transports.mdx is silent on the size so we pick a
// number large enough to survive a 10-second network hiccup.
type SSEBuffer struct {
	mu     sync.Mutex
	events []sseEvent
	max    int
}

func NewSSEBuffer(max int) *SSEBuffer { return &SSEBuffer{max: max} }

func (b *SSEBuffer) Append(id int64, data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	dup := make([]byte, len(data))
	copy(dup, data)
	b.events = append(b.events, sseEvent{ID: id, Data: dup})
	if len(b.events) > b.max {
		b.events = b.events[len(b.events)-b.max:]
	}
}

// Since returns every event with ID > lastID, in order.
func (b *SSEBuffer) Since(lastID int64) []sseEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]sseEvent, 0)
	for _, e := range b.events {
		if e.ID > lastID {
			dup := make([]byte, len(e.Data))
			copy(dup, e.Data)
			out = append(out, sseEvent{ID: e.ID, Data: dup})
		}
	}
	return out
}

// SSEStream writes SSE events to an http.ResponseWriter. The next event
// id is taken from the session-scoped counter so it stays monotonic
// even across POST-upgrade and GET streams.
type SSEStream struct {
	w        http.ResponseWriter
	flusher  http.Flusher
	counter  *atomic.Int64
	buf      *SSEBuffer
	mu       sync.Mutex
	closed   bool
}

func NewSSEStream(w http.ResponseWriter, counter *atomic.Int64, buf *SSEBuffer) (*SSEStream, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("ssestream: ResponseWriter is not a Flusher")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return &SSEStream{w: w, flusher: flusher, counter: counter, buf: buf}, nil
}

// Send marshals m as JSON, assigns a fresh event id, writes the SSE
// frame, and appends to the buffer for replay.
func (s *SSEStream) Send(m Message) (int64, error) {
	if m.JSONRPC == "" {
		m.JSONRPC = JSONRPCVersion
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return 0, fmt.Errorf("ssestream: marshal: %w", err)
	}
	return s.SendRaw(raw)
}

// SendRaw is the byte-level entry point — used by Replay so we don't
// re-serialize a buffered event.
func (s *SSEStream) SendRaw(data []byte) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errors.New("ssestream: closed")
	}
	id := s.counter.Add(1)
	frame := bytes.Buffer{}
	frame.WriteString("id: ")
	frame.WriteString(strconv.FormatInt(id, 10))
	frame.WriteByte('\n')
	frame.WriteString("data: ")
	frame.Write(data)
	frame.WriteString("\n\n")
	if _, err := s.w.Write(frame.Bytes()); err != nil {
		return id, err
	}
	s.flusher.Flush()
	if s.buf != nil {
		s.buf.Append(id, data)
	}
	return id, nil
}

// Replay re-emits buffered events with ID > lastID. Used for resumption.
// The replayed events keep their original IDs, not new ones — that's
// the invariant transports.mdx:189-190 demands.
func (s *SSEStream) Replay(lastID int64) error {
	if s.buf == nil {
		return nil
	}
	events := s.buf.Since(lastID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("ssestream: closed")
	}
	for _, e := range events {
		frame := bytes.Buffer{}
		frame.WriteString("id: ")
		frame.WriteString(strconv.FormatInt(e.ID, 10))
		frame.WriteByte('\n')
		frame.WriteString("data: ")
		frame.Write(e.Data)
		frame.WriteString("\n\n")
		if _, err := s.w.Write(frame.Bytes()); err != nil {
			return err
		}
	}
	s.flusher.Flush()
	return nil
}

func (s *SSEStream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

// ----------------------------------------------------------------------------
// SSE parser for the test client (and for any client written in Go that
// wants to consume our stream). Real clients would use a battle-tested
// EventSource implementation; ours is the 30-line version that does
// exactly what the spec demands and nothing more.
// ----------------------------------------------------------------------------

type SSEEvent struct {
	ID   string
	Data string
}

func ReadSSE(r io.Reader) (<-chan SSEEvent, <-chan error) {
	out := make(chan SSEEvent, 16)
	errs := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errs)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var cur SSEEvent
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				if cur.Data != "" || cur.ID != "" {
					out <- cur
					cur = SSEEvent{}
				}
				continue
			}
			if v, ok := strings.CutPrefix(line, "id: "); ok {
				cur.ID = v
			} else if v, ok := strings.CutPrefix(line, "data: "); ok {
				if cur.Data != "" {
					cur.Data += "\n"
				}
				cur.Data += v
			}
		}
		if err := sc.Err(); err != nil {
			errs <- err
		}
	}()
	return out, errs
}
