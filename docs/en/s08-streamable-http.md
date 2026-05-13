---
title: "s08 · Streamable HTTP transport"
chapter: 08
slug: s08-streamable-http
est_read_min: 18
---

# s08 · Streamable HTTP transport

> What this teaches: how MCP runs on top of HTTP. One endpoint, two methods (POST and GET), and Server-Sent Events to recover the duplex stream-of-messages property that stdio gave us for free in s01-s07. Sessions, `Mcp-Session-Id`, `MCP-Protocol-Version`, and `Last-Event-ID` replay are all here. Everything *inside* the JSON-RPC envelope is unchanged — that's the whole point.

---

## Problem

s01-s07 ran on stdio. One process, one bidirectional byte stream, newline-delimited JSON. Lovely. Now ship that to the web.

HTTP is request-response. There is no second message until the client asks for one. But MCP needs the *server* to push notifications, send `sampling/createMessage` requests back into the client, and stream partial progress on long tool calls. That doesn't fit in `POST → response → done`.

The Streamable HTTP transport (basic/transports.mdx:52-330) is the answer. It puts every client→server message on a POST, but lets the server choose at response time between a one-shot JSON body and an open SSE stream. A separate GET endpoint lets the server push *unsolicited* messages to the client. Session continuity is recovered via a header (`Mcp-Session-Id`), and resumption via SSE event IDs (`Last-Event-ID`).

The pain this chapter addresses: ship a real HTTP server that does all of this — sessions, headers, SSE upgrade when a tool needs sampling, GET-stream notifications, and replay on reconnect — without using any third-party libraries. `net/http` is enough.

## Solution

One handler at `/mcp`, dispatch by method:

- **POST** is every client→server message. The body is one JSON-RPC envelope. The response is *either* `application/json` (one-shot) or `text/event-stream` (SSE), and the server picks based on whether the handler needs to round-trip back to the client.
- **GET** opens a long-lived SSE stream for *unsolicited* server→client traffic (notifications, periodic progress, anything not tied to a specific request).
- **DELETE** terminates a session.

Key design decisions:

1. **`HTTPFramer` is per-request.** s01-s07 had a `Framer` that streamed messages forever. HTTP doesn't work that way: each request is one envelope in, one envelope out. We keep the *name* `Framer` for shape continuity but `Read()` returns `io.EOF` after one call.

2. **`SSEStream` is session-scoped, not request-scoped.** Event IDs must be monotonic across POST-upgraded streams *and* the GET stream, because `Last-Event-ID` is one cursor per session (basic/transports.mdx:174-178). So the counter lives on `Session`, not on the stream object.

3. **Sampling drives the SSE upgrade.** Synchronous tools return JSON; tools that *might* invoke `sampling/createMessage` upgrade to SSE so the server can interleave its outbound request with the eventual tool result. We use a per-request `Sampler` interface; the s06 stdio chapter used the same shape, the only change is what the implementation does (write to an SSE stream + wait for the client to POST back the response).

4. **Sessions are minted at `initialize`, opaque to the client.** A 16-byte hex string in `Mcp-Session-Id` is enough. The spec also accepts UUIDs and JWTs; the rule is "globally unique, ASCII 0x21-0x7E" (basic/transports.mdx:200-206). Session state (subscriptions, pending outbound requests, SSE event counter, replay buffer) hangs off `*Session`.

5. **Replay buffer is in-memory, last 100 events.** When a client reconnects with `Last-Event-ID: 3`, we replay events `4..N` from a ring buffer attached to the session. A real impl would back this with Redis; the API stays the same. Sizes are pragmatic — 100 events covers a 10-second hiccup at typical notification rates.

6. **Protocol version header is mandatory on every POST after `initialize`.** Returning 400 Bad Request when it's missing is what transports.mdx:268-272 prescribes. Skipping the check on `initialize` itself is also explicit in the spec ("on all subsequent requests").

## How It Works

