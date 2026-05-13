---
title: "s08 · Streamable HTTP 传输"
chapter: 08
slug: s08-streamable-http
est_read_min: 18
---

# s08 · Streamable HTTP 传输

> 本章教你什么：MCP 如何跑在 HTTP 之上。一个端点、两种方法（POST 与 GET），加上 Server-Sent Events，以便恢复 s01-s07 中 stdio 免费提供的"双向消息流"特性。会涉及会话、`Mcp-Session-Id`、`MCP-Protocol-Version`、`Last-Event-ID` 回放等。JSON-RPC 信封内部的所有内容都没变——这正是关键所在。

---

## 问题

s01-s07 都跑在 stdio：一个进程、一条双向字节流、换行分隔的 JSON。很优雅。现在要把它发布到网络上。

HTTP 是请求-响应模型。客户端不发起第二次请求，就不会有第二条消息。但 MCP 需要**服务端**主动推送通知、向客户端反向发起 `sampling/createMessage` 请求、在长耗时工具调用中流式返回进度。`POST → response → done` 根本装不下这些。

Streamable HTTP 传输（basic/transports.mdx:52-330）就是答案。它把每条 客户端→服务端 消息都装在 POST 里，但允许服务端在响应时刻在"一次性 JSON 主体"和"打开的 SSE 流"之间做选择。另外有一条独立的 GET 端点，让服务端主动**未请求即推送**消息给客户端。会话连续性靠头部 `Mcp-Session-Id` 维持，断线续传靠 SSE 事件 ID（`Last-Event-ID`）恢复。

本章要解决的痛点：写一个真正的 HTTP 服务器，把上述全部做到——会话、头部、当工具需要 sampling 时升级到 SSE、GET 流上的通知、重连时的回放——而且不用任何第三方库。`net/http` 已经够了。

## 解决方案

`/mcp` 一个 handler，按方法分发：

- **POST** 是每一条 客户端→服务端 消息。请求体是一条 JSON-RPC 信封。响应**要么**是 `application/json`（一次性）**要么**是 `text/event-stream`（SSE），服务端根据"该 handler 是否需要回环到客户端"来选。
- **GET** 打开一条长生命周期的 SSE 流，用于**未请求**的 服务端→客户端 流量（通知、定期进度、所有不绑定到具体请求的消息）。
- **DELETE** 终止一个会话。

关键设计决策：

1. **`HTTPFramer` 是每请求级的。** s01-s07 的 `Framer` 是永远流式拉消息的。HTTP 不是这样：每个请求一进一出。我们保留 `Framer` 这个**名字**以保持形态一致，但 `Read()` 在第一次调用之后就返回 `io.EOF`。

2. **`SSEStream` 是会话级而非请求级的。** 事件 ID 必须在 POST 升级流**和** GET 流之间都保持单调递增，因为 `Last-Event-ID` 是每会话一个游标（basic/transports.mdx:174-178）。所以计数器挂在 `Session` 上而不是 stream 对象上。

3. **Sampling 驱动 SSE 升级。** 同步工具返回 JSON；**可能**调用 `sampling/createMessage` 的工具升级到 SSE，这样服务端可以把自己的反向请求和最终的工具结果交织起来。我们用每请求级的 `Sampler` 接口；s06 stdio 章节用的是同一形态，唯一区别是实现做的事（写入 SSE 流 + 等客户端反向 POST 回响应）。

4. **会话在 `initialize` 时被铸造，对客户端不透明。** 16 字节的十六进制串塞进 `Mcp-Session-Id` 即可。规范也接受 UUID 和 JWT；规则是"全局唯一、ASCII 0x21-0x7E"（basic/transports.mdx:200-206）。会话状态（订阅、待回的反向请求、SSE 事件计数器、回放缓冲）挂在 `*Session` 上。

