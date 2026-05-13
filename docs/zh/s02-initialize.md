---
title: "s02 · 初始化握手与能力协商"
chapter: 02
slug: s02-initialize
est_read_min: 10
---

# s02 · 初始化握手与能力协商

> 本章要点：MCP 是如何把"两个进程互发 JSON-RPC"升级为"两个进程对各自能做什么有类型化共识"的——通过严格的三步握手（请求 → 结果 → 通知）和一份类型化的 capabilities 对象。

---

## Problem

s01 给所有 `initialize` 请求都回同一个写死的 blob：能跑通线上形状，但完全绕开了真正的协议契约。MCP 要求服务器**读取客户端宣告的能力、协商 `protocolVersion`、回复自己的能力声明，然后在客户端没说"我准备好了"之前拒绝做任何事**（`basic/lifecycle.mdx:36-205`）。

最后这条特别坑：常见的 JSON-RPC 服务器都是无状态的，MCP 不是。在客户端发出 `notifications/initialized` 之前，服务器必须用一个特定的错误码拒绝所有非握手/非 ping 方法。如果这里搞错，一个遵守规范的 MCP 客户端会在还没碰到有意思的功能之前就认为我们的服务器有问题。

## Solution

把服务器建模成一个四状态机（`new → initializing → operating → closed`），坐在一个通用 `Router` 后面。Router 把 `method` 字符串映射到 `Handler` 函数；生命周期处理器（`initialize`、`notifications/initialized`、`ping`）挂在一个 `Server` struct 上，它的 `handleRequest` fallback 负责把闸门管好。Capabilities——客户端和服务器两边——都是类型化 Go struct，每个可选子能力都是 pointer-to-struct，所以 `nil == "未宣告"`，`&struct{}{} == "宣告了但没附带任何选项"`。就这一个表示选择，让 Go 的线上形状跟 `schema.ts:308-459` 对齐，而不需要给每个字段写分支。

## How It Works

```text
┌──────────────────────────────────────────────────────────────┐
│ stateNew ─── initialize 请求 ──► handleInitialize            │
│                                       │                       │
│                                       ▼                       │
│                                stateInitializing              │
│                                       │                       │
│           notifications/initialized   │                       │
│                                       ▼                       │
│                                 stateOperating                │
│                                       │                       │
│                          tools/list, resources/read, ...      │
│                                       ▼                       │
│                                 （s03..s07 在此接管）          │
│                                                               │
│ 任何非 ping 的方法在 != operating 状态都会得到 -32002         │
│ Ping 在任何状态都允许。                                       │
└──────────────────────────────────────────────────────────────┘
```

生命周期闸门、版本协商规则和 dispatcher，大约 60 行（节选自 `agents/s02-initialize/lifecycle.go` 与 `router.go`）：

```go
func (s *Server) handleInitialize(_ context.Context, _ string, params json.RawMessage) (any, *Error) {
    var p InitializeRequestParams
    if err := json.Unmarshal(params, &p); err != nil {
        return nil, &Error{Code: InvalidParams, Message: "invalid initialize params: " + err.Error()}
    }
    s.mu.Lock(); defer s.mu.Unlock()
    if s.state != stateNew {
        return nil, &Error{Code: InvalidRequest, Message: "initialize already received"}
    }
    chosen := p.ProtocolVersion
    if !s.supports(p.ProtocolVersion) {
        chosen = s.ProtocolVersion // 服务器首选版本；是否断开由客户端决定。
    }
    s.state = stateInitializing
    return InitializeResult{
        ProtocolVersion: chosen,
        Capabilities:    s.Capabilities,
        ServerInfo:      s.Info,
        Instructions:    s.Instructions,
    }, nil
}

func (s *Server) handleRequest(ctx context.Context, method string, params json.RawMessage) (any, *Error) {
    switch method {
    case "initialize":              return s.handleInitialize(ctx, method, params)
    case "notifications/initialized": return s.handleInitializedNotification(ctx, method, params)
    case "ping":                    return struct{}{}, nil
    }
    s.mu.Lock(); state := s.state; s.mu.Unlock()
    if state != stateOperating {
        return nil, &Error{Code: ServerNotInitialized,
            Message: "server not initialized: method " + method + " rejected in state " + state.String()}
    }
    return nil, &Error{Code: MethodNotFound, Message: "method not found: " + method}
}
```

四个不显然的点：

