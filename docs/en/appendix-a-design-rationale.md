---
title: "Appendix A · Why JSON-RPC and date-based versioning"
chapter: A
slug: appendix-a-design-rationale
est_read_min: 12
---

# Appendix A · Why JSON-RPC and date-based versioning

> What this teaches: the *why* behind every wire-level decision you re-implemented in s01-s08. After eight chapters of "do it this way," this appendix steps back and asks: why this envelope, why dates not semver, why capability negotiation, and what is the spec deliberately silent about? Treat it as the rationale you can quote when someone on your team asks "why didn't they just use REST?"

---

## Bidirectional by construction

HTTP is asymmetric. The client asks; the server answers. There is no language in HTTP for "the server has something to tell you" except the polling/long-polling/SSE workarounds that every real-world API eventually layers on top.

MCP refuses to start from that asymmetry. In s06 you built `sampling/createMessage`: the *server* asks the *client* to run an LLM completion. In s07 you built `roots/list` and `elicitation/create`: the *server* asks the *client* about the user's filesystem and the user's intent. None of those make sense in a request/response transport.

JSON-RPC 2.0 solves this with one field: `id`. A request carries an `id`; the matching response carries the same `id`; that's how the receiver correlates them. The framer doesn't care which side initiated which message — both sides write requests, both sides write responses, and the `id` is the only thing that says "this result belongs to that request." MCP adds one extra constraint on top (request `id` MUST NOT be `null`, schema.ts:[14-16](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L14-L16)) so the receiver can rely on `id` always meaning *something*.

This is why the s06 router gained a "pending outbound" table keyed by `RequestId`. Once you accept that the protocol is bidirectional, the table is forced: the side that just sent a request needs somewhere to remember "I am waiting on this `id`," and the side that just received a response needs to look up "what was the original request that this `id` is answering?" In s01-s05 only the server held that table; in s06 you wrote it on both sides. JSON-RPC's `id` is what made the symmetric implementation possible.

## Transport-agnostic envelope

The same `Message` struct rode stdio for the first seven chapters (`agents/s01-min-loop/envelope.go` through `agents/s07-roots-elicitation/envelope.go`) and then rode HTTP+SSE for s08 (`agents/s08-streamable-http/framer.go`). Look at the diff between s07's stdioFramer and s08's httpFramer: not one byte of the envelope changed. Only `Read` and `Write` changed.

That's because JSON-RPC is just bytes-and-shape — not bytes-and-transport. Compare with gRPC, where the envelope is coupled to HTTP/2: a `grpc.Request` cannot ride stdio without re-implementing framing, flow control, and the trailer-headers convention. The protocol cannot be "popped off HTTP/2" because its message format encodes HTTP/2 details (path-as-method, headers, trailers, status codes).

MCP's choice goes the other way: the envelope is pure JSON, the framer interface is `Read` / `Write` / `Close`, and every transport plugs in below. The basic spec page (`docs/specification/2025-11-25/basic/index.mdx`, [permalink](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/basic/index.mdx)) is explicit: "MCP is a protocol that lives on top of JSON-RPC 2.0," and JSON-RPC was chosen specifically because it has no opinion about transport. This is why s08 was a *transport swap*, not a *rewrite*: the handlers from s01-s07 dropped in unchanged.

The practical consequence: when a future SEP introduces a WebSocket transport, or a QUIC transport, or a shared-memory transport for in-process embedding, the chapters you wrote do not need to change. Only a new `Framer` does. The envelope is the contract.

## Capability negotiation as a typed gate

Every method beyond `initialize` is gated by a capability flag. The mechanical chain is:

1. Client sends `initialize` with `ClientCapabilities` (schema.ts:[251-459](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L251-L459)).
2. Server replies with `ServerCapabilities`.
3. Both sides record what the other supports.
4. Every later method — `tools/call`, `resources/subscribe`, `sampling/createMessage`, etc. — is rejected unless the *receiver* advertised the capability.

In s02 you wrote the state machine that rejects `tools/list` if `ServerCapabilities.tools` was not set. In s06 you wrote the symmetric check on the server side: do not emit `sampling/createMessage` if `ClientCapabilities.sampling` was not set. The router's "is this method allowed in this state?" check is the spec's *only* mechanism for keeping a future-versioned peer from invoking a method the current peer has never heard of.

This is what lets MCP grow without breaking older clients. When `tasks/get` was added in 2025-11-25 (schema.ts:[1300-1506](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1300-L1506)), older clients that don't advertise the relevant capability simply never see the method. The server checks the capability bit and falls back. There is no "version sniffing," no "feature detection by trial," no "send it and hope" — there's a typed gate at initialize, and the rest of the protocol is mechanically derived from it.

Capability negotiation also implicitly *defines* what a capability is: a thing the receiver decides whether to support, declared once at handshake, then assumed for the rest of the session. That's why you don't see "server supports resources" as a hardcoded constant in s04 — it's a runtime value from `ServerCapabilities.resources`. The capability bit is the spec's join column between "what you can ask for" and "what you can answer."

## Date-based vs semver

`protocolVersion: "2025-11-25"` is not a version number in the semver sense. There is no compatibility relationship between `2025-06-18` and `2025-11-25` other than "they are two distinct snapshots." Five live versions coexist right now — `2024-11-05`, `2025-03-26`, `2025-06-18`, `2025-11-25`, and `draft` — and a server that speaks `2025-11-25` is not promising anything about whether it can also handle `2025-06-18`.

