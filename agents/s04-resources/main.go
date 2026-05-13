// Command s04-resources demonstrates the MCP `resources/*` surface: list,
// read, templates, subscribe/unsubscribe + `notifications/resources/updated`.
//
// Demo state:
//   - mem://log.txt          — static text resource that the demo will mutate
//                              once after a 100ms delay so a subscribed
//                              client observes a push notification.
//   - mem://users/{id}       — RFC-6570 level-1 template, expands to
//                              mem://users/alice and mem://users/bob.
//
// Usage:
//
//	go run ./agents/s04-resources < script.jsonl
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

var errSkip = errors.New("skip")

func run(framer Framer, store *memStore, n *notifier, lc *lifecycle) error {
	r := newRouter(lc, store, n)
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
			if _, werr := os.Stdout.Write(append(b, '\n')); werr != nil {
				return werr
			}
			continue
		}
		reply, herr := r.handle(msg)
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
	n := newNotifier(framer.Write)
	store := newMemStore(n)
	lc := &lifecycle{}

	// Register the demo state.
	store.Register("mem://log.txt", "log.txt", "Append-only demo log",
		"text/plain", "hello from s04\n")
	store.RegisterTemplate("mem://users/{id}", "user",
		"User record, keyed by id",
		"text/plain",
		map[string]string{
			"alice": "Alice Liddell",
			"bob":   "Bob Builder",
		})

	// After a brief delay, mutate log.txt so any active subscriber sees a
	// push notification. This makes `make demo` useful as a smoke test.
	go func() {
		time.Sleep(100 * time.Millisecond)
		store.Set("mem://log.txt", "text/plain", "hello from s04\nmutated at "+time.Now().Format(time.RFC3339)+"\n")
	}()

	if err := run(framer, store, n, lc); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
