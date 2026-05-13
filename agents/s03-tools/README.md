# s03 · tools/list and tools/call

A self-contained Go module that grows the s02 server into a *useful* one
by exposing the canonical server capability: tools.

## What this adds vs s02

- `tools` capability advertised in `InitializeResult.capabilities` (with
  `listChanged: false` — we don't push list-changed notifications).
- Two new router entries: `tools/list` and `tools/call`.
- A `Tool` struct (name + description + JSON-Schema inputSchema).
- A `ContentBlock` fat struct with `TextBlock` / `ImageBlock`
  constructors (Go has no tagged unions; we discriminate on `Type`).
- A `ToolRegistry` that pre-compiles every input schema at registration
  time via `github.com/santhosh-tekuri/jsonschema/v5`.
- The pivotal lesson: **`isError` convention**. Tool *execution* errors
  return `CallToolResult{IsError: true, Content: [text]}` so the model
  can see them and self-correct. Only *protocol-level* failures (unknown
  tool, args fail schema, malformed envelope) become JSON-RPC errors.

## Demo tools

| Name       | Input schema                                        | Behaviour                                              |
|------------|-----------------------------------------------------|--------------------------------------------------------|
| `echo`     | `{text: string}` (required)                         | Returns the `text` as a text content block.            |
| `get_time` | `{format?: "rfc3339" \| "unix"}` (optional)         | Returns the current server time as a text block.       |

## Build / test / demo

```bash
make test
make demo
```

`make demo` pipes a four-message conversation (initialize →
initialized → tools/list → tools/call echo) into the server and prints
the three JSON replies.

## File layout

```
s03-tools/
├── envelope.go      JSON-RPC envelope + stdio framer (re-declared; +-32002, +-32602)
├── lifecycle.go     Minimal state machine: Uninit → Init → Ready
├── tools.go         Tool / ContentBlock / ToolRegistry / handleListTools / handleCallTool
├── router.go        Dispatch table; rejects non-init requests with -32002
├── main.go          stdio entrypoint
└── tools_test.go    6 tests (5 required + 1 lifecycle gate)
```

## Source map

- `schema/2025-11-25/schema.ts:1082-1299` — Tool, CallToolRequest, CallToolResult.
- `schema/2025-11-25/schema.ts:1742-1839` — ContentBlock union.
- `docs/specification/2025-11-25/server/tools.mdx:36-186` — prose +
  message examples + the `isError` rationale.
