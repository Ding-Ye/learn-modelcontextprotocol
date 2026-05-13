---
title: "s07 · Roots and elicitation (form + URL)"
chapter: 07
slug: s07-roots-elicitation
est_read_min: 13
---

# s07 · Roots and elicitation (form + URL)

> What this teaches: two more **server→client** capabilities, both layered on top of the outbound-request scaffolding from s06. `roots/list` lets the server ask "what filesystem boundaries am I allowed inside?"; `elicitation/create` lets the server ask the user a question, in either a structured form (returns data) or a URL the client opens out of band (returns only an action). The chapter also introduces the first MCP-reserved error code, `-32042 URLElicitationRequired`, the typed short-circuit a tool returns when it cannot answer until the user finishes an URL elicitation.

---

## Problem

s06 inverted the protocol once: the server, mid-tool-call, asked the *model* via `sampling/createMessage` and waited. That was one direction. There are two more:

1. **Filesystem boundaries.** A server that wants to read files or grep a repo needs to know which paths the host is willing to expose. That set is *editor state* (a workspace picker, an open-folder dialog), not server state — so the server has to ask. The wire shape is `roots/list`; each `Root` is a `file://` URI plus an optional display name (`schema.ts:2089-2160`).
2. **User questions.** A server might need a non-sensitive bit of information from a human ("which channel?", "confirm destructive action?"); or it might need sensitive information that *must not* pass through the LLM / client at all (an API key, an OAuth consent). MCP unifies these under `elicitation/create` with a `mode` discriminator: `form` flows data inline, `url` directs the user to a browser surface and returns only `accept`/`decline`/`cancel` (`schema.ts:2161-2506`, `client/elicitation.mdx`).

The pain s07 addresses: ship a server that can issue both kinds of outbound request, and a client-side test harness that answers them; show the form-mode happy path, the cancel/decline paths, the URL-mode params shape, and the `-32042` short-circuit error code by which a handler defers until elicitation finishes.

## Solution

We reuse s06's idea — server-initiated requests go through an `OutboundRouter` that keeps a pending-id → channel table — and add two thin senders on top. The mode-split is what would otherwise hurt: TypeScript discriminated unions don't translate cleanly to Go, so we declare two distinct param structs (`ElicitRequestFormParams`, `ElicitRequestURLParams`) and two senders (`sendFormElicitation`, `sendURLElicitation`). The reply shape is shared (`ElicitResult` with `Action ∈ {accept, decline, cancel}`); the decoder scrubs `Content` for any non-accept reply so a buggy client can't leak data through cancel.

Four key design decisions:

1. **Client capabilities gate outbound sends.** Per the spec, "Servers MUST NOT send elicitation requests with modes that are not supported by the client." We record what the client declared during `initialize` (`elicitation.form`, `elicitation.url`) and refuse to send a mode the client never advertised. The empty-object back-compat sugar (`elicitation: {}` ≡ `{form: {}}`) lives in `lifecycle.go`.
2. **`URLElicitationRequiredError` is a Go-typed short-circuit, not a manually constructed `*Error`.** A handler returning `&URLElicitationRequiredError{Message: "...", Elicitations: [...]}` lets the router serialise the canonical `-32042` reply with `data.elicitations` populated. This keeps the error code path symmetric with the typed result path.
3. **The `OutboundRouter` is *re-implemented*, not imported from s06.** The plan forbids cross-module imports between sessions; the repetition is the lesson. We do trim it — `OutboundRouter` here has only `SendRequest` + `Dispatch` + a timeout; no progress notifications.
4. **Decline and cancel are *not* RPC errors.** They are valid user actions. A tool that called `sendFormElicitation` and got `action: "decline"` produces a normal text content block ("User declined to share..."), not an `isError: true` result. Errors are reserved for transport / protocol failure.

## How It Works

```text
┌──────────────────────────────────────────────────────────────────┐
│ Client                                       Server               │
│ ─────                                        ──────               │
│ tools/call who-are-you ─────────────────────► Router.Dispatch     │
│                                                  │                │
│                                                  ▼                │
│                                          whoAreYou() handler      │
│                                                  │                │
│                                                  ▼                │
│                                  sendFormElicitation              │
│ ◄── elicitation/create (mode:form) ──────── OutboundRouter        │
│        params.requestedSchema                 (parks goroutine)   │
│                                                                   │
│ user answers a name in the UI                                     │
│                                                                   │
│ {action:"accept",content:{name:"Ada"}} ───► Dispatch matches id   │
│                                              ▼                    │
│                                       resume handler              │
│                                              ▼                    │
│                                  CallToolResult{                  │
│                                    Content: "Hello, Ada!"         │
│                                  }                                │
│ ◄── tools/call result ─────────────────────                       │
└──────────────────────────────────────────────────────────────────┘
```

The outbound send / dispatch pair (`agents/s07-roots-elicitation/outbound.go`):

```go
func (r *OutboundRouter) SendRequest(method string, params any) (json.RawMessage, error) {
    rawParams, _ := json.Marshal(params)
    id := NumberID(r.nextID.Add(1))
    entry := &pendingEntry{ch: make(chan outboundReply, 1), method: method}
    r.mu.Lock(); r.pending[id.String()] = entry; r.mu.Unlock()
    if err := r.w.Write(Message{JSONRPC: JSONRPCVersion, ID: &id, Method: method, Params: rawParams}); err != nil {
        r.cancel(id.String()); return nil, err
    }
    select {
    case reply := <-entry.ch:
        if reply.err != nil { return nil, reply.err }
        return reply.result, nil
    case <-time.After(r.timeout):
        r.cancel(id.String())
        return nil, fmt.Errorf("outbound %s timed out", method)
    }
}
```

And the URL-elicitation-required short-circuit (`elicitation.go`):

