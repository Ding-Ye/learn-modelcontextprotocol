// Command s08-streamable-http is a minimal MCP server that speaks the
// Streamable HTTP transport from basic/transports.mdx:52-330.
//
// Run:
//
//	go run ./agents/s08-streamable-http -addr :8080
//
// Or via the Makefile (which also starts a tiny in-process client):
//
//	make demo
//
// The server exposes one tool, `summarize`, which demonstrates the
// SSE upgrade path: a POST tools/call returns text/event-stream, the
// server emits a sampling/createMessage request mid-stream, and the
// client POSTs its response back on a separate connection.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address (default localhost-only)")
	demo := flag.Bool("demo", false, "after starting the server, run a built-in test client and exit")
	flag.Parse()

	srv := NewServer()
	httpServer := &http.Server{
		Addr:    *addr,
		Handler: srv.Handler(),
	}

	go func() {
		log.Printf("s08: listening on http://%s/mcp", *addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	if *demo {
		// Give the listener a beat to bind before we connect.
		time.Sleep(150 * time.Millisecond)
		if err := runDemoClient("http://" + *addr + "/mcp"); err != nil {
			log.Printf("demo client error: %v", err)
			shutdown(httpServer)
			os.Exit(1)
		}
		shutdown(httpServer)
		return
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	shutdown(httpServer)
}

func shutdown(s *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Shutdown(ctx)
}

// ----------------------------------------------------------------------------
// runDemoClient
//
// A 100-line client written against net/http directly so a learner can
// see every wire move. Steps:
//   1. POST initialize → grab Mcp-Session-Id from response header.
//   2. POST notifications/initialized (202 Accepted).
//   3. POST tools/call summarize → response is text/event-stream.
//   4. While reading the SSE stream:
//        - On a sampling/createMessage event, POST a stub response back
//          on a separate connection (with the session header).
//        - On the final tools/call response event, print and exit.
// ----------------------------------------------------------------------------

func runDemoClient(endpoint string) error {
	httpClient := &http.Client{Timeout: 10 * time.Second}

	// 1. initialize
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"sampling":{}},"clientInfo":{"name":"demo","version":"0"}}}`
	req, _ := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(initBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("init POST: %w", err)
	}
	sessID := resp.Header.Get("Mcp-Session-Id")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if sessID == "" {
		return fmt.Errorf("missing Mcp-Session-Id; body=%s", string(body))
	}
	fmt.Printf("[demo] initialize → session=%s\n[demo]   body=%s\n", sessID, string(body))

	// 2. notifications/initialized
	if err := postOnly(httpClient, endpoint, sessID,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`); err != nil {
		return fmt.Errorf("notif/initialized: %w", err)
	}
	fmt.Println("[demo] notifications/initialized → 202")

	// 3. tools/call summarize — expect SSE.
	callBody := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"summarize","arguments":{"text":"MCP is a JSON-RPC protocol."}}}`
	callReq, _ := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(callBody))
	callReq.Header.Set("Content-Type", "application/json")
	callReq.Header.Set("Accept", "text/event-stream")
	callReq.Header.Set("Mcp-Session-Id", sessID)
	callReq.Header.Set("MCP-Protocol-Version", "2025-11-25")

	// We need an http.Client with no timeout for the SSE read so the
	// stream isn't cut mid-event.
	streamClient := &http.Client{}
	callResp, err := streamClient.Do(callReq)
	if err != nil {
		return fmt.Errorf("tools/call POST: %w", err)
	}
	defer callResp.Body.Close()
	if ct := callResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		return fmt.Errorf("expected text/event-stream, got %s", ct)
	}
	fmt.Println("[demo] tools/call → upgraded to SSE")

	events, errs := ReadSSE(callResp.Body)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			var m Message
			if err := json.Unmarshal([]byte(ev.Data), &m); err != nil {
				fmt.Printf("[demo]   garbled event: %s\n", ev.Data)
				continue
			}
			if m.Method == "sampling/createMessage" {
				fmt.Printf("[demo]   ← sampling/createMessage id=%s\n", m.ID.String())
				// Stub response: return a fixed summary.
				stub := `{"jsonrpc":"2.0","id":` + m.ID.String() + `,"result":{"role":"assistant","content":[{"type":"text","text":"MCP is a JSON-RPC protocol for LLMs."}],"model":"demo-stub"}}`
				if err := postOnly(httpClient, endpoint, sessID, stub); err != nil {
					return fmt.Errorf("post sampling response: %w", err)
				}
				fmt.Println("[demo]   → posted sampling response")
				continue
			}
			if m.IsResponse() {
				fmt.Printf("[demo] tools/call result: %s\n", string(m.Result))
				return nil
			}
		case err := <-errs:
			if err != nil {
				return fmt.Errorf("sse read: %w", err)
			}
		}
	}
}

func postOnly(c *http.Client, endpoint, sessID, body string) error {
	req, _ := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessID)
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
