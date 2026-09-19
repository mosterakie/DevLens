// Package handler 是 HTTP 层：只做绑定、校验、序列化。
//
// 业务规则一律放在 service 层，handler 不判断状态转移是否合法之类的领域问题。
package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mosterakie/DevLens/internal/domain"
	"github.com/mosterakie/DevLens/internal/repository"
	"github.com/mosterakie/DevLens/internal/service"
)

// 错误码。前端靠 code 区分处理方式，message 可直接展示给用户。
const (
	CodeInvalidLog        = "INVALID_LOG"
	CodeNotFound          = "NOT_FOUND"
	CodeInvalidTransition = "INVALID_TRANSITION"
	CodeInvalidRequest    = "INVALID_REQUEST"
	CodeRateLimited       = "RATE_LIMITED"
	CodeInternal          = "INTERNAL"
)

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

func fail(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, errorResponse{Error: errorBody{Code: code, Message: msg}})
}

// failFromError 把领域错误映射成 HTTP 状态码和错误码。
//
// 集中在一处映射，避免每个 handler 各写一份 switch 而逐渐不一致。
func failFromError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		fail(c, http.StatusNotFound, CodeNotFound, "incident not found")

	case errors.Is(err, domain.ErrLogEmpty),
		errors.Is(err, domain.ErrLogTooShort),
		errors.Is(err, domain.ErrLogTooLong):
		fail(c, http.StatusBadRequest, CodeInvalidLog, err.Error())

	case errors.Is(err, service.ErrInvalidTransition):
		fail(c, http.StatusConflict, CodeInvalidTransition, err.Error())

	default:
		fail(c, http.StatusInternalServerError, CodeInternal, "internal error")
	}
}
