package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// Limiter 是限流所需的计数能力。
type Limiter interface {
	RateLimit(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Duration, error)
}

// RateLimitConfig 描述一条限流规则。
type RateLimitConfig struct {
	// Name 用于构造 Redis key，不同规则必须不同。
	Name   string
	Limit  int
	Window time.Duration
	// ByIP 为 true 时按客户端 IP 区分，否则全局共享一个计数。
	ByIP bool
}

// RateLimit 按配置限流。
//
// Redis 出错时放行（fail-open）并记录日志：限流是保护性设施，
// 不是功能性设施。fail-closed 会让核心功能在 Redis 抖动时整体不可用。
// 代价是这段时间可能打满 LLM 配额，第二道防线是客户端的并发上限。
func RateLimit(l Limiter, cfg RateLimitConfig, log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := cfg.Name
		if cfg.ByIP {
			key += ":" + c.ClientIP()
		}

		allowed, remaining, resetIn, err := l.RateLimit(c.Request.Context(), key, cfg.Limit, cfg.Window)
		if err != nil {
			// Redis 出错时放行（fail-open）并记录日志：限流是保护性设施，
			// 不是功能性设施。fail-closed 会让核心功能在 Redis 抖动时
			// 整体不可用。代价是这段时间可能被打满配额，第二道防线是
			// LLM 客户端的并发上限。
			//
			// log 允许为 nil：这个中间件在依赖缺失时也要能用，
			// 强行要求 logger 与 fail-open 的设计矛盾。
			if log != nil {
				log.Warn("rate limit unavailable, allowing request",
					"rule", cfg.Name, "error", err)
			}
			c.Next()
			return
		}

		c.Header("X-RateLimit-Limit", strconv.Itoa(cfg.Limit))
		c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
		if resetIn > 0 {
			c.Header("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(resetIn).Unix(), 10))
		}

		if !allowed {
			if resetIn > 0 {
				c.Header("Retry-After", strconv.Itoa(int(resetIn.Seconds())+1))
			}
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{
					"code":    "RATE_LIMITED",
					"message": "too many requests, please retry later",
				},
			})
			return
		}

		c.Next()
	}
}
