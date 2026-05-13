---
title: "s05 · 提示模板与参数补全"
chapter: 05
slug: s05-prompts
est_read_min: 12
---

# s05 · 提示模板与参数补全

> 本章要点：第三类"原语受众"——**提示是用户驱动的**，而不是模型驱动（tools）或应用驱动（resources）。线上接口很小（`prompts/list`、`prompts/get`、`completion/complete`），但"模板替换 + 参数补全"这对组合，是每一个 MCP host 里 slash-command 体验的地基。

---

## Problem

Tools 回答的是"模型能调什么"；Resources 回答的是"应用能挂什么上下文"；这两者都没回答"**用户**能从 slash 菜单触发哪个预制模板"。s05 实现的就是第三类原语——**prompts**。

线上接口（`schema.ts:923-1081`）刻意做得很小：列出可用提示、取一条已替换好参数的提示、再单独（`schema.ts:2006-2088`）在用户输入参数时给出候选。规范把这两端拆开，是为了让编辑器把 `prompts/list` 接到 slash 菜单、把 `completion/complete` 接到弹窗，两条 UI 路径互不耦合。

本章要解决的痛点：写一个服务器，暴露一个提示（`summarize-file`），在请求时校验必填参数、替换 `{{argument}}` 占位符，并在 `completion/complete` 里基于一个小词典做前缀匹配返回候选。后续一切（真实补全后端、多轮提示）都是在这个骨架上扩展。

## Solution

一条提示 = **命名的模板 + 参数声明**。我们用一个 `Prompt` 值（name + description + `[]PromptArgument`）加上一个 `PromptHandler` 接口建模——接口的职责是"给定 `map[string]string` 参数，渲染出模板"。`PromptRegistry` 按注册顺序持有 (name → handler) 表。

四条关键设计决定：

1. **`PromptHandler` 是接口，不是函数。** 真实 handler 会带状态（文件 watcher、DB 句柄、LLM 客户端）。s05 里用 `func` 签名也够用，但 s06+ 一定要重构；一上来就用接口零成本，也更贴近真实 SDK 的形状。
2. **替换就是 `strings.ReplaceAll("{{key}}", value)`。** 不做表达式求值，没有转义规则。规范对模板语法只字未提——各家 SDK 自己定。我们选最贴近 spec MDX 示例（`server/prompts.mdx:101-138`）那种简单写法。
3. **`completion/complete` 共用 registry，但单独成文件。** 这个端点结构上不同：有 `ref` 这个判别联合字段、不需要渲染任何东西、`values` 上限是 100。拆文件后联合分发的代码更容易读。
4. **必填校验**在替换**之前**跑。如果先替换，未填的必填参数会悄悄变成 `{{path}}` 留在渲染结果里，用户根本不会发现。我们直接返回 `-32602 InvalidParams`，错误消息里点名缺失的参数。

## How It Works

```text
┌────────────────────────────────────────────────────────────────┐
│ stdin                                                          │
│   │                                                            │
│   ▼                                                            │
│ stdioFramer ─► Message ─► Router.Dispatch                      │
│                              │                                 │
│   ┌──────────────────────────┼──────────────────────────────┐  │
│   │                          │                              │  │
│   ▼                          ▼                              ▼  │
│ initialize          prompts/list                  completion/  │
│ (lifecycle)         prompts/get ──► registry      complete     │
│                         │            │ Lookup        │         │
│                         ▼            ▼               ▼         │
│                  PromptHandler.Get  validateArgs   词典前缀    │
│                         │           substitute     匹配        │
│                         ▼                          ─► values[] │
│                   GetPromptResult                              │
│                         │                                      │
│                         ▼                                      │
│                       stdout                                   │
└────────────────────────────────────────────────────────────────┘
```

替换 + 校验核心（节选自 `agents/s05-prompts/prompts.go`）：

```go
func validateArgs(decl []PromptArgument, args map[string]string) *Error {
    for _, a := range decl {
        if !a.Required { continue }
        if v, ok := args[a.Name]; !ok || v == "" {
            return &Error{
                Code:    InvalidParams,
                Message: fmt.Sprintf("missing required argument %q", a.Name),
            }
        }
    }
    return nil
}

func substitute(tmpl string, args map[string]string) string {
    out := tmpl
    for k, v := range args {
        out = strings.ReplaceAll(out, "{{"+k+"}}", v)
    }
    return out
}
```

补全联合分发（`completion.go`）：

```go
switch req.Ref.Type {
case "ref/prompt":
    return completeForPrompt(r, req)
case "ref/resource":
    return CompleteResult{Completion: CompletionPayload{Values: []string{}}}, nil
case "":
    return nil, &Error{Code: InvalidParams, Message: "ref.type is required"}
default:
    return nil, &Error{Code: InvalidParams, Message: "unknown ref.type: " + req.Ref.Type}
}
```

四个不显然的点：

1. **线上 `Arguments` 是 `map[string]string`。** 即便"语义上"是数字参数，JSON-RPC 也用带引号的字符串传。替换层只认字符串，我们不做类型强转。（`schema.ts:954-956`。）
2. **`ref/resource` 出现在 s05 的补全代码里，尽管资源模板是 s04 的事。** 规范把两种 ref 塞到同一个 RPC，因为补全的 *UI 体验* 是一样的——用户多打一个字符。我们接受 `ref/resource` 并返回空 `values[]`，这样客户端两端探测时行为是一致的。
3. **`values` 上限 100。** 来自 `schema.ts:2051-2053`。我们用 trim + `hasMore=true` 实现；demo 词典很小、永远不触发，但代码留着供阅读。
4. **`PromptHandler.Get` 返回 `(GetPromptResult, error)`，不是 `*Error`。** 路由把任意 `error` 翻成 `-32603 InternalError`——**除非** handler 直接返回 `*Error`，这时码原样保留。这让"缺必填参数"是 `-32602`，意外失败也能以可读形态露出来。

