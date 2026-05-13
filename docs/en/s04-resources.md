---
title: "s04 · Resources: read, templates, subscribe"
chapter: 04
slug: s04-resources
est_read_min: 12
---

# s04 · Resources: read, templates, subscribe

> What this teaches: the *file-like* server capability. Where tools (s03) are function-like ("call me with arguments"), resources are context-like ("read me by URI"). Plus the curriculum's first server-initiated push: `notifications/resources/updated`.

---

## Problem

A tool is a verb. A resource is a noun. The MCP spec is explicit about this distinction (`server/resources.mdx:30-278`): resources are *application-controlled* context the host surfaces into the model's context window, addressed by URI (`file://`, `https://`, custom schemes), enumerated by `resources/list`, fetched by `resources/read`. Some resources don't exist until you ask — they're parameterised URI templates (`file:///project/src/{path}`) that the client expands on demand. And the resources a client cares about can change *after* the read — so MCP grew a subscribe/notify channel for "this URI mutated, please re-read."

s01–s03 have only modeled one direction: client request, server response. s04 has to introduce the **server-initiated notification** — a JSON-RPC frame with no `id`, pushed asynchronously from a goroutine that has no relationship to the request that triggered the subscribe. Get the locking and the wire-shape wrong and the demo deadlocks or emits malformed frames. The pain this chapter addresses: ship a real subscribe/push loop end-to-end, then prove with tests that unsubscribe actually stops the push.

## Solution

Four layers, top to bottom:

1. **Router** — five new methods: `resources/list`, `resources/read`, `resources/templates/list`, `resources/subscribe`, `resources/unsubscribe`. Same dispatch pattern as s02.
2. **Store interface** — `List() []Resource`, `Read(uri) ([]ResourceContents, error)`, `Templates() []ResourceTemplate`, `Set(uri, ...)`. The interface is the lesson; the in-memory implementation is just a map.
3. **Template expander** — RFC-6570 *level 1 only* (`{name}` substitution, no `{+path}`, no `{?query}`, no exploded composites). 30 lines including the reverse matcher needed to route an expanded URI back to its template.
4. **Notifier** — a per-URI subscription counter plus a `sink func(Message) error`. When the store calls `Set`, the notifier checks the counter and (if non-zero) marshals a `notifications/resources/updated` frame through the sink.

Three design decisions worth calling out:

1. **`ResourceContents` is a fat struct, not a union.** Schema.ts says `(TextResourceContents | BlobResourceContents)`. Go has no tagged unions, so we collapse both into one struct with optional `text` and `blob` fields and rely on `omitempty` to keep the wire clean. The constructor (`TextContents` / `BlobContents`) enforces the invariant. Same trick as s03's `ContentBlock`.
2. **The notifier sink is a `func(Message) error`, not the `Framer`.** This is the change that makes the chapter testable. The stdio entry installs `framer.Write`; the tests install a slice-append closure. Same code path, different consumer.
3. **stdioFramer.Write takes a mutex.** This is the *only* concurrency in the chapter, and it's load-bearing. The request goroutine writes responses; a separate goroutine (or, in the test, the same goroutine) writes notifications. Without the lock, two writes can interleave bytes and produce a broken frame.

## How It Works

```text
┌─────────────────────────────────────────────────────────────┐
│  stdin                                                      │
│   │                                                         │
│   ▼                                                         │
│ stdioFramer.Read ──► router.handle ──► dispatch             │
│                              │                              │
│                              ├─ resources/list      ─► Store.List         │
│                              ├─ resources/read      ─► Store.Read         │
│                              ├─ resources/templates/list ─► Store.Templates │
│                              ├─ resources/subscribe ─► notifier.subscribe │
│                              └─ resources/unsubscribe ─► notifier.unsubscribe │
│                                                                           │
│  ┌──────────── independent goroutine ────────────┐                        │
│  │  store.Set(uri, ...) ──► notifier.fire(uri)   │                        │
│  │       │                       │               │                        │
│  │       ▼                       ▼               │                        │
│  │  mutate map         (if subscribed) emit:     │                        │
│  │                     {"jsonrpc":"2.0",         │                        │
│  │                      "method":"notifications/ │                        │
│  │                      resources/updated", ...} │                        │
│  └────────────────────────────────────────────────┘                       │
│                              ▼                                            │
│                       stdioFramer.Write (mutex-guarded)                   │
│                              │                                            │
│                              ▼                                            │
│                            stdout                                         │
└─────────────────────────────────────────────────────────────┘
```

### Template expansion + reverse matching

The minimal level-1 expander is a regex (`\{([A-Za-z_][A-Za-z0-9_]*)\}`) and 10 lines:

```go
func expandTemplate(tmpl string, vars map[string]string) string {
    return levelOneVar.ReplaceAllStringFunc(tmpl, func(match string) string {
        name := match[1 : len(match)-1]
        if v, ok := vars[name]; ok { return v }
        return match
    })
}
```

The *reverse* direction is what `resources/read` needs: given `mem://users/alice`, find which template it came from. We split the template at the variable, check that the URI has the right prefix and suffix, and pull the middle out:

```go
func matchTemplate(tmpl, uri string) (bool, string) {
    loc := levelOneVar.FindStringIndex(tmpl)
    if loc == nil { return tmpl == uri, "" }
    prefix := tmpl[:loc[0]]
    suffix := tmpl[loc[1]:]
    if !strings.HasPrefix(uri, prefix) || !strings.HasSuffix(uri, suffix) {
        return false, ""
    }
    value := uri[len(prefix) : len(uri)-len(suffix)]
    if value == "" || strings.Contains(value, "/") { return false, "" }
    return true, value
}
```

