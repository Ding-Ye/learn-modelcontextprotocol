# s08 — Streamable HTTP transport

The transport swap. Every previous chapter ran on stdio: one process,
one bidirectional byte stream, newline-delimited JSON. s08 throws that
out and rebuilds the wire on top of HTTP — a single endpoint that
accepts POST (client → server messages) and GET (long-lived SSE for
server → client unsolicited traffic), plus Server-Sent Events when a
POST request needs to round-trip back into the client (e.g. sampling).

The schema does not change. The envelope does not change. Every
session up to s07 still works *unchanged inside its handler*. What
changes is everything around it — sessions, headers, content-type
negotiation, event-id-based replay, and "is this request synchronous
or does it upgrade to a stream?"

## Run

```bash
make demo
```

The demo starts the server on `127.0.0.1:8087`, runs an in-process
client through the full handshake, makes a `tools/call` to `summarize`,
sees the response upgrade to `text/event-stream`, answers the
`sampling/createMessage` request, and prints the final
`CallToolResult`. Then it shuts down.

## Test

```bash
make test
```

Five tests cover the required cases:

1. POST `initialize` → 200 OK, JSON body, `Mcp-Session-Id` header set.
2. POST `tools/call summarize` → upgrades to SSE, round-trips a
   `sampling/createMessage`, terminates with the tool result.
3. GET `/mcp` → opens long-lived SSE; the server emits one
   `notifications/message`; the client decodes it.
4. GET `/mcp` with `Last-Event-ID: 3` → replays events 4 and 5 from
   the per-session ring buffer.
5. POST `tools/list` without `MCP-Protocol-Version` header → 400 Bad
   Request.

## Files

| file              | purpose                                                                                |
|-------------------|----------------------------------------------------------------------------------------|
| `envelope.go`     | `Message`, `ID`, `Error` — seventh re-declaration                                      |
| `lifecycle.go`    | `initialize` handler advertising `tools` (+ requiring `sampling` from the client)      |
| `framer.go`       | `HTTPFramer` (one-shot POST), `SSEStream` (server-side writer), `SSEBuffer` (replay)   |
| `session.go`      | `Session` and `SessionStore`; cryptographic session IDs                                |
| `tools.go`        | `Tool`/`CallToolResult` re-declared; the `summarize` tool that drives sampling         |
| `server.go`       | `http.Handler` for POST/GET/DELETE; the `runToolOverSSE` orchestrator                  |
| `main.go`         | listener + a one-flag built-in demo client                                             |
| `server_test.go`  | five httptest-driven tests                                                             |
| `Makefile`        | `build` / `test` / `demo`                                                              |

## Upstream source reading

See [`../../docs/zh/s08-streamable-http.md`](../../docs/zh/s08-streamable-http.md) (中文)
or [`../../docs/en/s08-streamable-http.md`](../../docs/en/s08-streamable-http.md) (English)
for the annotated walkthrough.

Key upstream sections:

- `docs/specification/2025-11-25/basic/transports.mdx:52-330` — the
  Streamable HTTP transport prose: POST/GET semantics, SSE event IDs,
  session lifecycle, protocol-version header, backwards compatibility.
- `seps/2243-http-standardization.md` — header standardization.
- `seps/2575-stateless-mcp.md` — proposal to remove `initialize` and
  make MCP stateless.
- `seps/2567-sessionless-mcp.md` — companion to 2575: drop sessions
  entirely, use explicit state handles.

Local offline copy: [`upstream-readings/s08-streamable-http.mdx`](../../upstream-readings/s08-streamable-http.mdx).
