package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// fakeLimiter 让测试控制限流判定，不需要 Redis。
type fakeLimiter struct {
	allowed   bool
	remaining int
	resetIn   time.Duration
	err       error

	// 记录收到的 key，用于验证 ByIP 是否生效。
	calls []string
}

func (f *fakeLimiter) RateLimit(_ context.Context, key string, _ int, _ time.Duration) (bool, int, time.Duration, error) {
	f.calls = append(f.calls, key)
	return f.allowed, f.remaining, f.resetIn, f.err
}

func newRouter(handler gin.HandlerFunc, mws ...gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	all := append(mws, handler)
	r.GET("/x", all...)
	return r
}

func TestRateLimitAllows(t *testing.T) {
	lim := &fakeLimiter{allowed: true, remaining: 7, resetIn: 30 * time.Second}
	r := newRouter(func(c *gin.Context) { c.String(http.StatusOK, "ok") },
		RateLimit(lim, RateLimitConfig{Name: "test", Limit: 10, Window: time.Minute}, nil))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))

	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d, want 200", w.Code)
	}
	// 响应头要能让客户端知道剩余额度。
	if got := w.Header().Get("X-RateLimit-Limit"); got != "10" {
		t.Errorf("X-RateLimit-Limit = %q, want 10", got)
	}
	if got := w.Header().Get("X-RateLimit-Remaining"); got != "7" {
		t.Errorf("X-RateLimit-Remaining = %q, want 7", got)
	}
	if got := w.Header().Get("X-RateLimit-Reset"); got == "" {
		t.Error("应当设置 X-RateLimit-Reset")
	}
}

func TestRateLimitBlocks(t *testing.T) {
	lim := &fakeLimiter{allowed: false, remaining: 0, resetIn: 42 * time.Second}
	r := newRouter(func(c *gin.Context) { c.String(http.StatusOK, "ok") },
		RateLimit(lim, RateLimitConfig{Name: "test", Limit: 10, Window: time.Minute}, nil))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("状态码 = %d, want 429", w.Code)
	}
	// Retry-After 让客户端知道等多久，比只给 429 有用。
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Error("应当设置 Retry-After")
	}
	// 响应体要带错误码，前端靠它区分"限流"与"参数错"。
	if !strings.Contains(w.Body.String(), "RATE_LIMITED") {
		t.Errorf("响应体应含 RATE_LIMITED, got %s", w.Body.String())
	}
	// 被限流后不该继续走到 handler。
	if strings.Contains(w.Body.String(), `"ok"`) {
		t.Error("被限流的请求不应到达 handler")
	}
}

// TestRateLimitFailsOpen 是刻意的设计：Redis 故障时放行。
//
// 限流是保护性设施而不是功能性设施。fail-closed 会让核心功能在
// Redis 抖动时整体不可用，代价大于被多打几次。
func TestRateLimitFailsOpen(t *testing.T) {
	lim := &fakeLimiter{err: errors.New("redis down")}
	r := newRouter(func(c *gin.Context) { c.String(http.StatusOK, "ok") },
		RateLimit(lim, RateLimitConfig{Name: "test", Limit: 10, Window: time.Minute}, nil))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))

	if w.Code != http.StatusOK {
		t.Errorf("Redis 故障时应当放行, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ok") {
		t.Error("应当执行到 handler")
	}
}

// TestRateLimitByIPUsesDistinctKeys 确认 ByIP 会把客户端区分开。
//
// 不区分会让一个用户把所有人的额度用完。
func TestRateLimitByIPUsesDistinctKeys(t *testing.T) {
	lim := &fakeLimiter{allowed: true, remaining: 1, resetIn: time.Second}
	r := newRouter(func(c *gin.Context) { c.String(http.StatusOK, "ok") },
		RateLimit(lim, RateLimitConfig{Name: "test", Limit: 10, Window: time.Minute, ByIP: true}, nil))

	for _, ip := range []string{"10.0.0.1:1234", "10.0.0.2:5678"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/x", nil)
		req.RemoteAddr = ip
		r.ServeHTTP(w, req)
	}

	if len(lim.calls) != 2 {
		t.Fatalf("应当调用 2 次, got %d", len(lim.calls))
	}
	if lim.calls[0] == lim.calls[1] {
		t.Errorf("不同的 IP 应当产生不同的 key, 都是 %q", lim.calls[0])
	}
}

// TestRateLimitGlobalSharesKey 确认 ByIP=false 时所有请求共用一个 key。
func TestRateLimitGlobalSharesKey(t *testing.T) {
	lim := &fakeLimiter{allowed: true, remaining: 1, resetIn: time.Second}
	r := newRouter(func(c *gin.Context) { c.String(http.StatusOK, "ok") },
		RateLimit(lim, RateLimitConfig{Name: "global", Limit: 100, Window: time.Minute, ByIP: false}, nil))

	for _, ip := range []string{"10.0.0.1:1234", "10.0.0.2:5678"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/x", nil)
		req.RemoteAddr = ip
		r.ServeHTTP(w, req)
	}

	if len(lim.calls) != 2 {
		t.Fatalf("应当调用 2 次, got %d", len(lim.calls))
	}
	if lim.calls[0] != lim.calls[1] {
		t.Errorf("全局规则应当共用一个 key, got %q 与 %q", lim.calls[0], lim.calls[1])
	}
}

// TestRateLimitNilLogger 确认 log 传 nil 不会 panic。
//
// fail-open 路径会用到 logger；把它写成必需依赖会让调用方
// 为了传一个 logger 而构造无意义的对象。
func TestRateLimitNilLogger(t *testing.T) {
	lim := &fakeLimiter{err: errors.New("boom")}
	r := newRouter(func(c *gin.Context) { c.String(http.StatusOK, "ok") },
		RateLimit(lim, RateLimitConfig{Name: "test", Limit: 1, Window: time.Minute}, nil))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != http.StatusOK {
		t.Errorf("状态码 = %d, want 200", w.Code)
	}
}
