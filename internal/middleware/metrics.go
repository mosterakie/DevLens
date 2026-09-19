package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mosterakie/DevLens/internal/metrics"
)

// Metrics 记录每个请求的计数与耗时。
//
// 用路由模板（c.FullPath()）而不是实际路径作为标签：
// 否则 /incidents/1 和 /incidents/2 会各生成一条时间序列，
// 标签基数会随数据量无限增长。
func Metrics(reg *metrics.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}

		reg.IncCounter("devlens_http_requests_total", map[string]string{
			"method": c.Request.Method,
			"route":  route,
			"status": strconv.Itoa(c.Writer.Status()),
		})
		reg.ObserveDuration("devlens_http_request_duration_seconds", time.Since(start).Seconds())

		if c.Writer.Status() == 429 {
			reg.IncCounter("devlens_rate_limited_total", map[string]string{"route": route})
		}
	}
}