```go
type URLElicitationRequiredError struct {
    Message      string
    Elicitations []ElicitRequestURLParams
}

func (e *URLElicitationRequiredError) asRPCError() *Error {
    payload := struct{ Elicitations []ElicitRequestURLParams `json:"elicitations"` }{e.Elicitations}
    data, _ := json.Marshal(payload)
    return &Error{Code: URLElicitationRequired, Message: e.Message, Data: data}
}
```

Four non-obvious points:

1. **`mode` is optional in form mode, required in URL mode.** The spec keeps form's `mode` optional for backward compatibility — clients MUST treat an absent `mode` as form. We always emit `"mode": "form"` to keep the wire unambiguous; older servers that omit it remain compatible.
2. **The form-mode `requestedSchema` is *not* full JSON Schema.** Only top-level primitives are allowed (string / number / boolean / enum). No nested objects. No arrays of objects. This deliberate restriction is what makes form-mode UI tractable (the client renders one flat form), and it's what justifies the existence of a separate URL mode for anything richer.
3. **URL mode returns *no* data on the JSON-RPC wire — by design.** That is the security boundary: if the user enters an API key into the browser surface, the MCP client never sees it. The server learns about it only through whatever side channel the URL terminates in (its own DB, an OAuth callback). The optional `notifications/elicitation/complete` exists so the client can update its UI when the out-of-band step finishes, but it still doesn't carry user data.
4. **The `-32042` error code lives in the implementation-specific JSON-RPC band [-32000, -32099].** It is the first MCP code outside the standard set. A server only returns it when URL-mode elicitation is required; returning it for any other reason is a spec violation.

## What Changed (vs. s06)

s06 introduced server-initiated requests for `sampling/createMessage`. s07 keeps the same pattern and adds two more endpoints on the same machinery:

- **Two new outbound RPCs**, no new transport features. `OutboundRouter` is structurally the same; only the per-method param/result types differ.
- **`ClientCapabilities` extended.** `elicitation: {form: {}, url: {}}` is new. The empty-object back-compat rule (`elicitation: {} ≡ {form: {}}`) is documented in `lifecycle.go`.
- **First non-standard JSON-RPC error code.** `-32042 URLElicitationRequired` joins the four standard codes (`-32700/-32600/-32601/-32602/-32603`). Returning it requires a typed `data.elicitations` payload; we wrap that in `URLElicitationRequiredError`.
- **First time we use an "out-of-band channel".** URL-mode elicitation is the spec's first acknowledgement that not every interaction can or should fit inside MCP's JSON-RPC channel.

## Try It

```bash
cd agents/s07-roots-elicitation
make test
make demo
```

`make test` runs all five tests. `make demo` runs two of them with `-v` so you can see what an accepted elicitation and a declined one look like end-to-end.

The five tests cover:

- `TestRootsListRoundTrip` — server fires `roots/list`, test harness answers with two `file://` URIs, server decodes the reply and the pending table empties.
- `TestFormElicitationAcceptReturnsContent` — server fires form elicitation, client replies `accept` + `{name: "Ada"}`, server reads it.
- `TestFormElicitationCancelDropsContent` — same flow, but client returns `cancel`; decoder scrubs `Content` even if the client leaks it.
- `TestURLElicitationParamsShape` — confirms URL params carry `mode: "url"`, `elicitationId`, `url`, and **no** `requestedSchema`.
- `TestDeclinedElicitationPropagates` — full `tools/call who-are-you` round-trip, client returns `decline`, tool result is a normal text block (not `isError`).

To inspect a real outbound elicitation by hand, you'd need a cooperating MCP client. The simplest path is to write one in twenty lines on top of `pipeFramer` — the test file shows the pattern.

## Upstream Source Reading

The s07 surface is one contiguous slice of `schema.ts` plus two prose pages.

**Schema: [`schema.ts:2089-2506`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L2089-L2506)** — Roots + Elicitation.

- **L2089-L2160** — `ListRootsRequest`, `ListRootsResult`, `Root`, `RootsListChangedNotification`. The `Root.uri` constraint ("MUST start with file://") is enforced in `roots.go:validate`.
- **L2161-L2189** — `ElicitRequestFormParams`. `mode?: "form"` — optional for back-compat; `requestedSchema` is required and constrained to top-level primitives. We decode without strictly enforcing the primitive shape; a real client would.
- **L2191-L2229** — `ElicitRequestURLParams`. `mode: "url"` (required), `message`, `elicitationId`, `url`. The fact that there's no `requestedSchema` here is the whole point of URL mode.
- **L2230-L2476** — `PrimitiveSchemaDefinition` and the four enum variants (`UntitledSingleSelectEnumSchema`, `TitledSingleSelectEnumSchema`, and the two multi-select counterparts). s07 doesn't implement enum forms; we surface only the string-property happy path.
- **L2476-L2506** — `ElicitResult`. Note `action: "accept" | "decline" | "cancel"`. `content` is optional and "only present when action is `accept` and mode was `form`."

**URLElicitationRequired error: [`schema.ts:181-201`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L181-L201)** — the implementation-specific JSON-RPC code `-32042` and the typed error response it accompanies.

**Prose: [`client/roots.mdx`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/client/roots.mdx)** — capability declaration, request/response example, the `notifications/roots/list_changed` flow, and security rules (clients MUST validate URIs to prevent path traversal).

**Prose: [`client/elicitation.mdx`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/client/elicitation.mdx)** — the long version of why URL mode exists, what `URLElicitationRequiredError` is for, and the phishing-prevention pattern around "connect URL" indirection.

Local offline slice: [`upstream-readings/s07-roots-elicitation.ts`](../../upstream-readings/s07-roots-elicitation.ts) (lines 2089-2506 of `schema.ts`).
