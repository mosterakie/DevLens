// Package middleware 提供 HTTP 中间件。
package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// HeaderRequestID 是请求标识的头部名。
const HeaderRequestID = "X-Request-ID"

// ContextRequestID 是存放在 gin.Context 里的键。
const ContextRequestID = "request_id"

// RequestID 为每个请求生成或透传一个标识。
//
// 这个值会一路带到 worker 的日志里。异步链路没有它就无法把
// "用户看到的结果不对"关联到具体那次分析。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(HeaderRequestID)
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(ContextRequestID, id)
		c.Header(HeaderRequestID, id)
		c.Next()
	}
}

// GetRequestID 取出当前请求的标识。
func GetRequestID(c *gin.Context) string {
	if v, ok := c.Get(ContextRequestID); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