1. **版本不匹配 *不是* 错误。** 规范（`basic/lifecycle.mdx:170-175`）明文规定：客户端要的版本服务器不支持时，服务器**必须**仍然回响——回自己的首选版本。是否断开是*客户端*的事。我们把这条编码成 `handleInitialize` 里悄悄替换的一行，而不是返回 `-32602`。`TestWrongProtocolVersionEchoesServerPreferred` 测试钉死了这个行为。
2. **`notifications/initialized` 返回 `(nil, nil)`。** 通知没有 `id`，所以永远不会产生回复。Router 先用 `IsNotification()` 判断，丢掉 handler 的返回值。状态切换是这个 handler 唯一可见的副作用。
3. **`-32002` "server not initialized" 不是 JSON-RPC 标准码。** 基础规范没定义；MCP TS SDK 和大多数服务器约定俗成用 `-32002` 表示"握手前闸门"。我们把它叫 `ServerNotInitialized`，紧挨着标准 `-32600..-32700` 那一块，让它属于不同层的事实显而易见。
4. **Pointer-to-struct 的子能力序列化非常干净。** `ServerCapabilities.Tools` 是 `*ToolsCapability`。nil → 借 `omitempty` 整个字段省掉。非 nil 但空结构 → 线上是 `"tools": {}`。同一个值表达三种行为。TS schema 那种"到处都是可选空对象"的写法直接对应到 Go，不需要写自定义 `MarshalJSON`。

## What Changed (vs. s01)

s01 只有一个文件（`envelope.go`）外加 `main.go`。s02 加了两个新文件，envelope 原样再写一遍，`main.go` 长成了 Router 装配步骤：

```diff
 agents/s02-initialize/
+ envelope.go     （照抄自 s01——同一份类型，不 import）
+ lifecycle.go    （新增：Implementation、ClientCapabilities、
+                       ServerCapabilities、InitializeRequest/Result、
+                       Server、serverState、handleInitialize、
+                       handleInitializedNotification、handleRequest）
+ router.go       （新增：Handler、Router、Process）
+ main.go         （现在：buildServer + buildRouter + run loop）
+ lifecycle_test.go（新增：6 个测试）
- （s01 那个写死的 `initializeResult` map[string]any）
```

最大的概念变化是引入了**状态**：s01 的服务器是从请求到响应的纯函数；s02 的 `Server` 携带生命周期。后续每一章只是扩 operating-state 那张 handler 表，不会动到生命周期闸门。

## Try It

```bash
cd agents/s02-initialize
make demo
```

预期输出：三行 JSON。第一行是 `InitializeResult`，第二行什么也没有（通知不回复），第三行是 `ping` 的空结果 `{}`。

```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"logging":{},"completions":{},"prompts":{},"resources":{},"tools":{}},"serverInfo":{"name":"learn-mcp-s02-initialize","version":"0.2.0"},"instructions":"s02: handshake + capability negotiation. Use `notifications/initialized` to unlock the rest."}}
{"jsonrpc":"2.0","id":2,"result":{}}
```

不发通知就直接调 `tools/list`，会触发预握手闸门：

```bash
{ printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}'; \
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'; } | ./s02-initialize
```

第二行回复会带上 `"error":{"code":-32002,"message":"server not initialized: method tools/list rejected in state initializing"}`。

跑全部测试：

```bash
make test
```

## Upstream Source Reading

把 [`schema.ts:251-459`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L251-L459) 跟 `agents/s02-initialize/lifecycle.go` 并排看。关键地标：

- **L251-L274** —— `InitializeRequestParams` 和 `InitializeRequest`。`protocolVersion`、`capabilities`、`clientInfo` 都是必填。
- **L276-L295** —— `InitializeResult`。注意注释：「If the client cannot support this version, it MUST disconnect」——断开是*客户端*的责任，不是服务器的。
- **L297-L305** —— `InitializedNotification`。无 params；方法名本身就是信号。
- **L308-L381** —— `ClientCapabilities`。我们的 Go struct 对应 `roots`、`sampling`、`elicitation`，再加上开放的 `experimental` map。`tasks` 留给附录 B 当扩展练习。
- **L388-L459** —— `ServerCapabilities`。同样的形状镜像。`logging` 和 `completions` 是 flag-only（空对象）；`prompts`/`resources`/`tools` 带 `listChanged` 和（resources 额外）`subscribe` 布尔。

然后看 [`basic/lifecycle.mdx:36-205`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/basic/lifecycle.mdx#L36-L205) 的规范散文。三段话承担了大部分信息量：

- 三步握手：`initialize` 请求 → `InitializeResult` → `notifications/initialized`。
- 版本协商：不匹配时服务器仍**必须**回响，给出自己首选版本。
- 预握手闸门：通知到来之前只允许 `ping`（以及服务器侧的 logging）。

schema 切片的本地离线副本：[`upstream-readings/s02-initialize.ts`](../../upstream-readings/s02-initialize.ts)。
