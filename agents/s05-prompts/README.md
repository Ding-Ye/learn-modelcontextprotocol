# s05 — prompts and completion/complete

User-controlled prompt templates with `{{argument}}` substitution, plus
`completion/complete` for autocompleting prompt-argument values.

This chapter introduces:

- The third "primitives audience": tools are model-controlled, resources
  are app-controlled, **prompts are user-controlled** (a slash command).
- The `Prompt`, `PromptArgument`, `PromptMessage`, `GetPromptResult`
  shapes from `schema.ts:923-1081`.
- The `completion/complete` endpoint from `schema.ts:2006-2088`, including
  the `ref/prompt` vs `ref/resource` discriminated union.
- `ContentBlock` re-declared (third time, after s03 and s04 reuse) — the
  repetition is the lesson.

## Run

```bash
make demo
```

You'll see five lines of JSON: the `initialize` reply, the `prompts/list`
reply (one prompt: `summarize-file`), the `prompts/get` reply (one
`user`-role message with the substituted template), and the
`completion/complete` reply (paths starting with `R`).

## Test

```bash
make test
```

Five tests cover the five required cases (list, substitute, missing arg,
complete candidates, unknown ref.type).

## Files

| file             | purpose                                                                            |
|------------------|------------------------------------------------------------------------------------|
| `envelope.go`    | re-declared `Message`, `ID`, `Error`, `stdioFramer` (same as s01 / s02 / s03 / s04)|
| `lifecycle.go`   | minimal `initialize` handshake declaring `prompts.listChanged` + `completions`     |
| `prompts.go`     | `Prompt`, `PromptArgument`, `GetPromptResult`, registry, substitution, demo prompt |
| `completion.go`  | `CompleteRequest`/`Result`, `CompletionReference`, dictionary-backed handler       |
| `router.go`      | method dispatcher with the lifecycle gate                                          |
| `main.go`        | stdio entry point                                                                  |
| `prompts_test.go`| five tests against an in-process router                                            |

## Upstream source reading

See [`../../docs/zh/s05-prompts.md`](../../docs/zh/s05-prompts.md) (中文)
or [`../../docs/en/s05-prompts.md`](../../docs/en/s05-prompts.md) (English)
for the annotated walkthrough. Key upstream sections:

- `schema/2025-11-25/schema.ts:923-1081` — Prompt types
- `schema/2025-11-25/schema.ts:2006-2088` — completion/complete
- `docs/specification/2025-11-25/server/prompts.mdx:28-174` — protocol prose

Local offline copy: [`upstream-readings/s05-prompts.ts`](../../upstream-readings/s05-prompts.ts).
