# s06 — sampling (server-initiated LLM)

The MCP highlight chapter. Until now everything flowed client → server; s06
flips one method around: the **server** asks the **client** to run an LLM
via `sampling/createMessage`. That single inversion is what makes MCP an
agent protocol rather than another RPC library.

This chapter introduces:

- **`sampling/createMessage`** — `schema.ts:1574-1998`. Server-initiated
  request whose params include `messages`, `maxTokens` (required), and
  optional `tools` / `toolChoice` / `modelPreferences` / `includeContext`.
- **`Sampler` interface** — the client-side seam. `StubSampler` ships
  canned replies for deterministic tests; `AnthropicSampler` is a
  skeleton (Phase G fills the HTTP body).
- **`OutboundRouter`** — pending-request table keyed by `RequestId`, with
  a 30-second default timeout. The same scaffolding will be reused by
  s07 elicitation/roots.
- **`ContentBlock` re-declared (5th time)** — with `tool_use` and
  `tool_result` variants this time. That's the agentic loop in one type.
- **Two demo tools:**
  - `summarize` — one outbound sampling round-trip.
  - `agentic-summarize` — two-turn loop: tools advertised → tool_use
    returned → tool run in-process → tool_result fed back → final reply.

## Run

```bash
make demo
```

You'll see four lines of JSON: `initialize` reply, `tools/list` reply
(three tools), and `tools/call summarize` reply that *contains* the stub
sampler's canned text.

## Test

```bash
make test
```

Five tests cover: plain text reply, agentic tool_use round-trip,
`maxTokens: 0` rejection (-32602), outbound timeout + pending-table
cleanup, and `includeContext: "thisServer"` round-trip.

## Files

| file                | purpose                                                                              |
|---------------------|--------------------------------------------------------------------------------------|
| `envelope.go`       | re-declared `Message`, `ID`, `Error`, `stdioFramer` (same as s01..s05)               |
| `lifecycle.go`      | minimal `initialize` handshake; records client `sampling` capability                 |
| `sampling.go`       | `CreateMessageRequestParams`, `Result`, `SamplingMessage`, `ContentBlock` (5 variants) |
| `sampler.go`        | `Sampler` interface, `StubSampler`, `AnthropicSampler` skeleton                       |
| `outbound.go`       | `OutboundRouter`: pending map + timeout + response correlation                       |
| `router.go`         | inbound dispatcher + demo tools `summarize` / `echo` / `agentic-summarize`           |
| `main.go`           | stdio entry point wiring `StubSampler` by default                                    |
| `sampling_test.go`  | five tests against an in-process router                                              |

## Upstream source reading

See [`../../docs/zh/s06-sampling.md`](../../docs/zh/s06-sampling.md) (中文)
or [`../../docs/en/s06-sampling.md`](../../docs/en/s06-sampling.md) (English)
for the annotated walkthrough. Key upstream sections:

- `schema/2025-11-25/schema.ts:1574-1998` — `CreateMessageRequest/Params/Result`, `SamplingMessage`, `ToolUseContent`, `ToolResultContent`, `ModelPreferences`, `ToolChoice`
- `docs/specification/2025-11-25/client/sampling.mdx` — protocol prose, human-in-the-loop guidance

Local offline copy: [`upstream-readings/s06-sampling.ts`](../../upstream-readings/s06-sampling.ts).
