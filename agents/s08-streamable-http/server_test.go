package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// helper: start an httptest.Server wrapping a fresh Server.
func newTestHTTP(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	srv := NewServer()
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv
}

// helper: POST a body, return (status, headers, body).
func postJSON(t *testing.T, url string, headers map[string]string, body string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

// helper: run a full handshake and return the session id.
func handshake(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	status, hdr, body := postJSON(t, ts.URL+"/mcp", nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"sampling":{}},"clientInfo":{"name":"t","version":"0"}}}`)
	if status != http.StatusOK {
		t.Fatalf("initialize: status=%d body=%s", status, string(body))
	}
	sessID := hdr.Get("Mcp-Session-Id")
	if sessID == "" {
		t.Fatalf("initialize: no Mcp-Session-Id; body=%s", string(body))
	}
	// notifications/initialized
	status, _, _ = postJSON(t, ts.URL+"/mcp", map[string]string{
		"Mcp-Session-Id":       sessID,
		"MCP-Protocol-Version": "2025-11-25",
	}, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if status != http.StatusAccepted {
		t.Fatalf("notifications/initialized: want 202, got %d", status)
	}
	return sessID
}

// ---------------------------------------------------------------------------
// Test 1: POST initialize returns Mcp-Session-Id and a JSON body
// (not SSE).
// ---------------------------------------------------------------------------

func TestPOSTInitializeReturnsJSONAndSessionID(t *testing.T) {
	ts, _ := newTestHTTP(t)
	status, hdr, body := postJSON(t, ts.URL+"/mcp", nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, string(body))
	}
	if got := hdr.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q want application/json", got)
	}
	if hdr.Get("Mcp-Session-Id") == "" {
		t.Fatalf("missing Mcp-Session-Id header")
	}
	var m Message
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode: %v body=%s", err, string(body))
	}
	if m.Error != nil {
		t.Fatalf("rpc error: %+v", m.Error)
	}
	var res InitializeResult
	if err := json.Unmarshal(m.Result, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.ProtocolVersion != "2025-11-25" {
		t.Fatalf("protocolVersion=%q", res.ProtocolVersion)
	}
	if res.Capabilities.Tools == nil {
		t.Fatalf("server should advertise tools capability")
	}
}

// ---------------------------------------------------------------------------
// Test 2: POST tools/call that triggers sampling upgrades to SSE and
// round-trips a sampling/createMessage, finishing with the tool result.
// ---------------------------------------------------------------------------

func TestPOSTToolsCallUpgradesToSSEAndRoundTripsSampling(t *testing.T) {
	ts, _ := newTestHTTP(t)
	sessID := handshake(t, ts)

	// POST tools/call; we read the SSE stream by hand.
	body := `{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"summarize","arguments":{"text":"abc"}}}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessID)
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("Content-Type=%q want text/event-stream", got)
	}

	events, errs := ReadSSE(resp.Body)

	// In parallel: when the server emits a sampling/createMessage, we
	// post a stub response on a separate connection.
	sawSampling := false
	sawResult := false
	gotResult := ""

	timeout := time.After(3 * time.Second)
	for !sawResult {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("stream closed before result")
			}
			var m Message
			if err := json.Unmarshal([]byte(ev.Data), &m); err != nil {
				t.Fatalf("decode event: %v data=%s", err, ev.Data)
			}
			if m.Method == "sampling/createMessage" {
				sawSampling = true
				// Post the stub response (separate connection).
				stub := `{"jsonrpc":"2.0","id":` + m.ID.String() + `,"result":{"role":"assistant","content":[{"type":"text","text":"summary"}],"model":"stub"}}`
				status, _, sbody := postJSON(t, ts.URL+"/mcp", map[string]string{
					"Mcp-Session-Id":       sessID,
					"MCP-Protocol-Version": "2025-11-25",
				}, stub)
				if status != http.StatusAccepted {
					t.Fatalf("post sampling response: status=%d body=%s", status, string(sbody))
				}
				continue
			}
			if m.IsResponse() {
				sawResult = true
				gotResult = string(m.Result)
			}
		case err := <-errs:
			if err != nil {
				t.Fatalf("sse error: %v", err)
			}
		case <-timeout:
			t.Fatalf("timeout; sawSampling=%v sawResult=%v", sawSampling, sawResult)
		}
	}
	if !sawSampling {
		t.Fatalf("did not see sampling/createMessage in stream")
	}
	if !strings.Contains(gotResult, "summary") {
		t.Fatalf("final result did not echo sampling content; got %q", gotResult)
	}
}

