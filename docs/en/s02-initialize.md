---
title: "s02 · Initialize handshake & capability negotiation"
chapter: 02
slug: s02-initialize
est_read_min: 10
---

# s02 · Initialize handshake & capability negotiation

> What this teaches: how MCP turns "two processes talking JSON-RPC" into "two processes with a typed contract about what each side can do" — via a strict three-step handshake (request → result → notification) and a typed capabilities object.

---

## Problem

s01 shipped a single hard-coded reply: any `initialize` request got the same blob back. That's enough to prove the wire shape works, but it papers over the actual protocol contract. MCP requires the server to **read the client's announced capabilities, negotiate a `protocolVersion`, advertise its own capabilities, and then refuse to do anything else until the client says "OK, I'm ready"** (`basic/lifecycle.mdx:36-205`).

That last clause is the one that bites: most JSON-RPC servers are stateless. MCP is not. Until the client posts `notifications/initialized`, the server must reject every non-handshake/non-ping method with a specific error code. If we get this wrong, an MCP client that follows the spec will think our server is broken before it's even reached the interesting features.

## Solution

Model the server as a four-state machine (`new → initializing → operating → closed`) sitting behind a generic `Router`. The Router maps `method` strings to `Handler` functions; the lifecycle handlers (`initialize`, `notifications/initialized`, `ping`) live on a `Server` struct whose `handleRequest` fallback is what enforces the gate. Capabilities — both client-side and server-side — are typed Go structs where every optional sub-capability is a pointer-to-struct, so `nil == "not advertised"` and `&struct{}{} == "advertised with no further options"`. That single representation choice is what makes the wire shape line up with `schema.ts:308-459` without per-field branching.

## How It Works

```text
┌──────────────────────────────────────────────────────────────┐
│ stateNew ─── initialize req ──► handleInitialize             │
│                                       │                       │
│                                       ▼                       │
│                                stateInitializing              │
│                                       │                       │
│           notifications/initialized   │                       │
│                                       ▼                       │
│                                 stateOperating                │
│                                       │                       │
│                          tools/list, resources/read, ...      │
│                                       ▼                       │
│                                 (s03..s07 hand off here)      │
│                                                               │
│ Any other method while state != operating ──► -32002          │
│ Ping is allowed at every state.                               │
└──────────────────────────────────────────────────────────────┘
```

The lifecycle gate, the version-negotiation rule, and the dispatcher in roughly 60 LOC (excerpts from `agents/s02-initialize/lifecycle.go` and `router.go`):

```go
func (s *Server) handleInitialize(_ context.Context, _ string, params json.RawMessage) (any, *Error) {
    var p InitializeRequestParams
    if err := json.Unmarshal(params, &p); err != nil {
        return nil, &Error{Code: InvalidParams, Message: "invalid initialize params: " + err.Error()}
    }
    s.mu.Lock(); defer s.mu.Unlock()
    if s.state != stateNew {
        return nil, &Error{Code: InvalidRequest, Message: "initialize already received"}
    }
    chosen := p.ProtocolVersion
    if !s.supports(p.ProtocolVersion) {
        chosen = s.ProtocolVersion // server-preferred; client decides whether to disconnect.
    }
    s.state = stateInitializing
    return InitializeResult{
        ProtocolVersion: chosen,
        Capabilities:    s.Capabilities,
        ServerInfo:      s.Info,
        Instructions:    s.Instructions,
    }, nil
}

func (s *Server) handleRequest(ctx context.Context, method string, params json.RawMessage) (any, *Error) {
    switch method {
    case "initialize":              return s.handleInitialize(ctx, method, params)
    case "notifications/initialized": return s.handleInitializedNotification(ctx, method, params)
    case "ping":                    return struct{}{}, nil
    }
    s.mu.Lock(); state := s.state; s.mu.Unlock()
    if state != stateOperating {
        return nil, &Error{Code: ServerNotInitialized,
            Message: "server not initialized: method " + method + " rejected in state " + state.String()}
    }
    return nil, &Error{Code: MethodNotFound, Message: "method not found: " + method}
}
```

Four non-obvious points:

1. **Version mismatch is NOT an error.** The spec (`basic/lifecycle.mdx:170-175`) says: if the client asks for a version the server doesn't support, the server **MUST** still respond — with its own preferred version. The *client* then decides whether to disconnect. We encode this as a quiet substitution inside `handleInitialize`, not as a `-32602` error. The `TestWrongProtocolVersionEchoesServerPreferred` test pins this rule.
2. **`notifications/initialized` returns `(nil, nil)`.** Notifications have no `id`, so they can never produce a reply. The router checks `IsNotification()` first and discards whatever the handler returns. The state flip is the *only* observable effect.
3. **`-32002` is "server not initialized", not a JSON-RPC standard code.** The base spec doesn't define it; the MCP TS SDK and most servers use it by convention for the pre-handshake gate. We hard-code it as `ServerNotInitialized` next to the standard `-32600..-32700` block so it's obvious it lives on a different layer.
4. **Pointer-to-struct sub-capabilities serialize cleanly.** `ServerCapabilities.Tools` is `*ToolsCapability`. Nil → field omitted via `omitempty`. Non-nil empty → `"tools": {}` on the wire. Same value, three behaviors. The TS schema's "optional empty objects everywhere" idiom maps to Go this way without writing custom `MarshalJSON`.

## What Changed (vs. s01)

s01 had one file (`envelope.go`) plus `main.go`. s02 adds two new files, keeps the envelope re-declared verbatim, and grows `main.go` into a Router wiring step:

```diff
 agents/s02-initialize/
+ envelope.go     (copied from s01 — same types, no import)
+ lifecycle.go    (NEW: Implementation, ClientCapabilities,
+                       ServerCapabilities, InitializeRequest/Result,
+                       Server, serverState, handleInitialize,
+                       handleInitializedNotification, handleRequest)
+ router.go       (NEW: Handler, Router, Process)
+ main.go         (now: buildServer + buildRouter + run loop)
+ lifecycle_test.go (NEW: 6 tests)
- (s01's hard-coded `initializeResult` map[string]any)
```

The biggest conceptual shift is the introduction of **state**: s01's server was a pure function from request to response; s02's `Server` carries a lifecycle. Every later chapter widens the operating-state handler table without touching the lifecycle gate.

## Try It

```bash
cd agents/s02-initialize
make demo
```

Expected output: three lines of JSON. The first is the `InitializeResult`, the second is nothing (because the notification has no reply), the third is the `ping` empty result `{}`.

```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"logging":{},"completions":{},"prompts":{},"resources":{},"tools":{}},"serverInfo":{"name":"learn-mcp-s02-initialize","version":"0.2.0"},"instructions":"s02: handshake + capability negotiation. Use `notifications/initialized` to unlock the rest."}}
{"jsonrpc":"2.0","id":2,"result":{}}
```

Try the pre-init rejection by calling `tools/list` before sending the notification:

```bash
{ printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}'; \
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'; } | ./s02-initialize
```

You'll see the second reply carry `"error":{"code":-32002,"message":"server not initialized: method tools/list rejected in state initializing"}`.

Test the whole suite:

```bash
make test
```

## Upstream Source Reading

Read [`schema.ts:251-459`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L251-L459) side by side with `agents/s02-initialize/lifecycle.go`. Key landmarks:

- **L251-L274** — `InitializeRequestParams` and `InitializeRequest`. `protocolVersion`, `capabilities`, `clientInfo` are all required.
- **L276-L295** — `InitializeResult`. Note the comment: "If the client cannot support this version, it MUST disconnect" — the disconnect is the *client's* responsibility, not the server's.
- **L297-L305** — `InitializedNotification`. No params required; the method name is the entire signal.
- **L308-L381** — `ClientCapabilities`. Our Go struct mirrors `roots`, `sampling`, `elicitation`, plus the open `experimental` map. We omit the `tasks` capability (it's an exercise in Appendix B).
- **L388-L459** — `ServerCapabilities`. Same shape mirror. `logging` and `completions` are flag-only (empty objects); `prompts`/`resources`/`tools` carry `listChanged` and (for resources) `subscribe` booleans.

Then read [`basic/lifecycle.mdx:36-205`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/basic/lifecycle.mdx#L36-L205) for the normative prose. Three paragraphs do most of the work:

- The three-step handshake: `initialize` request → `InitializeResult` → `notifications/initialized`.
- Version negotiation: server MUST still reply on mismatch, with its own preferred version.
- The pre-handshake gate: only `ping` (and server-side logging) is legal before the notification.

A local copy of the schema slice lives at [`upstream-readings/s02-initialize.ts`](../../upstream-readings/s02-initialize.ts) for offline reading.
