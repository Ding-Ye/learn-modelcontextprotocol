---
title: "s03 · tools/list 与 tools/call"
chapter: 03
slug: s03-tools
est_read_min: 12
---

# s03 · tools/list 与 tools/call

> 教什么：MCP 怎么把服务端函数暴露给 LLM 调用。本章给 server 加上 `tools` capability、注册两个 demo 工具、并引入**关键约定 `isError`**——哪些失败走 JSON-RPC error、哪些藏在结果里让 LLM 看到自己改正。

---

## Problem / 问题

s02 之后服务器握手完整：能协商 protocol version、能广告（暂时空的）capabilities 树、在 `notifications/initialized` 到达之前用 `-32002 SERVER_NOT_INITIALIZED` 拒绝一切非 `initialize` 方法。电话线接通了，但接通后只有拨号音——服务器一个真正"干活"的方法都没有。LLM 能跟它握手却发现不了任何工具、也调不了任何工具。

最小可用的 capability 就是 `tools`。Tools 是**模型可控**的函数：`tools/list` 列出所有工具（每个带一份 JSON-Schema 描述 arguments），`tools/call` 按名字调用、传 JSON 类型的 `arguments` 对象、返回类型化的 `ContentBlock[]`。两个设计决策很容易写错——返回值的形态（`ContentBlock` 是个**带 tag 的联合类型**，不是纯字符串）、以及执行错误放在哪儿（`CallToolResult.isError`，**不是** JSON-RPC error）。这两个决策的存在，是为了让 LLM 客户端不用为每个服务器写一条特殊路径就能渲染、并从工具调用失败中恢复。

## Solution / 解决方案

把 `tools/call` 想成一条三段式过滤器：

1. **协议层校验。** 工具有没有注册？`arguments` 是否符合它声明的 `inputSchema`？任一失败 → JSON-RPC error（未知名字 `-32601`、schema 失败 `-32602`），工具根本没跑。
2. **执行。** 调 handler。用 `defer recover` 包一层 panic 防护，buggy 工具不能把整个 loop 干掉。
3. **结果整形。** handler 返回的（字符串 / 结构体 / 图片字节）包成一个或多个 `ContentBlock`。panic 或返回 Go error 变成 `CallToolResult{IsError: true, Content: [text("...")]}`——**不是** JSON-RPC error。

三个关键决策点：

1. **`ContentBlock` 用"胖 struct + `Type` 区分"。** TypeScript 用 tagged union (`TextContent | ImageContent | AudioContent | ResourceLink | EmbeddedResource`)；Go 既没有 sum type、也不会做 type narrowing，所以把每个 variant 的字段全摊在一个 struct 上，靠变体构造函数（`TextBlock` / `ImageBlock`）保证创建时只有合法组合。
2. **Schema 在注册阶段编译，不是每次调用都编译。** `santhosh-tekuri/jsonschema/v5` 校验快、编译慢；一次性在 `Register` 编译完，`tools/call` 路径就只剩校验，且 schema 写错会在启动时立刻报错。
3. **`isError` 的分界是 load-bearing 的。** "未知工具"是**客户端**的错 → JSON-RPC error；"weather API 返回 500"是**服务端运行时**的状态，LLM 应该看到并重试 → `IsError: true` 放在结果里。分界点是"工具到底执行了没"——执行了，结果里带 IsError；没执行，协议层直接报错。

## How It Works / 工作原理

```text
┌─────────────────────────────────────────────────────────────┐
│ Message: {"method":"tools/call","params":{                  │
│            "name":"echo","arguments":{"text":"hi"}}}        │
│                                                             │
│   ┌──────────── Router.Dispatch ────────────┐               │
│   │ lifecycle state == Ready?               │ no → -32002   │
│   │ method registered?                      │ no → -32601   │
│   │                                         │               │
│   │ handleCallTool(params)                  │               │
│   │   1. lookup(name)                       │ miss → -32601 │
│   │   2. validator.Validate(arguments)      │ fail → -32602 │
│   │   3. safeCall(handler, arguments) ────┐ │               │
│   └───────────────────────────────────────┘ │               │
│                                             │               │
│                       ┌─── ok? ─────────────┘               │
│                       │                                     │
│                  ┌────▼─────┐         ┌───────────────┐     │
│                  │ Go err / │ ──yes── │CallToolResult │     │
│                  │ panic?   │         │ IsError: true │     │
│                  └────┬─────┘         └───────────────┘     │
│                       │ no                                  │
│                  ┌────▼────────────┐                        │
│                  │ CallToolResult  │                        │
│                  │ Content: [...]  │                        │
│                  └─────────────────┘                        │
└─────────────────────────────────────────────────────────────┘
```

核心 50 行（节选自 [`agents/s03-tools/tools.go`](../../agents/s03-tools/tools.go)）：