// ---------------------------------------------------------------------------
// Test 3: GET /mcp opens long-lived SSE; client sees one notification.
// ---------------------------------------------------------------------------

func TestGETStreamReceivesNotification(t *testing.T) {
	ts, srv := newTestHTTP(t)
	sessID := handshake(t, ts)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/mcp", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessID)
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("Content-Type=%q want text/event-stream", got)
	}

	// Push a notification from another goroutine.
	go func() {
		time.Sleep(50 * time.Millisecond)
		sess := srv.store.Get(sessID)
		sess.Notify(Message{
			JSONRPC: JSONRPCVersion,
			Method:  "notifications/message",
			Params:  json.RawMessage(`{"level":"info","data":"hello"}`),
		})
	}()

	events, _ := ReadSSE(resp.Body)
	select {
	case ev := <-events:
		var m Message
		if err := json.Unmarshal([]byte(ev.Data), &m); err != nil {
			t.Fatalf("decode: %v data=%s", err, ev.Data)
		}
		if m.Method != "notifications/message" {
			t.Fatalf("want notifications/message, got %q", m.Method)
		}
		if ev.ID == "" {
			t.Fatalf("event id should be present for resumability")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("never received notification")
	}
}

// ---------------------------------------------------------------------------
// Test 4: Reconnect with Last-Event-ID replays missed events.
// We seed the SSE buffer with 5 events, then GET with Last-Event-ID: 3
// and expect to see events 4 and 5 only.
// ---------------------------------------------------------------------------

func TestGETLastEventIDReplaysMissedEvents(t *testing.T) {
	ts, srv := newTestHTTP(t)
	sessID := handshake(t, ts)

	// Seed 5 events directly through the session buffer.
	sess := srv.store.Get(sessID)
	for i := 1; i <= 5; i++ {
		sess.sseCounter.Add(1)
		data, _ := json.Marshal(Message{
			JSONRPC: JSONRPCVersion,
			Method:  "notifications/seed",
			Params:  json.RawMessage(`{"n":` + string(rune('0'+i)) + `}`),
		})
		sess.buf.Append(int64(i), data)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/mcp", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessID)
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	req.Header.Set("Last-Event-ID", "3")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	events, _ := ReadSSE(resp.Body)
	got := []string{}
	mu := sync.Mutex{}
	done := make(chan struct{})
	go func() {
		for ev := range events {
			mu.Lock()
			got = append(got, ev.ID)
			mu.Unlock()
			if len(got) >= 2 {
				close(done)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		mu.Lock()
		t.Fatalf("timeout waiting for replay; got=%v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) < 2 {
		t.Fatalf("want 2 replayed events, got %d (%v)", len(got), got)
	}
	if got[0] != "4" || got[1] != "5" {
		t.Fatalf("want event ids 4,5; got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Test 5: POST without MCP-Protocol-Version → 400 Bad Request.
// (Initialize is exempt; the gate applies to subsequent requests only.)
// ---------------------------------------------------------------------------

func TestPOSTWithoutProtocolHeaderRejected(t *testing.T) {
	ts, _ := newTestHTTP(t)
	sessID := handshake(t, ts)

	// Drop the MCP-Protocol-Version header on a tools/list.
	body := `{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{}}`
	status, _, b := postJSON(t, ts.URL+"/mcp",
		map[string]string{"Mcp-Session-Id": sessID}, body)
	if status != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", status, string(b))
	}
}