## What Changed (vs. s04)

s04 引入了资源——文件式、应用驱动的上下文，配 list/read/templates/subscribe 四件套。s05 沿用同一套 router 骨架，把暴露面整个换掉：

- **新声明能力：** `prompts.listChanged` + `completions`（后者没有子字段，就是 `{}`）。见 `lifecycle.go:39-44`。
- **`ContentBlock` 再写一遍。** 这是第三次写这个 struct 了（s03 第一次、s04 用 `resource_link` 是第二次、现在 s05 是第三次）。重复就是教学法——到 s06 你会写到第五次，那时你不会再觉得自己很聪明。
- **没有 I/O。** demo 提示故意做成无状态：它不读 `path` 处的文件，只是把路径塞到一条 user 消息里，让 LLM 去总结。资源做"文件系统这类活"；提示只组字符串。
- **第一次出现"判别联合"的 RPC。** `completion/complete` 的 `ref` 字段是我们第一次在 *RPC 参数层* 切 `type` 判别字段。s06 在 `ContentBlock` 的 `tool_use` / `tool_result` 变体上会再来一次；这一章是热身。

## Try It

```bash
cd agents/s05-prompts
make demo
```

会看到五行 JSON。挑两条看：

```json
{"jsonrpc":"2.0","id":2,"result":{"prompts":[{"name":"summarize-file","description":"Ask the LLM to summarize the file at the given path.","arguments":[{"name":"path","description":"Path to the file to summarize.","required":true},{"name":"style","description":"Output style: bullets|prose|tldr (default: prose)."}]}]}}
```

```json
{"jsonrpc":"2.0","id":3,"result":{"description":"summarize the file at README.md","messages":[{"role":"user","content":{"type":"text","text":"Please summarize the file at `README.md` in bullets form. Focus on what the code or document is *for*, not a line-by-line restatement."}}]}}
```

```json
{"jsonrpc":"2.0","id":4,"result":{"completion":{"values":["README.en.md","README.md"],"total":2}}}
```

试试缺必填参数：

```bash
echo '{"jsonrpc":"2.0","id":9,"method":"prompts/get","params":{"name":"summarize-file","arguments":{}}}' | ./s05-prompts
```

（注意：要先发 `initialize` + `notifications/initialized`，否则 lifecycle 闸门会回 `-32600`。）会看到 `"error":{"code":-32602,"message":"missing required argument \"path\""}`。

再试一个未知 ref.type：

```bash
echo '{"jsonrpc":"2.0","id":10,"method":"completion/complete","params":{"ref":{"type":"ref/banana","name":"summarize-file"},"argument":{"name":"path","value":""}}}' | ./s05-prompts
```

`"error":{"code":-32602,"message":"completion/complete: unknown ref.type: ref/banana"}`。

## Upstream Source Reading

本章对应的两段上游源码都在 `schema/2025-11-25/schema.ts`。建议把它跟 `agents/s05-prompts/{prompts,completion}.go` 并排看。

**Prompts: [`schema.ts:923-1081`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L923-L1081)** —— list / get / message / list-changed 通知。

- **L929-L940** —— `ListPromptsRequest` / `ListPromptsResult`。注意 `PaginatedRequest`/`PaginatedResult`：每个 server 端 list 接口都继承游标约定。s05 不实现分页，因为 demo 只有一条提示。
- **L947-L966** —— `GetPromptRequestParams` / `GetPromptRequest`。关键是这行：`arguments?: { [key: string]: string }`——string 键、string 值，整个字段还是可选。
- **L973-L979** —— `GetPromptResult`。`messages` 用复数即便 demo 只回一条；这是故意的，多轮模板可以承载一整段对话。
- **L986-L1001** —— `Prompt`。注意它 extends `BaseMetadata, Icons`——这两块我们丢了。Go 重写的好处之一是逼你 *选择* 暴露哪些字段。
- **L1008-L1017** —— `PromptArgument`。`required?: boolean`——handler 唯一能不重述 schema 就依赖的校验规则。
- **L1034-L1037** —— `PromptMessage`。Role + ContentBlock。同一个 role 枚举到 s06 的 sampling 还会复用。
- **L1077-L1080** —— `PromptListChangedNotification`。我们声明了 `listChanged: true` 但 demo 从不 mutate；真实服务器在改动 registry 时应当发这条通知。

**Completion: [`schema.ts:2006-2088`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L2006-L2088)** —— 规范里唯一的"参数补全"RPC。

- **L2006-L2031** —— `CompleteRequestParams`。`ref` 字段就是我们 switch 的判别联合。`context.arguments` 是"URI 模板或提示里已解析的变量"——我们接受但不用。
- **L2048-L2063** —— `CompleteResult`。注意 **values 上限 100**，以及可选 `total`/`hasMore` 字段。线上形状像分页列表，但游标分页并没在这里定义——`hasMore: true` 是单 bit 提示。
- **L2070-L2087** —— 两种 `ref` 变体。`ResourceTemplateReference` 带 `uri`，`PromptReference` extends `BaseMetadata`（`name` 来自这里）。

**散文: [`server/prompts.mdx:28-174`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/server/prompts.mdx#L28-L174)** —— 规范把能力声明、列出、获取、list-changed 通知用一连串 JSON 例子走完。建议先通读一遍这段，再去啃 TypeScript。

本地离线副本：[`upstream-readings/s05-prompts.ts`](../../upstream-readings/s05-prompts.ts)（923-1081 + 2006-2088 拼接而成）。
