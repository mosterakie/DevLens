// Package analyzer 负责把日志交给模型并解析结构化诊断。
//
// 这个包不依赖 DB / Redis / HTTP，便于测试和替换实现。
package analyzer

import (
	"context"
	"errors"

	"github.com/mosterakie/DevLens/internal/domain"
)

// ErrInvalidResponse 表示模型输出无法解析成期望结构，
// 或解析成功但未通过校验。
var ErrInvalidResponse = errors.New("invalid analysis response")

// ErrTruncated 表示模型输出达到 max_tokens 上限被截断。
//
// 与 ErrInvalidResponse 分开，是因为它不该被重试：截断是确定性的，
// 重试会得到同样的结果，只会白费两次调用。遇到它应当调高上限。
var ErrTruncated = errors.New("analysis output truncated")

// ErrRetryable 标记瞬时故障（网络超时、5xx、429），值得退避后重试。
//
// 与它相对的是确定性错误（4xx、截断、schema 不符），重试不会改变结果。
var ErrRetryable = errors.New("retryable upstream error")

// Input 是一次分析所需的输入。
type Input struct {
	RawLog     string
	Normalized string
	IncidentID int64
	// RelatedSummaries 是同类历史问题的摘要，供模型参考。
	RelatedSummaries []string

	// Lang 决定诊断内容使用哪种语言。零值表示未指定，
	// 由 LangOr 兜底成默认语言。
	Lang domain.Lang
}

// LangOr 返回有效的语言，未设置时给出默认值。
func (in Input) LangOr() domain.Lang {
	if in.Lang.IsValid() {
		return in.Lang
	}
	return domain.DefaultLang()
}

// Result 是一次分析的完整产出。
//
// domain.Analysis 只对应 incident_analysis 表，而 title / Severity /
// Category 归属于 incidents 表，所以需要在结果里一并带出。
type Result struct {
	Analysis *domain.Analysis
	Title    string
	Severity domain.Severity
	Category string
}

// Analyzer 是分析能力的抽象。
type Analyzer interface {
	// Name 返回实现标识，用于记录到 analysis.model。
	Name() string
	Analyze(ctx context.Context, in Input) (Result, error)
}
