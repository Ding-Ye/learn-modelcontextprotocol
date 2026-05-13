---
title: "s06 · 反向 LLM 请求：sampling"
chapter: 06
slug: s06-sampling
est_read_min: 14
---

# s06 · 反向 LLM 请求：sampling

> 本章要点：让 MCP 真正成为 *agent* 协议的那一刀——**服务器**让**客户端**去跑 LLM。`sampling/createMessage` 是 base spec 里唯一一条 server→client 方向的请求，围绕它要补出"待响应表 + 响应 id 关联 + 超时 + `tool_use` / `tool_result` 两种 ContentBlock 变体"这一整套基础设施，agentic loop 才闭得回来。

---

## Problem

s01..s05 一路下来请求都是单方向的：客户端问，服务器答。这够"把 tools / resources 暴露给模型"用，但不够"让服务器自己问模型一个问题"。后者才是 agent 的底气——服务器可以读日志、决定下一步是去总结一份文档、查一次 DB、还是再调一个工具。

MCP 规范用**一条**反向 RPC 解决这个问题：`sampling/createMessage`。服务器发一条请求，参数里塞 messages、maxTokens、可选的 tools / modelPreferences。客户端跑 LLM、回包。这一次方向反转是其余 MCP 能力（roots、elicitation、list_changed 通知）能自洽的根：它们都复用 s06 这一章搭出来的 server→client 请求骨架。

本章要解决的痛点：**实现这次反转**。定义 `CreateMessageRequestParams` / `Result`、新加 `tool_use` 和 `tool_result` 两种 `ContentBlock` 变体、抽出 `Sampler` 接口把 LLM 调用点变成可插拔的、再写一个 `OutboundRouter` 按 id 跟踪未完成请求并自带 30 秒超时。最后用一个 demo 工具，**它的 body 里**就要发 sampling 请求。

## Solution

四件套：

1. **`CreateMessageRequestParams`** —— 跟 schema.ts:1580-1624 一一对应。`Temperature` 用 `*float64`（0 是合法的温度，需要区分 nil 与 0）；`MaxTokens` 用普通 `int` 但校验必须 `> 0`；`Tools` / `ToolChoice` 只在客户端声明了 `sampling.tools` 时才合法。
2. **`Sampler` 接口** —— `CreateMessage(ctx, params) → (Result, error)`。两个实现：`StubSampler`（罐头回复、可排队 tool_use，测试用，确定性）和 `AnthropicSampler`（骨架；HTTP body 留到 Phase G 多模型分支再补）。即便 s06 demo 是单进程，这个接口也定在"客户端边界"——真实客户端会在这里分发到对应模型 provider。
3. **`OutboundRouter`** —— 一张 `map[string]*pendingEntry` 表，按请求 id 索引。`SendRequest` 写帧 → 注册待响应 channel → 要么读到回包要么 30 秒后超时。超时分支会在返回前**删掉表项**，断线的客户端不会泄漏 goroutine。
4. **5 变体的 `ContentBlock`** —— text、image、audio（沿用 s03），加上 `tool_use` 和 `tool_result`。后两者是 sampling 的"agentic 标记"：LLM 发 `tool_use`，服务器在原地跑工具，下一次 sampling 请求里把 `tool_result` 带回去。

三个值得点名的设计取舍：

- **`Sampler` 用接口，不用 callback。** s06 用 `func` 也够，但真实 Sampler 会带状态（HTTP client、重试、限流、缓存）。一开始就升成接口是零成本。
- **进程内 Sampler 短路。** 当 `Router.sampler != nil` 时，`requestSampling` 直接调它、跳过线路。demo 与测试都同步、离线、确定性。字段为 `nil` 时才走 `OutboundRouter` 真把帧写出去——这才是真实客户端-服务器部署用的路径。
- **校验放在 `validateCreateMessage` 函数里，不靠类型系统。** `maxTokens > 0`、`includeContext ∈ {none, thisServer, allServers}` 在 Go 里要表达只能靠 wrapper，而 wrapper 会把约束藏起来。直接函数校验、错就 `-32602`，最直白。

## How It Works

