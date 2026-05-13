---
title: "Multi-model guide: plugging Anthropic / OpenAI / DeepSeek / Qwen into the Sampler"
slug: multi-model
---

# Multi-model guide: plugging Anthropic / OpenAI / DeepSeek / Qwen into the Sampler

In MCP, the LLM is not in the server — it's behind the **client**. Whenever a server emits `sampling/createMessage`, the client decides which model to use and runs the completion. In `agents/s06-sampling/sampler.go` we exposed exactly one interface for this:

```go
type Sampler interface {
    CreateMessage(ctx context.Context, req CreateMessageRequest) (CreateMessageResult, error)
}
```

`StubSampler` ships canned replies for tests. To plug in a real LLM you implement `Sampler` once. Below are minimal recipes for the four providers most learners will reach for.

## Why this is different from an "agent loop" multi-model layer

In a typical agent framework, the provider abstraction lives at the call site of `messages.create` and translates Anthropic↔OpenAI message shapes. In MCP, that translation happens **inside the client's Sampler**, completely orthogonal to the wire protocol. The server doesn't know — and cannot care — which model produced the reply. That's a deliberate design choice: it lets a single MCP server serve clients running Claude, Llama, or any model the user prefers.

So the multi-model story here is short: implement one method.

## Anthropic (native)

Add `agents/s06-sampling/sampler_anthropic.go`:

```go
package main

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "os"
)

type AnthropicSampler struct {
    APIKey string
    Model  string
    URL    string // default https://api.anthropic.com/v1/messages
}

func (s *AnthropicSampler) CreateMessage(ctx context.Context, req CreateMessageRequest) (CreateMessageResult, error) {
    url := s.URL
    if url == "" {
        url = "https://api.anthropic.com/v1/messages"
    }
    payload := map[string]any{
        "model":       s.Model,
        "max_tokens":  req.MaxTokens,
        "system":      req.SystemPrompt,
        "messages":    samplingToAnthropic(req.Messages),
        "temperature": req.Temperature,
    }
    body, _ := json.Marshal(payload)
    httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
    httpReq.Header.Set("x-api-key", s.APIKey)
    httpReq.Header.Set("anthropic-version", "2023-06-01")
    httpReq.Header.Set("content-type", "application/json")
    httpResp, err := http.DefaultClient.Do(httpReq)
    if err != nil {
        return CreateMessageResult{}, fmt.Errorf("anthropic call: %w", err)
    }
    defer httpResp.Body.Close()
    if httpResp.StatusCode >= 400 {
        b, _ := io.ReadAll(httpResp.Body)
        return CreateMessageResult{}, errors.New("anthropic " + httpResp.Status + ": " + string(b))
    }
    var ar struct {
        Content    []struct{ Type, Text string } `json:"content"`
        StopReason string                        `json:"stop_reason"`
        Model      string                        `json:"model"`
    }
    if err := json.NewDecoder(httpResp.Body).Decode(&ar); err != nil {
        return CreateMessageResult{}, err
    }
    blocks := make([]ContentBlock, 0, len(ar.Content))
    for _, c := range ar.Content {
        if c.Type == "text" {
            blocks = append(blocks, ContentBlock{Type: "text", Text: c.Text})
        }
    }
    return CreateMessageResult{
        Role:       "assistant",
        Content:    blocks,
        Model:      ar.Model,
        StopReason: ar.StopReason,
    }, nil
}

func samplingToAnthropic(msgs []SamplingMessage) []map[string]any {
    out := make([]map[string]any, 0, len(msgs))
    for _, m := range msgs {
        parts := make([]map[string]any, 0, len(m.Content))
        for _, c := range m.Content {
            if c.Type == "text" {
                parts = append(parts, map[string]any{"type": "text", "text": c.Text})
            }
        }
        out = append(out, map[string]any{"role": m.Role, "content": parts})
    }
    return out
}

func NewAnthropicSamplerFromEnv() *AnthropicSampler {
    return &AnthropicSampler{APIKey: os.Getenv("ANTHROPIC_API_KEY"), Model: "claude-sonnet-4-6"}
}
```

In `main.go`, swap the StubSampler:

```go
if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
    sampler = NewAnthropicSamplerFromEnv()
}
```

## OpenAI-compatible providers (OpenAI, DeepSeek, Moonshot, Qwen, Groq, OpenRouter, vLLM)

Almost every modern provider speaks the OpenAI Chat Completions API. One adapter covers all of them; only the base URL and model name change.

