package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// echoHandler 模拟真实 handler：绑定 JSON，失败时用
// IsBodyTooLarge 区分"太大"与"格式错"。
func echoHandler(c *gin.Context) {
	var in struct {
		Log string `json:"log"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		if IsBodyTooLarge(err) {
			AbortTooLarge(c, MaxBodyBytes)
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "INVALID_REQUEST"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"len": len(in.Log)})
}

func bodyRouter(limit int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/x", BodyLimit(limit), echoHandler)
	return r
}

func post(r *gin.Engine, body string, chunked bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/x", strings.NewReader(body))
	if chunked {
		req.ContentLength = -1
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestBodyLimitAllowsUnderLimit(t *testing.T) {
	r := bodyRouter(1024)
	body := `{"log":"` + strings.Repeat("a", 100) + `"}`

	if w := post(r, body, false); w.Code != http.StatusOK {
		t.Errorf("状态码 = %d, want 200", w.Code)
	}
}

// TestBodyLimitRejectsDeclaredOversize 覆盖 Content-Length 就超限的路径。
func TestBodyLimitRejectsDeclaredOversize(t *testing.T) {
	const limit = 1024
	r := bodyRouter(limit)
	body := `{"log":"` + strings.Repeat("a", limit*2) + `"}`

	w := post(r, body, false)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("状态码 = %d, want 413", w.Code)
	}
	if !strings.Contains(w.Body.String(), "PAYLOAD_TOO_LARGE") {
		t.Errorf("响应应含错误码: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "limit_bytes") {
		t.Errorf("响应应含 limit_bytes，前端要靠它给出准确提示: %s", w.Body.String())
	}
}

// TestBodyLimitRejectsChunkedOversize 覆盖没有 Content-Length 的情况。
//
// 这时 ContentLength 是 -1，只能在读取过程中发现超限。
// 这条路径原先完全没被保护。
func TestBodyLimitRejectsChunkedOversize(t *testing.T) {
	const limit = 512
	r := bodyRouter(limit)
	body := `{"log":"` + strings.Repeat("a", limit*3) + `"}`

	w := post(r, body, true)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("状态码 = %d, want 413（chunked 请求也要受限）", w.Code)
	}
	if !strings.Contains(w.Body.String(), "PAYLOAD_TOO_LARGE") {
		t.Errorf("响应应含错误码: %s", w.Body.String())
	}
}

// TestBodyLimitDistinguishesFormatError 确认格式错误仍返回 400。
//
// 把"太大"和"格式错"混成一个状态码，客户端就无法给出正确提示。
func TestBodyLimitDistinguishesFormatError(t *testing.T) {
	r := bodyRouter(1024)

	w := post(r, `{"log": not json`, false)
	if w.Code != http.StatusBadRequest {
		t.Errorf("格式错误应当返回 400, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "PAYLOAD_TOO_LARGE") {
		t.Error("格式错误不该报成超限")
	}
}

// TestBodyLimitAtExactBoundary 确认恰好等于上限时放行。
//
// 差一字节的 bug 在生产里的表现是"偶发拒绝"，很难追查。
func TestBodyLimitAtExactBoundary(t *testing.T) {
	const limit = 200
	r := bodyRouter(limit)

	prefix, suffix := `{"log":"`, `"}`
	pad := limit - len(prefix) - len(suffix)
	body := prefix + strings.Repeat("a", pad) + suffix
	if len(body) != limit {
		t.Fatalf("测试数据长度 %d，应当等于 %d", len(body), limit)
	}

	if w := post(r, body, false); w.Code != http.StatusOK {
		t.Errorf("恰好等于上限应当放行, got %d", w.Code)
	}
}

func TestBodyLimitOverBoundaryByOne(t *testing.T) {
	const limit = 200
	r := bodyRouter(limit)

	prefix, suffix := `{"log":"`, `"}`
	pad := limit + 1 - len(prefix) - len(suffix)
	body := prefix + strings.Repeat("a", pad) + suffix
	if len(body) != limit+1 {
		t.Fatalf("测试数据长度 %d，应当为 %d", len(body), limit+1)
	}

	if w := post(r, body, false); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("超出 1 字节应当被拒绝, got %d", w.Code)
	}
}

// TestBodyLimitEmptyBody 确认空 body 不会 panic 或 500。
func TestBodyLimitEmptyBody(t *testing.T) {
	r := bodyRouter(1024)

	req := httptest.NewRequest("POST", "/x", nil)
	req.Body = nil
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusInternalServerError {
		t.Errorf("空 body 不该导致 500: %s", w.Body.String())
	}
}

// TestMaxBodyBytesLeavesRoomForJSON 确认上限与字段限制的关系。
//
// 日志字段限 64KB，而 JSON 转义会让换行变成 \n（两个字符）。
// 把上限设成与字段限制相同会把合法请求挡在外面——这是最容易做错的地方。
func TestMaxBodyBytesLeavesRoomForJSON(t *testing.T) {
	const fieldLimit = 64 * 1024

	if MaxBodyBytes <= fieldLimit {
		t.Fatalf("上限 %d 必须大于字段限制 %d", MaxBodyBytes, fieldLimit)
	}

	// 最坏情况：64KB 全是换行。
	raw := strings.Repeat("\n", fieldLimit)
	escaped := strings.ReplaceAll(raw, "\n", `\n`)
	bodyLen := len(`{"log":"`) + len(escaped) + len(`"}`)

	if int64(bodyLen) > MaxBodyBytes {
		t.Errorf("最坏情况下 64KB 日志的 body 是 %d 字节，超过上限 %d",
			bodyLen, MaxBodyBytes)
	}
}

// TestIsBodyTooLarge 确认判定函数本身的行为。
func TestIsBodyTooLarge(t *testing.T) {
	if IsBodyTooLarge(nil) {
		t.Error("nil 不该被判为超限")
	}
	if IsBodyTooLarge(errors.New("syntax error")) {
		t.Error("普通错误不该被判为超限")
	}
	if !IsBodyTooLarge(&http.MaxBytesError{Limit: 100}) {
		t.Error("MaxBytesError 应当被判为超限")
	}
	// 包装过的错误也要能识别——绑定逻辑常会再包一层。
	wrapped := fmt.Errorf("bind failed: %w", &http.MaxBytesError{Limit: 100})
	if !IsBodyTooLarge(wrapped) {
		t.Error("包装后的 MaxBytesError 应当能被识别")
	}
}