```text
┌──────────────────────────────────────────────────────────────────┐
│                          INBOUND PATH                            │
│ stdin → stdioFramer → Message → Router.DispatchInbound           │
│                                       │                          │
│            ┌──────────────────────────┼────────────────────┐     │
│            ▼                          ▼                    ▼     │
│      initialize                  tools/call           response   │
│      (lifecycle)                      │            (路由到       │
│                                       ▼            OutboundRouter│
│                                callSummarize         .HandleResponse) │
│                                       │                          │
│                                       ▼                          │
│                          requestSampling(params)                 │
│                                       │                          │
│                       ┌───────────────┴────────────┐             │
│                       ▼                            ▼             │
│              sampler != nil          sampler == nil              │
│              StubSampler.CreateMessage   OutboundRouter.SendRequest │
│                       │                            │             │
│                       │                            ▼             │
│                       │                  framer.Write + pending  │
│                       │                            │             │
│                       │             ┌──────────────┴────────┐    │
│                       │             ▼                       ▼    │
│                       │     <-pendingEntry.ch         <-timeoutCtx│
│                       │             │                       │    │
│                       └─────────────┴───────────────────────┘    │
│                                     │                            │
│                                     ▼                            │
│                            CreateMessageResult                   │
└──────────────────────────────────────────────────────────────────┘
```

校验核心（`sampling.go`）：

```go
func validateCreateMessage(p CreateMessageRequestParams) *Error {
    if p.MaxTokens <= 0 {
        return &Error{Code: InvalidParams, Message: "sampling/createMessage: maxTokens must be > 0"}
    }
    if len(p.Messages) == 0 {
        return &Error{Code: InvalidParams, Message: "sampling/createMessage: messages must not be empty"}
    }
    switch p.IncludeContext {
    case "", "none", "thisServer", "allServers":
    default:
        return &Error{Code: InvalidParams, Message: "sampling/createMessage: invalid includeContext: " + p.IncludeContext}
    }
    return nil
}
```

待响应表机制（`outbound.go`）：

```go
ch := make(chan pendingResult, 1)
timeoutCtx, cancel := context.WithTimeout(ctx, o.timeout)
o.mu.Lock()
o.pending[key] = &pendingEntry{ch: ch, cancel: cancel}
o.mu.Unlock()

if err := o.framer.Write(msg); err != nil { ... }

select {
case res := <-ch:
    cancel(); return res.Result, res.Err
case <-timeoutCtx.Done():
    o.removePending(key)
    return nil, fmt.Errorf("outbound %s: timed out after %s", method, o.timeout)
}
```

四个不显然的点：

1. **先注册 pending，再写帧。** 否则一个快速的进程内 Sampler（或本地 HTTP loopback）可能在 channel 还没建好之前就回包。多一次 `mu.Lock` 是值得的代价。
2. **`pendingEntry.ch` 带 1 的缓冲。** 接收方可能走了超时分支；没缓冲的话 `HandleResponse` 在一个"孤儿 id"上会永远阻塞。
3. **`ID.Key()` 用的是**原始 JSON 字符串**。** `ID` 内部包了 slice，不能直接做 map key。`string(rawJSON)` 的区分度刚好等于 JSON-RPC 规范要求的：不同 id ⇒ 不同 key。
4. **路由器对 `IsResponse` 分支的处理在 lifecycle 闸门**之前**。** 即便入站表面被 gate 住，对外响应也必须能流回——否则一次慢握手会把 sampler 调用卡死。

## What Changed (vs. s05)

- **第一次出现 server-initiated 请求。** s01..s05 全是单向分发；s06 多了整整一个 `outbound.go`。
- **`ContentBlock` 又被重写，多了 `tool_use` + `tool_result`。** s05 只有 text + image + audio + resource。这两个新变体就是 sampling 之所以"agentic"的标记——LLM 用它们说"请再来一轮"。
- **Client capability 第一次真起作用。** 初始化握手记录 `clientSampling = (caps.sampling != nil)`。线路路径下，服务器**应当**在客户端没声明 sampling 时拒绝发——demo 跳过这一步，因为 StubSampler 永远能跑。
- **新类型：`ToolChoice` 和 `ModelPreferences`。** 都是 advisory（建议而非强制）。demo 接住了字段但 StubSampler 不去看——真实客户端也可以这么做。

