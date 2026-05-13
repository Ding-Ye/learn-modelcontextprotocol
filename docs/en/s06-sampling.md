---
title: "s06 · Reverse-direction LLM: sampling"
chapter: 06
slug: s06-sampling
est_read_min: 14
---

# s06 · Reverse-direction LLM: sampling

> What this teaches: the move that makes MCP an *agent* protocol — the **server** asks the **client** to run an LLM. `sampling/createMessage` is the only request that flows server→client in the base spec, and around it we have to build a "pending outbound" table, response correlation, timeouts, and the `tool_use` / `tool_result` content variants that close the agentic loop.

---

## Problem

Through s01..s05, every request went one way: the client asked, the server answered. That's enough for "expose tools and resources to a model", but it isn't enough for "let the server ask the model a question of its own". The latter is what powers an agent that can, say, parse log output and *decide* whether it needs to summarize a doc, query a DB, or call another tool.

The MCP spec answers this with **one** reverse-direction RPC: `sampling/createMessage`. The server emits a request whose params include a message history, a max-tokens cap, optional tools, and model preferences. The client runs the LLM and replies. That single inversion is what makes the rest of MCP (roots, elicitation, listing-changed notifications) feel coherent — they all reuse the same server→client request scaffolding s06 builds.

The pain this chapter addresses: implement the inversion. Define the `CreateMessageRequestParams` / `Result` shapes, the new `tool_use` and `tool_result` `ContentBlock` variants, a `Sampler` interface so the LLM call site is pluggable, and an `OutboundRouter` that tracks pending requests by id with a 30-second timeout. Round it off with a demo tool whose body *emits* a sampling request mid-flight.

## Solution

Four pieces compose:

1. **`CreateMessageRequestParams`** — schema.ts:1580-1624 verbatim. `Temperature` is `*float64` (0 is a meaningful temperature; nil-vs-zero matters), `MaxTokens` is non-pointer-but-required (validated to `> 0`), and `Tools` / `ToolChoice` only make sense if the client advertised `sampling.tools`.

2. **`Sampler` interface** — `CreateMessage(ctx, params) → (Result, error)`. Two implementations ship: `StubSampler` (canned text or queued tool_use blocks, deterministic for tests) and `AnthropicSampler` (skeleton; HTTP body deferred to Phase G). The interface lives at the *client* boundary even though the s06 demo is one in-process binary — that's where a real client would dispatch to whichever provider it has.

3. **`OutboundRouter`** — a `map[string]*pendingEntry` keyed by request id. `SendRequest` writes the frame, registers the pending channel, and either reads the reply or hits a 30-second timeout. On timeout the entry is deleted *before* returning, so a vanished client doesn't leak goroutines.

4. **`ContentBlock` with five variants** — text, image, audio (carried over from s03), plus `tool_use` and `tool_result`. The latter two are what make sampling agentic: the LLM emits `tool_use`, the server runs the tool in-process, and the next sampling request carries the `tool_result` back.

Three design decisions worth calling out:

- **Sampler interface, not callback.** A `func` would work for s06, but real Samplers carry state (HTTP client, retry policy, rate limiter, cache). Promoting to an interface up front costs nothing.
- **In-process Sampler short-circuit.** When `Router.sampler` is non-nil, `requestSampling` calls it directly instead of going on the wire. This keeps the demo + tests fully synchronous and offline. When the field is `nil`, we *do* emit through `OutboundRouter` — that's the path a real client→server deployment uses.
- **Validation lives in `validateCreateMessage`, not the type system.** `maxTokens > 0` and `includeContext ∈ {none, thisServer, allServers}` aren't expressible in Go's type system without a wrapper, and a wrapper would just hide the constraint. Plain validation function + `-32602` on failure stays honest.

## How It Works

```text
┌──────────────────────────────────────────────────────────────────┐
│                          INBOUND PATH                            │
│ stdin → stdioFramer → Message → Router.DispatchInbound           │
│                                       │                          │
│            ┌──────────────────────────┼────────────────────┐     │
│            ▼                          ▼                    ▼     │
│      initialize                  tools/call           response   │
│      (lifecycle)                      │            (route to     │
│                                       ▼             OutboundRouter│
│                                callSummarize         .HandleResponse) │
│                                       │                          │
│                                       ▼                          │
│                          requestSampling(params)                 │
│                                       │                          │
│                       ┌───────────────┴────────────┐             │
│                       ▼                            ▼             │
│              sampler != nil          sampler == nil              │
│              StubSampler.CreateMessage   OutboundRouter.SendRequest │
│                       │                            │             │
│                       │                            ▼             │
│                       │                  framer.Write + pending  │
│                       │                            │             │
│                       │             ┌──────────────┴────────┐    │
│                       │             ▼                       ▼    │
│                       │     <-pendingEntry.ch         <-timeoutCtx│
│                       │             │                       │    │
│                       └─────────────┴───────────────────────┘    │
│                                     │                            │
│                                     ▼                            │
│                            CreateMessageResult                   │
└──────────────────────────────────────────────────────────────────┘
```

The validation core (`sampling.go`):

```go
func validateCreateMessage(p CreateMessageRequestParams) *Error {
    if p.MaxTokens <= 0 {
        return &Error{Code: InvalidParams, Message: "sampling/createMessage: maxTokens must be > 0"}
    }
    if len(p.Messages) == 0 {
        return &Error{Code: InvalidParams, Message: "sampling/createMessage: messages must not be empty"}
    }
    switch p.IncludeContext {
    case "", "none", "thisServer", "allServers":
    default:
        return &Error{Code: InvalidParams, Message: "sampling/createMessage: invalid includeContext: " + p.IncludeContext}
    }
    return nil
}
```

