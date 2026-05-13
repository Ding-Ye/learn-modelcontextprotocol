# s07 — roots and elicitation

Two more **server→client** capabilities, both reusing the
outbound-request scaffolding the previous chapter introduced for
sampling:

- `roots/list`: server asks the client which `file://` paths it is
  allowed to operate inside.
- `elicitation/create`: server asks the user a question via the
  client, in either **form mode** (returns structured data) or
  **URL mode** (the user completes the interaction out of band; only
  an `action` comes back).

The chapter also introduces the MCP-reserved error code
`-32042 URLElicitationRequired` (`schema.ts:181-201`), which a tool
handler can return as a short-circuit when it can't answer until the
user finishes an URL-mode elicitation.

## Run

```bash
make test
make demo
```

The demo is a `go test -v` invocation — elicitation needs a cooperating
client, so the demo spawns the "client" inside an in-process pipe pair.

## Files

| file                   | purpose                                                                            |
|------------------------|------------------------------------------------------------------------------------|
| `envelope.go`          | re-declared `Message`, `ID`, `Error`, `stdioFramer` + the new `-32042` constant    |
| `lifecycle.go`         | minimal `initialize`; recognizes `roots` + `elicitation.{form,url}` client caps     |
| `roots.go`             | `Root`, `ListRootsRequest`, `ListRootsResult`, `handleListRoots` (client helper)    |
| `elicitation.go`       | form + URL params, `ElicitResult`, `sendFormElicitation`, `sendURLElicitation`     |
| `outbound.go`          | `OutboundRouter` with pending-id table + timeout (re-implemented from s06)         |
| `router.go`            | inbound dispatch, `tools/list` + `tools/call who-are-you` demo                     |
| `main.go`              | stdio entry point                                                                  |
| `elicitation_test.go`  | five tests against an in-process router + pipe framer                              |

## Upstream source reading

See [`../../docs/zh/s07-roots-elicitation.md`](../../docs/zh/s07-roots-elicitation.md) (中文)
or [`../../docs/en/s07-roots-elicitation.md`](../../docs/en/s07-roots-elicitation.md) (English)
for the annotated walkthrough. Key upstream sections:

- `schema/2025-11-25/schema.ts:2089-2506` — Roots + Elicitation
- `docs/specification/2025-11-25/client/roots.mdx` — roots protocol prose
- `docs/specification/2025-11-25/client/elicitation.mdx` — elicitation prose, including URL-mode security rules

Local offline copy: [`upstream-readings/s07-roots-elicitation.ts`](../../upstream-readings/s07-roots-elicitation.ts).
