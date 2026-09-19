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

// rule 描述一类常见错误的关键词与对应判断。
type rule struct {
	category string
	severity domain.Severity
	summary  string
	keywords []string
	causes   []string
	actions  []string
}

// rules 按顺序匹配，命中第一条即返回。
//
// 顺序上把更具体的模式放在前面：`context deadline exceeded` 比
// 单纯出现 "timeout" 更能说明问题。
var rules = []rule{
	{
		category: "Database / Timeout",
		severity: domain.SeverityHigh,
		summary:  "The request exceeded its deadline while waiting on the database.",
		keywords: []string{"context deadline exceeded", "pool", "connection refused"},
		causes: []string{
			"Database connection pool exhausted",
			"Slow query holding connections",
			"Connection leak",
			"Network latency between service and database",
		},
		actions: []string{
			"Inspect connection pool utilization",
			"Check for slow queries",
			"Verify connections are released back to the pool",
		},
	},
	{
		category: "Timeout",
		severity: domain.SeverityHigh,
		summary:  "An operation exceeded its configured timeout.",
		keywords: []string{"i/o timeout", "timeout", "deadline"},
		causes: []string{
			"Downstream dependency slower than expected",
			"Timeout threshold set too low",
			"Network latency",
		},
		actions: []string{
			"Identify the slow dependency",
			"Compare current latency with the configured budget",
		},
	},
	{
		category: "Concurrency / Nil",
		severity: domain.SeverityCritical,
		summary:  "The process panicked while handling a request.",
		keywords: []string{"panic", "nil pointer", "invalid memory address"},
		causes: []string{
			"Nil pointer dereference",
			"Uninitialized dependency",
			"Concurrent map access",
		},
		actions: []string{
			"Locate the panic site in the stack trace",
			"Add a regression test for the failing input",
		},
	},
	{
		category: "Network / DNS",
		severity: domain.SeverityHigh,
		summary:  "A hostname could not be resolved.",
		keywords: []string{"no such host", "dns", "lookup"},
		causes: []string{
			"DNS resolution failure",
			"Misconfigured service name",
			"Resolver unreachable",
		},
		actions: []string{
			"Verify the hostname is correct",
			"Check DNS resolver health",
		},
	},
	{
		category: "Auth",
		severity: domain.SeverityMedium,
		summary:  "An authentication or authorization check failed.",
		keywords: []string{"unauthorized", "permission denied", "token expired", "forbidden"},
		causes: []string{
			"Expired credentials",
			"Missing permission on the caller",
			"Clock skew invalidating tokens",
		},
		actions: []string{
			"Check credential expiry",
			"Verify the caller has the required scope",
		},
	},
	{
		category: "Resource / Memory",
		severity: domain.SeverityCritical,
		summary:  "The process ran out of memory.",
		keywords: []string{"out of memory", "oom", "cannot allocate memory"},
		causes: []string{
			"Memory leak",
			"Working set larger than the container limit",
			"Unbounded buffer growth",
		},
		actions: []string{
			"Check memory usage trend",
			"Compare the limit with the actual working set",
		},
	},
}

// Analyze 按关键词给出诊断。
//
// 命中不了任何规则时返回一个明确的"信息不足"结果，
// 而不是编造一个分类——这是这类工具最重要的克制。
func (h *Heuristic) Analyze(_ context.Context, in Input) (Result, error) {
	lower := strings.ToLower(in.RawLog)

	for _, r := range rules {
		token, ok := matchRule(lower, r)
		if !ok {
			continue
		}

		evidence := findEvidence(in.RawLog, token)
		a := &domain.Analysis{
			IncidentID:       in.IncidentID,
			Summary:          r.summary,
			PossibleCauses:   r.causes,
			Evidence:         evidence,
			SuggestedActions: r.actions,
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
			Title:    titleFor(r.category),
			Severity: r.severity,
			Category: r.category,
		}, nil
	}

	a := &domain.Analysis{
		IncidentID:       in.IncidentID,
		Summary:          "The log does not contain enough information to identify a likely cause.",
		PossibleCauses:   []string{"Insufficient detail in the submitted log"},
		Evidence:         nil,
		SuggestedActions: []string{"Include the full stack trace and surrounding context"},
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
		Title:    "Unclassified error",
		Severity: domain.SeverityLow,
		Category: "Unknown",
	}, nil
}

// titleFor 由分类生成一个简短标题。
func titleFor(category string) string {
	if i := strings.LastIndex(category, "/"); i >= 0 {
		return strings.TrimSpace(category[i+1:]) + " issue"
	}
	return category
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
func findEvidence(raw, token string) []domain.Evidence {
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
