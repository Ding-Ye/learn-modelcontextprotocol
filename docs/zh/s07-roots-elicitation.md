---
title: "s07 · 根目录与表单/URL 引导"
chapter: 07
slug: s07-roots-elicitation
est_read_min: 13
---

# s07 · 根目录与表单/URL 引导

> 本章要点：再加两类**服务器→客户端**能力，都搭在 s06 引入的"出站请求脚手架"上。`roots/list` 让服务器问"我被允许在哪些文件系统目录里操作？"；`elicitation/create` 让服务器经由客户端向用户提问——可以是结构化表单（数据回流），也可以是客户端打开一个 URL 让用户在浏览器里完成（只回传一个 action）。同时这章首次出现 MCP 保留错误码 `-32042 URLElicitationRequired`，工具在"必须先完成 URL 引导才能继续"时返回它作为类型化短路。

---

## Problem

s06 让协议反向了一次：tool 调用中途，服务器通过 `sampling/createMessage` 反过来问**模型**并等待回答。那只是一个方向。还有两个：

1. **文件系统边界。** 想读文件或 grep 一个仓库的服务器，得先知道宿主愿意暴露哪些路径。这套集合是*编辑器的状态*（工作区选择器、打开文件夹弹窗），不是服务器的——所以得反过来问。线上接口是 `roots/list`，每个 `Root` 是一个 `file://` URI 加可选展示名（`schema.ts:2089-2160`）。
2. **向用户提问。** 服务器可能需要从人那里拿一点非敏感信息（"哪个频道？"、"确认要删？"），也可能需要敏感信息——这类信息*绝不能*经过 LLM 或客户端（API key、OAuth 同意页等）。MCP 把这两类合并到 `elicitation/create`，用 `mode` 字段区分：`form` 是带内传数据，`url` 引导用户去浏览器界面，只回传 `accept`/`decline`/`cancel`（`schema.ts:2161-2506`、`client/elicitation.mdx`）。

s07 要解决的痛点：写一个能发两类出站请求的服务器，再写一个能应答它们的客户端测试夹具；走通 form 接受的喜路径、cancel/decline 的旁路、URL 模式的参数形状，以及 `-32042` 那条"先做完引导再继续"的短路错误码。

## Solution

复用 s06 的核心思路——服务器发起的请求都走一个 `OutboundRouter`，由一张 `pending-id → channel` 表负责认领回信——上面再加两个薄薄的 sender。模式拆分本来会很疼：TypeScript 的判别联合在 Go 里翻不动，于是我们干脆声明两个 params 类型（`ElicitRequestFormParams` / `ElicitRequestURLParams`）和两个 sender（`sendFormElicitation` / `sendURLElicitation`）。回信共用一个 shape（`ElicitResult` + `Action ∈ {accept, decline, cancel}`）；解码器对任何非 accept 的回复一律抹掉 `Content`，免得错乱的客户端通过 cancel 漏数据。

四条关键设计决定：

1. **客户端能力门控出站发送。** 规范原话："Servers MUST NOT send elicitation requests with modes that are not supported by the client."。我们在 `initialize` 时记下客户端声明了什么（`elicitation.form`、`elicitation.url`），对没声明过的模式直接拒发。空对象的兼容糖（`elicitation: {} ≡ {form: {}}`）写在 `lifecycle.go` 里。
2. **`URLElicitationRequiredError` 是 Go 的类型化短路，不是手搓 `*Error`。** handler 直接 `return &URLElicitationRequiredError{Message: "...", Elicitations: [...]}`，路由器自动把它序列化成带 `data.elicitations` 的标准 `-32042` 回信。这样错误路径和结果路径就同形了。
3. **`OutboundRouter` 在本章是*重新写*的，没从 s06 引入。** plan 禁止跨 module 引用，重复就是教学法。我们做了点裁剪——本章只保留 `SendRequest` + `Dispatch` + 超时，没有进度通知。
4. **decline 与 cancel *不是* RPC 错误。** 它们是合法的用户行为。`sendFormElicitation` 拿到 `action: "decline"` 的工具，会回一段普通的 text content（"User declined to share..."），不是 `isError: true`。错误位只留给传输层/协议层失败。

