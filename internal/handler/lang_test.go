package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mosterakie/DevLens/internal/domain"
)

func langContext(query, accept string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/x?"+query, nil)
	if accept != "" {
		req.Header.Set("Accept-Language", accept)
	}
	c.Request = req
	return c
}

func TestParseLang(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		accept string
		want   domain.Lang
	}{
		{"都没给时用默认", "", "", domain.LangZhHans},
		{"查询参数优先", "lang=en", "zh-CN", domain.LangEn},
		{"查询参数中文", "lang=zh", "", domain.LangZhHans},
		{"Accept-Language 中文", "", "zh-CN,zh;q=0.9", domain.LangZhHans},
		{"Accept-Language 英文", "", "en-US,en;q=0.9", domain.LangEn},
		{"Accept-Language 不支持的退回默认", "", "fr-FR,fr;q=0.9", domain.LangZhHans},
		{"查询参数无效时退回默认", "lang=fr", "", domain.LangZhHans},
		{"空查询参数时用头", "lang=", "en", domain.LangEn},
		{"只有分号的质量值", "", "zh;q=0.8", domain.LangZhHans},
		{"带空格的标签", "", "  en  ", domain.LangEn},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLang(langContext(tt.query, tt.accept))
			if got != tt.want {
				t.Errorf("parseLang = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestParseLangNeverFails 确认语言解析不会产生非法值。
//
// 语言偏好不该让请求失败，任何输入都要落到一个受支持的值上。
// 这里直接测域名层的解析，避免把空格等字符塞进 URL。
func TestParseLangNeverFails(t *testing.T) {
	for _, in := range []string{
		"", " ", "!!!", "中文", "zh-Hant-TW", "en-GB-oed", ";;q=1", ",,,",
		"\t", "ZH", "en;q=0", "und",
	} {
		got := domain.ParseLang(in)
		if !got.IsValid() {
			t.Errorf("input %q produced invalid lang %q", in, got)
		}
	}
}

// TestAcceptLanguageHeaderEdgeCases 覆盖头部解析的边界。
func TestAcceptLanguageHeaderEdgeCases(t *testing.T) {
	tests := []struct {
		accept string
		want   domain.Lang
	}{
		{"", domain.LangZhHans},
		{"*", domain.LangZhHans},
		{"en,zh", domain.LangEn},          // 取第一个
		{"zh-Hans-CN", domain.LangZhHans}, // 主语言 zh
		{"EN-us", domain.LangEn},          // 大小写无关
	}

	for _, tt := range tests {
		t.Run(tt.accept, func(t *testing.T) {
			got := parseLang(langContext("", tt.accept))
			if got != tt.want {
				t.Errorf("accept %q -> %q, want %q", tt.accept, got, tt.want)
			}
		})
	}
}
