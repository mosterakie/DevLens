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

// Input 是一次分析所需的输入。
type Input struct {
	RawLog     string
	Normalized string
	IncidentID int64
	// RelatedSummaries 是同类历史问题的摘要，供模型参考。
	RelatedSummaries []string
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
