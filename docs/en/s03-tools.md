---
title: "s03 · tools/list and tools/call"
chapter: 03
slug: s03-tools
est_read_min: 12
---

# s03 · tools/list and tools/call

> What this teaches: how MCP exposes server-side functions to a model. We add the `tools` capability, ship two demo tools, and introduce the **isError convention** — the rule that decides which failures become JSON-RPC errors and which travel back inside the result so the model can see them and self-correct.

---

## Problem

After s02 the server handshakes cleanly: it negotiates a protocol version, advertises a (currently empty) capabilities tree, and refuses every non-`initialize` method with `-32002 SERVER_NOT_INITIALIZED` until the client sends `notifications/initialized`. That gives us a phone line, but the only thing on the other end is a dial tone — the server has no methods that *do* anything. A model speaking to us can discover nothing and call nothing.

The smallest useful capability is `tools`. Tools are model-controlled functions: `tools/list` enumerates them (each carries a JSON-Schema for its arguments), and `tools/call` invokes one by name with a JSON-typed `arguments` object and returns a typed `ContentBlock[]`. Two design decisions are easy to get wrong — what shape the result has (`ContentBlock` is a *tagged union*, not a plain string), and where execution errors live (`CallToolResult.isError`, NOT a JSON-RPC error). Both decisions exist so an LLM client can render *and* recover from tool calls without a custom path per server.

## Solution

Think of `tools/call` as a three-stage filter:

1. **Protocol-level validation.** Is the tool registered? Do its `arguments` conform to its declared `inputSchema`? If either fails, send back a JSON-RPC error (`-32601` for unknown name, `-32602` for schema failure). The tool never ran.
2. **Execution.** Call the handler. Wrap in a panic-safe defer so a buggy tool can't crash the loop.
3. **Result shaping.** Wrap whatever the handler returned (string output, structured object, image bytes) into one or more `ContentBlock`s. A panic or returned Go error becomes `CallToolResult{IsError: true, Content: [text("...")]}` — *not* a JSON-RPC error.

Three key design decisions:

1. **`ContentBlock` as a "fat struct" + `Type` discriminator.** TypeScript uses a tagged union (`TextContent | ImageContent | AudioContent | ResourceLink | EmbeddedResource`); Go has neither sum types nor type-narrowing, so we put every variant's fields on one struct and rely on variant constructors (`TextBlock`, `ImageBlock`) to guarantee a valid combination at creation time.
2. **Schemas compile at registration, not per call.** `santhosh-tekuri/jsonschema/v5` is fast at validate time but slow at compile time. Doing it once per tool keeps `tools/call` cheap and surfaces malformed schemas as a startup error.
3. **The `isError` split is load-bearing.** "Unknown tool" is a *client* mistake → JSON-RPC error. "The weather API returned 500" is a *server-side runtime* condition the model should be able to see and retry → `IsError: true` inside the result. The boundary is "did the tool execute?" — if yes, the result has IsError; if no, the protocol does.

## How It Works

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

Core 50 lines (excerpt from [`agents/s03-tools/tools.go`](../../agents/s03-tools/tools.go)):

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
    // Per spec: handler panic or returned Go error becomes IsError:true,
    // NOT a JSON-RPC error. That's what lets the model self-correct.
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

**Four non-obvious points**:

1. **Schema validation rejects with `-32602`, not `-32600`.** `-32600 InvalidRequest` is for malformed *envelopes* (bad `jsonrpc` field, missing `id`); `-32602 InvalidParams` is for valid envelopes whose `params` violate the method's contract. Schema failure is firmly in the second bucket.
2. **`panic` → `IsError: true`, *not* `-32603`.** `safeCall` recovers panics and converts them into a Go `error`; `handleCallTool` then puts that error text into a content block. The model can read "tool error: panic: division by zero" and try different args. If we mapped panic → `-32603 InternalError` the model would only see "the server is broken" and could not recover.
3. **Empty `arguments` is normalized to `{}` before validation.** The schema `{type: "object", required: ["text"]}` requires `arguments` to be an *object* before the `required` check fires. `null` (which is what we'd see if the client omitted the field) doesn't pass `type: object`, but more importantly the error message would be confusing. Defaulting to `{}` produces the right error: "missing property text".
4. **`ToolRegistry.order` preserves registration order in `tools/list`.** Map iteration in Go is randomized, which would make `tools/list` non-deterministic between calls — and the test that asserts "echo and get_time both present" wouldn't catch a ListChanged bug. The slice is the source of truth for ordering; the map is just an O(1) lookup index.

## What Changed (vs. s02)

```diff
 // ServerCapabilities — only the bits we need.
 type ServerCapabilities struct {
-    // s02: empty
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

Semantically the server is no longer a hollow shell. s02 could only echo its own capabilities; s03 has a *registry* (a `map[string]ToolHandler` keyed by name) and dispatches to it. The bigger structural change is the new mental model: from *"every method is a router entry"* to *"some methods are dispatch tables that nest a second registry inside them"*. That pattern repeats for resources (s04), prompts (s05), and sampling (s06).

## Try It

```bash
cd agents/s03-tools

# Run the 6-test suite (5 required + 1 lifecycle gate)
go test -v ./...

# Drive a 4-message conversation: initialize → initialized → tools/list → tools/call echo
make demo
```

Expected `make demo` output (3 lines; the second message is a notification with no response, hence 3 not 4):

```
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"learn-mcp-s03-tools","version":"0.1.0"},"instructions":"..."}}
{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"echo","description":"...","inputSchema":{...}},{"name":"get_time","description":"...","inputSchema":{...}}]}}
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"hi"}]}}
```

Try a deliberately-broken call to see the `-32602` path:

```bash
printf '%s\n%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"d","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{}}}' \
  | go run .
