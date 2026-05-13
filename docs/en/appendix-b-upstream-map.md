---
title: "Appendix B · Upstream source-reading map"
chapter: B
slug: appendix-b-upstream-map
est_read_min: 15
---

# Appendix B · Upstream source-reading map

> What this teaches: how to navigate the upstream `modelcontextprotocol/modelcontextprotocol` repo after you've built the Go reimplementation. The single most valuable upstream file is `schema/2025-11-25/schema.ts` — 2,587 lines of TypeScript that is the source of truth for every wire shape. This appendix gives you the line-by-line guide, the top SEPs to read for design history, and five extension exercises to keep going after s08.

---

## Reading order

If you read upstream cold, you will drown. Read it in this order:

1. **`README.md`** at the repo root — orients you to "this is a specification repo, not a reference implementation."
2. **`docs/specification/2025-11-25/index.mdx`** — the spec overview. JSON-RPC base, capability negotiation, modular features. ~10 minutes.
3. **`docs/specification/2025-11-25/basic/lifecycle.mdx`** — the handshake in prose, with sequence diagrams. This is the *one* document you should read in full before touching `schema.ts`.
4. **Split-pane** `schema/2025-11-25/schema.ts` on the left, the relevant `docs/specification/2025-11-25/{server,client}/*.mdx` on the right. Use the line-range guide below to jump to the section that matches the chapter you just finished — s03 → tools, s04 → resources, etc.
5. **`seps/`** — visit *only when curious* about why a design decision went the way it did. The SEPs are not required reading; they are background. Five of them are worth your time and listed below.

The trap to avoid: opening `schema.ts` first and trying to read top-to-bottom. The file is ordered by JSDoc grouping, not by reading difficulty — Tasks (1300-1506) and Sampling (1574-1998) appear before the simpler Roots (2089-2160) and Elicitation (2161-2506). Jump around using the guide below, not in file order.

## `schema.ts` line-range guide

Permalinks are pinned to upstream sha `85ec377139c3026d98086eadbb23806e65517f21`. Click through to land at the exact line on github.

| Lines     | Section                              | Session              | Permalink                                                                                                                                                |
| --------- | ------------------------------------ | -------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1-180     | JSON-RPC envelope, error codes       | s01                  | [schema.ts:1-180](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1-L180) |
| 211-249   | Cancellation                         | (mentioned in s06)   | [schema.ts:211-249](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L211-L249) |
| 251-459   | Initialize, capabilities             | s02                  | [schema.ts:251-459](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L251-L459) |
| 651-921   | Resources                            | s04                  | [schema.ts:651-921](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L651-L921) |
| 923-1081  | Prompts                              | s05                  | [schema.ts:923-1081](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L923-L1081) |
| 1082-1299 | Tools                                | s03                  | [schema.ts:1082-1299](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1082-L1299) |
| 1300-1506 | Tasks (backup)                       | App B exercise       | [schema.ts:1300-1506](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1300-L1506) |
| 1574-1998 | Sampling                             | s06                  | [schema.ts:1574-1998](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1574-L1998) |
| 1742-1900 | ContentBlock                         | s03 / s05 / s06      | [schema.ts:1742-1900](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1742-L1900) |
| 2006-2088 | Completion                           | s05                  | [schema.ts:2006-2088](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L2006-L2088) |
| 2089-2160 | Roots                                | s07                  | [schema.ts:2089-2160](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L2089-L2160) |
| 2161-2506 | Elicitation                          | s07                  | [schema.ts:2161-2506](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L2161-L2506) |

A practical workflow once you've finished sNN: open the corresponding row in this table, click the permalink, and read the TS in the right pane while looking at your Go reimplementation in the left pane. The mismatches are the lesson — every place where the TS interface has a field you didn't implement, ask yourself "did I need it?" Most of the time you didn't, because the curriculum implements an MVP subset. But the diff is the map of what you'd add to make it production-grade.

## Top 5 SEPs

The `seps/` directory holds 35 design proposals. Five are worth reading even if you never plan to implement them — they show how the protocol thinks.