```text
┌──────────────────────────────────────────────────────────────────────┐
│ POST /mcp                                                            │
│   │                                                                  │
│   ▼                                                                  │
│ read body, decode Message                                            │
│   │                                                                  │
│   ▼                                                                  │
│ validate MCP-Protocol-Version (unless initialize)                    │
│   │                                                                  │
│   ▼                                                                  │
│ resolve Mcp-Session-Id  ──► SessionStore.Get / New                   │
│   │                                                                  │
│   ▼                                                                  │
│ dispatch on method                                                   │
│   ├── notification/response  ─► 202 Accepted                         │
│   ├── initialize             ─► JSON body + session header           │
│   ├── synchronous request    ─► JSON body                            │
│   └── sampling-bound request ─► SSE upgrade                          │
│                                   │                                  │
│                                   ▼                                  │
│                              NewSSEStream                            │
│                                   │                                  │
│                                   ├── server sends sampling/...      │
│                                   │   (event id N, replay buffer)    │
│                                   ▼                                  │
│                              wait on sess.pending[id]                │
│                                   │                                  │
│                                   │  (client POSTs response on a     │
│                                   │   separate connection; the POST  │
│                                   │   handler delivers it via the    │
│                                   │   pending channel)               │
│                                   ▼                                  │
│                              server sends CallToolResult             │
│                                   │                                  │
│                                   ▼                                  │
│                              close stream                            │
│                                                                      │
│ GET /mcp                                                             │
│   │                                                                  │
│   ▼                                                                  │
│ same header checks, then NewSSEStream                                │
│   │                                                                  │
│   ├── if Last-Event-ID present: SSEBuffer.Since → replay             │
│   │                                                                  │
│   ▼                                                                  │
│ for { select { sess.notif → stream.Send | r.Context().Done() → exit }}│
└──────────────────────────────────────────────────────────────────────┘
```

The SSE writer is short. Excerpted from `agents/s08-streamable-http/framer.go`:

```go
func (s *SSEStream) Send(m Message) (int64, error) {
    raw, _ := json.Marshal(m)
    return s.SendRaw(raw)
}

func (s *SSEStream) SendRaw(data []byte) (int64, error) {
    id := s.counter.Add(1)
    fmt.Fprintf(s.w, "id: %d\ndata: %s\n\n", id, data)
    s.flusher.Flush()
    s.buf.Append(id, data)
    return id, nil
}
```

The orchestrator that ties sampling to the SSE stream lives in `server.go`. The key trick is that the `Sampler` we pass into the tool handler is *per-request* — it writes to *this* response's SSE stream and waits on `sess.pending[id]` for the client's reply (which arrives on a *different* HTTP connection, demultiplexed by request id):

```go
func (s *sseSampler) CreateMessage(ctx context.Context, req CreateMessageRequest) (CreateMessageResult, error) {
    id := StringID("s8-" + strconv.FormatInt(s.nextID.Add(1), 10))
    ch := make(chan Message, 1)
    s.sess.pending[id.String()] = ch
    s.stream.Send(Message{ID: &id, Method: "sampling/createMessage", Params: paramsRaw})
    select {
    case reply := <-ch: /* unmarshal and return */
    case <-ctx.Done(): /* cancelled */
    }
}
```

Four non-obvious points:

1. **POST and GET share the SSE event counter.** A response delivered on a POST-upgraded stream and a notification delivered on the GET stream are *in the same id space*. That's because resumption (`Last-Event-ID`) is per-session and the buffer is shared. If they were in separate spaces, a `Last-Event-ID: 7` would be ambiguous.

2. **The client's *response* to a sampling request comes back on a separate connection.** It is *not* delivered on the same TCP socket that's holding the SSE stream open. The client POSTs the response with the session header, the POST handler sees an envelope with `ID` and no `Method`, looks it up in `sess.pending`, and delivers via channel. The SSE-side goroutine is parked on that channel.

3. **`MCP-Protocol-Version` is rejected even when it differs only by an old date.** s08 hard-codes `2025-11-25`. transports.mdx:280-282 says "if invalid or unsupported, 400 Bad Request" — so we do that, mechanically. The spec also has a back-compat path (assume `2025-03-26` if the header is absent on a non-initialize); we choose strict 400 instead, because the lesson is that the header is *normative*.

4. **Sessions can be terminated by either side.** DELETE from the client; or the server may evict on idle. We don't implement timeout eviction (it's a real-world concern; the test surface doesn't need it), but `SessionStore.Delete` is in place. After eviction, the next request with that session id returns 404 (transports.mdx:214-217), which tells the client "start over with a fresh initialize".

## What Changed (vs. s07)

s07 introduced `roots/list` and the elicitation flow — client-side handlers driven by server requests. s08 keeps every per-message shape unchanged. What changed is *outside* the envelope:

- **Transport swap, top to bottom.** `stdioFramer` is gone. In its place we have `HTTPFramer` (per-request) and `SSEStream` (server-side writer over a flushed response).
- **`Framer.Read()` semantics changed.** s01-s07: blocking, returns the next message forever. s08: one-shot per request, returns `io.EOF` after the first call. We kept the name to make the lesson "the abstraction shape was right; the implementation is what changes".
- **State lives outside the request.** s07 had one process, one session, all in memory. s08 has a `SessionStore` keyed by `Mcp-Session-Id`; every handler looks up its session at the top of the request. The state machine inside the lifecycle (uninitialized → initializing → ready) is now per-session.
- **The `initialize` handler is the *only* method that can mint a session.** Every other request that arrives without a session id (or with an unknown one) is rejected at the door — 400 for missing, 404 for stale. This is the wire-level enforcement of the lifecycle gate s02 introduced as a state machine.
- **Three new headers.** `Mcp-Session-Id` (response on initialize; request on every subsequent POST). `MCP-Protocol-Version` (request on every POST after initialize; 400 if missing). `Last-Event-ID` (request on GET to resume an SSE stream). All three live in the HTTP layer; the JSON-RPC body never carries them.
- **First time the server pushes a message asynchronously.** s06 had `sampling/createMessage` over stdio; the stream was always-on so "asynchronous" was free. s08 has to *create* the bidirectional channel via SSE before any push is possible. The cost of HTTP modeling is making this transition explicit.