The pending-table mechanic (`outbound.go`):

```go
ch := make(chan pendingResult, 1)
timeoutCtx, cancel := context.WithTimeout(ctx, o.timeout)
o.mu.Lock()
o.pending[key] = &pendingEntry{ch: ch, cancel: cancel}
o.mu.Unlock()

if err := o.framer.Write(msg); err != nil { ... }

select {
case res := <-ch:
    cancel(); return res.Result, res.Err
case <-timeoutCtx.Done():
    o.removePending(key)
    return nil, fmt.Errorf("outbound %s: timed out after %s", method, o.timeout)
}
```

Four non-obvious points:

1. **Register pending *before* writing.** A fast in-process Sampler (or a local-loopback HTTP transport) could otherwise reply before the channel exists. The cost of the extra `mu.Lock` is the trade.
2. **`pendingEntry.ch` is buffered to 1.** The receiver may have taken the timeout branch; without a buffer, `HandleResponse` would block forever on an orphan id.
3. **`ID.Key()` is the *raw* JSON form.** We can't make `ID` map-comparable (it wraps a slice). Using `string(rawJSON)` as the key is exactly as discriminating as the JSON-RPC spec requires: distinct ids ⇒ distinct keys.
4. **The router's `IsResponse` branch routes to `OutboundRouter.HandleResponse` *before* the lifecycle gate.** Outbound replies must flow even if the inbound surface is gated — otherwise a slow handshake could deadlock a sampler call.

## What Changed (vs. s05)

- **Server-initiated requests appear for the first time.** s01..s05 all assumed unidirectional dispatch. s06 adds an entire `outbound.go` file.
- **`ContentBlock` re-declared with `tool_use` + `tool_result`.** s05 only had text + image + audio + resource. The two new variants are what make sampling agentic — they're the LLM's "request another round" markers.
- **Client capability matters.** The init handshake records `clientSampling = (caps.sampling != nil)`. In the wire path, a server SHOULD refuse to emit sampling if the client didn't advertise it; the in-process demo skips the check because StubSampler always works.
- **`ToolChoice` and `ModelPreferences`.** New types, advisory only. The s06 demo wires them but the StubSampler ignores them — that's exactly how a real client may behave.

## Try It

```bash
cd agents/s06-sampling
make demo
```

You should see four lines of JSON. The interesting one is the third reply (id=3): it's a `tools/call` response whose `content` is *the StubSampler's text*, because the `summarize` tool body emitted a `sampling/createMessage` and rolled the answer up into its own result.

```json
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"(stub summary) the server asked the client to run an LLM; here is the canned reply."}]}}
```

Try the agentic-loop test directly:

```bash
go test -v -run TestSamplingAgenticToolLoop ./agents/s06-sampling
```

You'll see two stub calls (CallCount=2): the first returns a `tool_use` for `echo`, the server runs `echo` in-process, and the second sampling call has a `tool_result` block in its messages. The final tool result is the second reply's text.

## Upstream Source Reading

The s06 region is `schema/2025-11-25/schema.ts:1574-1998` — read it with `agents/s06-sampling/{sampling,sampler,outbound,router}.go` open on the side.

**Sampling: [`schema.ts:1574-1998`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1574-L1998)**

- **L1580-L1624** — `CreateMessageRequestParams`. Note `maxTokens` is non-optional and `includeContext` is a 3-value union ("none" | "thisServer" | "allServers"). `tools` and `toolChoice` are gated on `ClientCapabilities.sampling.tools` (the spec says the client MUST return an error if they appear without the capability — we don't enforce in s06).
- **L1628-L1640** — `ToolChoice`. Three modes: `auto` (default), `required`, `none`. Provider-specific.
- **L1646-L1654** — `CreateMessageRequest`. The method literal is `"sampling/createMessage"` — note it's *server-initiated* but uses the same `JSONRPCRequest` envelope.
- **L1658-L1681** — `CreateMessageResult`. Extends `SamplingMessage` (role + content) plus `model` and an open-string `stopReason` whose standard values are `endTurn` / `stopSequence` / `maxTokens` / `toolUse`.
- **L1683-L1696** — `SamplingMessage`. Role is `user | assistant`; content may be a single block *or* an array. Our Go `UnmarshalJSON` handles both.
- **L1700-L1839** — text / image / audio content blocks (carried over from s03's tools chapter).
- **L1840-L1869** — `ToolUseContent`: `id`, `name`, `input: { [string]: unknown }`. The id pairs with the `toolResult.toolUseId` later.
- **L1874-L1898** — `ToolResultContent`: `toolUseId`, `content: ContentBlock[]`, optional `structuredContent`, optional `isError`. This is what closes the agentic loop.
- **L1930-L1979** — `ModelPreferences`. Three numeric priorities (cost / speed / intelligence, each 0..1) plus a `hints` array of name substrings. Advisory only.

**Prose: [`client/sampling.mdx`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/client/sampling.mdx)** walks through the full server→client interaction with worked JSON examples, including the *human-in-the-loop* requirement: a client SHOULD prompt the user before running an LLM on a server's behalf, and again before returning the result. Our `StubSampler` skips both checks because it's a test scaffold — a production client must not.

Two paragraphs worth memorizing from that page:

- **"Trust & Safety: Sampling allows servers to ask clients to run an LLM, so clients SHOULD implement explicit user approval flows."** The reason `Sampler` is an interface, not a callback: real implementations gate on user input.
- **"Implementation Notes: clients SHOULD implement timeouts."** That's where our 30-second `DefaultOutboundTimeout` comes from.

Local offline slice: [`upstream-readings/s06-sampling.ts`](../../upstream-readings/s06-sampling.ts) (lines 1574-1998 of schema.ts).
