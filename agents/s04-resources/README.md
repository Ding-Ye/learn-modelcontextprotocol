# s04 — resources: read, templates, subscribe

The third server capability (after `tools` in s03). Resources are *file-like*
context: clients enumerate them, read them, and can subscribe to be pushed an
update when they mutate.

This chapter introduces:

- `resources/list`, `resources/read`, `resources/templates/list` — the three
  read-side methods.
- `resources/subscribe` / `resources/unsubscribe` plus the first
  **server-initiated push** in this curriculum:
  `notifications/resources/updated`.
- An RFC-6570 level-1 URI template expander (`{name}` substitution only).
- A `Store` interface backed by an in-memory `mem://` scheme so the chapter
  has zero filesystem dependencies.

## Run

```bash
make demo
```

You'll see, in order: `InitializeResult` → empty subscribe ack →
`ListResourcesResult` → `ReadResourceResult` → (after ~100ms) a
`notifications/resources/updated` frame because the demo's background
goroutine mutates `mem://log.txt`.

## Test

```bash
make test
```

Five tests cover: list, read text, template substitution, subscribe →
mutate → notification, unsubscribe → silence.

## Files

- `envelope.go` — `Message`, `ID`, `Error`, `stdioFramer` (mutex-guarded
  writes so the notify goroutine can share the wire safely).
- `lifecycle.go` — minimal `initialize` handshake that advertises
  `resources: {subscribe: true, listChanged: true}`.
- `resources.go` — wire types, `Store` interface, `memStore`, RFC-6570 L1
  expander + reverse matcher.
- `notifications.go` — per-URI subscription tracker + `notifications/
  resources/updated` emitter.
- `router.go` — dispatch table.
- `main.go` — stdio entry; registers `mem://log.txt` and
  `mem://users/{id}`.

## Upstream source reading

See [`../../docs/zh/s04-resources.md`](../../docs/zh/s04-resources.md)
(中文) or [`../../docs/en/s04-resources.md`](../../docs/en/s04-resources.md)
(English) for the annotated walkthrough:

- `schema/2025-11-25/schema.ts:651-921` — every resource-related type.
- `docs/specification/2025-11-25/server/resources.mdx:30-278` — capability
  block, protocol-message shapes, subscription semantics.
