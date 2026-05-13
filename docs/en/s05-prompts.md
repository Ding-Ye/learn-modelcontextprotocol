---
title: "s05 · Prompts and completion/complete"
chapter: 05
slug: s05-prompts
est_read_min: 12
---

# s05 · Prompts and completion/complete

> What this teaches: the third "primitives audience" — **prompts are user-controlled**, not model-controlled (tools) or app-controlled (resources). The wire shape is small (`prompts/list`, `prompts/get`, `completion/complete`), but the substitution + autocomplete duo is the foundation for every slash-command UX you'll see in an MCP host.

---

## Problem

Tools answer "what can the model invoke?". Resources answer "what context can the app attach?". Neither answers "what canned prompt can the *user* trigger from a slash menu?". That third primitive — **prompts** — is what s05 implements.

The wire surface (`schema.ts:923-1081`) is deliberately tiny: list available prompts, fetch one with arguments substituted, and (separately, at `schema.ts:2006-2088`) autocomplete an argument value as the user types. The spec keeps these endpoints separate so an editor can wire `prompts/list` to a slash menu and `completion/complete` to a popup without coupling them.

The pain this chapter addresses: ship a server that exposes one prompt (`summarize-file`), validates required arguments at request time, substitutes `{{argument}}` placeholders, and replies to `completion/complete` with prefix-matched candidates from a small dictionary. Everything later (real autocomplete backends, multi-message prompts) is an extension of this skeleton.

## Solution

A prompt is a **named template plus an argument schema**. We model it as a `Prompt` value (name + description + `[]PromptArgument`) plus a `PromptHandler` interface that knows how to render the template given a `map[string]string` of arguments. A `PromptRegistry` owns the (name → handler) table, in registration order.

Key design decisions:

