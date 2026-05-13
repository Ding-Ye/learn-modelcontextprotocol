---
title: "s01 · 最小回路：JSON-RPC 与 stdio 帧"
chapter: 01
slug: s01-min-loop
est_read_min: 8
---

# s01 · 最小回路：JSON-RPC 与 stdio 帧

> 本章要点：能跑起来的最小 MCP 服务器——一个 JSON-RPC 信封、一个 `initialize` 回复、一条 stdio 行帧规则。后续每一章都是这个回路的"加大版"。

---

## Problem

MCP 是协议，不是库。在能谈"协商能力 / 列工具 / 反向请求 LLM"之前，得先确认一条线上消息长什么样。规范（`schema/2025-11-25/schema.ts:1-180`）定义了标准 JSON-RPC 2.0 信封，但加了一处微妙的限制——请求 `id` **绝不能为 `null`**，而标准 JSON-RPC 是允许的。在它之上，stdio 传输（`basic/transports.mdx:1-51`）又叠了一层帧规则：每条 JSON 单独占一行，行内不许有换行。

任何一处弄错，后续每一章都是建在沙子上。本章要解决的痛点：写一个能跑的程序，解析 `initialize` 请求、返回合法的 `InitializeResult`，对其他一切方法拒绝。一旦它能立起来，我们就知道信封写对了。

## Solution

把 MCP 服务器想象成一个微型事件循环，分三层竖直叠在一起：**framer** 管 stdio 字节流的行分割，**envelope decoder** 把一行变成类型化的 `Message`，**handler** 根据 `method` 分派。s01 的 handler 只有一条 `"initialize"` 分支 + 兜底 `-32601 MethodNotFound`。后续每章只扩 handler，从不动 framer。

三条关键设计决定：

1. **`Message` 一个 struct 覆盖四种线上形态。** 请求 / 通知 / 成功响应 / 错误响应共享同一个 JSON 骨架；用 pointer + `omitempty` 而非"七种独立 struct"，再用辅助方法（`IsRequest` / `IsNotification` / `IsResponse`）做形态判定。
2. **`null` id 在解码阶段就拒绝。** 自定义 `UnmarshalJSON` 挂在 `Message` 上（而不是 `ID` 上——Go `encoding/json` 看到 `null` 会把指针置 nil，根本不会调指针指向类型的 `UnmarshalJSON`）。
3. **framer 在写入时阻止嵌入换行。** `json.Marshal` 已经把字符串里的 `\n` 转义掉；这里再加一道运行时校验是防御性编程，把"行内换行"视为程序员错误。

## How It Works

```text
┌──────────────────────────────────────────────────────┐
│ stdin (字节流)                                       │
│   │                                                  │
│   ▼                                                  │
│ ┌──────────────┐    一行                             │
│ │ stdioFramer  │─────────────► json.Unmarshal ──┐    │
│ └──────────────┘                                ▼    │
│                                          ┌──────────┐│
│                                          │ Message  ││
│                                          └──────────┘│
│                                                ▼     │
│   ┌────────── handle(msg) ──────────┐                │
│   │ method == "initialize" ─► result│                │
│   │ default                ─► -32601│                │
│   └────────────────────────────────-┘                │
│                                                ▼     │
│                            json.Marshal ──► stdout   │
└──────────────────────────────────────────────────────┘
```

核心 40 行（节选自 `agents/s01-min-loop/envelope.go`）：

```go
type Message struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      *ID             `json:"id,omitempty"`
    Method  string          `json:"method,omitempty"`
    Params  json.RawMessage `json:"params,omitempty"`
    Result  json.RawMessage `json:"result,omitempty"`
    Error   *Error          `json:"error,omitempty"`
}

func (m *Message) UnmarshalJSON(data []byte) error {
    type rawMessage Message
    var probe struct{ ID json.RawMessage `json:"id"` }
    if err := json.Unmarshal(data, &probe); err == nil {
        if len(probe.ID) > 0 && bytes.Equal(bytes.TrimSpace(probe.ID), []byte("null")) {
            return errors.New("mcp: request id must not be null")
        }
    }
    return json.Unmarshal(data, (*rawMessage)(m))
}

func (f *stdioFramer) Write(m Message) error {
    if m.JSONRPC == "" { m.JSONRPC = JSONRPCVersion }
    raw, err := json.Marshal(m)
    if err != nil { return err }
    if bytes.ContainsAny(raw, "\r\n") {
        return errors.New("mcp: serialized frame contains forbidden newline")
    }
    raw = append(raw, '\n')
    _, err = f.out.Write(raw)
    return err
}
```

