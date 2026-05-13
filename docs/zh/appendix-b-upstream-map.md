---
title: "附录 B · 上游源码导读地图"
chapter: B
slug: appendix-b-upstream-map
est_read_min: 15
---

# 附录 B · 上游源码导读地图

> 本章要点：你写完 Go reimplementation 后，怎么去看上游 `modelcontextprotocol/modelcontextprotocol` 仓库。上游最值钱的单一文件是 `schema/2025-11-25/schema.ts` —— 2,587 行 TypeScript，是所有线上格式的"真理之源"。本章给你按行号的导读、必读 SEP 列表、以及 s08 之后还能继续做的 5 个扩展练习。

---

## 阅读顺序

冷读上游你会淹死。请按以下顺序读：

1. **`README.md`**（仓库根目录）—— 让你立刻明白"这是规范仓库，不是参考实现"。
2. **`docs/specification/2025-11-25/index.mdx`** —— 规范总览。JSON-RPC 基础、能力协商、模块化特性。约 10 分钟。
3. **`docs/specification/2025-11-25/basic/lifecycle.mdx`** —— 握手流程的文字描述 + 时序图。这是动 `schema.ts` 之前 *必须* 完整读完的 *一份* 文档。
4. **左右分屏**：左屏 `schema/2025-11-25/schema.ts`，右屏对应章节的 `docs/specification/2025-11-25/{server,client}/*.mdx`。用下面的行号表跳到你刚写完的章节对应的那段 —— s03 → tools、s04 → resources……
5. **`seps/`** —— *仅当好奇* 某条设计为什么这么定时再访问。SEP 不是必读，是背景。下面列了 5 个值得花时间的。

要避免的坑：上来就打开 `schema.ts` 从头读到尾。这个文件是按 JSDoc 分组排版的，不是按阅读难度 —— Tasks（1300-1506）和 Sampling（1574-1998）出现在更简单的 Roots（2089-2160）和 Elicitation（2161-2506）之前。请按下面的表跳读，不要按文件顺序读。

## `schema.ts` 行号导读

Permalink 全部 pin 到上游 sha `85ec377139c3026d98086eadbb23806e65517f21`。点击会跳到 github 对应行。

| 行号       | 章节                                  | 对应 Session         | Permalink                                                                                                                                                |
| --------- | ------------------------------------ | -------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1-180     | JSON-RPC 信封、错误码                  | s01                  | [schema.ts:1-180](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1-L180) |
| 211-249   | Cancellation                          | （s06 提到）          | [schema.ts:211-249](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L211-L249) |
| 251-459   | Initialize、能力协商                  | s02                  | [schema.ts:251-459](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L251-L459) |
| 651-921   | Resources                            | s04                  | [schema.ts:651-921](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L651-L921) |
| 923-1081  | Prompts                              | s05                  | [schema.ts:923-1081](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L923-L1081) |
| 1082-1299 | Tools                                | s03                  | [schema.ts:1082-1299](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1082-L1299) |
| 1300-1506 | Tasks（备用）                          | 附录 B 练习           | [schema.ts:1300-1506](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1300-L1506) |
| 1574-1998 | Sampling                             | s06                  | [schema.ts:1574-1998](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1574-L1998) |
| 1742-1900 | ContentBlock                         | s03 / s05 / s06      | [schema.ts:1742-1900](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1742-L1900) |
| 2006-2088 | Completion                            | s05                  | [schema.ts:2006-2088](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L2006-L2088) |
| 2089-2160 | Roots                                | s07                  | [schema.ts:2089-2160](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L2089-L2160) |
| 2161-2506 | Elicitation                          | s07                  | [schema.ts:2161-2506](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L2161-L2506) |

实操工作流：写完 sNN 后，在上面的表里找到对应行，点 permalink，右屏看 TS、左屏看你的 Go 实现。两边的 *差异* 才是真功课 —— 每一个 TS 接口里有、你 Go 里没实现的字段，问自己"我刚才需要它吗"。多半你不需要，因为课程实现的是 MVP 子集。但这些差异是一张地图，告诉你：要从教学版进化到生产版，得补哪些东西。

## Top 5 SEPs

`seps/` 目录有 35 份设计提案。其中 5 份值得读 —— 不是因为你会实现它们，而是它们示范了协议是怎么思考的。

**SEP-2575 · Make MCP Stateless**（[seps/2575-stateless-mcp.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2575-stateless-mcp.md)）