```go
package main

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
)

type OpenAISampler struct {
    APIKey  string
    BaseURL string // e.g. https://api.deepseek.com/v1
    Model   string // e.g. deepseek-chat
}

func (s *OpenAISampler) CreateMessage(ctx context.Context, req CreateMessageRequest) (CreateMessageResult, error) {
    payload := map[string]any{
        "model":       s.Model,
        "max_tokens":  req.MaxTokens,
        "temperature": req.Temperature,
        "messages":    samplingToOpenAI(req.SystemPrompt, req.Messages),
    }
    body, _ := json.Marshal(payload)
    httpReq, _ := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/chat/completions", bytes.NewReader(body))
    httpReq.Header.Set("Authorization", "Bearer "+s.APIKey)
    httpReq.Header.Set("Content-Type", "application/json")
    httpResp, err := http.DefaultClient.Do(httpReq)
    if err != nil {
        return CreateMessageResult{}, err
    }
    defer httpResp.Body.Close()
    var or struct {
        Choices []struct {
            Message      struct{ Content string }
            FinishReason string `json:"finish_reason"`
        }
        Model string
    }
    if err := json.NewDecoder(httpResp.Body).Decode(&or); err != nil {
        return CreateMessageResult{}, err
    }
    if len(or.Choices) == 0 {
        return CreateMessageResult{}, fmt.Errorf("openai-compat: empty choices")
    }
    return CreateMessageResult{
        Role:       "assistant",
        Content:    []ContentBlock{{Type: "text", Text: or.Choices[0].Message.Content}},
        Model:      or.Model,
        StopReason: or.Choices[0].FinishReason,
    }, nil
}

func samplingToOpenAI(sys string, msgs []SamplingMessage) []map[string]string {
    out := make([]map[string]string, 0, len(msgs)+1)
    if sys != "" {
        out = append(out, map[string]string{"role": "system", "content": sys})
    }
    for _, m := range msgs {
        text := ""
        for _, c := range m.Content {
            if c.Type == "text" {
                text += c.Text
            }
        }
        out = append(out, map[string]string{"role": m.Role, "content": text})
    }
    return out
}
```

### Provider profiles

| Provider     | Base URL                                                   | Default model               | Env var               |
|--------------|------------------------------------------------------------|-----------------------------|-----------------------|
| OpenAI       | `https://api.openai.com/v1`                                | `gpt-4o-mini`               | `OPENAI_API_KEY`      |
| DeepSeek     | `https://api.deepseek.com/v1`                              | `deepseek-chat`             | `DEEPSEEK_API_KEY`    |
| Moonshot/Kimi| `https://api.moonshot.cn/v1`                               | `moonshot-v1-8k`            | `MOONSHOT_API_KEY`    |
| Qwen         | `https://dashscope.aliyuncs.com/compatible-mode/v1`        | `qwen-plus`                 | `DASHSCOPE_API_KEY`   |
| Groq         | `https://api.groq.com/openai/v1`                           | `llama-3.3-70b-versatile`   | `GROQ_API_KEY`        |
| OpenRouter   | `https://openrouter.ai/api/v1`                             | `openai/gpt-4o-mini`        | `OPENROUTER_API_KEY`  |
| vLLM (local) | `http://localhost:8000/v1`                                 | `local-model`               | `OPENAI_API_KEY`      |

## Wiring it to a command-line flag

```go
provider := flag.String("provider", "stub", "stub | anthropic | openai | deepseek | qwen | local")
flag.Parse()

var sampler Sampler
switch *provider {
case "stub":
    sampler = NewStubSampler()
case "anthropic":
    sampler = NewAnthropicSamplerFromEnv()
case "openai":
    sampler = &OpenAISampler{APIKey: os.Getenv("OPENAI_API_KEY"), BaseURL: "https://api.openai.com/v1", Model: "gpt-4o-mini"}
case "deepseek":
    sampler = &OpenAISampler{APIKey: os.Getenv("DEEPSEEK_API_KEY"), BaseURL: "https://api.deepseek.com/v1", Model: "deepseek-chat"}
case "qwen":
    sampler = &OpenAISampler{APIKey: os.Getenv("DASHSCOPE_API_KEY"), BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", Model: "qwen-plus"}
case "local":
    sampler = &OpenAISampler{APIKey: "x", BaseURL: "http://localhost:8000/v1", Model: "local-model"}
}
```

## Caveats

- **Tool use translation.** MCP's `tool_use` / `tool_result` blocks (s06) are a superset of OpenAI's `tool_calls` and a subset of Anthropic's. Both adapters here intentionally only translate `text` content — adding tool-use translation is the natural follow-up exercise. The schemas are very close to OpenAI's structured tool-call format.
- **Streaming.** Both APIs support streaming. For an MCP `Sampler` you don't need it (the spec returns a single `CreateMessageResult`), but if you want to forward token-level progress to the user you can wrap a stream into MCP's `notifications/progress` push.
- **Context window.** Don't forget to pass `max_tokens` honestly; some providers refuse silently if you exceed the model's context.

That's the whole multi-model story for MCP.