## How It Works

```text
┌──────────────────────────────────────────────────────────────────┐
│ Client                                       Server               │
│ ─────                                        ──────               │
│ tools/call who-are-you ─────────────────────► Router.Dispatch     │
│                                                  │                │
│                                                  ▼                │
│                                          whoAreYou() handler      │
│                                                  │                │
│                                                  ▼                │
│                                  sendFormElicitation              │
│ ◄── elicitation/create (mode:form) ──────── OutboundRouter        │
│        params.requestedSchema                 (parks goroutine)   │
│                                                                   │
│ 用户在 UI 里填了名字                                                │
│                                                                   │
│ {action:"accept",content:{name:"Ada"}} ───► Dispatch 匹配 id      │
│                                              ▼                    │
│                                       恢复 handler                  │
│                                              ▼                    │
│                                  CallToolResult{                  │
│                                    Content: "Hello, Ada!"         │
│                                  }                                │
│ ◄── tools/call 结果 ───────────────────────                        │
└──────────────────────────────────────────────────────────────────┘
```

出站发送 / 派发那一对（`agents/s07-roots-elicitation/outbound.go`）：

```go
func (r *OutboundRouter) SendRequest(method string, params any) (json.RawMessage, error) {
    rawParams, _ := json.Marshal(params)
    id := NumberID(r.nextID.Add(1))
    entry := &pendingEntry{ch: make(chan outboundReply, 1), method: method}
    r.mu.Lock(); r.pending[id.String()] = entry; r.mu.Unlock()
    if err := r.w.Write(Message{JSONRPC: JSONRPCVersion, ID: &id, Method: method, Params: rawParams}); err != nil {
        r.cancel(id.String()); return nil, err
    }
    select {
    case reply := <-entry.ch:
        if reply.err != nil { return nil, reply.err }
        return reply.result, nil
    case <-time.After(r.timeout):
        r.cancel(id.String())
        return nil, fmt.Errorf("outbound %s timed out", method)
    }
}
```

URL 引导短路错误（`elicitation.go`）：

```go
type URLElicitationRequiredError struct {
    Message      string
    Elicitations []ElicitRequestURLParams
}

func (e *URLElicitationRequiredError) asRPCError() *Error {
    payload := struct{ Elicitations []ElicitRequestURLParams `json:"elicitations"` }{e.Elicitations}
    data, _ := json.Marshal(payload)
    return &Error{Code: URLElicitationRequired, Message: e.Message, Data: data}
}
```

四个不显然的点：

1. **`mode` 在 form 模式可选，在 URL 模式必填。** 规范刻意把 form 的 `mode` 保留为可选——这是向后兼容，旧客户端把缺字段当 form 处理。我们总是显式写 `"mode": "form"`，让线上字段无歧义；不带的旧服务器照样兼容。
2. **form 模式的 `requestedSchema` *不是*完整 JSON Schema。** 只允许顶层 primitive（string/number/boolean/enum），不允许嵌套对象，也不允许对象数组。这种刻意的限制让客户端能用一张扁平表单渲染——这也正是 URL 模式作为"承载所有复杂场景"的姊妹模式存在的理由。
3. **URL 模式在 JSON-RPC 通道上*不带*任何数据，这是设计意图。** 这就是安全边界：如果用户在浏览器界面里输了 API key，MCP 客户端永远看不到它。服务器只能通过 URL 终点的旁路（自己的 DB、OAuth 回调）知道结果。可选的 `notifications/elicitation/complete` 是让客户端在带外流程结束时刷新 UI，但同样不带任何用户数据。
4. **`-32042` 落在 JSON-RPC 实现保留段 [-32000, -32099]。** 它是 MCP 第一个跳出标准码的错误码。服务器**只能**在确实需要 URL 引导时回它；任何其他场景下返回都是违反规范。

## What Changed (vs. s06)

s06 引入了"服务器发起请求"，用在 `sampling/createMessage`。s07 沿用同一个机制，在同一根脚手架上加两个新端点：

