package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// captureServer 记录收到的请求体，并按设定返回响应。
type captureServer struct {
	bodies   []map[string]any
	statuses []int
	replies  []string
	requests int
}

func (c *captureServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		c.bodies = append(c.bodies, parsed)

		idx := c.requests
		c.requests++

		status := http.StatusOK
		if idx < len(c.statuses) {
			status = c.statuses[idx]
		}
		reply := ""
		if idx < len(c.replies) {
			reply = c.replies[idx]
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":{"message":"upstream failure"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		payload := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": reply}},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)
	return srv
}

const jsonReply = `{
  "title": "DB timeout", "severity": "HIGH", "category": "Database / Timeout",
  "summary": "deadline exceeded", "possible_causes": ["pool exhausted"],
  "evidence": [{"key": "timeout", "value": "5s", "source_line": 3}],
  "suggested_actions": ["check pool"], "confidence": 0.8
}`

func newTestAnalyzer(t *testing.T, srv *httptest.Server, jsonMode bool) *HTTPAnalyzer {
	t.Helper()
	return NewHTTP(HTTPConfig{
		APIKey:      "test-key",
		Model:       "deepseek-flash",
		BaseURL:     srv.URL,
		Timeout:     5 * time.Second,
		MaxAttempts: 3,
		MaxTokens:   2048,
		JSONMode:    jsonMode,
	})
}

// TestRequestShape 确认请求发往 /chat/completions，且带上认证头。
func TestRequestShape(t *testing.T) {
	c := &captureServer{replies: []string{jsonReply}}
	srv := c.start(t)

	a := newTestAnalyzer(t, srv, false)
	if _, err := a.Analyze(context.Background(), Input{RawLog: "boom"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(c.bodies) != 1 {
		t.Fatalf("expected 1 request, got %d", len(c.bodies))
	}
	body := c.bodies[0]
	if body["model"] != "deepseek-flash" {
		t.Errorf("model = %v", body["model"])
	}
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("expected system + user messages, got %v", body["messages"])
	}
}

// TestJSONModeSendsResponseFormat 确认开启时发送 response_format。
func TestJSONModeSendsResponseFormat(t *testing.T) {
	c := &captureServer{replies: []string{jsonReply}}
	srv := c.start(t)

	a := newTestAnalyzer(t, srv, true)
	if _, err := a.Analyze(context.Background(), Input{RawLog: "boom"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rf, ok := c.bodies[0]["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing: %v", c.bodies[0])
	}
	if rf["type"] != "json_object" {
		t.Errorf("response_format.type = %v, want json_object", rf["type"])
	}
}

// TestJSONModeDisabledOmitsResponseFormat 确认关闭时不发送。
// 不是所有兼容服务都接受这个字段，误发会导致 400。
func TestJSONModeDisabledOmitsResponseFormat(t *testing.T) {
	c := &captureServer{replies: []string{jsonReply}}
	srv := c.start(t)

	a := newTestAnalyzer(t, srv, false)
	if _, err := a.Analyze(context.Background(), Input{RawLog: "boom"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, present := c.bodies[0]["response_format"]; present {
		t.Error("response_format should be omitted when JSONMode is off")
	}
}

// TestSystemPromptMentionsJSON 是 DeepSeek JSON Output 的硬性要求：
// prompt 里必须出现 json 字样并提供格式示例，否则该参数不生效。
func TestSystemPromptMentionsJSON(t *testing.T) {
	lower := strings.ToLower(systemPrompt)
	if !strings.Contains(lower, "json") {
		t.Error("system prompt must mention json for DeepSeek JSON Output to work")
	}
	if !strings.Contains(systemPrompt, "{") {
		t.Error("system prompt should include a JSON shape example")
	}
}

// TestRetriesOnEmptyContent 覆盖 DeepSeek 文档提到的已知问题：
// JSON Output 偶尔返回空 content。空内容必须触发重试而不是直接失败。
func TestRetriesOnEmptyContent(t *testing.T) {
	c := &captureServer{replies: []string{"", "   ", jsonReply}}
	srv := c.start(t)

	a := newTestAnalyzer(t, srv, true)
	got, err := a.Analyze(context.Background(), Input{RawLog: "boom"})
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if got.Analysis.Summary != "deadline exceeded" {
		t.Errorf("unexpected summary: %q", got.Analysis.Summary)
	}
	if c.requests != 3 {
		t.Errorf("expected 3 attempts, got %d", c.requests)
	}
}

// TestRetriesOnMalformedJSON 确认非法 JSON 会重试。
func TestRetriesOnMalformedJSON(t *testing.T) {
	c := &captureServer{replies: []string{"not json", jsonReply}}
	srv := c.start(t)

	a := newTestAnalyzer(t, srv, true)
	if _, err := a.Analyze(context.Background(), Input{RawLog: "boom"}); err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if c.requests != 2 {
		t.Errorf("expected 2 attempts, got %d", c.requests)
	}
}

// TestGivesUpAfterMaxAttempts 确认超过重试上限后返回错误。
func TestGivesUpAfterMaxAttempts(t *testing.T) {
	c := &captureServer{replies: []string{"bad", "bad", "bad"}}
	srv := c.start(t)

	a := newTestAnalyzer(t, srv, true)
	if _, err := a.Analyze(context.Background(), Input{RawLog: "boom"}); err == nil {
		t.Fatal("expected an error after exhausting attempts")
	}
	if c.requests != 3 {
		t.Errorf("expected 3 attempts, got %d", c.requests)
	}
}

// TestHTTPErrorNotSwallowed 确认服务端错误会被返回而不是被吞掉。
func TestHTTPErrorNotSwallowed(t *testing.T) {
	c := &captureServer{statuses: []int{http.StatusUnauthorized}}
	srv := c.start(t)

	a := newTestAnalyzer(t, srv, true)
	_, err := a.Analyze(context.Background(), Input{RawLog: "boom"})
	if err == nil {
		t.Fatal("expected an error for 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention the status code, got %v", err)
	}
}

// TestRequestCarriesAuthHeader 确认认证头存在。
func TestRequestCarriesAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, jsonReply)
	}))
	defer srv.Close()

	a := NewHTTP(HTTPConfig{
		APIKey: "sk-test", Model: "m", BaseURL: srv.URL,
		Timeout: 5 * time.Second, MaxAttempts: 1,
	})
	if _, err := a.Analyze(context.Background(), Input{RawLog: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}