1. **`PromptHandler` is an interface, not a function.** Real handlers carry state (a file watcher, a DB handle, an LLM client). A bare `func` signature would still work for s05 but would force us to refactor in s06+; promoting it to an interface up front costs nothing and matches what a real SDK ships.
2. **Substitution is `strings.ReplaceAll("{{key}}", value)`.** No expression evaluation, no escaping rules. The spec is silent on syntax — every SDK rolls its own. We pick the simplest thing that's recognizable from the spec's own MDX examples (`server/prompts.mdx:101-138`).
3. **`completion/complete` shares the registry but lives in its own file.** The endpoint is structurally different (it has a discriminated-union `ref` field; it doesn't render anything; it caps at 100 values). Splitting the file makes the union-handling code easier to read.
4. **Required-arg validation runs *before* substitution.** If we substituted first, a missing required arg would silently produce `{{path}}` in the rendered prompt and the user would never know. We return `-32602 InvalidParams` instead, with the offending argument name in the error message.

## How It Works

```text
┌────────────────────────────────────────────────────────────────┐
│ stdin                                                          │
│   │                                                            │
│   ▼                                                            │
│ stdioFramer ─► Message ─► Router.Dispatch                      │
│                              │                                 │
│   ┌──────────────────────────┼──────────────────────────────┐  │
│   │                          │                              │  │
│   ▼                          ▼                              ▼  │
│ initialize          prompts/list                  completion/  │
│ (lifecycle)         prompts/get ──► registry      complete     │
│                         │            │ Lookup        │         │
│                         ▼            ▼               ▼         │
│                  PromptHandler.Get  validateArgs   prefix-     │
│                         │           substitute     match the   │
│                         ▼                          dictionary  │
│                   GetPromptResult                  ─► values[] │
│                         │                                      │
│                         ▼                                      │
│                       stdout                                   │
└────────────────────────────────────────────────────────────────┘
```

The substitution + validation core (excerpt from `agents/s05-prompts/prompts.go`):

```go
func validateArgs(decl []PromptArgument, args map[string]string) *Error {
    for _, a := range decl {
        if !a.Required { continue }
        if v, ok := args[a.Name]; !ok || v == "" {
            return &Error{
                Code:    InvalidParams,
                Message: fmt.Sprintf("missing required argument %q", a.Name),
            }
        }
    }
    return nil
}

func substitute(tmpl string, args map[string]string) string {
    out := tmpl
    for k, v := range args {
        out = strings.ReplaceAll(out, "{{"+k+"}}", v)
    }
    return out
}
```

And the completion union dispatch (`completion.go`):

```go
switch req.Ref.Type {
case "ref/prompt":
    return completeForPrompt(r, req)
case "ref/resource":
    return CompleteResult{Completion: CompletionPayload{Values: []string{}}}, nil
case "":
    return nil, &Error{Code: InvalidParams, Message: "ref.type is required"}
default:
    return nil, &Error{Code: InvalidParams, Message: "unknown ref.type: " + req.Ref.Type}
}
```

Four non-obvious points:

1. **`Arguments` on the wire is `map[string]string`.** Even if the prompt argument is "semantically" numeric, JSON-RPC delivers it as a quoted string. The substitution code only handles strings; we don't try to coerce. (`schema.ts:954-956`.)
2. **`ref/resource` lives in s05's completion code even though resource templates are s04's concern.** The spec puts both refs under one RPC because the *autocomplete UX* is the same — typing the next character of either a prompt argument or a URI-template variable. We accept `ref/resource` and return an empty `values[]` so a client that probes both endpoints behaves consistently.
3. **Completion caps `values` at 100.** Schema rule (`schema.ts:2051-2053`). We enforce it with a trim + `hasMore=true`; the demo dictionary is tiny so the path never triggers, but the code is there to read.
4. **`PromptHandler.Get` returns `(GetPromptResult, error)`, not `(GetPromptResult, *Error)`.** The router converts any `error` into `-32603 InternalError` *unless* the handler returns `*Error` directly, in which case it preserves the code. That gives us "missing required arg" as `-32602` while leaving room for unexpected failures to surface cleanly.

## What Changed (vs. s04)

s04 introduced resources — file-like, app-controlled context with a list/read/templates/subscribe shape. s05 keeps the same router scaffold but swaps the surface entirely:

- **New capability declared:** `prompts.listChanged` + `completions` (no nested object — it's just `{}`). See `lifecycle.go:39-44`.
- **`ContentBlock` re-declared.** This is the third time we've written this struct (s03, s04 for `resource_link`, now s05). The repetition is the lesson — by s06 you'll have written it five times and stopped feeling clever about it.
- **No I/O.** The demo prompt is intentionally stateless: it doesn't read the file at `path`, it just embeds the path into a user message asking the LLM to summarize it. Resources do filesystem-y things; prompts assemble strings.
- **First "discriminated union" RPC.** `completion/complete`'s `ref` field is the first time we've had to switch on a `type` discriminator at the *RPC params* level. s06 will hit the same shape on `ContentBlock`'s `tool_use` / `tool_result` variants; this is the warm-up.

## Try It

```bash
cd agents/s05-prompts
make demo
```

You should see five lines of JSON. Highlights:

```json
{"jsonrpc":"2.0","id":2,"result":{"prompts":[{"name":"summarize-file","description":"Ask the LLM to summarize the file at the given path.","arguments":[{"name":"path","description":"Path to the file to summarize.","required":true},{"name":"style","description":"Output style: bullets|prose|tldr (default: prose)."}]}]}}
```

```json
{"jsonrpc":"2.0","id":3,"result":{"description":"summarize the file at README.md","messages":[{"role":"user","content":{"type":"text","text":"Please summarize the file at `README.md` in bullets form. Focus on what the code or document is *for*, not a line-by-line restatement."}}]}}
```

```json
{"jsonrpc":"2.0","id":4,"result":{"completion":{"values":["README.en.md","README.md"],"total":2}}}
```

Try a missing required arg:

```bash
echo '{"jsonrpc":"2.0","id":9,"method":"prompts/get","params":{"name":"summarize-file","arguments":{}}}' | ./s05-prompts
```

(Note: you'll need to send `initialize` + `notifications/initialized` first; the lifecycle gate replies `-32600` otherwise.) You should see `"error":{"code":-32602,"message":"missing required argument \"path\""}`.

Try an unknown ref.type:

```bash
echo '{"jsonrpc":"2.0","id":10,"method":"completion/complete","params":{"ref":{"type":"ref/banana","name":"summarize-file"},"argument":{"name":"path","value":""}}}' | ./s05-prompts
```

`"error":{"code":-32602,"message":"completion/complete: unknown ref.type: ref/banana"}`.

## Upstream Source Reading

The two upstream regions for this chapter are both in `schema/2025-11-25/schema.ts`. Read them with `agents/s05-prompts/{prompts,completion}.go` open in the other pane.

**Prompts: [`schema.ts:923-1081`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L923-L1081)** — list, get, message, list-changed notification.

- **L929-L940** — `ListPromptsRequest` / `ListPromptsResult`. Note `PaginatedRequest`/`PaginatedResult`: every server-side list endpoint inherits the cursor convention. We skip pagination in s05 because the demo has one prompt.
- **L947-L966** — `GetPromptRequestParams` / `GetPromptRequest`. The critical line is `arguments?: { [key: string]: string }` — string-keyed and string-valued, with the whole field optional.
- **L973-L979** — `GetPromptResult`. `messages` is plural even though our demo returns one; that's deliberate, a multi-turn template can carry a conversation.
- **L986-L1001** — `Prompt`. Note it extends `BaseMetadata, Icons` — fields we drop. The lesson here is that re-implementing in Go forces you to *choose* what surface to expose.
- **L1008-L1017** — `PromptArgument`. `required?: boolean` — the only validation rule a handler can rely on without re-stating the schema.
- **L1034-L1037** — `PromptMessage`. Role + ContentBlock. The same role enum is reused by sampling in s06.
- **L1077-L1080** — `PromptListChangedNotification`. We declare `listChanged: true` but the demo never mutates; in a real server, mutating the registry should emit this.

**Completion: [`schema.ts:2006-2088`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L2006-L2088)** — the only "argument autocomplete" RPC in the spec.

- **L2006-L2031** — `CompleteRequestParams`. The `ref` field is the discriminated union we switch on. The `context.arguments` map is "previously-resolved variables in a URI template or prompt" — we accept it but don't use it.
- **L2048-L2063** — `CompleteResult`. Note the **100-value cap** and the optional `total`/`hasMore` fields. The wire shape mirrors a paginated list, but pagination via cursor isn't defined here — `hasMore: true` is a one-bit hint.
- **L2070-L2087** — the two `ref` variants. `ResourceTemplateReference` has `uri`, `PromptReference` extends `BaseMetadata` (which is where `name` comes from).

**Prose: [`server/prompts.mdx:28-174`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/server/prompts.mdx#L28-L174)** — the spec walks through capability declaration, listing, getting, and the list-changed notification with worked JSON examples. Worth reading once end-to-end before diving into the TypeScript.

Local offline slice: [`upstream-readings/s05-prompts.ts`](../../upstream-readings/s05-prompts.ts) (both `923-1081` and `2006-2088` concatenated).
