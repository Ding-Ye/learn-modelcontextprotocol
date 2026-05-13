// Command s01-min-loop is the smallest possible MCP server: it speaks
// JSON-RPC 2.0 over line-delimited stdio and responds to exactly one
// method, `initialize`, with a hard-coded reply. Every other method
// returns -32601 MethodNotFound.
//
// This is the "minimum loop" — the smallest program that shows the wire
// shape of MCP. Subsequent chapters (s02..s08) grow it into a real
// implementation.
//
// Usage:
//
//	echo '{"jsonrpc":"2.0","id":1,"method":"initialize",
//	       "params":{"protocolVersion":"2025-11-25","capabilities":{},
//	                 "clientInfo":{"name":"demo","version":"0"}}}' \
//	  | go run ./agents/s01-min-loop
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// initializeResult is the hard-coded reply. The real handshake (with
// version negotiation, capability merging, etc.) lands in s02.
type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      map[string]any `json:"serverInfo"`
	Instructions    string         `json:"instructions,omitempty"`
}

func handle(m Message) (Message, error) {
	if m.IsNotification() {
		// s01 ignores all notifications. s02 honours `notifications/initialized`.
		return Message{}, errSkip
	}
	if !m.IsRequest() {
		return Message{}, fmt.Errorf("expected a request, got response/other")
	}
	switch m.Method {
	case "initialize":
		r := initializeResult{
			ProtocolVersion: "2025-11-25",
			Capabilities:    map[string]any{},
			ServerInfo:      map[string]any{"name": "learn-mcp-s01-min-loop", "version": "0.1.0"},
			Instructions:    "This is s01: a single-shot initialize echo. Try anything else and you'll get -32601.",
		}
		return NewResultResponse(*m.ID, r)
	default:
		return NewErrorResponse(*m.ID, MethodNotFound, "method not found: "+m.Method), nil
	}
}

var errSkip = errors.New("skip")

func run(framer Framer) error {
	for {
		msg, err := framer.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// Decode failures are written back as -32700 ParseError per the spec.
			fmt.Fprintln(os.Stderr, "read:", err)
			resp := Message{
				JSONRPC: JSONRPCVersion,
				Error:   &Error{Code: ParseError, Message: err.Error()},
			}
			b, _ := json.Marshal(resp)
			fmt.Fprintln(os.Stdout, string(b))
			continue
		}
		reply, herr := handle(msg)
		if errors.Is(herr, errSkip) {
			continue
		}
		if herr != nil {
			fmt.Fprintln(os.Stderr, "handle:", herr)
			continue
		}
		if err := framer.Write(reply); err != nil {
			return fmt.Errorf("write: %w", err)
		}
	}
}

func main() {
	framer := NewStdioFramer(os.Stdin, os.Stdout)
	if err := run(framer); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