```go
func handleCallTool(reg *ToolRegistry, params json.RawMessage) (any, *Error) {
    var p CallToolRequestParams
    if err := json.Unmarshal(params, &p); err != nil {
        return nil, &Error{Code: InvalidParams, Message: "decode params: " + err.Error()}
    }
    entry := reg.Lookup(p.Name)
    if entry == nil {
        return nil, &Error{Code: MethodNotFound, Message: "unknown tool: " + p.Name}
    }
    if entry.validator != nil {
        var any any
        args := p.Arguments
        if len(args) == 0 { args = []byte("{}") }
        _ = json.Unmarshal(args, &any)
        if err := entry.validator.Validate(any); err != nil {
            return nil, &Error{Code: InvalidParams, Message: "arguments fail schema: " + err.Error()}
        }
    }
    // 按规范：handler 抛 panic 或返回 Go error → IsError:true，
    // **不**走 JSON-RPC error。这就是让 LLM 能自我纠错的关键。
    result, err := safeCall(entry.handler, p.Arguments)
    if err != nil {
        return CallToolResult{
            Content: []ContentBlock{TextBlock("tool error: " + err.Error())},
            IsError: true,
        }, nil
    }
    return result, nil
}

func safeCall(h ToolHandler, args json.RawMessage) (out CallToolResult, err error) {
    defer func() {
        if r := recover(); r != nil { err = fmt.Errorf("panic: %v", r) }
    }()
    return h.Call(args)
}
```

**四个不显然之处**：

1. **Schema 失败回 `-32602` 而不是 `-32600`。** `-32600 InvalidRequest` 是"信封"层面的错（`jsonrpc` 字段不对、缺 `id`）；`-32602 InvalidParams` 是信封合法但 `params` 违反方法契约。Schema 校验失败稳稳落在第二档。
2. **`panic` → `IsError: true`，**不是** `-32603`。** `safeCall` 把 panic 捕获并转成 Go `error`；`handleCallTool` 再把它塞进 text content block。LLM 看到 "tool error: panic: division by zero" 就知道该换参数试试。要是 panic 映射成 `-32603 InternalError`，LLM 只能看到"服务器坏了"、没法恢复。
3. **空 `arguments` 在校验前归一化为 `{}`。** Schema `{type: "object", required: ["text"]}` 要先确认 `arguments` 是个 object，`required` 才会跑；`null`（客户端省略字段时的形态）过不了 `type: object`，但错误信息会让人困惑。默认 `{}` 后报错就变成清晰的"missing property text"。
4. **`ToolRegistry.order` 在 `tools/list` 中保住注册顺序。** Go map 迭代是随机的——`tools/list` 的输出会在两次调用之间不稳定，断言"echo 和 get_time 都在"的测试可能漏掉 ListChanged 类型的 bug。slice 是顺序的真理来源、map 只是 O(1) lookup 索引。

## What Changed / 与 s02 的变化

```diff
 // ServerCapabilities — 本章只用得上这一个字段。
 type ServerCapabilities struct {
-    // s02：空结构
+    Tools *ToolsCapability `json:"tools,omitempty"`
 }

+type ToolsCapability struct {
+    ListChanged bool `json:"listChanged"`
+}
+
+type Tool struct { Name, Description string; InputSchema, OutputSchema json.RawMessage }
+
+type ContentBlock struct {
+    Type     string          `json:"type"`
+    Text     string          `json:"text,omitempty"`
+    Data, MIMEType, URI string `json:"...,omitempty"`
+    Resource json.RawMessage `json:"resource,omitempty"`
+}
+type CallToolResult struct {
+    Content []ContentBlock `json:"content"`
+    IsError bool           `json:"isError,omitempty"`
+}

 func NewRouter(lc *Lifecycle, reg *ToolRegistry) *Router {
     r.requestHandlers["initialize"] = r.handleInitialize
+    r.requestHandlers["tools/list"]  = func(p json.RawMessage) (any, *Error) { return handleListTools(reg, p) }
+    r.requestHandlers["tools/call"]  = func(p json.RawMessage) (any, *Error) { return handleCallTool(reg, p) }
 }
```

语义层面：服务器不再是个空壳。s02 只能回声它自己的 capabilities；s03 多了一张**注册表**（`map[string]ToolHandler`，按 name 索引），把请求分派进去。更大的结构变化是心智模型从"每个方法是一个 router entry"变成"有些方法是分派表、里面再嵌一层注册表"。这个 pattern 在 resources (s04)、prompts (s05)、sampling (s06) 都会重现。

## Try It / 动手试一试

```bash
cd agents/s03-tools

# 跑 6 个测试（要求的 5 个 + 1 个 lifecycle 守卫）
go test -v ./...

# 用 4 条消息驱动：initialize → initialized → tools/list → tools/call echo
make demo
```

`make demo` 预期输出（3 行；第二条是 notification 无回复，所以是 3 行不是 4 行）：

