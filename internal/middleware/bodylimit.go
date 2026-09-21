package middleware

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// MaxBodyBytes 是请求体的上限。
//
// 取 256KB 而不是"字段限制的两倍"。最坏情况下 64KB 日志全是换行，
// JSON 转义后每个 \n 占两个字符，加包装字段是 131082 字节——恰好比
// 128KB（131072）多 10 字节。所以留真正的余量，不卡在算出来的边界上。
const MaxBodyBytes int64 = 256 * 1024

// BodyLimit 给请求体装上流式的大小上限。
//
// 为什么需要它：handler 里的日志长度校验发生在 JSON 解析**之后**，
// 而解析要先把整个请求体读进内存。那条校验保护的是"入库的数据量"，
// 挡不住超大请求——100MB 的 body 会先在内存里展开。
// http.MaxBytesReader 是流式的，读到上限就报错，不会展开整个请求体。
//
// 这里只负责装上限制，不负责生成响应。原因：超限错误是在 handler
// 绑定 JSON 时产生的，那里最清楚发生了什么；中间件事后改状态码时
// 响应头已经提交，改不动。所以 413 由 handler 用
// IsBodyTooLarge 判断后返回。
func BodyLimit(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body == nil || limit <= 0 {
			c.Next()
			return
		}

		// 声明长度就超限时不必读——这是最省的一条路径。
		if c.Request.ContentLength > limit {
			AbortTooLarge(c, limit)
			return
		}

		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

// IsBodyTooLarge 报告错误是否由请求体超限引起。
//
// handler 在自己的绑定逻辑失败后调用它，把"请求过大"与"请求格式
// 错误"区分开——两者的处理方式完全不同，返回给客户端的提示也不同。
func IsBodyTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

// AbortTooLarge 返回 413 而不是 400。
//
// 语义上请求格式没问题，只是太大了。客户端据此能区分"要精简内容"
// 与"请求写错了"。
func AbortTooLarge(c *gin.Context, limit int64) {
	c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
		"error": gin.H{
			"code": "PAYLOAD_TOO_LARGE",
			"message": "请求体超过上限 " + strconv.FormatInt(limit, 10) +
				" 字节。日志字段本身限 64KB，如果内容确实很长，" +
				"请只保留与故障相关的部分。",
			"limit_bytes": limit,
		},
	})
}
