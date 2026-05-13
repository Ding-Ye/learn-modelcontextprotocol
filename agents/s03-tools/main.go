// Command s03-tools is an MCP server that supports `initialize`,
// `notifications/initialized`, `tools/list`, and `tools/call` over
// line-delimited JSON-RPC on stdio.
//
// Two demo tools are registered:
//
//   - echo     — returns its `text` argument as a text content block.
//   - get_time — returns the current server time (RFC3339 by default,
//                or unix seconds if `format: "unix"` is passed).
//
// Usage (a four-message conversation):
//
//	cat <<'EOF' | go run ./agents/s03-tools
//	{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}
//	{"jsonrpc":"2.0","method":"notifications/initialized"}
//	{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
//	{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}
//	EOF
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

func run(framer Framer, router *Router) error {
	for {
		msg, err := framer.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// Decode failures: emit a parse-error envelope and continue.
			fmt.Fprintln(os.Stderr, "read:", err)
			resp := Message{JSONRPC: JSONRPCVersion, Error: &Error{Code: ParseError, Message: err.Error()}}
			b, _ := json.Marshal(resp)
			fmt.Fprintln(os.Stdout, string(b))
			continue
		}
		reply, herr := router.Dispatch(msg)
		if errors.Is(herr, errSkip) {
			continue
		}
		if herr != nil {
			fmt.Fprintln(os.Stderr, "dispatch:", herr)
			continue
		}
		if err := framer.Write(reply); err != nil {
			return fmt.Errorf("write: %w", err)
		}
	}
}

func main() {
	lifecycle := NewLifecycle()
	registry := NewToolRegistry()
	if err := RegisterDemoTools(registry); err != nil {
		fmt.Fprintln(os.Stderr, "fatal: register demo tools:", err)
		os.Exit(1)
	}
	router := NewRouter(lifecycle, registry)
	framer := NewStdioFramer(os.Stdin, os.Stdout)
	if err := run(framer, router); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
