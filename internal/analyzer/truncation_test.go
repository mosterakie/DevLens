package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// finishServer 按设定返回 finish_reason，用于验证截断检测。
type finishServer struct {
	requests int
	replies  []struct {
		status int
		reason string
		body   string
	}
}

func (f *finishServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		idx := f.requests
		f.requests++

		if idx >= len(f.replies) {
			idx = len(f.replies) - 1
		}
		rep := f.replies[idx]

		if rep.status != http.StatusOK {
			w.WriteHeader(rep.status)
			fmt.Fprint(w, `{"error":{"message":"upstream"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]string{"role": "assistant", "content": rep.body},
					"finish_reason": rep.reason,
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newFinishAnalyzer(t *testing.T, srv *httptest.Server, maxAttempts int) *HTTPAnalyzer {
	t.Helper()
	return NewHTTP(HTTPConfig{
		APIKey: "k", Model: "m", BaseURL: srv.URL,
		Timeout: 5 * time.Second, MaxAttempts: maxAttempts,
		JSONMode: true,
	})
}

// TestTruncatedOutputIsNotRetried 是这一轮真实调用暴露出的问题：
// max_tokens 不够时服务端返回 finish_reason=length，内容是断裂的 JSON。
// 截断是确定性的，重试只会得到同样结果，必须立即失败。
func TestTruncatedOutputIsNotRetried(t *testing.T) {
	f := &finishServer{}
	// 三次都返回被截断的内容。
	for i := 0; i < 3; i++ {
		f.replies = append(f.replies, struct {
			status int
			reason string
			body   string
		}{http.StatusOK, "length", `{"title":"half a jso`})
	}
	srv := f.start(t)

	a := newFinishAnalyzer(t, srv, 3)
	_, err := a.Analyze(context.Background(), Input{RawLog: "boom"})

	if err == nil {
		t.Fatal("expected an error for truncated output")
	}
	if !errors.Is(err, ErrTruncated) {
		t.Errorf("error should wrap ErrTruncated, got %v", err)
	}
	if !strings.Contains(err.Error(), "LLM_MAX_TOKENS") {
		t.Errorf("error should tell the operator what to change, got %v", err)
	}
	// 关键断言：只请求了一次，没有白费重试。
	if f.requests != 1 {
		t.Errorf("expected exactly 1 request, got %d", f.requests)
	}
}

// TestNormalFinishIsAccepted 确认 finish_reason=stop 正常通过。
func TestNormalFinishIsAccepted(t *testing.T) {
	f := &finishServer{}
	f.replies = append(f.replies, struct {
		status int
		reason string
		body   string
	}{http.StatusOK, "stop", jsonReply})
	srv := f.start(t)

	a := newFinishAnalyzer(t, srv, 3)
	got, err := a.Analyze(context.Background(), Input{RawLog: "boom"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Analysis.Summary != "deadline exceeded" {
		t.Errorf("unexpected summary: %q", got.Analysis.Summary)
	}
}

// TestServerErrorIsRetried 确认 5xx 会重试。
func TestServerErrorIsRetried(t *testing.T) {
	f := &finishServer{}
	f.replies = append(f.replies,
		struct {
			status int
			reason string
			body   string
		}{http.StatusInternalServerError, "", ""},
		struct {
			status int
			reason string
			body   string
		}{http.StatusOK, "stop", jsonReply},
	)
	srv := f.start(t)

	a := newFinishAnalyzer(t, srv, 3)
	if _, err := a.Analyze(context.Background(), Input{RawLog: "boom"}); err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if f.requests != 2 {
		t.Errorf("expected 2 requests, got %d", f.requests)
	}
}

// TestRateLimitIsRetried 确认 429 会重试。
func TestRateLimitIsRetried(t *testing.T) {
	f := &finishServer{}
	f.replies = append(f.replies,
		struct {
			status int
			reason string
			body   string
		}{http.StatusTooManyRequests, "", ""},
		struct {
			status int
			reason string
			body   string
		}{http.StatusOK, "stop", jsonReply},
	)
	srv := f.start(t)

	a := newFinishAnalyzer(t, srv, 3)
	if _, err := a.Analyze(context.Background(), Input{RawLog: "boom"}); err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if f.requests != 2 {
		t.Errorf("expected 2 requests, got %d", f.requests)
	}
}

// TestClientErrorIsNotRetried 确认 4xx 不重试：请求本身有问题，
// 重试只会重复失败并浪费配额。
func TestClientErrorIsNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := &finishServer{}
			f.replies = append(f.replies, struct {
				status int
				reason string
				body   string
			}{status, "", ""})
			srv := f.start(t)

			a := newFinishAnalyzer(t, srv, 3)
			if _, err := a.Analyze(context.Background(), Input{RawLog: "boom"}); err == nil {
				t.Fatal("expected an error")
			}
			if f.requests != 1 {
				t.Errorf("expected exactly 1 request for %d, got %d", status, f.requests)
			}
		})
	}
}

// TestPromptRequestsConciseOutput 确认提示词限制了输出规模。
// 输出越长越容易触顶被截断。
func TestPromptRequestsConciseOutput(t *testing.T) {
	if !strings.Contains(systemPrompt, "concise") {
		t.Error("system prompt should ask for concise output to reduce truncation risk")
	}
}