**SEP-2575 · Make MCP Stateless** ([seps/2575-stateless-mcp.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2575-stateless-mcp.md))

The most consequential pending change. It proposes removing the `initialize` handshake entirely and carrying protocol version + capabilities per-request, so every request is self-contained. Motivation: a load balancer cannot route a stateful MCP session across server instances without sticky sessions or shared session storage, and operators are forced into complex sticky-session setups. If this lands, s02 of this curriculum becomes "the legacy handshake" and s08's `Mcp-Session-Id` is the artifact of a transitional era. Read this if you've ever deployed a stateful WebSocket service behind a load balancer.

**SEP-2567 · Sessionless MCP via Explicit State Handles** ([seps/2567-sessionless-mcp.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2567-sessionless-mcp.md))

The companion to SEP-2575. Where 2575 removes `initialize`, 2567 removes the `Mcp-Session-Id` header — replacing per-session state with explicit server-minted "state handles" that the model threads through subsequent calls. The shopping-cart example: instead of a session-scoped cart, the server exposes `create_basket()` returning a `basket_id`, and the model passes that id to `add_item(basket_id, ...)`. The argument is that sessions don't have consistent meaning across MCP clients today (ChatGPT scopes per tool call, IDEs per app launch, web apps per page load) and explicit handles let server authors design against a stable abstraction.

**SEP-2243 · HTTP Header Standardization** ([seps/2243-http-standardization.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2243-http-standardization.md))

Already landed in 2025-11-25. Adds `Mcp-Method` and `Mcp-Name` HTTP headers that mirror the JSON-RPC `method` and `params.name` / `params.uri` fields. The motivation is pragmatic: load balancers, WAFs, and observability tools can route and apply policy without parsing the JSON body. You implemented the `MCP-Protocol-Version` header in s08; this SEP is the reasoning for *why* HTTP headers carry information that is already in the JSON body.

**SEP-1686 · Tasks** ([seps/1686-tasks.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/1686-tasks.md))

The async-execution primitive landed in 2025-11-25, lines 1300-1506 of `schema.ts`. A tool call can now return a `taskId` instead of a full result; the client polls `tasks/get` or subscribes to `tasks/result` to retrieve the eventual outcome. Motivation: real workloads (drug-discovery pipelines, enterprise workflow automation, CI/CD) take minutes to hours, and the current "send request, wait for response" model forces servers to invent per-tool polling tools. Tasks generalize that into a protocol primitive. This is the "exercise 4" candidate below.

**SEP-2322 · Multi Round-Trip Requests (MRTR)** ([seps/2322-MRTR.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2322-MRTR.md))

The cleanup of how server-initiated requests work *during* a client-initiated request. Today, if a tool call triggers `elicitation/create`, the server needs to correlate the elicitation response with the in-flight tool call — and that correlation lives implicitly in the session. MRTR makes the correlation explicit via a new field carried through the request/response cycle. The motivation overlaps with SEP-2575: removing implicit session state reduces operational complexity for remote MCP servers that can't easily run sticky-session load balancers. This is a breaking change.

## Notable other SEPs

**SEP-2133 · Extensions** ([seps/2133-extensions.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2133-extensions.md)). Defines how the protocol grows without forcing every implementation to keep up: an "extension identifier" namespace (`{vendor-prefix}/{extension-name}`, e.g. `io.modelcontextprotocol/oauth-client-credentials`) plus governance for official versus experimental extensions. Worth skimming if you want to know how non-core capabilities get into the spec.

**SEP-1036 · URL Mode Elicitation** ([seps/1036-url-mode-elicitation-for-secure-out-of-band-intera.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/1036-url-mode-elicitation-for-secure-out-of-band-intera.md)). Where the URL-mode you implemented in s07 came from. The motivation is security: form-mode elicitation passes data through the MCP client, which is unacceptable for credentials, OAuth flows, or payment data. URL mode delegates to the user's browser, keeping sensitive bytes off the JSON-RPC channel entirely.

