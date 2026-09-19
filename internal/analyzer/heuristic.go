package analyzer

import (
	"context"
	"strings"

	"github.com/mosterakie/DevLens/internal/domain"
)

// Heuristic 是一个不调用外部模型的本地实现。
//
// 它的存在不是为了替代模型，而是让整条链路（提交 → 队列 → worker →
// 落库 → 轮询可见）在没有 API key 的环境下也能端到端验证。
// 判断依据是关键词匹配，覆盖面有限，结果会带较低的 confidence。
type Heuristic struct{}

// NewHeuristic 构造本地分析器。
func NewHeuristic() *Heuristic { return &Heuristic{} }

// Name 返回实现标识，会记入 analysis.model。
func (h *Heuristic) Name() string { return "heuristic-v1" }

// rule 描述一类常见错误的关键词与分类。
//
// 文案不放在这里：它随语言变化，而判定逻辑不变，
// 所以分开到 heuristic_text.go。
type rule struct {
	category string
	severity domain.Severity
	keywords []string
}

// rules 按顺序匹配，命中第一条即返回。
//
// 顺序上把更具体的模式放在前面：`context deadline exceeded` 比
// 单纯出现 "timeout" 更能说明问题。
var rules = []rule{
	{
		category: "Database / Timeout",
		severity: domain.SeverityHigh,
		keywords: []string{"context deadline exceeded", "pool", "connection refused"},
	},
	{
		category: "Timeout",
		severity: domain.SeverityHigh,
		keywords: []string{"i/o timeout", "timeout", "deadline"},
	},
	{
		category: "Concurrency / Nil",
		severity: domain.SeverityCritical,
		keywords: []string{"panic", "nil pointer", "invalid memory address"},
	},
	{
		category: "Network / DNS",
		severity: domain.SeverityHigh,
		keywords: []string{"no such host", "dns", "lookup"},
	},
	{
		category: "Auth",
		severity: domain.SeverityMedium,
		keywords: []string{"unauthorized", "permission denied", "token expired", "forbidden"},
	},
	{
		category: "Resource / Memory",
		severity: domain.SeverityCritical,
		keywords: []string{"out of memory", "oom", "cannot allocate memory"},
	},
}

// Analyze 按关键词给出诊断。
//
// 命中不了任何规则时返回一个明确的"信息不足"结果，
// 而不是编造一个分类——这是这类工具最重要的克制。
func (h *Heuristic) Analyze(_ context.Context, in Input) (Result, error) {
	lang := in.LangOr()
	lower := strings.ToLower(in.RawLog)

	for _, r := range rules {
		token, ok := matchRule(lower, r)
		if !ok {
			continue
		}

		text := textFor(r.category, lang)
		evidence := findEvidence(in.RawLog, token, lang)

		a := &domain.Analysis{
			IncidentID:       in.IncidentID,
			Summary:          text.summary,
			PossibleCauses:   text.causes,
			Evidence:         evidence,
			SuggestedActions: text.actions,
			Model:            h.Name(),
			PromptVersion:    PromptVersion,
			Confidence:       heuristicConfidence(evidence),
			RawResponse:      "matched keyword: " + token,
		}
		if err := a.Validate(); err != nil {
			return Result{}, err
		}
		return Result{
			Analysis: a,
			Title:    text.titles,
			Severity: r.severity,
			Category: r.category,
		}, nil
	}

	text := unknownText(lang)
	a := &domain.Analysis{
		IncidentID:       in.IncidentID,
		Summary:          text.summary,
		PossibleCauses:   text.causes,
		Evidence:         nil,
		SuggestedActions: text.actions,
		Model:            h.Name(),
		PromptVersion:    PromptVersion,
		Confidence:       0.2,
		RawResponse:      "no rule matched",
	}
	if err := a.Validate(); err != nil {
		return Result{}, err
	}
	return Result{
		Analysis: a,
		Title:    text.titles,
		Severity: domain.SeverityLow,
		Category: "Unknown",
	}, nil
}

func matchRule(lower string, r rule) (string, bool) {
	for _, k := range r.keywords {
		if strings.Contains(lower, k) {
			return k, true
		}
	}
	return "", false
}

// heuristicConfidence 有证据时给较高置信度，没有则压低。
//
// 这样 confidence 才有意义地波动，而不是恒为某个常数。
func heuristicConfidence(evidence []domain.Evidence) float64 {
	switch {
	case len(evidence) >= 2:
		return 0.75
	case len(evidence) == 1:
		return 0.6
	default:
		return 0.4
	}
}

// findEvidence 在日志里找出包含关键词的那一行，记录行号。
//
// 行号让用户能直接核对原文。没有可核对证据的诊断是不可验证的断言。
// value 始终引用日志原文，不翻译——证据的意义就是可核对。
func findEvidence(raw, token string, _ domain.Lang) []domain.Evidence {
	lines := strings.Split(raw, "\n")
	var out []domain.Evidence

	for i, line := range lines {
		if !strings.Contains(strings.ToLower(line), token) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		out = append(out, domain.Evidence{
			Key:        token,
			Value:      truncate(trimmed, 120),
			SourceLine: i + 1,
		})
		if len(out) >= 3 {
			break
		}
	}
	return out
}
