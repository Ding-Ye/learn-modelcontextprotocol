---
title: "多模型接入指南：把 Anthropic / OpenAI / DeepSeek / Qwen 接到 Sampler"
slug: multi-model
---

# 多模型接入指南：把 Anthropic / OpenAI / DeepSeek / Qwen 接到 Sampler

MCP 里 LLM 不在服务器一侧——它在**客户端**身后。每当服务器发 `sampling/createMessage`，客户端决定用哪个模型、跑哪一段补全。我们在 `agents/s06-sampling/sampler.go` 里只暴露了一个接口承担这件事：

```go
type Sampler interface {
    CreateMessage(ctx context.Context, req CreateMessageRequest) (CreateMessageResult, error)
}
```

`StubSampler` 是测试用的固定回复。要接真模型，实现这一个方法即可。下面给四类最常见 provider 的最小食谱。

## 为什么这和"agent loop 的多模型层"不一样

典型 agent 框架里，provider 抽象层在 `messages.create` 调用点做 Anthropic↔OpenAI 形状翻译。MCP 里这层翻译在**客户端的 Sampler 内部**完成，跟 wire protocol 正交。服务器**不知道**也**不需要**知道是哪个模型回的。这是有意为之：一台 MCP 服务器可以同时服务 Claude 用户、Llama 用户、自托管模型用户。

所以 MCP 的"多模型故事"很短：实现一个方法。

## Anthropic（原生）

新建 `agents/s06-sampling/sampler_anthropic.go`：

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
    URL    string // 默认 https://api.anthropic.com/v1/messages
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

在 `main.go` 里把 StubSampler 换掉：

```go
if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
    sampler = NewAnthropicSamplerFromEnv()
}
```

## OpenAI 兼容协议（OpenAI / DeepSeek / Moonshot / Qwen / Groq / OpenRouter / vLLM）

几乎所有现代 provider 都讲 OpenAI Chat Completions API。一个适配器覆盖全部，只是 base URL 和 model 名变了。

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
    BaseURL string // 例如 https://api.deepseek.com/v1
    Model   string // 例如 deepseek-chat
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

### Provider 速查表

| Provider      | Base URL                                                   | 默认模型                    | 环境变量              |
|---------------|------------------------------------------------------------|-----------------------------|-----------------------|
| OpenAI        | `https://api.openai.com/v1`                                | `gpt-4o-mini`               | `OPENAI_API_KEY`      |
| DeepSeek      | `https://api.deepseek.com/v1`                              | `deepseek-chat`             | `DEEPSEEK_API_KEY`    |
| Moonshot/Kimi | `https://api.moonshot.cn/v1`                               | `moonshot-v1-8k`            | `MOONSHOT_API_KEY`    |
| Qwen          | `https://dashscope.aliyuncs.com/compatible-mode/v1`        | `qwen-plus`                 | `DASHSCOPE_API_KEY`   |
| Groq          | `https://api.groq.com/openai/v1`                           | `llama-3.3-70b-versatile`   | `GROQ_API_KEY`        |
| OpenRouter    | `https://openrouter.ai/api/v1`                             | `openai/gpt-4o-mini`        | `OPENROUTER_API_KEY`  |
| vLLM (本地)   | `http://localhost:8000/v1`                                 | `local-model`               | `OPENAI_API_KEY`      |

## 用命令行 flag 切换

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

## 注意事项

- **Tool use 翻译。** MCP 的 `tool_use` / `tool_result` blocks（s06）是 OpenAI `tool_calls` 的超集、Anthropic 的子集。本文两个适配器只翻译 `text` 内容；加 tool-use 翻译是顺手的下一步练习，OpenAI 的结构化 tool-call 字段跟 MCP 几乎一一对应。
- **流式输出。** 两边 API 都支持流式。MCP `Sampler` 不需要（规范只返回一个 `CreateMessageResult`），但如果想把 token 进度透传给用户，可以把流式响应包装成 `notifications/progress` push。
- **上下文窗口。** `max_tokens` 要传得诚实——某些 provider 超出模型上下文时会静默拒绝。

MCP 的多模型故事就这么短。