```
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"learn-mcp-s03-tools","version":"0.1.0"},"instructions":"..."}}
{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"echo","description":"...","inputSchema":{...}},{"name":"get_time","description":"...","inputSchema":{...}}]}}
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"hi"}]}}
```

试一个故意错的调用看 `-32602` 路径：

```bash
printf '%s\n%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"d","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{}}}' \
  | go run .
```

第三条回应会是 `"error":{"code":-32602,"message":"arguments fail schema: ..."}`——schema 校验在工具执行**之前**跑，所以这是协议层拒绝。

## Upstream Source Reading / 上游源码阅读

上游规范两段、都不长。

```upstream:schema/2025-11-25/schema.ts#L1082-L1299
// Source: schema/2025-11-25/schema.ts:1082-1299

export interface CallToolResult extends Result {
  content: ContentBlock[];
  structuredContent?: { [key: string]: unknown };
  // 工具自身抛出的错误 SHOULD 用 isError=true 表达，
  // **不要**走 MCP 协议错误响应——否则 LLM 看不到错误、
  // 也没法自我纠正。
  // 但是"找不到这个工具"或"服务器根本不支持工具调用"这类
  // 异常 MUST 走 MCP error 响应。
  isError?: boolean;
}

export interface Tool extends BaseMetadata, Icons {
  description?: string;
  inputSchema: {
    type: "object";
    properties?: { [key: string]: object };
    required?: string[];
  };
  outputSchema?: { ...同上形态... };
  execution?: ToolExecution;        // taskSupport: forbidden|optional|required
  annotations?: ToolAnnotations;    // readOnly/destructive/idempotent 提示
}
```

```upstream:schema/2025-11-25/schema.ts#L1742-L1839
// Source: schema/2025-11-25/schema.ts:1742-1839

export type ContentBlock =
  | TextContent       // {type:"text", text:string}
  | ImageContent      // {type:"image", data:base64, mimeType:string}
  | AudioContent      // {type:"audio", data:base64, mimeType:string}
  | ResourceLink      // {type:"resource_link", uri:string, ...}
  | EmbeddedResource; // {type:"resource", resource:{...}}
```

**对照阅读要点**：

- **`isError` 是整段里唯一一个 JSDoc 写了一整段的字段**——这是规范作者在显式强调设计意图，配套散文见 [`server/tools.mdx:132-148`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/server/tools.mdx#L132-L148)。写错这块、LLM 就失去了恢复能力。
- **`Tool extends BaseMetadata, Icons` 用了 TypeScript 的 mixin。** Go 没有 mixin——两个嵌入类型字段名重叠会产生 ambiguous-selector 编译错。s03 只把要用的 (`Name` / `Description` / `InputSchema`) 内联进来；完整实现还要内联 `Title` / `Icons` / `_meta`。
- **上游 `Tool.inputSchema.type` 字面值是 `"object"`。** TS 类型系统在编译期保证；我们不专门校验，因为 jsonschema 编译器遇到非 object 的根类型会在 `Register` 阶段直接拒绝。
- **`execution.taskSupport` 是 SEP-1686（tasks）的钩子。** s03 全部忽略——每个工具同步执行。真实有长任务的服务器会设 `taskSupport: "optional"`、让客户端通过 `tasks/get` 轮询。
- **Annotations (`readOnlyHint` / `destructiveHint`) 故意标注为**不可信**的提示。** 规范明说"not guaranteed to provide a faithful description of tool behavior"，客户端**不**能基于它做信任决策。s03 跳过这块、把重点放在 wire shape。

```upstream:docs/specification/2025-11-25/server/tools.mdx#L36-L186
// Source: docs/specification/2025-11-25/server/tools.mdx:36-186 (节选)

支持 tools 的服务器 MUST 声明 `tools` capability：
  { "capabilities": { "tools": { "listChanged": true } } }

`listChanged` 表明工具列表变化时服务器会不会推送通知。

[tools/list 请求 / 响应示例]
[tools/call 请求 / 响应示例]
[mermaid 时序图：LLM ↔ Client ↔ Server，tools/list → 选工具 →
 tools/call → 结果；可选 list_changed 推送]
```

完整散文：[`server/tools.mdx:36-186`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/server/tools.mdx#L36-L186)。

本地离线副本（两段 schema 切片合并）：[`upstream-readings/s03-tools.ts`](../../upstream-readings/s03-tools.ts)。

**想读更多**：从 `schema.ts:1082` 的 `/* Tools */` 标题开始、跟着 1742 行的 `ContentBlock` 类型别名、再翻到 1840 行往下的 `ToolUseContent` / `ToolResultContent`（s06 sampling 的 agentic loop 再用一次）。这条线就是 s03 → s05 (prompts 也复用 ContentBlock) → s06 (tool_use/tool_result blocks) 的真实源码地图。

---

**下一节预告**：s04 接手**文件类型**的服务端能力——resources——并解释为什么 `resources/read.contents[]` 是个数组（一个 URI 模板可以展开成多个文件）。
