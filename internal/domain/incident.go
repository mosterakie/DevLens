package domain

import (
	"errors"
	"strings"
	"time"
)

// 日志长度限制。与 01-product-scope.md 和 06-api.md 中的约定保持一致。
const (
	MinLogBytes = 100
	MaxLogBytes = 64 * 1024 // 64KB
)

// ErrLogTooShort / ErrLogTooLong 用于让 handler 区分错误类型，
// 以便返回对应的错误码而不是笼统的 500。
var (
	ErrLogTooShort = errors.New("log too short")
	ErrLogTooLong  = errors.New("log too long")
	ErrLogEmpty    = errors.New("log is empty")
)

// Incident 是一次错误上报及其分析结果的聚合根。
//
// 字段与 03-data-model.md 的 incidents 表对应。
type Incident struct {
	ID          int64
	Title       string
	RawLog      string
	Normalized  string
	Fingerprint []byte
	Severity    Severity
	Category    string
	Status      Status
	IsRecurring bool
	// Lang 是这条记录诊断内容的语言。
	Lang      Lang
	CreatedBy *int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Evidence 是 AI 在日志中找到的一条具体证据。
// 保留 SourceLine 是为了让用户能核对原文，让诊断可验证。
type Evidence struct {
	Key        string `json:"key"`
	Value      string `json:"value"`
	SourceLine int    `json:"source_line"`
}

// Analysis 是一次 AI 诊断的完整结果。
type Analysis struct {
	IncidentID       int64
	Summary          string
	PossibleCauses   []string
	Evidence         []Evidence
	SuggestedActions []string
	Model            string
	PromptVersion    string
	Confidence       float64
	RawResponse      string
	CreatedAt        time.Time
}

// ValidateLog 校验用户提交的日志。
//
// 注意用 len 而不是 utf8.RuneCountInString：这里的上限是为了防止
// 超大请求，字节数才是真正关心的量。
func ValidateLog(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return ErrLogEmpty
	}
	n := len(raw)
	if n < MinLogBytes {
		return ErrLogTooShort
	}
	if n > MaxLogBytes {
		return ErrLogTooLong
	}
	return nil
}

// Validate 校验 Analysis 的业务约束。
//
// 这里实现 07-reliability.md 中那条业务规则：高置信度必须有证据支撑。
// 模型可以返回 confidence = 0.95 而 evidence 为空，这不该被信任，
// 所以在校验阶段降级而不是报错——降级后仍然是有用的诊断。
func (a *Analysis) Validate() error {
	if strings.TrimSpace(a.Summary) == "" {
		return errors.New("summary is required")
	}
	if a.Confidence < 0 || a.Confidence > 1 {
		return errors.New("confidence must be in [0, 1]")
	}
	a.downgradeUnsupportedConfidence()
	return nil
}

// HighConfidenceWithoutEvidence 是触发降级的阈值。
const HighConfidenceWithoutEvidence = 0.9

// downgradeUnsupportedConfidence 在没有证据时压低置信度。
func (a *Analysis) downgradeUnsupportedConfidence() {
	if a.Confidence > HighConfidenceWithoutEvidence && len(a.Evidence) == 0 {
		a.Confidence = HighConfidenceWithoutEvidence
	}
}