四个不显然的点：

1. **指针 `*ID` + 在 `Message` 上挂 `UnmarshalJSON`。** Go `encoding/json` 看到 `"id": null` 会直接把 `*ID` 置 nil，不会调用指针指向类型的 `UnmarshalJSON`。`Message.UnmarshalJSON` 里的 probe 先把 raw `id` 字段抠出来检查 null，再用 type alias 走一遍标准解码（避免递归）。
2. **没有 `Result.Marshal` / `Error.Marshal` 拆分。** 一条响应要么有 `Result`、要么有 `Error`，但我们没拆成两种独立类型。线上多 4 个字节（`omitempty` 占位字段），教学成本为 0。
3. **`stdioFramer.Read` 用 `bufio.ReadBytes('\n')`，没用 `bufio.Scanner`。** 后者默认 64KB 单行上限；真实 MCP 载荷（资源内容、base64 blob）轻松超出。
4. **`json.RawMessage` 装 `params` / `result`。** 信封层从不解码内层；每个 session 自己在 dispatch 后决定怎么解析 `m.Params`。这正是后续每章都能在不动这个文件的情况下新增方法的关键。

## What Changed (vs. （无）)

引导章，前面没有 sNN。对比的基准是**上游规范**：我们写了约 150 行 Go（信封 + framer），对应上游 `schema.ts:1-180`（信封类型）和 `basic/transports.mdx:1-51`（帧规则）。

## Try It

```bash
cd agents/s01-min-loop
make demo
```

预期输出（单行）：

```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{},"serverInfo":{"name":"learn-mcp-s01-min-loop","version":"0.1.0"},"instructions":"This is s01: a single-shot initialize echo. Try anything else and you'll get -32601."}}
```

试一个未实现的方法：

```bash
echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' | ./s01-min-loop
```

会看到 `"error":{"code":-32601,"message":"method not found: tools/list"}`。

再试一个 null id：

```bash
echo '{"jsonrpc":"2.0","id":null,"method":"initialize"}' | ./s01-min-loop
```

会看到一个 parse-error 响应（framer 解码就失败，handler 根本没跑）。

## Upstream Source Reading

上游 MCP 规范用 TypeScript 写。建议把 [`schema.ts:1-180`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L1-L180) 跟 `agents/s01-min-loop/envelope.go` 并排看，重点：

- **L1-L16** —— `JSONRPCMessage` 联合类型 + `JSONRPC_VERSION = "2.0"` 常量。
- **L17-L73** —— `Request`、`Notification`、`Result`、`Error` 形状。
- **L100-L130** —— `RequestId = string | number`。TypeScript 联合类型已经排除了 `null`，我们用运行时校验把这条规则落到 Go。
- **L140-L156** —— `JSONRPCError`（注意可选 `data` 字段——我们用 `json.RawMessage` 透传）。
- **L175-L180** —— 标准错误码常量。我们硬编码同名常量。

然后看 [`basic/transports.mdx:1-51`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/docs/specification/2025-11-25/basic/transports.mdx#L1-L51) 学帧规则。有两段是关键：消息**不**含嵌入换行；消息必须 UTF-8。

本地离线副本：[`upstream-readings/s01-min-loop.ts`](../../upstream-readings/s01-min-loop.ts)（schema 切片）+ [`upstream-readings/s01-min-loop.mdx`](../../upstream-readings/s01-min-loop.mdx)（transports 散文段）。