5. **回放缓冲在内存里，保留最近 100 条事件。** 当客户端用 `Last-Event-ID: 3` 重连时，我们从挂在会话上的环形缓冲里回放事件 `4..N`。真实实现会把这层挪到 Redis 里；API 保持不变。100 这个数字是务实之选——按典型通知频率覆盖大约 10 秒的网络抖动。

6. **`initialize` 之后每个 POST 都必须带协议版本头。** 头部缺失时返回 400 Bad Request 是 transports.mdx:268-272 明文规定的。`initialize` 本身豁免这个检查也是规范明文（"on all subsequent requests"）。

## 工作原理

```text
┌──────────────────────────────────────────────────────────────────────┐
│ POST /mcp                                                            │
│   │                                                                  │
│   ▼                                                                  │
│ 读 body, 解码 Message                                                │
│   │                                                                  │
│   ▼                                                                  │
│ 校验 MCP-Protocol-Version (除非是 initialize)                        │
│   │                                                                  │
│   ▼                                                                  │
│ 解析 Mcp-Session-Id  ──► SessionStore.Get / New                      │
│   │                                                                  │
│   ▼                                                                  │
│ 按 method 分发                                                       │
│   ├── 通知/响应             ─► 202 Accepted                          │
│   ├── initialize            ─► JSON 主体 + 会话头                    │
│   ├── 同步请求              ─► JSON 主体                             │
│   └── 需要 sampling 的请求  ─► 升级 SSE                              │
│                                  │                                   │
│                                  ▼                                   │
│                             NewSSEStream                             │
│                                  │                                   │
│                                  ├── 服务端发出 sampling/...         │
│                                  │   (事件 id 为 N, 写入回放缓冲)    │
│                                  ▼                                   │
│                             阻塞在 sess.pending[id]                  │
│                                  │                                   │
│                                  │  (客户端在另一条连接上 POST       │
│                                  │   响应；POST handler 通过 pending │
│                                  │   channel 投递)                   │
│                                  ▼                                   │
│                             服务端发出 CallToolResult                │
│                                  │                                   │
│                                  ▼                                   │
│                             关闭流                                   │
│                                                                      │
│ GET /mcp                                                             │
│   │                                                                  │
│   ▼                                                                  │
│ 同样的头部校验，然后 NewSSEStream                                    │
│   │                                                                  │
│   ├── 若带 Last-Event-ID: SSEBuffer.Since → 回放                     │
│   │                                                                  │
│   ▼                                                                  │
│ for { select { sess.notif → stream.Send | r.Context().Done() → 退出 }}│
└──────────────────────────────────────────────────────────────────────┘
```

SSE 写入端很短。摘自 `agents/s08-streamable-http/framer.go`：

```go
func (s *SSEStream) Send(m Message) (int64, error) {
    raw, _ := json.Marshal(m)
    return s.SendRaw(raw)
}

func (s *SSEStream) SendRaw(data []byte) (int64, error) {
    id := s.counter.Add(1)
    fmt.Fprintf(s.w, "id: %d\ndata: %s\n\n", id, data)
    s.flusher.Flush()
    s.buf.Append(id, data)
    return id, nil
}
```

把 sampling 和 SSE 流粘在一起的编排者在 `server.go` 里。关键技巧：传给工具 handler 的 `Sampler` 是**每请求级**的——它写到**当前**响应的 SSE 流，并在 `sess.pending[id]` 上等客户端的回复（回复是从**另一条** HTTP 连接来的，按请求 id 多路解复用）：

```go
func (s *sseSampler) CreateMessage(ctx context.Context, req CreateMessageRequest) (CreateMessageResult, error) {
    id := StringID("s8-" + strconv.FormatInt(s.nextID.Add(1), 10))
    ch := make(chan Message, 1)
    s.sess.pending[id.String()] = ch
    s.stream.Send(Message{ID: &id, Method: "sampling/createMessage", Params: paramsRaw})
    select {
    case reply := <-ch: /* 反序列化并返回 */
    case <-ctx.Done(): /* 取消 */
    }
}
```

