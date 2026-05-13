---
title: "附录 A · 为何 JSON-RPC + 日期版本"
chapter: A
slug: appendix-a-design-rationale
est_read_min: 12
---

# 附录 A · 为何 JSON-RPC + 日期版本

> 本章要点：你在 s01-s08 中亲手实现的每一项线上决策，背后的 *why*。八章看完"该这么写"，本章退一步问：为什么是这个信封？为什么用日期不用 semver？为什么要能力协商？规范刻意 *不写* 哪些东西？把它当成下次队友问"他们怎么不直接用 REST？"时你能引用的那份答案。

---

## 双向通信不是补丁，是出发点

HTTP 是不对称的。客户端发问，服务端回答。HTTP 本身没有任何语言能表达"服务端有话要说"——所有"服务端推送"的现实方案（轮询、长轮询、SSE）都是后来叠上去的补丁。

MCP 拒绝从这个不对称起步。s06 你写了 `sampling/createMessage`：*服务端* 反过来请 *客户端* 跑一次 LLM 推理。s07 你写了 `roots/list` 和 `elicitation/create`：*服务端* 反过来问 *客户端* 用户的文件系统在哪、用户想干啥。这些方法在"请求-响应"传输里根本不成立。

JSON-RPC 2.0 用一个字段就解决了：`id`。请求带 `id`，对应的响应回相同的 `id`，接收方靠它做关联。framer 不关心谁先发——双方都能发请求、都能发响应，`id` 是唯一说明"这个结果对应那个请求"的东西。MCP 在此之上多加一条约束：请求的 `id` **绝不允许是 `null`**（schema.ts:[14-16](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L14-L16)），这样接收方就能假定 `id` 永远 *有内容*。

这就是为什么 s06 的 router 多出一张"待发请求"哈希表（key 是 `RequestId`）。一旦你接受协议是双向的，这张表是被 *逼出来* 的：刚发出请求的一方要记住"我在等这个 `id`"，刚收到响应的一方要查"这个 `id` 对应的原始请求是谁"。s01-s05 只有服务端持表，s06 你在双方都写了一遍。JSON-RPC 的 `id` 字段，就是让对称实现得以成立的那块基石。

## 信封与传输解耦

同一个 `Message` struct 在前七章跑 stdio（`agents/s01-min-loop/envelope.go` 到 `agents/s07-roots-elicitation/envelope.go`），在 s08 跑 HTTP+SSE（`agents/s08-streamable-http/framer.go`）。对比 s07 的 stdioFramer 和 s08 的 httpFramer 的 diff：信封一个字节都没改，只有 `Read` / `Write` 改了。

因为 JSON-RPC 只规定"字节怎么排" — 不规定"字节怎么传"。对比一下 gRPC：信封和 HTTP/2 是绑死的，`grpc.Request` 想跑 stdio 就得重做帧格式、流控、trailer header 协议。协议没法"从 HTTP/2 摘下来"，因为它的消息格式里编码了 HTTP/2 的细节（path 当作 method、headers、trailers、status code）。

MCP 走的是相反路线：信封是纯 JSON，framer 接口只有 `Read` / `Write` / `Close`，下面想插什么传输都行。basic 那篇规范页（`docs/specification/2025-11-25/basic/index.mdx`，[permalink](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/basic/index.mdx)）讲得直白：*"MCP 是一个跑在 JSON-RPC 2.0 之上的协议"*，选 JSON-RPC 就是因为它对传输不持立场。这就是为什么 s08 是一次 *换传输*，而不是 *重写* —— s01-s07 的 handler 原封不动落进去。

实际后果：等未来某个 SEP 引入 WebSocket 传输、QUIC 传输、或者"同进程嵌入"用的共享内存传输，你写过的章节都不需要动。只需要新写一个 `Framer`。信封即契约。

## 类型化网关：能力协商

`initialize` 之外的每一个方法，都被一个能力位 gate 住。机械链条是：

1. 客户端发 `initialize`，带 `ClientCapabilities`（schema.ts:[251-459](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L251-L459)）。
2. 服务端回 `ServerCapabilities`。
3. 双方都记下对方支持哪些。
4. 之后所有方法 — `tools/call`、`resources/subscribe`、`sampling/createMessage` 等等 — 除非 *接收方* 声明了对应能力，否则一律拒绝。