最有分量的待定变更。它提议彻底移除 `initialize` 握手，把协议版本和能力位塞进每一个请求里，让每个请求自包含。动机：负载均衡器没法把有状态的 MCP session 在多个服务实例间分流（除非用 sticky session 或共享 session 存储），运维只能搭复杂的 sticky-session 方案。如果它落地，本课程的 s02 就成了"历史握手"，s08 的 `Mcp-Session-Id` 也就成了过渡时代的遗物。如果你部署过任何"有状态 WebSocket 服务跑在负载均衡后面"的东西，这份必读。

**SEP-2567 · Sessionless MCP via Explicit State Handles**（[seps/2567-sessionless-mcp.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2567-sessionless-mcp.md)）

SEP-2575 的搭档。2575 移掉 `initialize`，2567 移掉 `Mcp-Session-Id` header —— 用服务端铸造的"显式 state handle"替代 session 作用域。购物车的例子：服务端不再把 cart 绑在 session 上，而是暴露一个 `create_basket()` 返回 `basket_id`，模型把这个 id 串到后续的 `add_item(basket_id, ...)`。论据是：今天 session 在各 MCP 客户端里没有一致语义（ChatGPT 按每次 tool call 一个、IDE 按每次启动应用一个、Web 按每次刷页面一个），显式 handle 能让服务端开发者面对一个稳定的抽象去设计。

**SEP-2243 · HTTP Header 标准化**（[seps/2243-http-standardization.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2243-http-standardization.md)）

已经在 2025-11-25 落地。新增 `Mcp-Method` 和 `Mcp-Name` 两个 HTTP header，把 JSON-RPC 的 `method` 和 `params.name`/`params.uri` 字段镜像出来。动机很务实：负载均衡器、WAF、观测工具不再需要解析 JSON body 才能做路由和策略。你在 s08 实现过 `MCP-Protocol-Version` header，这份 SEP 是"为什么 HTTP header 要重复携带 JSON body 里已经有的信息"的论证。

**SEP-1686 · Tasks**（[seps/1686-tasks.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/1686-tasks.md)）

2025-11-25 引入的异步执行原语，对应 `schema.ts:1300-1506`。一次 tool call 可以返回一个 `taskId` 而不是完整结果，客户端去 poll `tasks/get` 或订阅 `tasks/result` 拿最终结果。动机：真实业务负载（药物发现流水线、企业流程自动化、CI/CD）动辄几分钟到几小时，现行"发请求等响应"逼着服务端为每个工具自己造一套 polling 工具。tasks 把这件事升级为协议原语。下面的练习 4 就是它。

**SEP-2322 · Multi Round-Trip Requests（MRTR）**（[seps/2322-MRTR.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2322-MRTR.md)）

整理"客户端请求进行中、服务端反向发起请求"这套机制。今天，一次 tool call 触发 `elicitation/create` 时，服务端需要把 elicitation 的响应跟未完成的 tool call 关联起来 —— 这个关联状态目前隐含在 session 里。MRTR 在请求/响应流转中加一个显式字段，让关联显式起来。动机跟 SEP-2575 重叠：去掉隐式 session 状态，能降低远程 MCP 服务端的运维复杂度（这些服务端没法轻易跑 sticky-session 负载均衡）。这是一次破坏性变更。

## 其他值得提的 SEP

**SEP-2133 · Extensions**（[seps/2133-extensions.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2133-extensions.md)）。定义协议如何在不强制所有实现跟进的前提下生长：一套"扩展标识"命名空间（`{vendor-prefix}/{extension-name}`，例如 `io.modelcontextprotocol/oauth-client-credentials`），加上"官方扩展 vs 实验性扩展"的治理流程。想知道非核心能力是怎么进入规范的，扫一眼即可。

**SEP-1036 · URL 模式 Elicitation**（[seps/1036-url-mode-elicitation-for-secure-out-of-band-intera.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/1036-url-mode-elicitation-for-secure-out-of-band-intera.md)）。你在 s07 实现的 URL 模式的出处。动机是安全：form 模式的 elicitation 把数据穿过 MCP 客户端转发，这对凭据、OAuth flow、支付场景不可接受。URL 模式把交互让渡给用户的浏览器，敏感字节根本不进 JSON-RPC 通道。