The `strings.Contains(value, "/")` check is the level-1 invariant — level-1 variables match a single path segment, not a path. If a learner wants to subscribe to whole directories they need `{+path}` (level 2), which we leave as an Appendix B exercise.

### The subscribe → notify race

```go
func (n *notifier) fire(uri string) {
    n.mu.Lock()
    _, ok := n.subs[uri]
    sink := n.sink
    n.mu.Unlock()
    if !ok || sink == nil { return }
    // ... marshal + sink(msg)
}
```

Read-then-release-then-call. We **don't** hold the lock across `sink(msg)`: the sink is `stdioFramer.Write` which calls into `io.Writer` and can block on a slow consumer. Holding `n.mu` across that would prevent any new subscribe / unsubscribe / fire while a write is pending. The downside: a tiny window where `unsubscribe` returns but a fire that already captured the sink may still write. For real-time-correctness-sensitive systems you'd need an epoch counter; for a teaching server, "one trailing notification" is fine.

## What Changed (vs. s02)

s02 introduced the router + lifecycle. s04 reuses both and adds:

- **+ five method handlers** (`resources/list`, `resources/read`, `resources/templates/list`, `resources/subscribe`, `resources/unsubscribe`).
- **+ ResourcesCapability** advertised in `initialize` (`subscribe: true, listChanged: true`).
- **+ server-initiated notification path** — the first time bytes leave the server *not* in response to a request. This is the foundation for s06 (sampling) and s07 (roots/elicitation), where server-initiated *requests* (not just notifications) need a pending-outbound table on top of the same write-side mutex.
- **+ in-memory `Store`** abstraction so the chapter has no filesystem dependency. Swapping in fsnotify-backed file storage is one of the Appendix B suggested exercises.

We did *not* implement `notifications/resources/list_changed` — that's an exercise. The capability declares it, the wire format is trivial (no params required), but the demo store never registers new resources after start.

## Try It

```bash
cd agents/s04-resources
make demo
```

You should see five frames on stdout, one per request, in order:

```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"resources":{"subscribe":true,"listChanged":true}},"serverInfo":{...},"instructions":"..."}}
{"jsonrpc":"2.0","id":2,"result":{}}
{"jsonrpc":"2.0","id":3,"result":{"resources":[{"uri":"mem://log.txt","name":"log.txt","description":"Append-only demo log","mimeType":"text/plain","size":15}]}}
{"jsonrpc":"2.0","id":4,"result":{"contents":[{"uri":"mem://log.txt","mimeType":"text/plain","text":"hello from s04\n"}]}}
```

To observe a push notification, keep stdin open longer than 100ms after the subscribe — the demo's background goroutine mutates `mem://log.txt` and the notifier fires. (The `make demo` recipe pipes a fixed script and closes stdin immediately, so it usually exits before the push lands; this is what the test suite is for.)

Try reading a templated URI:

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"d","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"resources/templates/list"}
{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"mem://users/alice"}}' \
  | go run ./agents/s04-resources
```

You'll see the template advertised in frame 3 and `Alice Liddell` returned in frame 4.

## Upstream Source Reading

The relevant slice of the spec is [`schema.ts:651-921`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L651-L921). Read it side by side with `agents/s04-resources/resources.go`:

- **L651-L668** — `ListResourcesRequest`, `ListResourcesResult`. Pagination via `PaginatedRequest`/`Result` — s04 ignores cursors (single page), but the field is in our struct so a paginated future works.
- **L675-L687** — `ListResourceTemplatesRequest` / `Result`. Identical shape.
- **L693-L731** — `ReadResourceRequest` and `ReadResourceResult`. The crucial line is **L731**: `contents: (TextResourceContents | BlobResourceContents)[]`. An array. One URI can expand into multiple contents.
- **L738-L771** — `SubscribeRequest`, `UnsubscribeRequest`. Trivial: just `{ uri }`.
- **L778-L803** — `ResourceUpdatedNotificationParams` + `ResourceUpdatedNotification`. Note the type is `JSONRPCNotification` (no id).
- **L804-L845** — `Resource` interface. We inline the fields rather than mix in `BaseMetadata` + `Icons`.
- **L847-L885** — `ResourceTemplate` interface. Carries `uriTemplate` (RFC-6570 string), not the expanded URI.
- **L887-L921** — `ResourceContents`, `TextResourceContents`, `BlobResourceContents`. The "fat struct" decision lives here.

Then [`server/resources.mdx:30-278`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/server/resources.mdx#L30-L278) for the prose semantics:

- **L30-L79** — the capability block (`subscribe`, `listChanged`) and the rule that both are optional.
- **L82-L138** — `resources/list` request/response examples.
- **L140-L177** — `resources/read` examples. The example uses `file://` but the wire shape is scheme-agnostic.
- **L180-L226** — `resources/templates/list` and a note pointing to `completion/complete` (s05 territory).
- **L228-L268** — subscriptions: subscribe → update notification. The example notification has `params.uri` only, no `revision`/`etag` — that's deliberate, the protocol expects clients to re-read.

Local copies for offline reference live in [`upstream-readings/s04-resources.ts`](../../upstream-readings/s04-resources.ts).