四个不那么显然的点：

1. **POST 和 GET 共用 SSE 事件计数器。** 在某个 POST 升级流上发出的响应、和在 GET 流上发出的通知，**处在同一个 id 空间**里。这是因为续传（`Last-Event-ID`）是按会话维度的，缓冲也共享。如果两边各自独立计数，`Last-Event-ID: 7` 就会有歧义。

2. **客户端对 sampling 请求的*响应*回到服务端时走另一条连接。** 它**不是**通过那条挂着 SSE 流的 TCP 套接字回来的。客户端带着会话头 POST 那条响应过来，POST handler 看见这条信封带 `ID` 且无 `Method`，在 `sess.pending` 里查到对应的 channel，通过 channel 投递。SSE 那侧的 goroutine 正阻塞在这个 channel 上。

3. **`MCP-Protocol-Version` 只差一个旧日期也会被拒。** s08 硬编码 `2025-11-25`。transports.mdx:280-282 说"无效或不支持就返回 400"——我们就是这么机械地做。规范里其实也有一条回退路径（非 initialize 上头部缺失时假设是 `2025-03-26`），但我们选择严格 400，因为本章的教学点是：这个头部是**规范要求**的。

4. **会话可以由任一侧终止。** 客户端发 DELETE；或者服务端发现空闲后驱逐。我们没实现超时驱逐（这是真实世界的工程问题，测试面用不到），但 `SessionStore.Delete` 是齐的。驱逐后下一个带这个 session id 的请求会得到 404（transports.mdx:214-217），等于告诉客户端"用新的 initialize 重头来"。

## 与 s07 的差异

s07 引入了 `roots/list` 和 elicitation 流——由服务端请求驱动的客户端 handler。s08 让所有"每条消息的形状"都保持不变。变化都在**信封之外**：

- **传输从上到下换了一遍。** `stdioFramer` 不见了。取而代之的是 `HTTPFramer`（每请求级）和 `SSEStream`（在已 flush 的响应上做服务端写入）。
- **`Framer.Read()` 的语义改了。** s01-s07：阻塞，永远返回下一条消息。s08：每请求一次，第一次之后返回 `io.EOF`。我们保留这个名字是想让学员看出"抽象的形状是对的；变的是实现"。
- **状态搬到了请求之外。** s07 一个进程一个会话全在内存里。s08 有个按 `Mcp-Session-Id` 索引的 `SessionStore`；每个 handler 进来第一件事就是查会话。生命周期里的状态机（uninitialized → initializing → ready）现在是每会话一份。
- **`initialize` handler 是**唯一**能铸造会话的方法。** 任何不带会话 id（或带的会话 id 不识别）的请求都在门口被拒——缺失返回 400，过期返回 404。这是把 s02 引入的生命周期门控搬到了传输层强制执行。
- **三个新头部。** `Mcp-Session-Id`（initialize 响应给；之后每个 POST 都带）。`MCP-Protocol-Version`（initialize 之后每个 POST 都带；缺失就 400）。`Last-Event-ID`（GET 时带，用于续传 SSE 流）。三个都活在 HTTP 层；JSON-RPC 主体从不带它们。
- **第一次出现服务端异步推送消息。** s06 也有 `sampling/createMessage`，但跑在 stdio 上，那条流一直开着，所以"异步"是免费的。s08 必须**先**通过 SSE 建出双向通道，才有推送的可能。HTTP 建模的成本就是把这一步明确地暴露出来。

## 试一下

```bash
cd agents/s08-streamable-http
make demo
```

你会看到大约七行控制台输出。重点：

```
[demo] initialize → session=957a70ce941ab062ecf1d6e45be9718a
[demo]   body={"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{...}}}
[demo] tools/call → upgraded to SSE
[demo]   ← sampling/createMessage id="s8-1"
[demo]   → posted sampling response
[demo] tools/call result: {"content":[{"type":"text","text":"MCP is a JSON-RPC protocol for LLMs."}]}
```

