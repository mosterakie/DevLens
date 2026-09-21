package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mosterakie/DevLens/internal/metrics"
)

func TestRequestIDGenerates(t *testing.T) {
	r := newRouter(func(c *gin.Context) { c.String(http.StatusOK, "ok") }, RequestID())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))

	id := w.Header().Get(HeaderRequestID)
	if id == "" {
		t.Fatal("应当生成 request id")
	}
}

// TestRequestIDPropagates 确认客户端传进来的 id 会被沿用。
//
// 分布式追踪依赖这一点：上游传了 id，下游就该接着用，
// 而不是另起一个。
func TestRequestIDPropagates(t *testing.T) {
	const given = "trace-from-upstream"
	r := newRouter(func(c *gin.Context) { c.String(http.StatusOK, "ok") }, RequestID())

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set(HeaderRequestID, given)
	r.ServeHTTP(w, req)

	if got := w.Header().Get(HeaderRequestID); got != given {
		t.Errorf("request id = %q, want %q", got, given)
	}
}

// TestRequestIDDistinct 确认不同请求拿到不同的 id。
func TestRequestIDDistinct(t *testing.T) {
	seen := map[string]bool{}
	r := newRouter(func(c *gin.Context) { c.String(http.StatusOK, "ok") }, RequestID())

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
		id := w.Header().Get(HeaderRequestID)
		if seen[id] {
			t.Fatalf("request id 重复: %q", id)
		}
		seen[id] = true
	}
}

// TestGetRequestID 确认 handler 内能取到中间件写入的值。
func TestGetRequestID(t *testing.T) {
	var inside string
	r := newRouter(func(c *gin.Context) {
		inside = GetRequestID(c)
		c.String(http.StatusOK, "ok")
	}, RequestID())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))

	if inside == "" {
		t.Error("handler 内应当能取到 request id")
	}
	if inside != w.Header().Get(HeaderRequestID) {
		t.Error("handler 内取到的 id 应与响应头一致")
	}
}

// TestGetRequestIDWithoutMiddleware 确认没有中间件时返回空串而不是 panic。
func TestGetRequestIDWithoutMiddleware(t *testing.T) {
	var inside string
	r := newRouter(func(c *gin.Context) {
		inside = GetRequestID(c)
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))

	if inside != "" {
		t.Errorf("无中间件时应返回空串, got %q", inside)
	}
}

// TestRecoveryCatchesPanic 确认 panic 不会打挂进程。
func TestRecoveryCatchesPanic(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	r := newRouter(func(c *gin.Context) { panic("boom") }, Recovery(log))

	w := httptest.NewRecorder()
	// 不 recover 的话这里会直接崩溃，测试进程结束。
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("状态码 = %d, want 500", w.Code)
	}
	if !strings.Contains(w.Body.String(), "INTERNAL") {
		t.Errorf("响应体应含错误码, got %s", w.Body.String())
	}
	// 日志里要留下记录，否则线上只会看到一个 500 而没有线索。
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("日志应记录 panic 内容, got %s", buf.String())
	}
}

// TestLoggerRecordsStatus 确认访问日志带上了关键字段。
func TestLoggerRecordsStatus(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	r := newRouter(func(c *gin.Context) { c.String(http.StatusTeapot, "x") },
		RequestID(), Logger(log))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("日志不是合法 JSON: %v (%s)", err, buf.String())
	}
	if entry["status"] != float64(http.StatusTeapot) {
		t.Errorf("status = %v, want 418", entry["status"])
	}
	if entry["path"] != "/x" {
		t.Errorf("path = %v, want /x", entry["path"])
	}
	if entry["request_id"] == nil || entry["request_id"] == "" {
		t.Error("日志应当带上 request_id，否则异步链路无法关联")
	}
}

// TestLoggerDoesNotRecordBody 确认日志里不含请求体。
//
// raw_log 可能包含凭据和内网地址，写进日志等于把它们扩散到
// 日志系统里。
func TestLoggerDoesNotRecordBody(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	secret := "PASSWORD=supersecret"
	r := newRouter(func(c *gin.Context) { c.String(http.StatusOK, "ok") }, Logger(log))

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/x", strings.NewReader(secret))
	r.ServeHTTP(w, req)

	if strings.Contains(buf.String(), "supersecret") {
		t.Errorf("日志不应包含请求体内容: %s", buf.String())
	}
}

// TestMetricsCounts 确认请求被计入指标。
func TestMetricsCounts(t *testing.T) {
	reg := metrics.New()
	r := newRouter(func(c *gin.Context) { c.String(http.StatusOK, "ok") }, Metrics(reg))

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	}

	out := reg.Render()
	if !strings.Contains(out, "devlens_http_requests_total") {
		t.Fatalf("缺少请求计数指标:\n%s", out)
	}
	if !strings.Contains(out, " 3") {
		t.Errorf("计数应为 3:\n%s", out)
	}
}

// TestMetricsUsesRouteTemplate 确认标签用路由模板而不是实际路径。
//
// 用实际路径会让 /incidents/1 和 /incidents/2 各生成一条时间序列，
// 标签基数随数据量无限增长。
func TestMetricsUsesRouteTemplate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := metrics.New()
	r := gin.New()
	r.Use(Metrics(reg))
	r.GET("/incidents/:id", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	for _, id := range []string{"1", "2", "3"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/incidents/"+id, nil))
	}

	out := reg.Render()
	if !strings.Contains(out, `route="/incidents/:id"`) {
		t.Errorf("应当使用路由模板作为标签:\n%s", out)
	}
	if strings.Contains(out, `route="/incidents/1"`) {
		t.Errorf("不应使用具体路径:\n%s", out)
	}
}