## Try It

```bash
cd agents/s06-sampling
make demo
```

会看到 4 行 JSON。注意看第三条（id=3）：它是一次 `tools/call` 的回包，但 `content` 就是 StubSampler 那段罐头文本——因为 `summarize` 工具的 body 里发出了一次 `sampling/createMessage`，再把答案揉进工具自己的结果。

```json
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"(stub summary) the server asked the client to run an LLM; here is the canned reply."}]}}
```

直接跑 agentic loop 的测试：

```bash
go test -v -run TestSamplingAgenticToolLoop ./agents/s06-sampling
```

会看到 stub 被调了 2 次（CallCount=2）：第一次回了一个 `tool_use` 找 `echo`，服务器在原地跑 `echo`，第二次 sampling 请求的 messages 里就带着一个 `tool_result`。最终工具结果用的是第二次回包的文本。

## Upstream Source Reading

s06 对应的上游区段是 `schema/2025-11-25/schema.ts:1574-1998`——建议跟 `agents/s06-sampling/{sampling,sampler,outbound,router}.go` 并排看。

**Sampling: [`schema.ts:1574-1998`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1574-L1998)**

- **L1580-L1624** —— `CreateMessageRequestParams`。`maxTokens` 不是可选的；`includeContext` 是三值联合（`"none" | "thisServer" | "allServers"`）。`tools` 和 `toolChoice` 受 `ClientCapabilities.sampling.tools` 约束——规范说客户端在该能力未声明时**必须**报错；s06 没强制。
- **L1628-L1640** —— `ToolChoice`。三种模式：`auto`（默认）、`required`、`none`。Provider 自己解释。
- **L1646-L1654** —— `CreateMessageRequest`。method 字面量 `"sampling/createMessage"`——虽然是 *server-initiated*，envelope 沿用 `JSONRPCRequest`。
- **L1658-L1681** —— `CreateMessageResult`。extends `SamplingMessage`（role + content），再加 `model` 和一个开放字符串 `stopReason`，标准值为 `endTurn` / `stopSequence` / `maxTokens` / `toolUse`。
- **L1683-L1696** —— `SamplingMessage`。role 是 `user | assistant`；content 可以是单条 block 也可以是数组。我们的 Go `UnmarshalJSON` 两种都吃。
- **L1700-L1839** —— text / image / audio 三种 content block（沿用 s03 tools 章的成果）。
- **L1840-L1869** —— `ToolUseContent`：`id`、`name`、`input: { [string]: unknown }`。这个 id 之后会跟 `toolResult.toolUseId` 对上。
- **L1874-L1898** —— `ToolResultContent`：`toolUseId`、`content: ContentBlock[]`、可选 `structuredContent`、可选 `isError`。这是 agentic loop 闭环的那一块。
- **L1930-L1979** —— `ModelPreferences`。三个数值优先级（cost / speed / intelligence，各 0..1）加上一个 `hints` 名字子串数组。advisory only。

**散文：[`client/sampling.mdx`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/client/sampling.mdx)** 用配图 JSON 把完整 server→client 交互走了一遍，特别强调 *human-in-the-loop*：客户端**应当**在替服务器跑 LLM **之前**请用户确认，回结果**之前**再请用户确认。我们的 `StubSampler` 两步都跳过了——它是测试脚手架；真实客户端必须实现。

那个 mdx 里两段值得背下来：

- **"Trust & Safety: Sampling allows servers to ask clients to run an LLM, so clients SHOULD implement explicit user approval flows."** 这就是为什么 `Sampler` 是接口而不是回调函数：真实实现要在这里 gate 用户确认。
- **"Implementation Notes: clients SHOULD implement timeouts."** 我们 30 秒 `DefaultOutboundTimeout` 的来源。

本地离线副本：[`upstream-readings/s06-sampling.ts`](../../upstream-readings/s06-sampling.ts)（schema.ts 1574-1998 行）。
