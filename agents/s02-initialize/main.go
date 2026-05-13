// Command s02-initialize is a stdio MCP server with a real handshake.
//
// Compared to s01 (a single hard-coded `initialize` echo) this chapter:
//
//   - parses InitializeRequestParams and validates protocolVersion,
//   - builds a typed ServerCapabilities reply,
//   - enforces the lifecycle state machine — non-handshake methods are
//     rejected with -32002 server-not-initialized until the client
//     sends `notifications/initialized`.
//
// Demo:
//
//	{ printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}'; \
//	  printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}'; \
//	  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"ping","params":{}}'; \
//	} | go run ./agents/s02-initialize
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// buildServer wires up the Server identity + capability advertisement
// this chapter ships with. Tools/Resources/Prompts capabilities are
// declared so that, end-to-end, a client sees what *would* be available
// — but the actual handlers land in s03..s05.
func buildServer() *Server {
	caps := ServerCapabilities{
		Logging:     &LoggingCapability{},
		Completions: &CompletionsCapability{},
		Prompts:     &PromptsCapability{ListChanged: false},
		Resources:   &ResourcesCapability{Subscribe: false, ListChanged: false},
		Tools:       &ToolsCapability{ListChanged: false},
	}
	s := NewServer(Implementation{Name: "learn-mcp-s02-initialize", Version: "0.2.0"}, caps)
	s.Instructions = "s02: handshake + capability negotiation. Use `notifications/initialized` to unlock the rest."
	return s
}

// buildRouter wires the Server's lifecycle handlers into a Router and
// installs the lifecycle gate as the fallback so every uncategorised
// method passes through state-check + MethodNotFound.
func buildRouter(s *Server) *Router {
	r := NewRouter(s.handleRequest)
	r.Handle("initialize", s.handleInitialize)
	r.Handle("notifications/initialized", s.handleInitializedNotification)
	r.Handle("ping", func(_ context.Context, _ string, _ json.RawMessage) (any, *Error) {
		return struct{}{}, nil
	})
	return r
}

func run(ctx context.Context, framer Framer) error {
	server := buildServer()
	router := buildRouter(server)

	for {
		msg, err := framer.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "read:", err)
			// Best-effort parse-error response per JSON-RPC 2.0.
			resp := Message{JSONRPC: JSONRPCVersion, Error: &Error{Code: ParseError, Message: err.Error()}}
			b, _ := json.Marshal(resp)
			fmt.Fprintln(os.Stdout, string(b))
			continue
		}
		reply, ok := router.Process(ctx, msg)
		if !ok {
			continue
		}
		if err := framer.Write(reply); err != nil {
			return fmt.Errorf("write: %w", err)
		}
	}
}

func main() {
	framer := NewStdioFramer(os.Stdin, os.Stdout)
	if err := run(context.Background(), framer); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
