package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// Logger 记录结构化访问日志。
//
// 有意不记录请求体：raw_log 可能包含凭据和内网地址。
// 需要内容时用 incident_id 去库里取。
func Logger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		attrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", c.ClientIP(),
			"request_id", GetRequestID(c),
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, "errors", c.Errors.String())
		}

		switch {
		case c.Writer.Status() >= 500:
			log.Error("request", attrs...)
		case c.Writer.Status() >= 400:
			log.Warn("request", attrs...)
		default:
			log.Info("request", attrs...)
		}
	}
}

// Recovery 捕获 panic 并返回 500，避免单个请求打挂进程。
func Recovery(log *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		log.Error("panic recovered",
			"error", recovered,
			"path", c.Request.URL.Path,
			"request_id", GetRequestID(c))
		c.AbortWithStatusJSON(500, gin.H{
			"error": gin.H{"code": "INTERNAL", "message": "internal error"},
		})
	})
}
