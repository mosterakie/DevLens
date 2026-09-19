package handler

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mosterakie/DevLens/internal/domain"
)

// parseLang 决定这次请求应当用哪种语言生成诊断。
//
// 优先级：显式查询参数 > Accept-Language 头 > 默认语言。
// 语言偏好不该让请求失败，所以无法识别时一律退回默认值。
func parseLang(c *gin.Context) domain.Lang {
	if v := c.Query("lang"); v != "" {
		return domain.ParseLang(v)
	}
	return domain.ParseLang(primaryLanguageTag(c.GetHeader("Accept-Language")))
}

// primaryLanguageTag 从 Accept-Language 里取出第一个标签。
//
// 头部形如 "zh-CN,zh;q=0.9,en;q=0.8"。这里只取第一个标签并按
// 主语言匹配，不做 q 值排序：客户端已经按优先级排好了顺序，
// 而我们只区分中英两种语言，完整实现没有必要。
func primaryLanguageTag(header string) string {
	if header == "" {
		return ""
	}
	first, _, _ := strings.Cut(header, ",")
	tag, _, _ := strings.Cut(first, ";")
	return strings.TrimSpace(tag)
}