s02 里你写了这个状态机：服务端若没 set `ServerCapabilities.tools`，`tools/list` 直接被拒。s06 里你在服务端写了对称的检查：若客户端没 set `ClientCapabilities.sampling`，服务端就不能发 `sampling/createMessage`。router 那个"这个方法当前允许吗"的判断，是规范用来防止"未来某版本的对端调用本版从未听说过的方法"的 *唯一* 机制。

正是这条机制让 MCP 可以演进而不破坏旧客户端。2025-11-25 加 `tasks/get`（schema.ts:[1300-1506](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1300-L1506)）时，没声明对应能力的旧客户端根本不会看到这个方法。服务端检查 cap 位，落回旧路径。整个过程没有"嗅探版本"、没有"试探着发一发看 work 不 work"、没有"发出去赌一把"——只有 initialize 那一道类型化网关，剩下全是机械推导。

能力协商隐含地 *定义* 了什么叫"能力"：接收方决定要不要支持、握手时一次性声明、整个会话内假定不变的某种东西。这就是为什么 s04 里你看不到"服务端支持 resources"的硬编码常量 —— 那是从 `ServerCapabilities.resources` 读出来的运行时值。能力位是规范在"你能问什么"和"你能回什么"之间做 join 的那一列。

## 日期版本而非 semver

`protocolVersion: "2025-11-25"` 不是 semver 意义上的版本号。`2025-06-18` 和 `2025-11-25` 之间没有任何兼容关系，除了"它们是两个不同的快照"。当前在世的版本一共 5 个 —— `2024-11-05`、`2025-03-26`、`2025-06-18`、`2025-11-25`、`draft` —— 一台讲 `2025-11-25` 的服务端，并未承诺它能不能讲 `2025-06-18`。

semver 隐含的是"兼容补丁"：`2.1.0` 兼容 `2.0.0` 再加新特性；`3.0.0` 是破坏性变更；你可以约束"任何 >= 2.0.0 且 < 3.0.0"的版本。MCP 不这么工作。每个版本都是一个不可变的时间点快照 —— 一旦发布，那一坨字节就再也不变。客户端说"我讲这个 *精确* 快照"，服务端说"我讲这个 *精确* 快照"，要么一致（继续），要么不一致（lifecycle 规范 `docs/specification/2025-11-25/basic/lifecycle.mdx:165-175` 让对端断连）。

这条选择带来三个连锁后果：

1. **没有回滚 backport 的撕扯。** `2025-06-18` 里发现的 bug 不能就地打补丁 —— 那会默默地改变这个版本的意义。只能在下个快照里修。实现 pin 到快照，不 pin 到"最低版本"。
2. **semver 本该承担的活，由能力协商接手。** 不再说"2.1 版本引入了 tools"，而是说"任何 `ServerCapabilities.tools` 已设置的版本"。日期版本决定 *存不存在这个位*，能力位决定 *这次握手中这个位亮没亮*。
3. **日期字符串自带历法语义。** 看到 `2025-11-25` 你立刻知道这是哪天发的；`3.2.1` 永远做不到这一点。代价是规范维护者不能用版本号去表达"这是个大版本"或"这只是个小补丁"—— 每一个快照都只是另一个快照而已。

上游 `.learn/upstream/CLAUDE.md` 把这点写明：*"Specifications use date-based versioning (YYYY-MM-DD), not semantic versioning."* 这句话是承重墙。

## 设计先例：LSP