```

The third reply will be `"error":{"code":-32602,"message":"arguments fail schema: ..."}` — schema validation runs *before* the tool, so this is a protocol-level rejection.

## Upstream Source Reading

The upstream spec has two relevant sections; both are tiny.

```upstream:schema/2025-11-25/schema.ts#L1082-L1299
// Source: schema/2025-11-25/schema.ts:1082-1299

export interface CallToolResult extends Result {
  content: ContentBlock[];
  structuredContent?: { [key: string]: unknown };
  // Errors that originate from the tool SHOULD be reported with
  // isError = true, NOT as MCP protocol-level errors. Otherwise the
  // LLM would not be able to see the error and self-correct.
  // However, errors in *finding* the tool (or the server not
  // supporting tool calls) MUST be MCP error responses.
  isError?: boolean;
}

export interface Tool extends BaseMetadata, Icons {
  description?: string;
  inputSchema: {
    type: "object";
    properties?: { [key: string]: object };
    required?: string[];
  };
  outputSchema?: { ...same shape... };
  execution?: ToolExecution;        // taskSupport: forbidden|optional|required
  annotations?: ToolAnnotations;    // readOnly/destructive/idempotent hints
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

**Reading notes**:

- **`isError` is the only field whose JSDoc spans a full paragraph.** That's the spec authors explicitly calling out the design — see [`server/tools.mdx:132-148`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/server/tools.mdx#L132-L148). Get this wrong and the model loses recoverability.
- **`Tool extends BaseMetadata, Icons` uses TypeScript mixins.** Go has no real mixins — embedding produces ambiguous-selector errors when two embedded types share a field name. We inline the only fields s03 needs (`Name`, `Description`, `InputSchema`); a full implementation would inline `Title`, `Icons`, and `_meta` too.
- **Upstream `Tool.inputSchema.type` is the literal `"object"`.** The TS type system enforces this at compile time; we don't validate it because the jsonschema compiler will reject any other root type at registration.
- **`execution.taskSupport` is the hook for SEP-1686 (tasks).** We ignore it in s03 — every tool runs synchronously. A real server with long-running tools would set `taskSupport: "optional"` and let the client poll via `tasks/get`.
- **Annotations (`readOnlyHint`, `destructiveHint`) are deliberately untrusted hints.** The spec calls them out as "not guaranteed to provide a faithful description of tool behavior" — clients MUST NOT base trust decisions on them. We skip them in s03 to keep the focus on the wire shape.

```upstream:docs/specification/2025-11-25/server/tools.mdx#L36-L186
// Source: docs/specification/2025-11-25/server/tools.mdx:36-186 (excerpt)

Servers that support tools MUST declare the `tools` capability:
  { "capabilities": { "tools": { "listChanged": true } } }

`listChanged` indicates whether the server will emit notifications when
the list of available tools changes.

[tools/list request / response examples]
[tools/call request / response examples]
[message-flow mermaid diagram: LLM ↔ Client ↔ Server, tools/list →
selection → tools/call → result; plus optional list_changed push]
```

Full prose: [`server/tools.mdx:36-186`](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/85ec377139c3026d98086eadbb23806e65517f21/docs/specification/2025-11-25/server/tools.mdx#L36-L186).

Local offline copy with both schema slices interleaved: [`upstream-readings/s03-tools.ts`](../../upstream-readings/s03-tools.ts).

**Read further**: from `schema.ts:1082` (`/* Tools */` banner) follow the `ContentBlock` type alias at line 1742, then `ToolUseContent`/`ToolResultContent` further down (line 1840+) which s06 re-uses for the sampling-side agentic loop. That trace is the real-source map for s03 → s05 (prompts also use ContentBlock) → s06 (tool_use/tool_result blocks).

---

**Next**: s04 picks up the *file-like* server capability — resources — and shows why `resources/read.contents[]` is an array (one URI template can expand to many files).
