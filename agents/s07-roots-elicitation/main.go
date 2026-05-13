// Command s07-roots-elicitation demonstrates two **server→client**
// capabilities new in this chapter:
//
//   - `roots/list`: server asks the client what filesystem boundaries
//     it is allowed to operate inside.
//   - `elicitation/create`: server asks the user a question via the
//     client, in either form mode (returns structured data) or URL mode
//     (out-of-band; returns only an action).
//
// The demo tool `who-are-you` exercises the form-mode path: calling it
// causes the server to send an elicitation form back, wait for the
// client's reply, and fold whatever the user typed into the tool
// response.
//
// Because elicitation is bidirectional, a useful stdio demo requires
// a cooperating client. See `make demo` for one that scripts both
// sides through an in-process pipe pair.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

func run(framer Framer) error {
	lc := NewLifecycle()
	ob := NewOutboundRouter(framer, 5*time.Second)
	router := NewRouter(lc, ob)

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
		// Tools that emit outbound requests need to dispatch the inbound
		// loop in a goroutine, otherwise the handler blocks while waiting
		// for the very response it needs to read. We unconditionally
		// goroutine the request side; responses and notifications take
		// the cheap path.
		if msg.IsRequest() {
			go handleRequest(framer, router, msg)
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

func handleRequest(framer Framer, router *Router, msg Message) {
	reply, hasReply := router.Dispatch(msg)
	if !hasReply {
		return
	}
	if err := framer.Write(reply); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
	}
}

func main() {
	framer := NewStdioFramer(os.Stdin, os.Stdout)
	if err := run(framer); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
