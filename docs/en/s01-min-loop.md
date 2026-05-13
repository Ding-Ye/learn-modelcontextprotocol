---
title: "s01 · Minimum loop: JSON-RPC + stdio framing"
chapter: 01
slug: s01-min-loop
est_read_min: 8
---

# s01 · Minimum loop: JSON-RPC + stdio framing

> What this teaches: the smallest MCP server you can build — one JSON-RPC envelope, one `initialize` reply, one stdio framing rule. Everything later in this curriculum is a series of strictly-larger versions of this loop.

---

## Problem

MCP is a protocol, not a library. Before we can negotiate capabilities, list tools, or sample LLMs, we need to know exactly what a wire message looks like. The spec (`schema/2025-11-25/schema.ts:1-180`) defines a JSON-RPC 2.0 envelope with one subtle twist — request `id` MUST NOT be `null`, where standard JSON-RPC permits it. And the stdio transport (`basic/transports.mdx:1-51`) layers a framing rule on top: one JSON message per line, no embedded newlines.

If we get either of those wrong, every subsequent chapter is built on sand. The pain this chapter addresses: ship a runnable program that parses an `initialize` request, replies with a valid `InitializeResult`, and refuses to do anything else. Once we can stand that up, we know our envelope is right.

## Solution

Think of an MCP server as a tiny event loop with three layers stacked vertically: a **framer** that owns the line-delimited stdio bytes, an **envelope decoder** that turns a single line into a typed `Message`, and a **handler** that pattern-matches on `method`. In s01 the handler has one arm (`"initialize"`) and a default arm (`-32601 MethodNotFound`). Every later chapter widens the handler, never the framer.

Key design decisions:

1. **`Message` is one struct covering four wire shapes.** Request / notification / response / error all share the same JSON skeleton; we use pointer-and-omitempty rather than a discriminated union, and rely on helper methods (`IsRequest`, `IsNotification`, `IsResponse`) to classify.
2. **`null` request ids are rejected at decode time.** Custom `UnmarshalJSON` on `Message` (not on `ID` — Go's `encoding/json` skips pointer Unmarshalers on `null`).
3. **The framer guards against embedded newlines on write.** `json.Marshal` already escapes `\n` inside strings, but a bug elsewhere could still produce a corrupted line; we treat that as a programming error and refuse to write.

## How It Works

```text
┌──────────────────────────────────────────────────────┐
│ stdin (bytes)                                        │
│   │                                                  │
│   ▼                                                  │
│ ┌──────────────┐    one line                         │
│ │ stdioFramer  │─────────────► json.Unmarshal ──┐    │
│ └──────────────┘                                ▼    │
│                                          ┌──────────┐│
│                                          │  Message ││
│                                          └──────────┘│
│                                                ▼     │
│   ┌────────── handle(msg) ──────────┐                │
│   │ method == "initialize" ─► result│                │
│   │ default                ─► -32601│                │
│   └────────────────────────────────-┘                │
│                                                ▼     │
│                            json.Marshal ──► stdout   │
└──────────────────────────────────────────────────────┘
```

The core 40 lines (excerpt from `agents/s01-min-loop/envelope.go`):

```go
type Message struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      *ID             `json:"id,omitempty"`
    Method  string          `json:"method,omitempty"`
    Params  json.RawMessage `json:"params,omitempty"`
    Result  json.RawMessage `json:"result,omitempty"`
    Error   *Error          `json:"error,omitempty"`
}

func (m *Message) UnmarshalJSON(data []byte) error {
    type rawMessage Message
    var probe struct{ ID json.RawMessage `json:"id"` }
    if err := json.Unmarshal(data, &probe); err == nil {
        if len(probe.ID) > 0 && bytes.Equal(bytes.TrimSpace(probe.ID), []byte("null")) {
            return errors.New("mcp: request id must not be null")
        }
    }
    return json.Unmarshal(data, (*rawMessage)(m))
}

func (f *stdioFramer) Write(m Message) error {
    if m.JSONRPC == "" { m.JSONRPC = JSONRPCVersion }
    raw, err := json.Marshal(m)
    if err != nil { return err }
    if bytes.ContainsAny(raw, "\r\n") {
        return errors.New("mcp: serialized frame contains forbidden newline")
    }
    raw = append(raw, '\n')
    _, err = f.out.Write(raw)
    return err
}
```

Four non-obvious points:

1. **Pointer-`*ID` + custom `UnmarshalJSON` on `Message`.** Go's `encoding/json` sees `"id": null` and sets a `*ID` field to `nil` without invoking the pointee's `UnmarshalJSON`. The probe in `Message.UnmarshalJSON` catches null *before* the standard decoder runs, then re-decodes via a type alias to avoid infinite recursion.
2. **No `Result.Marshal` / `Error.Marshal` separation.** A response either has `Result` or `Error`, never both, but we don't split them into separate types. Encoding cost: 4 bytes (an extra `omitempty` field on the wire); pedagogy cost: zero.
3. **`stdioFramer.Read` uses `bufio.ReadBytes('\n')`, not `Scanner`.** `bufio.Scanner` has a 64 KB line limit by default; real MCP payloads (resource contents, base64 blobs) easily exceed that.
4. **`json.RawMessage` for params/result.** We never decode the inner shape at the envelope layer. Each session decides what to do with `m.Params` after dispatch. This is what lets later chapters add new methods without touching this file.

## What Changed (vs. (none))

This is the bootstrap chapter; nothing precedes it. The reference point for "what changed" is the *upstream* spec: we wrote ~150 lines of Go (envelope + framer) that mirror `schema.ts:1-180` (envelope types) and `basic/transports.mdx:1-51` (framing rules).

## Try It

```bash
cd agents/s01-min-loop
make demo
```

Expected output (single line):

```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{},"serverInfo":{"name":"learn-mcp-s01-min-loop","version":"0.1.0"},"instructions":"This is s01: a single-shot initialize echo. Try anything else and you'll get -32601."}}
```

Try sending an unknown method:

```bash
echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' | ./s01-min-loop
```

You should see `"error":{"code":-32601,"message":"method not found: tools/list"}`.

And the null-id rejection:

```bash
echo '{"jsonrpc":"2.0","id":null,"method":"initialize"}' | ./s01-min-loop
```

You should see a parse-error response (the framer can't even decode this message, so the handler never runs).

## Upstream Source Reading

The upstream MCP spec is in TypeScript. Read [`schema.ts:1-180`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1-L180) side by side with `agents/s01-min-loop/envelope.go`. Key landmarks:

- **L1-L16** — `JSONRPCMessage` union and `JSONRPC_VERSION = "2.0"`.
- **L17-L73** — `Request`, `Notification`, `Result`, `Error` shapes.
- **L100-L130** — `RequestId = string | number`. The TypeScript union excludes `null`; we encode this constraint as a runtime check.
- **L140-L156** — `JSONRPCError` (note the optional `data` field; we keep it as `json.RawMessage`).
- **L175-L180** — Standard error code constants. We hard-code the same numbers.

Then [`basic/transports.mdx:1-51`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/basic/transports.mdx#L1-L51) for the framing rules. Two paragraphs are load-bearing: messages MUST NOT contain embedded newlines, and they MUST be UTF-8.

Local copies for offline reference live in [`upstream-readings/s01-min-loop.ts`](../../upstream-readings/s01-min-loop.ts) (the relevant schema slice) and [`upstream-readings/s01-min-loop.mdx`](../../upstream-readings/s01-min-loop.mdx) (the transports prose).
