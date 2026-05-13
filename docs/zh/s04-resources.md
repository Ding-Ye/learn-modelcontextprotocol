---
title: "s04 · 资源读取、模板与订阅"
chapter: 04
slug: s04-resources
est_read_min: 12
---

# s04 · 资源读取、模板与订阅

> 本章要点：**类文件**形态的服务器能力。s03 的 tools 是"函数式"（"按参数调用我"），resources 则是"上下文式"（"按 URI 读取我"）。同时本章首次出现**服务器主动推送**：`notifications/resources/updated`。

---

## Problem

工具是动词，资源是名词。MCP 规范在 `server/resources.mdx:30-278` 把这条边界划得很清楚——资源是 *application-controlled* 的上下文，由 host 在合适的时机塞进模型上下文窗口，按 URI 寻址（`file://`、`https://`、自定义 scheme 都行），用 `resources/list` 枚举、用 `resources/read` 读取。还有一类资源在"被询问之前"根本不存在——它是参数化的 URI 模板（`file:///project/src/{path}`），由客户端按需展开。再有，客户端关心的资源在读完后可能会变——所以 MCP 长出了一条订阅/通知通道："这条 URI 变了，请重新读取。"

s01–s03 只演示了一个方向：客户端发请求、服务器响应。s04 必须引入**服务器主动通知**——一条没有 `id` 的 JSON-RPC 帧，从一个跟原始 subscribe 请求毫无关联的 goroutine 异步推出来。锁加错或线格式写错，demo 直接死锁或者吐出畸形帧。本章要解决的痛点：端到端跑通真正的订阅/推送链路，再用测试证明 unsubscribe 之后推送真的停了。

## Solution

从上到下四层：

1. **Router** —— 五个新方法：`resources/list`、`resources/read`、`resources/templates/list`、`resources/subscribe`、`resources/unsubscribe`。dispatch 模式跟 s02 一致。
2. **Store 接口** —— `List() []Resource`、`Read(uri) ([]ResourceContents, error)`、`Templates() []ResourceTemplate`、`Set(uri, ...)`。接口才是重点；内存实现是一个 map。
3. **模板展开器** —— RFC-6570 **仅 level 1**（`{name}` 替换，不支持 `{+path}`、`{?query}`、`{?list*}`）。算上反向匹配也就 30 行。
4. **Notifier** —— 一个按 URI 计数的订阅表 + 一个 `sink func(Message) error`。Store 调用 `Set` 时，notifier 检查计数，非零就 marshal 一条 `notifications/resources/updated` 帧塞给 sink。

三个值得拎出来讲的设计决定：

1. **`ResourceContents` 是 fat struct，不是 union。** schema.ts 写的是 `(TextResourceContents | BlobResourceContents)`。Go 没有 tagged union，所以把两者塌成一个 struct，`text` 和 `blob` 各自可选，靠 `omitempty` 保持线上整洁。构造函数（`TextContents` / `BlobContents`）保不变式。和 s03 处理 `ContentBlock` 用的是同一招。
2. **notifier 的 sink 是 `func(Message) error`，不是 `Framer`。** 这正是让本章可测的关键。stdio 入口装 `framer.Write`；测试装一个 slice-append 闭包。同一段代码路径，不同消费者。
3. **stdioFramer.Write 持锁。** 这是全章**唯一**的并发原语，但少不了。请求 goroutine 写响应；另一个 goroutine（或在测试里就是同一个）写通知。少了这把锁，两次 Write 的字节就会交织成一帧坏数据。

## How It Works

```text
┌─────────────────────────────────────────────────────────────┐
│  stdin                                                      │
│   │                                                         │
│   ▼                                                         │
│ stdioFramer.Read ──► router.handle ──► dispatch             │
│                              │                              │
│                              ├─ resources/list      ─► Store.List         │
│                              ├─ resources/read      ─► Store.Read         │
│                              ├─ resources/templates/list ─► Store.Templates │
│                              ├─ resources/subscribe ─► notifier.subscribe │
│                              └─ resources/unsubscribe ─► notifier.unsubscribe │
│                                                                           │
│  ┌──────────── 独立 goroutine ────────────────┐                           │
│  │  store.Set(uri, ...) ──► notifier.fire(uri)│                           │
│  │       │                       │            │                           │
│  │       ▼                       ▼            │                           │
│  │  改 map               （若已订阅）发出：    │                           │
│  │                       {"jsonrpc":"2.0",    │                           │
│  │                        "method":"notifica- │                           │
│  │                         tions/resources/   │                           │
│  │                         updated", ...}     │                           │
│  └────────────────────────────────────────────┘                           │
│                              ▼                                            │
│                       stdioFramer.Write（mutex 保护）                     │
│                              │                                            │
│                              ▼                                            │
│                            stdout                                         │
└─────────────────────────────────────────────────────────────┘
```

### 模板展开 + 反向匹配

level-1 展开器就是一条正则（`\{([A-Za-z_][A-Za-z0-9_]*)\}`）加 10 行：

```go
func expandTemplate(tmpl string, vars map[string]string) string {
    return levelOneVar.ReplaceAllStringFunc(tmpl, func(match string) string {
        name := match[1 : len(match)-1]
        if v, ok := vars[name]; ok { return v }
        return match
    })
}
```

**反向**才是 `resources/read` 真正需要的：拿到 `mem://users/alice`，要倒推它属于哪个模板。把模板按变量切开，验证 URI 的前缀和后缀，中间一段就是变量值：

```go
func matchTemplate(tmpl, uri string) (bool, string) {
    loc := levelOneVar.FindStringIndex(tmpl)
    if loc == nil { return tmpl == uri, "" }
    prefix := tmpl[:loc[0]]
    suffix := tmpl[loc[1]:]
    if !strings.HasPrefix(uri, prefix) || !strings.HasSuffix(uri, suffix) {
        return false, ""
    }
    value := uri[len(prefix) : len(uri)-len(suffix)]
    if value == "" || strings.Contains(value, "/") { return false, "" }
    return true, value
}
```

