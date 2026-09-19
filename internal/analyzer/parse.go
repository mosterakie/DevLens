package analyzer

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mosterakie/DevLens/internal/domain"
)

// rawAnalysis 对应模型返回的 JSON 结构。
//
// 与 domain.Analysis 分开定义：前者是外部输入的形状，
// 后者是内部领域模型。外部格式变化时不会污染领域层。
type rawAnalysis struct {
	Title            string        `json:"title"`
	Severity         string        `json:"severity"`
	Category         string        `json:"category"`
	Summary          string        `json:"summary"`
	PossibleCauses   []string      `json:"possible_causes"`
	Evidence         []rawEvidence `json:"evidence"`
	SuggestedActions []string      `json:"suggested_actions"`
	Confidence       float64       `json:"confidence"`
}

type rawEvidence struct {
	Key        string `json:"key"`
	Value      string `json:"value"`
	SourceLine int    `json:"source_line"`
}

// Parsed 是一次解析的完整结果。
//
// domain.Analysis 里没有 title / severity / category ——
// 那三个字段存在 incidents 表而不是 incident_analysis 表，
// 所以需要单独带出来。
type Parsed struct {
	Analysis *domain.Analysis
	Title    string
	Severity domain.Severity
	Category string
}

// Parse 把模型的原始输出解析成领域模型。
//
// RawResponse 保留模型的原始文本，便于在结果异常时排查。
func Parse(incidentID int64, modelName, raw string) (*Parsed, error) {
	cleaned := stripCodeFence(raw)

	var ra rawAnalysis
	if err := json.Unmarshal([]byte(cleaned), &ra); err != nil {
		return nil, fmt.Errorf("%w: unmarshal: %v", ErrInvalidResponse, err)
	}

	severity, err := domain.ParseSeverity(ra.Severity)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if strings.TrimSpace(ra.Summary) == "" {
		return nil, fmt.Errorf("%w: summary is empty", ErrInvalidResponse)
	}
	if ra.Confidence < 0 || ra.Confidence > 1 {
		return nil, fmt.Errorf("%w: confidence %v out of range", ErrInvalidResponse, ra.Confidence)
	}

	evidence := make([]domain.Evidence, 0, len(ra.Evidence))
	for _, e := range ra.Evidence {
		evidence = append(evidence, domain.Evidence{
			Key:        e.Key,
			Value:      e.Value,
			SourceLine: e.SourceLine,
		})
	}

	a := &domain.Analysis{
		IncidentID:       incidentID,
		Summary:          ra.Summary,
		PossibleCauses:   ra.PossibleCauses,
		Evidence:         evidence,
		SuggestedActions: ra.SuggestedActions,
		Model:            modelName,
		PromptVersion:    PromptVersion,
		Confidence:       ra.Confidence,
		RawResponse:      raw,
	}

	// 领域校验，其中包含"高置信度必须有 evidence 支撑"这条规则。
	// 它在这里生效而不是只在 DB 约束里，是因为降级后的结果仍然有用，
	// 不该让整个分析失败。
	//
	// 降级会修改 a.Confidence，所以 rawResponse 里保留的仍是模型原值，
	// 两者不一致是预期的，便于事后发现模型在无证据时给了高置信度。
	if err := a.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}

	return &Parsed{
		Analysis: a,
		Title:    ra.Title,
		Severity: severity,
		Category: ra.Category,
	}, nil
}

// stripCodeFence 去掉模型常用的 markdown 代码围栏。
//
// 模型极爱把 JSON 包在 ```json ... ``` 里，不处理就直接解析失败。
// 这是实际中最常见的失败原因，所以单独成函数并有独立测试。
func stripCodeFence(s string) string {
	t := strings.TrimSpace(s)

	if !strings.HasPrefix(t, "```") {
		return t
	}

	// 丢掉起始行（可能是 ``` 也可能是 ```json）。
	nl := strings.IndexByte(t, '\n')
	if nl < 0 {
		// 只有一行，去掉围栏标记本身。
		return strings.TrimSpace(strings.TrimLeft(t, "`"))
	}
	t = t[nl+1:]

	// 丢掉结尾围栏之后的内容。
	if end := strings.Index(t, "```"); end >= 0 {
		t = t[:end]
	}

	return strings.TrimSpace(t)
}
