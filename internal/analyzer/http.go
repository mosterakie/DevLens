package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

// HTTPConfig 是 HTTP analyzer 的配置。
type HTTPConfig struct {
	APIKey      string
	Model       string
	BaseURL     string
	Timeout     time.Duration
	MaxAttempts int
	MaxTokens   int
	Temperature float64

	// JSONMode 请求服务端保证输出是合法 JSON。DeepSeek 和 OpenAI
	// 都支持，但并非所有兼容服务都支持，所以做成开关。
	JSONMode bool
}

// HTTPAnalyzer 通过 HTTP 调用兼容 OpenAI 协议的接口。
type HTTPAnalyzer struct {
	cfg    HTTPConfig
	client *http.Client
}

// NewHTTP 构造 HTTP analyzer。
func NewHTTP(cfg HTTPConfig) *HTTPAnalyzer {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 1024
	}
	return &HTTPAnalyzer{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// Name 返回实现标识。
func (a *HTTPAnalyzer) Name() string { return a.cfg.Model }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`

	// ResponseFormat 请求结构化输出。DeepSeek 要求同时设置
	// response_format 并在 prompt 里出现 "json" 字样和格式示例，
	// 否则该参数不生效。SystemPrompt 已满足后两个条件。
	//
	// 用 omitempty：不是所有兼容 OpenAI 协议的服务都接受这个字段，
	// 留空时就不发送。
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

// responseFormat 是 OpenAI 协议的 response_format 字段。
type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
		// FinishReason 为 "length" 表示输出被 max_tokens 截断。
		// 截断的 JSON 必然非法，且重试也会得到同样的结果，
		// 所以必须单独识别出来，而不是等解析失败。
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Analyze 调用模型并解析结果。
//
// 每次尝试的策略和退避见 07-reliability.md：解析失败时收紧 prompt 约束
// 再试，而不是原样重试——原样重试对确定性错误没有意义。
func (a *HTTPAnalyzer) Analyze(ctx context.Context, in Input) (Result, error) {
	var lastErr error

	for attempt := 1; attempt <= a.cfg.MaxAttempts; attempt++ {
		if attempt > 1 {
			// 指数退避加抖动：多个 worker 同时失败时不要同时重试，
			// 否则会形成新的尖峰。
			delay := time.Second * time.Duration(1<<(attempt-2))
			jitter := time.Duration(rand.Int63n(int64(delay)/2 + 1))
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case <-time.After(delay + jitter):
			}
		}

		raw, err := a.call(ctx, in, attempt)
		if err != nil {
			// 只有瞬时故障值得重试。4xx、截断这类确定性错误
			// 立刻返回，免得白费调用。
			if !errors.Is(err, ErrRetryable) {
				return Result{}, err
			}
			lastErr = fmt.Errorf("attempt %d: %w", attempt, err)
			continue
		}

		parsed, err := Parse(in.IncidentID, a.Name(), raw)
		if err != nil {
			// 解析失败属于可重试：重试时会收紧 prompt 约束，
			// 有机会拿到合法 JSON。
			lastErr = fmt.Errorf("attempt %d (%s): %w", attempt, describeAttempt(attempt), err)
			continue
		}
		return Result{
			Analysis: parsed.Analysis,
			Title:    parsed.Title,
			Severity: parsed.Severity,
			Category: parsed.Category,
		}, nil
	}

	return Result{}, lastErr
}

func (a *HTTPAnalyzer) call(ctx context.Context, in Input, attempt int) (string, error) {
	body := chatRequest{
		Model: a.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: buildRetryUserPrompt(in, attempt)},
		},
		Temperature: a.cfg.Temperature,
		MaxTokens:   a.cfg.MaxTokens,
	}
	if a.cfg.JSONMode {
		body.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	if attempt >= 3 {
		// 最后一次尝试用确定性采样，减少随机性带来的解析失败。
		body.Temperature = 0
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := a.cfg.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)

	resp, err := a.client.Do(req)
	if err != nil {
		// 网络层面的失败通常是瞬时的，值得重试。
		return "", fmt.Errorf("%w: call llm: %v", ErrRetryable, err)
	}
	defer resp.Body.Close()

	// 限制读取量，避免异常大的响应体占用内存。
	limited := io.LimitReader(resp.Body, 1<<20)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		detail := truncate(string(data), 300)
		// 5xx 和 429 是服务端瞬时状态；4xx 是请求本身有问题，
		// 重试只会重复失败。
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return "", fmt.Errorf("%w: status %d: %s", ErrRetryable, resp.StatusCode, detail)
		}
		return "", fmt.Errorf("llm returned status %d: %s", resp.StatusCode, detail)
	}

	var parsed chatResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("unmarshal chat response: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("llm error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("llm returned no choices")
	}

	choice := parsed.Choices[0]
	if choice.FinishReason == finishReasonLength {
		// 截断属于确定性失败：重试只会得到同样被截断的结果，
		// 所以直接返回错误，由上层记录为分析失败。
		return "", fmt.Errorf("%w: output truncated at max_tokens=%d, raise LLM_MAX_TOKENS",
			ErrTruncated, a.cfg.MaxTokens)
	}

	return choice.Message.Content, nil
}

// finishReasonLength 是服务端表示"达到输出上限"的 finish_reason。
const finishReasonLength = "length"

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