Semver implies compatibility patches: `2.1.0` is compatible with `2.0.0` plus features; `3.0.0` is a breaking change; you can request "anything >= 2.0.0, < 3.0.0." MCP does not work that way. A spec version is an immutable point-in-time snapshot — once published, the bytes never change. The client says `"I speak this exact snapshot"`, the server says `"I speak this exact snapshot"`, and they either match (proceed) or they don't (the lifecycle spec at `docs/specification/2025-11-25/basic/lifecycle.mdx:165-175` directs the peer to disconnect).

This choice has three knock-on consequences:

1. **No backporting drama.** A bug discovered in `2025-06-18` cannot be patched in place because that would silently re-define what the version means. It can only be addressed in the next snapshot. Implementations pin to a snapshot; they don't pin to a "minimum version."
2. **Capability negotiation does the work semver would have done.** Instead of "version 2.1 added tools," it's "any version where `ServerCapabilities.tools` is set." The version date establishes which bit *exists*; the capability flag establishes which bit *is on for this peer*.
3. **Date strings carry calendar meaning.** A reader sees `2025-11-25` and immediately knows when the snapshot was minted; that's a property version strings like `3.2.1` will never have. The cost is that the spec maintainers cannot use the version number to signal "this is a big release" or "this is a tiny patch" — every snapshot is just another snapshot.

The repo's `CLAUDE.md` (in `.learn/upstream/`) makes this explicit: *"Specifications use date-based versioning (YYYY-MM-DD), not semantic versioning."* Treat that as load-bearing.

## LSP as design precedent

MCP is, structurally, *"LSP for LLMs."* The Language Server Protocol (Microsoft, [microsoft.github.io/language-server-protocol](https://microsoft.github.io/language-server-protocol/)) standardized the editor↔language-server interface using exactly the design moves MCP later adopted:

| LSP move                                    | MCP analogue                                        |
| ------------------------------------------- | --------------------------------------------------- |
| JSON-RPC 2.0 envelope                       | JSON-RPC 2.0 envelope                               |
| `initialize` / `initialized` handshake      | `initialize` / `notifications/initialized` handshake |
| `ClientCapabilities` / `ServerCapabilities` | `ClientCapabilities` / `ServerCapabilities`         |
| Stdio for local subprocess, sockets for remote | Stdio for local subprocess, HTTP+SSE for remote   |
| Server can request client work (`workspace/applyEdit`) | Server can request client work (`sampling/createMessage`) |
| Per-method capability flags                 | Per-method capability flags                         |

The design heritage is not coincidence. LSP solved the M-editors × N-languages problem by inserting a JSON-RPC protocol between them; MCP solves the M-LLM-apps × N-context-services problem the same way. If you've ever wired up `pylsp` or `gopls`, the s01 + s02 shape will feel uncannily familiar — `initialize`, capabilities, `notifications/initialized`, then per-method dispatch. The lesson MCP took from LSP: a protocol with a typed handshake, transport-agnostic envelopes, and per-method capability negotiation has demonstrably stayed maintainable across years of feature growth. It's a precedent worth copying.

For deeper reading, the LSP specification at <https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/> is the obvious comparison text. The first three sections (Base Protocol, Initialize, Capabilities) map almost line-for-line to s01 + s02 of this repo.

## What the spec omits and why

A protocol is also defined by what it refuses to include. Three deliberate omissions in MCP:

**No auth in the envelope.** There is no `auth` field in `Message`. There is no `Authorization` header in the basic JSON-RPC envelope. Auth is delegated entirely to the transport: stdio runs in the trust domain of the parent process, and Streamable HTTP rides standard OAuth 2.1 / OIDC at the HTTP layer. The protocol does not invent its own auth model. The benefit is that you cannot misconfigure something MCP doesn't ship; the cost is that every transport must bring its own. (For HTTP, see `docs/specification/2025-11-25/basic/authorization.mdx` — separate from the envelope spec at [docs/specification/2025-11-25/basic/index.mdx](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/basic/index.mdx).)

**No retry policy.** If a request times out, the spec does not say what to do. It does not say "retry with the same id," it does not say "back off exponentially," it does not say "give up after N attempts." Retry is left to the implementer. The reasoning is that retry semantics are application-specific — a database query and a credit-card charge have wildly different idempotency stories, and a one-size-fits-all retry rule would be wrong for most use cases. The protocol gives you `id` for correlation and `notifications/cancelled` for "I gave up"; the rest is your choice. SEP-1686 (tasks) is the closest thing to a structured retry primitive, but it's an *augmentation*, not a default.

**No schema migration tools.** When the protocol moves from `2025-06-18` to `2025-11-25`, there is no `migrate-v1-to-v2` script in the repo, no `compatibility shim` document, no "here's how to translate the old `Tool` struct to the new one." Implementations are expected to support multiple versions independently, dispatching at the `initialize` step. This is the deliberate price of immutable snapshots: every version is its own world. The benefit is that there's no migration code to maintain; the cost is that supporting N versions means N parallel handler trees. The spec's bet is that most servers will pin to one version at a time and let clients adapt — which, in practice, is what happens.

These three omissions are load-bearing. Adding auth to the envelope would make every transport's auth story redundant; adding a retry policy would lock in a wrong default; adding migration tools would calcify the implementation against future redesigns. The spec stays small so the implementations can be many.

---

## Further reading

- JSON-RPC 2.0: <https://www.jsonrpc.org/specification>
- Language Server Protocol: <https://microsoft.github.io/language-server-protocol/>
- MCP basic envelope: `docs/specification/2025-11-25/basic/index.mdx` ([permalink](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/basic/index.mdx))
- MCP envelope types (TS source of truth): `schema/2025-11-25/schema.ts:1-180` ([permalink](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1-L180))
- Lifecycle handshake: `docs/specification/2025-11-25/basic/lifecycle.mdx` ([permalink](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/basic/lifecycle.mdx))