MCP 在结构上就是 *"给 LLM 用的 LSP"*。Language Server Protocol（微软出品，[microsoft.github.io/language-server-protocol](https://microsoft.github.io/language-server-protocol/)）当年用一套设计动作把"编辑器↔语言服务"这个接口标准化了，MCP 几乎逐条照搬：

| LSP 的动作                                     | MCP 的对应动作                                       |
| --------------------------------------------- | ---------------------------------------------------- |
| JSON-RPC 2.0 信封                              | JSON-RPC 2.0 信封                                    |
| `initialize` / `initialized` 握手             | `initialize` / `notifications/initialized` 握手     |
| `ClientCapabilities` / `ServerCapabilities`   | `ClientCapabilities` / `ServerCapabilities`          |
| 本地子进程跑 stdio、远程跑 socket             | 本地子进程跑 stdio、远程跑 HTTP+SSE                  |
| 服务端可以反过来请客户端干活（`workspace/applyEdit`） | 服务端可以反过来请客户端干活（`sampling/createMessage`） |
| 按方法的能力位                                 | 按方法的能力位                                       |

设计血缘不是巧合。LSP 用 "在编辑器和语言服务之间塞一层 JSON-RPC" 解了 *M 个编辑器 × N 种语言* 问题；MCP 用同一套手法解 *M 个 LLM app × N 个上下文服务* 问题。如果你接过 `pylsp` 或 `gopls`，s01 + s02 那套形状会让你觉得熟得发毛 —— `initialize`、能力、`notifications/initialized`、然后按方法分派。MCP 从 LSP 学到的教训是：一个有类型化握手 + 传输无关信封 + 按方法能力协商的协议，在多年特性增长里被实证可维护。这个先例值得抄。

进一步阅读：LSP 规范全文 <https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/>。前三节（Base Protocol、Initialize、Capabilities）跟本仓库 s01 + s02 几乎逐行对应。

## 规范刻意不写的，与为什么

一个协议也由它 *拒绝写* 的东西定义。MCP 有三处刻意留白：

**信封里没有 auth。** `Message` 没有 `auth` 字段。基础 JSON-RPC 信封里没有 `Authorization` header。auth 完全下推给传输层：stdio 跑在父进程的信任域里，Streamable HTTP 走标准的 OAuth 2.1 / OIDC。协议不发明自己的 auth 模型。好处是 MCP 没发明的东西你也没法配错；代价是每个传输自己得带一份。（HTTP 见 `docs/specification/2025-11-25/basic/authorization.mdx`，跟信封规范 [docs/specification/2025-11-25/basic/index.mdx](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/basic/index.mdx) 是分开的。）

**没有重试策略。** 请求超时怎么办？规范没说。没说"用同样的 id 重试"，没说"指数退避"，没说"N 次以后放弃"。重试留给实现者。理由是重试语义是应用层的事 —— 一次数据库查询和一次刷信用卡的幂等故事完全不同，一刀切的重试规则在大多数场景都是错的。协议给了你 `id`（做关联）和 `notifications/cancelled`（"我不等了"），剩下你自己定。SEP-1686（tasks）是最接近"结构化重试原语"的东西，但它是 *增强*，不是 *默认*。

**没有 schema 迁移工具。** 协议从 `2025-06-18` 升到 `2025-11-25` 时，仓库里没有 `migrate-v1-to-v2` 脚本，没有 `compatibility shim` 文档，没有"老 `Tool` 怎么翻译成新 `Tool`"的指南。实现者被预期为：独立支持多版本、在 `initialize` 那一步分派。这是不可变快照的代价：每个版本是自己的世界。好处是没有迁移代码要维护；代价是想支持 N 个版本就得维护 N 棵 handler 树。规范的赌注是：大多数服务端会一次只 pin 一个版本，让客户端去适配 —— 实际中也确实如此。

这三处留白都是承重的。把 auth 塞进信封，会让每个传输的 auth 故事变冗余；把重试塞进协议，会锁进一个大概率错的默认；把迁移工具塞进规范，会把实现的设计自由度冻死。规范越小，实现的种类才能越多。

---

## 延伸阅读

- JSON-RPC 2.0：<https://www.jsonrpc.org/specification>
- Language Server Protocol：<https://microsoft.github.io/language-server-protocol/>
- MCP 基础信封：`docs/specification/2025-11-25/basic/index.mdx`（[permalink](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/basic/index.mdx)）
- MCP 信封类型（TS source of truth）：`schema/2025-11-25/schema.ts:1-180`（[permalink](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1-L180)）
- Lifecycle 握手：`docs/specification/2025-11-25/basic/lifecycle.mdx`（[permalink](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/basic/lifecycle.mdx)）