`strings.Contains(value, "/")` 这条检查是 level-1 不变式——level-1 变量只匹配单个路径段，不匹配整条路径。要订阅整个目录就需要 `{+path}`（level 2），那是附录 B 留的扩展练习。

### subscribe → notify 的竞态

```go
func (n *notifier) fire(uri string) {
    n.mu.Lock()
    _, ok := n.subs[uri]
    sink := n.sink
    n.mu.Unlock()
    if !ok || sink == nil { return }
    // ... marshal + sink(msg)
}
```

"读完释放再调用"。我们**故意不**在 `sink(msg)` 期间持锁：sink 是 `stdioFramer.Write`，它会进 `io.Writer`，可能因为下游慢而阻塞。如果一直持着 `n.mu`，写期间就没人能 subscribe / unsubscribe / fire。代价：一个极小的窗口——unsubscribe 刚返回，但已经拿到 sink 的某次 fire 还是会写一帧出来。对实时正确性敏感的系统得加 epoch 计数器；教学服务器里，"尾随一条通知"完全可以接受。

## What Changed (vs. s02)

s02 引入了 router 和 lifecycle。s04 复用两者，并叠上：

- **+ 五个方法 handler**（`resources/list`、`resources/read`、`resources/templates/list`、`resources/subscribe`、`resources/unsubscribe`）。
- **+ ResourcesCapability** 在 `initialize` 里声明（`subscribe: true, listChanged: true`）。
- **+ 服务器主动通知通道** —— 第一次有字节**不是**作为请求响应离开服务器。这是 s06（sampling）、s07（roots/elicitation）的地基；它们要在同一把写锁之上再叠一张"出站请求待响应表"。
- **+ 内存 `Store`** 抽象 —— 本章因此不依赖文件系统。把它换成 fsnotify 监听真实文件，是附录 B 的推荐练习。

我们**没有**实现 `notifications/resources/list_changed`——那是个练习。capability 声明它了、线格式无参数也很简单，但 demo store 启动后从不新增资源。

## Try It

```bash
cd agents/s04-resources
make demo
```

stdout 应该按顺序出 5 帧，每次请求一帧：

```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"resources":{"subscribe":true,"listChanged":true}},"serverInfo":{...},"instructions":"..."}}
{"jsonrpc":"2.0","id":2,"result":{}}
{"jsonrpc":"2.0","id":3,"result":{"resources":[{"uri":"mem://log.txt","name":"log.txt","description":"Append-only demo log","mimeType":"text/plain","size":15}]}}
{"jsonrpc":"2.0","id":4,"result":{"contents":[{"uri":"mem://log.txt","mimeType":"text/plain","text":"hello from s04\n"}]}}
```

要看推送，得让 stdin 在 subscribe 之后保持开放超过 100ms——demo 的后台 goroutine 会变更 `mem://log.txt`，notifier 就会推出来。（`make demo` 的脚本是死的，stdin 一灌完就关，所以通常在推送到达之前进程就退出了；这正是我们需要测试套件的原因。）

试一下读模板 URI：

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"d","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"resources/templates/list"}
{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"mem://users/alice"}}' \
  | go run ./agents/s04-resources
```

第 3 帧会列出模板，第 4 帧返回 `Alice Liddell`。

## Upstream Source Reading

规范切片 [`schema.ts:651-921`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/schema/2025-11-25/schema.ts#L651-L921)。建议跟 `agents/s04-resources/resources.go` 并排看：

- **L651-L668** —— `ListResourcesRequest`、`ListResourcesResult`。`PaginatedRequest`/`Result` 提供分页字段——s04 忽略 cursor（单页），但 struct 里留了字段，未来直接升级即可。
- **L675-L687** —— `ListResourceTemplatesRequest` / `Result`。形状跟上面一样。
- **L693-L731** —— `ReadResourceRequest` 和 `ReadResourceResult`。关键是 **L731**：`contents: (TextResourceContents | BlobResourceContents)[]`。是**数组**。一条 URI 可以展开成多条 contents。
- **L738-L771** —— `SubscribeRequest`、`UnsubscribeRequest`。极简：只有 `{ uri }`。
- **L778-L803** —— `ResourceUpdatedNotificationParams` + `ResourceUpdatedNotification`。注意它的父类是 `JSONRPCNotification`（无 id）。
- **L804-L845** —— `Resource` interface。我们把字段直接铺平，没有用 `BaseMetadata` + `Icons` 这种 TS 混入。
- **L847-L885** —— `ResourceTemplate` interface。带 `uriTemplate`（RFC-6570 字符串），不是展开后的 URI。
- **L887-L921** —— `ResourceContents`、`TextResourceContents`、`BlobResourceContents`。"fat struct" 那个决策就落在这里。

然后看 [`server/resources.mdx:30-278`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/server/resources.mdx#L30-L278) 的散文语义：

- **L30-L79** —— capability 块（`subscribe`、`listChanged`），以及两者都可选这条规则。
- **L82-L138** —— `resources/list` 请求/响应示例。
- **L140-L177** —— `resources/read` 示例。例子用了 `file://`，但线格式跟 scheme 无关。
- **L180-L226** —— `resources/templates/list`，并指向 `completion/complete`（s05 的领地）。
- **L228-L268** —— 订阅：subscribe → update notification。示例通知里只有 `params.uri`，没有 `revision` / `etag`——这是故意的，协议预期客户端重新读。

本地离线副本：[`upstream-readings/s04-resources.ts`](../../upstream-readings/s04-resources.ts)。