**SEP-1024 · 本地服务安装的客户端安全要求**（[seps/1024-mcp-client-security-requirements-for-local-server-.md](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/1024-mcp-client-security-requirements-for-local-server-.md)）。针对"一键安装恶意 MCP 服务端"的攻击面 —— stdio 服务端本质就是 `command + args + env`，一份精心构造的 config 就能执行任意命令。这份 SEP 要求本地服务端安装必须显式用户同意 + 命令可见。如果你在做 MCP host 应用（而不只是服务端），这份相关。

## 5 个扩展练习

课程在 s08 收尾，但协议还有大量可玩的东西。下面 5 个练习，难度大致递增。

**1. s04 接真实 fsnotify 订阅**

s04 的 `resources/subscribe` 用了内存 watcher，因为 demo 的资源是 `mem://`。练习：换成真实文件系统资源（`file://` URI 限定在某个沙箱目录内），把 `resources/subscribe` 接到 `github.com/fsnotify/fsnotify`。文件改动时发 `notifications/resources/updated`。坑点：去抖（多数编辑器用"原子 rename"保存，会触发多次 fsnotify 事件）、文件删后重建怎么办、模板被订阅时怎么办（要监听父目录、每次变动都重新展开模板吗）。从 `agents/s04-resources/subscribe.go` 起步。

**2. s05 的 `completion/complete` 接 LLM**

s05 的 completion handler 返回写死的候选列表。练习：接真实 LLM。把静态候选换成一次 `Sampler` 调用 —— 提示 `summarize-file` 的参数 `path` 的补全变成"问 LLM：『给定 prompt `summarize-file`、当前已填参数 {…}，请给 `path` 5 个最可能的取值』"。教学意义是 completion 和 sampling 可以组合：s05 + s06 = "能理解你代码库的补全"。从 `agents/s05-prompts/completion.go` 起步，把 `Sampler` 接口从 `agents/s06-sampling/sampling.go` 端过来。

**3. 2024-11-05 HTTP+SSE fallback 传输**

Streamable HTTP 之前，MCP 用的是另一套 HTTP 传输：两个 endpoint（`POST /message` 和 `GET /sse`），客户端→服务端和服务端→客户端各走一条。上游仓库 `docs/specification/2024-11-05/basic/transports.mdx` 有完整描述。练习：把那套 *老* 传输实现一遍，跟 s08 的 Streamable HTTP 并存，服务端在请求时机协商 —— 收到 2024-11-05 形状的请求就走老传输，否则走 Streamable HTTP。教学意义是看一次"向后兼容到底要付什么代价"。从 `agents/s08-streamable-http/server.go` 起步，新加一份 `framer_legacy.go`。

**4. 实现 SEP-1686 tasks**

最具体的一次规范扩展。Schema 行号是 `schema.ts:1300-1506`。练习：实现 `tasks/get`、`tasks/result`、`tasks/cancel`，以及"task-augmented request"封装 —— 让 `tools/call` 可以返回 `taskId` 而不是完整结果。造一个真正耗时的 tool（比如 `crawl_url`），立刻返回 task、异步收尾。坑点：持久化任务存储（内存 vs sqlite vs 文件系统）、task TTL、"这个请求是 task-augmented"在线上格式里怎么表达。从 `agents/s03-tools/tools.go` 起步，新加一个 `tasks` 包；你会需要一个 goroutine pool 驱动 task 执行。

**5. 实现 SEP-2575 stateless 模式**

最难的一个。把 s08 里的 `initialize` 握手干掉，协议版本 + 能力位放进 *每一个* HTTP 请求 header（`MCP-Protocol-Version` s08 已经有了，再加几个 capability header），证明"两个不同客户端的 POST 请求落到不同服务端副本上仍然能正常工作"。这个练习的真正意义是亲手体会"为什么有状态那么难甩掉"：每一处今天住在 `Session{}` 里的数据结构，要么搬进请求（能力、版本），要么改成能用 id 从外部存储拉回（订阅、待发请求）。从 `agents/s08-streamable-http/server.go` 起步，假装你必须把这玩意儿放到 L4 round-robin 负载均衡后面跑。

---

## 延伸阅读

- 上游仓库根目录：<https://github.com/modelcontextprotocol/modelcontextprotocol>
- 规范总览：[docs/specification/2025-11-25/index.mdx](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/index.mdx)
- Schema 真理之源：[schema/2025-11-25/schema.ts](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts)
- SEP 总入口：[seps/](https://github.com/modelcontextprotocol/modelcontextprotocol/tree/85ec377139c3026d98086eadbb23806e65517f21/seps)