**SEP-1024 · Client Security Requirements for Local Server Installation** ([seps/1024-mcp-client-security-requirements-for-local-server-.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/1024-mcp-client-security-requirements-for-local-server-.md)). Addresses the "one-click install of a malicious MCP server" attack vector — a stdio server is just `command + args + env`, and a crafted config can execute arbitrary commands. The SEP mandates explicit user consent and command transparency before any local-server install. Relevant if you're building an MCP host application, not just a server.

## Suggested extension exercises (5)

The curriculum stops at s08, but the protocol has plenty more to chew on. Five exercises, in roughly increasing difficulty.

**1. Real fsnotify subscribe in s04**

s04's `resources/subscribe` uses an in-memory watcher because the demo backing store is `mem://`. The exercise: swap in a real filesystem-backed resource (`file://` URIs rooted under a sandbox directory) and wire `resources/subscribe` to `github.com/fsnotify/fsnotify`. When the watched file changes on disk, emit `notifications/resources/updated`. Tricky bits: debouncing rapid writes (most editors save by atomic-rename, which fires multiple fsnotify events), handling the case where the file is deleted then re-created, and deciding what to do when a *template* is subscribed to (do you watch the parent directory and re-evaluate every change?). Start from `agents/s04-resources/subscribe.go`.

**2. LLM-backed `completion/complete` in s05**

s05's completion handler returns a static list of candidates. The exercise: wire it to an actual LLM. Replace the static candidates with a `Sampler` call — `completion/complete` for prompt argument `path` becomes "ask the LLM 'given the prompt `summarize-file` with arguments so far {…}, suggest up to 5 plausible values for `path`'." The lesson is that completion and sampling can compose: s05 + s06 = "completions that understand your codebase." Start from `agents/s05-prompts/completion.go` and lift the `Sampler` interface from `agents/s06-sampling/sampling.go`.

**3. 2024-11-05 HTTP+SSE fallback transport**

Before Streamable HTTP, MCP used a different HTTP transport: two endpoints (`POST /message` and `GET /sse`) with separate channels for client→server and server→client. It's documented in `docs/specification/2024-11-05/basic/transports.mdx` of the upstream repo. The exercise: implement that *older* transport alongside s08's Streamable HTTP, and make the server negotiate at request time — if the client sends a 2024-11-05-shaped request, use the old transport; otherwise use Streamable HTTP. Lesson: backward compatibility in practice. Start from `agents/s08-streamable-http/server.go` and add a `framer_legacy.go`.

**4. Implement SEP-1686 tasks**

The most concrete spec extension. The schema rows are at `schema.ts:1300-1506`. The exercise: add `tasks/get`, `tasks/result`, `tasks/cancel`, and a "task-augmented request" wrapper that lets `tools/call` return a `taskId` instead of a full result. Build a long-running tool (e.g. `crawl_url`) that returns a task immediately and resolves it asynchronously. Tricky bits: durable task storage (in-memory vs sqlite vs filesystem), task TTL, and the wire-format for "this request is task-augmented, give me a task id." Start from `agents/s03-tools/tools.go` and add a `tasks` package; you'll need a goroutine pool to drive task execution.

**5. Implement SEP-2575 stateless mode**

The hardest. Remove the `initialize` handshake from s08, carry protocol version + capabilities on *every* HTTP request via headers (`MCP-Protocol-Version` is already there from s08, you'd add capability headers), and prove that two POST requests from "different clients" can hit different server replicas and still work. The point of the exercise is to feel why statefulness is so hard to dislodge: every server-side data structure that today lives in `Session{}` has to be either moved into the request (capabilities, version) or made externally retrievable by id (subscriptions, pending outbound requests). Start from `agents/s08-streamable-http/server.go` and pretend you have to deploy this thing behind an L4 round-robin load balancer.

---

## Further reading

- Upstream repo root: <https://github.com/modelcontextprotocol/modelcontextprotocol>
- Spec index: [docs/specification/2025-11-25/index.mdx](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/index.mdx)
- Schema source of truth: [schema/2025-11-25/schema.ts](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts)
- SEP index: [seps/](https://github.com/modelcontextprotocol/modelcontextprotocol/tree/85ec377139c3026d98086eadbb23806e65517f21/seps)
