# s01 — minimum loop (JSON-RPC + stdio)

The smallest program that reveals MCP's wire shape. It speaks JSON-RPC 2.0
over line-delimited stdio and answers exactly one method, `initialize`, with
a hard-coded reply. Every other request returns `-32601 MethodNotFound`.

This chapter introduces:

- The `Message` envelope (request / notification / response / error) shared
  by every later chapter.
- The MCP rule that request `id` MUST NOT be `null` — enforced at parse time.
- The stdio framing rule from `basic/transports.mdx:1-51`: one JSON per line,
  no embedded newlines.

## Run

```bash
make demo
```

You should see a single line of JSON containing the server's
`InitializeResult`.

## Test

```bash
make test
```

## Files

- `envelope.go` — `Message`, `ID`, `Error`, `stdioFramer` (~150 LOC).
- `main.go` — single-method handler dispatch.
- `envelope_test.go` — six unit tests covering round-trips and edge cases.

## Upstream source reading

See [`../../docs/zh/s01-min-loop.md`](../../docs/zh/s01-min-loop.md) (中文) or
[`../../docs/en/s01-min-loop.md`](../../docs/en/s01-min-loop.md) (English) for
the annotated walkthrough of the upstream sections this chapter implements:

- `schema/2025-11-25/schema.ts:1-180` — JSON-RPC envelope, error codes
- `docs/specification/2025-11-25/basic/transports.mdx:1-51` — stdio framing