- **两个新出站 RPC**，没有新增传输能力。`OutboundRouter` 结构上完全一样，只是每个 method 的 params / result 类型不同。
- **`ClientCapabilities` 扩展。** 新增 `elicitation: {form: {}, url: {}}`。空对象的兼容规则（`elicitation: {} ≡ {form: {}}`）在 `lifecycle.go` 里记录。
- **第一个非标准 JSON-RPC 错误码。** `-32042 URLElicitationRequired` 加入了原本只有四个标准码（`-32700/-32600/-32601/-32602/-32603`）的家族。回它必须带类型化的 `data.elicitations`；我们用 `URLElicitationRequiredError` 来包装。
- **第一次出现"带外通道"。** URL 模式引导是规范第一次承认：不是所有交互都能（或都应该）塞进 MCP 的 JSON-RPC 通道里。

## Try It

```bash
cd agents/s07-roots-elicitation
make test
make demo
```

`make test` 跑全部 5 个测试。`make demo` 加 `-v` 跑其中两个，能直接看到"接受"和"拒绝"两条端到端轨迹。

五个测试覆盖：

- `TestRootsListRoundTrip`：服务器发 `roots/list`，测试夹具回两个 `file://` URI，服务器解码成功且 pending 表清空。
- `TestFormElicitationAcceptReturnsContent`：服务器发 form 引导，客户端回 `accept` + `{name: "Ada"}`，服务器读到。
- `TestFormElicitationCancelDropsContent`：同样流程但回 `cancel`；解码器把 `Content` 抹掉，哪怕客户端硬塞进来。
- `TestURLElicitationParamsShape`：确认 URL params 携带 `mode: "url"`、`elicitationId`、`url`，且**没有** `requestedSchema`。
- `TestDeclinedElicitationPropagates`：完整的 `tools/call who-are-you` 往返，客户端回 `decline`，工具结果是普通 text block（不是 `isError`）。

想手工跑一条真实出站引导，需要一个会回应的 MCP 客户端。最短路径是依照测试文件里的 `pipeFramer` 模式自己写 20 行客户端。

## Upstream Source Reading

s07 对应一段连续的 `schema.ts` 切片，加上两份 prose 文档。

**Schema: [`schema.ts:2089-2506`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L2089-L2506)** —— Roots + Elicitation。

- **L2089-L2160** —— `ListRootsRequest` / `ListRootsResult` / `Root` / `RootsListChangedNotification`。`Root.uri` 的"MUST start with file://"约束由 `roots.go:validate` 强制。
- **L2161-L2189** —— `ElicitRequestFormParams`。`mode?: "form"` 可选是为了向后兼容；`requestedSchema` 必填且被限制为顶层 primitive。我们解码时没严格校验 primitive 结构；真正的客户端会。
- **L2191-L2229** —— `ElicitRequestURLParams`。`mode: "url"`（必填）、`message`、`elicitationId`、`url`。这里**没有** `requestedSchema`，这正是 URL 模式的全部意义。
- **L2230-L2476** —— `PrimitiveSchemaDefinition` 和四种 enum 变体（`UntitledSingleSelectEnumSchema` / `TitledSingleSelectEnumSchema` 加两个多选对应物）。s07 不实现 enum 表单；只暴露 string property 的喜路径。
- **L2476-L2506** —— `ElicitResult`。注意 `action: "accept" | "decline" | "cancel"`，`content` 可选且"只在 action=accept 且 mode=form 时存在"。

**URLElicitationRequired 错误: [`schema.ts:181-201`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L181-L201)** —— 实现保留段错误码 `-32042` 以及对应的类型化错误响应。

**Prose: [`client/roots.mdx`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/client/roots.mdx)** —— 能力声明、请求/响应 JSON 例、`notifications/roots/list_changed` 流程、安全规则（客户端必须校验 URI 防路径穿越）。

**Prose: [`client/elicitation.mdx`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/client/elicitation.mdx)** —— URL 模式为什么必须存在的长版叙述、`URLElicitationRequiredError` 的用法、以及绕开钓鱼攻击需要的"连接 URL 间接层"模式。

本地离线副本：[`upstream-readings/s07-roots-elicitation.ts`](../../upstream-readings/s07-roots-elicitation.ts)（`schema.ts` 2089-2506 行）。
