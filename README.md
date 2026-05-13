# learn-modelcontextprotocol

> 用 Go 渐进式重建 [modelcontextprotocol/modelcontextprotocol](https://github.com/modelcontextprotocol/modelcontextprotocol) 协议规范的核心机制——每一章一个机制，每一章末尾有上游 TypeScript 规范的注解阅读。教学法借鉴 [shareAI-lab/learn-claude-code](https://github.com/shareAI-lab/learn-claude-code)。

English version: [README.en.md](./README.en.md).

## 这是什么 / What

MCP（Model Context Protocol）是 Anthropic 主导、社区共建的开放 JSON-RPC 协议，让 LLM 应用能发现并调用工具、读取上下文资源、发起反向 sampling 请求等。上游仓库 **没有** 一份"完整参考实现"——它是规范（schema.ts + docs/）和增强提案（seps/），所有 SDK 都在外面分散维护。

本仓库的目标：**用 Go 从零渐进重建上游协议规范的核心机制**——每章实现一个能跑的子集，最终凑出一个可以承载 `initialize → tools/list → tools/call (含 sampling)` 全链路的 MCP 服务器 + 客户端。

每章 ≤ 1000 行 Go，每章是独立的 Go module（`agents/sNN-*/`，互不 import），重复定义是教学法刻意的：你会把 `Message`、`ContentBlock` 这些核心类型亲手写六遍。

## Curriculum

| #     | 章节                                        | 状态 |
|-------|---------------------------------------------|------|
| s01   | 最小回路：JSON-RPC 与 stdio 帧              | ✅   |
| s02   | 初始化握手与能力协商                        | ✅   |
| s03   | tools/list 与 tools/call                    | ✅   |
| s04   | 资源读取、模板与订阅                        | ✅   |
| s05   | 提示模板与参数补全                          | ✅   |
| s06   | 反向 LLM 请求：sampling                     | ✅   |
| s07   | 根目录与表单/URL 引导                        | ✅   |
| s08   | Streamable HTTP 传输                         | ✅   |
| s_full| 端到端集成穿刺                              | ✅   |
| App A | 附录 A · 为何 JSON-RPC + 日期版本           | ✅   |
| App B | 附录 B · 上游源码导读地图                   | ✅   |

## 快速开始 / Quickstart

```bash
git clone https://github.com/Ding-Ye/learn-modelcontextprotocol
cd learn-modelcontextprotocol
go work sync

# s01 demo：一行 initialize 请求 → 一行 InitializeResult
cd agents/s01-min-loop
make demo
```

需要 Go 1.22+。

## 双语文档浏览 / Bilingual doc viewer

```bash
cd web
npm install
npm run dev    # http://localhost:3000
```

侧边栏可切换章节，右栏渲染上游源码片段。

## 致谢 / Acknowledgements

- 上游：[modelcontextprotocol/modelcontextprotocol](https://github.com/modelcontextprotocol/modelcontextprotocol)（Apache-2.0 + CC-BY-4.0）。本仓库的 `upstream-readings/` 直接节选上游 `schema/2025-11-25/schema.ts` 与 `docs/specification/2025-11-25/*.mdx`，用于注解阅读，遵循上游 license。
- 教学法：[shareAI-lab/learn-claude-code](https://github.com/shareAI-lab/learn-claude-code) 启发了"每章一个机制 + 上游源码导读"的结构。
- 生成工具：本仓库由 [Anthropic's learn-repo-generator skill](https://docs.anthropic.com/) 自动生成。

## License

MIT — 见 [LICENSE](./LICENSE)。注意：本仓库**复刻**上游协议机制的 Go 实现是 MIT 的；`upstream-readings/` 下的上游源码片段保留上游 Apache-2.0 / CC-BY-4.0 许可。
