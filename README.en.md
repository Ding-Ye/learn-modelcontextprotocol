# learn-modelcontextprotocol

> Re-grow the core mechanisms of [modelcontextprotocol/modelcontextprotocol](https://github.com/modelcontextprotocol/modelcontextprotocol) from scratch in Go — one mechanism per chapter, each ending with an annotated reading of the upstream TypeScript spec. Pedagogy inspired by [shareAI-lab/learn-claude-code](https://github.com/shareAI-lab/learn-claude-code).

中文版本: [README.md](./README.md).

## What

MCP (Model Context Protocol) is an open JSON-RPC specification led by Anthropic and built by the community, enabling LLM applications to discover tools, read context resources, request reverse-direction sampling, and more. The upstream repo is **not** a single reference implementation — it ships the spec (`schema.ts` + `docs/`) plus 35+ enhancement proposals (`seps/`); the actual SDKs live elsewhere.

This repo: **re-grow the core protocol mechanisms in Go, chapter by chapter**. Each chapter ships a runnable subset, and by the end you've built a Go MCP server + client that can drive `initialize → tools/list → tools/call (with sampling)` end-to-end.

Each chapter is ≤ 1000 lines of Go and is its own Go module (`agents/sNN-*/`, no cross-imports). The duplication is deliberate pedagogy — you'll write `Message` and `ContentBlock` by hand half a dozen times.

## Curriculum

| #     | Chapter                                              | Status |
|-------|------------------------------------------------------|--------|
| s01   | Minimum loop: JSON-RPC + stdio framing               | ✅     |
| s02   | Initialize handshake & capabilities                  | ✅     |
| s03   | tools/list and tools/call                            | ✅     |
| s04   | Resources: read, templates, subscribe                | ✅     |
| s05   | Prompts and completion/complete                      | ✅     |
| s06   | Reverse-direction LLM: sampling                      | ⏳     |
| s07   | Roots and elicitation (form + URL)                   | ⏳     |
| s08   | Streamable HTTP transport                            | ⏳     |
| s_full| End-to-end integration trace                         | ⏳     |
| App A | Appendix A · Why JSON-RPC & date-based versioning    | ⏳     |
| App B | Appendix B · Upstream source-reading map             | ⏳     |

## Quickstart

```bash
git clone https://github.com/Ding-Ye/learn-modelcontextprotocol
cd learn-modelcontextprotocol
go work sync

# s01 demo: one initialize request line → one InitializeResult line
cd agents/s01-min-loop
make demo
```

Requires Go 1.22+.

## Bilingual doc viewer

```bash
cd web
npm install
npm run dev    # http://localhost:3000
```

The sidebar switches chapters; the right pane renders annotated upstream slices.

## Acknowledgements

- Upstream: [modelcontextprotocol/modelcontextprotocol](https://github.com/modelcontextprotocol/modelcontextprotocol) (Apache-2.0 + CC-BY-4.0). The `upstream-readings/` directory contains literal slices of `schema/2025-11-25/schema.ts` and `docs/specification/2025-11-25/*.mdx` for annotated reading, kept under their original license.
- Pedagogy: [shareAI-lab/learn-claude-code](https://github.com/shareAI-lab/learn-claude-code) — the "one mechanism per chapter + upstream source reading" structure is borrowed wholesale.
- Generator: this repo was scaffolded by [Anthropic's learn-repo-generator skill](https://docs.anthropic.com/).

## License

MIT — see [LICENSE](./LICENSE). Note: the **Go re-implementation** of upstream mechanisms is MIT; the **upstream slices** under `upstream-readings/` retain their original Apache-2.0 / CC-BY-4.0 license.
