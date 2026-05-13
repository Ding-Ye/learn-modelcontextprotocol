// Command s06-sampling is a minimal MCP server that demonstrates
// **server-initiated** `sampling/createMessage` — the defining MCP
// capability that lets a server reach back into the client to run an LLM.
//
// The demo tool `summarize` triggers a single sampling round-trip;
// `agentic-summarize` exercises the two-turn agentic loop (tools list
// → tool_use reply → tool_result fed back).
//
// Default Sampler is `StubSampler`: tests and the stdio demo both use it
// so the loop is deterministic and runs offline. `AnthropicSampler` is
// the live-provider skeleton (body deferred to Phase G).
//
// Run a one-shot stdio demo:
//
//	make demo
//
// Or pipe in a session by hand:
//
//	(
//	  echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"sampling":{}}}}'
//	  echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
//	  echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
//	  echo '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"summarize","arguments":{"text":"hello"}}}'
//	) | go run ./agents/s06-sampling
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

func run(framer Framer, sampler Sampler) error {
	lc := NewLifecycle()
	out := NewOutboundRouter(framer)
	router := NewRouter(lc, out, sampler)

	ctx := context.Background()
	for {
		msg, err := framer.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "read:", err)
			resp := Message{
				JSONRPC: JSONRPCVersion,
				Error:   &Error{Code: ParseError, Message: err.Error()},
			}
			b, _ := json.Marshal(resp)
			fmt.Fprintln(os.Stdout, string(b))
			continue
		}
		reply, hasReply := router.DispatchInbound(ctx, msg)
		if !hasReply {
			continue
		}
		if err := framer.Write(reply); err != nil {
			if errors.Is(err, io.ErrClosedPipe) {
				return nil
			}
			return fmt.Errorf("write: %w", err)
		}
	}
}

func main() {
	framer := NewStdioFramer(os.Stdin, os.Stdout)
	// Default to a stub sampler with a canned text reply so the demo
	// shows the full loop without needing a real LLM behind it.
	sampler := &StubSampler{
		Model:     "stub-model-v0",
		TextReply: "(stub summary) the server asked the client to run an LLM; here is the canned reply.",
	}
	if err := run(framer, sampler); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