跑测试套件来看代码形式的断言：

```bash
make test
```

试一下不带协议头去 POST：

```bash
curl -i -X POST http://127.0.0.1:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'Mcp-Session-Id: ...' \
  -d '{"jsonrpc":"2.0","id":7,"method":"tools/list"}'
```

应该看到 `HTTP/1.1 400 Bad Request`，主体是 `missing MCP-Protocol-Version header`。

试一下打开 GET 流然后给自己推一条通知：

```bash
curl -N http://127.0.0.1:8080/mcp \
  -H 'Accept: text/event-stream' \
  -H 'Mcp-Session-Id: ...' \
  -H 'MCP-Protocol-Version: 2025-11-25'
```

（demo 服务端没有从外部主动推送的公开方法；这个场景能在 Go 测试里看到，那里有一段并行 goroutine 注入 `sess.Notify(...)`。）

## 上游源码导读

本章唯一的规范来源是 [`basic/transports.mdx:52-330`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/basic/transports.mdx#L52-L330)。一边读它一边在另一个面板里打开 `agents/s08-streamable-http/server.go`——文件就是按这段散文一段一段实现的。

- **L52-L84** —— 概述、端点形状、安全告警（校验 `Origin`、本地运行时绑定 localhost、为所有连接做认证）。
- **L88-L128** —— "Sending Messages to the Server"。POST 契约：主体是一条 JSON-RPC 信封；响应要么 `application/json` 要么 `text/event-stream`；SSE 流**可以**在用响应终止之前交织插入服务端发起的请求和通知。直接对应 `Server.handlePOST` 与 `Server.runToolOverSSE`。
- **L132-L150** —— "Listening for Messages from the Server"——GET 流。对应 `Server.handleGET`。
- **L154-L162** —— "Multiple Connections"。一条消息属于一条流；不广播。我们没把这个约束暴露给用户，因为 s08 一个会话只有一条 GET 流。
- **L166-L192** —— "Resumability and Redelivery"。每流单调递增的事件 ID、`Last-Event-ID` 回放、"服务端**不得**重放会发到不同流上的消息"。我们的 `SSEBuffer.Since(lastID)` 强制保证了这点——每会话只有一个缓冲，所以约束自动满足。
- **L196-L232** —— "Session Management"。`Mcp-Session-Id` 生命周期、ASCII 0x21-0x7E 约束、服务端过期会话时返回 404、可选的 DELETE。
- **L258-L282** —— "Protocol Version Header"。后续每个请求必须带；无效或不支持返回 400。对应 `handlePOST` 与 `handleGET` 顶部的头部检查。
- **L286-L312** —— "Backwards Compatibility"。s08 故意没做 2024-11-05 HTTP+SSE 兼容回退；本课程到 2025-11-25 为止。

读完规范后值得看的伴生 SEP，它们告诉你设计在往哪走：

- [SEP-2243 — HTTP Header Standardization](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2243-http-standardization.md)。主张把路由字段镜像到 HTTP 头部，这样负载均衡、WAF、可观察性工具就不用做深包检查也能干活。其实已经部分落地（协议版本头和会话头）。
- [SEP-2575 — Make MCP Stateless](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2575-stateless-mcp.md)。提议去掉 `initialize` 握手，把协议版本和能力按请求携带。读起来像是面向 serverless/边缘部署的未来方向，那种场景下在内存里维护每客户端状态根本不可行。
- [SEP-2567 — Sessionless MCP via Explicit State Handles](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/seps/2567-sessionless-mcp.md)。2575 的伴生提案：彻底干掉会话，把状态托管的责任推到工具定义的不透明 ID 上（例如 `create_basket` 返回的 `basket_id`）。两个 SEP 都落地的话，s08 的 `SessionStore` 就是最后一代需要存在的这种代码。

本地离线副本：[`upstream-readings/s08-streamable-http.mdx`](../../upstream-readings/s08-streamable-http.mdx)。