## Try It

```bash
cd agents/s08-streamable-http
make demo
```

You'll see seven lines of console output. Highlights:

```
[demo] initialize → session=957a70ce941ab062ecf1d6e45be9718a
[demo]   body={"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{...}}}
[demo] tools/call → upgraded to SSE
[demo]   ← sampling/createMessage id="s8-1"
[demo]   → posted sampling response
[demo] tools/call result: {"content":[{"type":"text","text":"MCP is a JSON-RPC protocol for LLMs."}]}
```

Try the test suite to see the assertions in code form:

```bash
make test
```

Try POSTing without the protocol header:

```bash
curl -i -X POST http://127.0.0.1:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'Mcp-Session-Id: ...' \
  -d '{"jsonrpc":"2.0","id":7,"method":"tools/list"}'
```

You should see `HTTP/1.1 400 Bad Request` and the body `missing MCP-Protocol-Version header`.

Try opening a GET stream and pushing yourself a notification:

```bash
curl -N http://127.0.0.1:8080/mcp \
  -H 'Accept: text/event-stream' \
  -H 'Mcp-Session-Id: ...' \
  -H 'MCP-Protocol-Version: 2025-11-25'
```

(The demo server has no public method to push from the outside; you'd see this in the Go test that injects a `sess.Notify(...)` from a parallel goroutine.)

## Upstream Source Reading

The single normative source for this chapter is [`basic/transports.mdx:52-330`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/basic/transports.mdx#L52-L330). Read it with `agents/s08-streamable-http/server.go` open in the other pane — the file matches the prose section-by-section.

- **L52-L84** — overview, endpoint shape, security warning (validate `Origin`, bind to localhost when running locally, authenticate).
- **L88-L128** — "Sending Messages to the Server". The POST contract: body is one JSON-RPC envelope; response is either `application/json` or `text/event-stream`; the SSE stream MAY interleave server-initiated requests and notifications before terminating with the response to the originating request. Maps directly to `Server.handlePOST` and `Server.runToolOverSSE`.
- **L132-L150** — "Listening for Messages from the Server" — the GET stream. Maps to `Server.handleGET`.
- **L154-L162** — "Multiple Connections". One message belongs to one stream; no broadcasting. We don't expose this constraint to the user because s08 only has one GET stream per session.
- **L166-L192** — "Resumability and Redelivery". Per-stream monotonic event IDs, `Last-Event-ID` for replay, "server MUST NOT replay messages that would have been delivered on a different stream". Our `SSEBuffer.Since(lastID)` enforces this — there's only one buffer per session, so the constraint is automatic.
- **L196-L232** — "Session Management". `Mcp-Session-Id` lifecycle, ASCII 0x21-0x7E constraint, 404 when the server has expired the session, optional DELETE.
- **L258-L282** — "Protocol Version Header". Mandatory on subsequent requests; 400 on invalid/unsupported. Maps to the header check at the top of `handlePOST` and `handleGET`.
- **L286-L312** — "Backwards Compatibility". s08 deliberately omits the 2024-11-05 HTTP+SSE fallback; the curriculum stops at 2025-11-25.

Companion SEPs worth reading **after** the spec, because they tell you where the design is going:

- [SEP-2243 — HTTP Header Standardization](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2243-http-standardization.md). Argues for mirroring routing fields into HTTP headers so load balancers, WAFs, and observability tools can act without deep packet inspection. Already partially landed (the protocol-version and session-id headers).
- [SEP-2575 — Make MCP Stateless](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2575-stateless-mcp.md). Proposes removing the `initialize` handshake and carrying protocol version + capabilities per-request. Reads as the future direction for serverless/edge deployments where keeping per-client state in memory is infeasible.
- [SEP-2567 — Sessionless MCP via Explicit State Handles](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2567-sessionless-mcp.md). The companion to 2575: drop sessions entirely, push state-keeping out into tool-defined opaque IDs (e.g. a `basket_id` returned from `create_basket`). If both SEPs ship, s08's `SessionStore` is the last generation of code that needs to exist.

Local offline slice: [`upstream-readings/s08-streamable-http.mdx`](../../upstream-readings/s08-streamable-http.mdx).
