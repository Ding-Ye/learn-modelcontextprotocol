# s02 — Initialize handshake & capability negotiation

s01 echoed `initialize` as a static blob. s02 replaces that with a real
handshake:

- **Typed capability structs.** `ClientCapabilities` and
  `ServerCapabilities` mirror `schema.ts:308-459` — each optional
  feature is a pointer-to-struct, so `nil` means "not advertised" and
  `&struct{}{}` means "advertised, empty body".
- **Lifecycle state machine.** Four states: `new → initializing →
  operating → closed`. Non-handshake methods are rejected with
  `-32002 ServerNotInitialized` until the client sends
  `notifications/initialized`.
- **Version negotiation.** If the client asks for a `protocolVersion`
  this server doesn't support, the server responds with its own
  preferred version (and does NOT disconnect — that's the client's
  call, per `basic/lifecycle.mdx:170-175`).
- **A reusable Router.** Method → `Handler` dispatch, used by every
  later chapter (s03..s08).

## Run

```bash
make demo
```

You should see three lines: an `InitializeResult`, no reply for the
notification, and an empty result for the trailing `ping`.

## Test

```bash
make test
```

Six unit tests cover: happy-path init, pre-init method rejection
(-32002), wrong-version fallback, duplicate-init rejection, the
notification flipping state silently, and ping pre-handshake.

## Files

- `envelope.go` — `Message`, `ID`, `Error`, `stdioFramer` (re-declared
  from s01; each session is a self-contained module).
- `lifecycle.go` — capabilities, `Server`, state machine, handlers.
- `router.go` — generic method dispatcher, used by every later chapter.
- `main.go` — stdio entry point + capability advertisement.
- `lifecycle_test.go` — the six tests above.

## Upstream source reading

See [`../../docs/zh/s02-initialize.md`](../../docs/zh/s02-initialize.md)
or [`../../docs/en/s02-initialize.md`](../../docs/en/s02-initialize.md)
for the annotated walkthrough. Citations:

- `schema/2025-11-25/schema.ts:251-459` — Initialize request/result + Capabilities.
- `docs/specification/2025-11-25/basic/lifecycle.mdx:36-205` — handshake prose.

Offline slice: [`../../upstream-readings/s02-initialize.ts`](../../upstream-readings/s02-initialize.ts).
