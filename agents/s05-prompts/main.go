// Command s05-prompts is a minimal MCP server that exposes one
// **prompt template** (`summarize-file`) and the matching
// `completion/complete` endpoint for autocompleting its arguments.
//
// Run a one-shot demo:
//
//	make demo
//
// Or pipe in a full session by hand:
//
//	(
//	  echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
//	  echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
//	  echo '{"jsonrpc":"2.0","id":2,"method":"prompts/list","params":{}}'
//	  echo '{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"summarize-file","arguments":{"path":"README.md"}}}'
//	  echo '{"jsonrpc":"2.0","id":4,"method":"completion/complete","params":{"ref":{"type":"ref/prompt","name":"summarize-file"},"argument":{"name":"path","value":"R"}}}'
//	) | go run ./agents/s05-prompts
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

func run(framer Framer) error {
	lc := NewLifecycle()
	reg := NewPromptRegistry()
	RegisterDemoPrompt(reg)
	router := NewRouter(lc, reg)

	for {
		msg, err := framer.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// Wire-level decode failures become -32700.
			fmt.Fprintln(os.Stderr, "read:", err)
			resp := Message{
				JSONRPC: JSONRPCVersion,
				Error:   &Error{Code: ParseError, Message: err.Error()},
			}
			b, _ := json.Marshal(resp)
			fmt.Fprintln(os.Stdout, string(b))
			continue
		}
		reply, hasReply := router.Dispatch(msg)
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
	if err := run(framer); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
