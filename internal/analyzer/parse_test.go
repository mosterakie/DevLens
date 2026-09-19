package analyzer

import (
	"errors"
	"strings"
	"testing"

	"github.com/mosterakie/DevLens/internal/domain"
)

const validJSON = `{
  "title": "DB pool timeout",
  "severity": "HIGH",
  "category": "Database / Timeout",
  "summary": "The request exceeded the deadline waiting for a connection.",
  "possible_causes": ["pool exhausted", "slow query"],
  "evidence": [{"key": "timeout", "value": "5s", "source_line": 12}],
  "suggested_actions": ["check pool"],
  "confidence": 0.8
}`

func TestParseValid(t *testing.T) {
	got, err := Parse(42, "test-model", validJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Analysis.IncidentID != 42 {
		t.Errorf("IncidentID = %d, want 42", got.Analysis.IncidentID)
	}
	if got.Analysis.Model != "test-model" {
		t.Errorf("Model = %q", got.Analysis.Model)
	}
	if got.Analysis.PromptVersion != PromptVersion {
		t.Errorf("PromptVersion = %q", got.Analysis.PromptVersion)
	}
	if got.Severity != domain.SeverityHigh {
		t.Errorf("Severity = %q, want HIGH", got.Severity)
	}
	if got.Category != "Database / Timeout" {
		t.Errorf("Category = %q", got.Category)
	}
	if len(got.Analysis.Evidence) != 1 || got.Analysis.Evidence[0].SourceLine != 12 {
		t.Errorf("evidence not parsed correctly: %+v", got.Analysis.Evidence)
	}
	if got.Analysis.Confidence != 0.8 {
		t.Errorf("Confidence = %v, want 0.8", got.Analysis.Confidence)
	}
	if got.Analysis.RawResponse != validJSON {
		t.Error("RawResponse should preserve the original text")
	}
}

// TestParseStripsCodeFence 覆盖实际中最常见的失败原因：
// 模型把 JSON 包在 markdown 代码围栏里。
func TestParseStripsCodeFence(t *testing.T) {
	inputs := []struct {
		name string
		raw  string
	}{
		{"带 json 标记", "```json\n" + validJSON + "\n```"},
		{"无标记", "```\n" + validJSON + "\n```"},
		{"前后有空白", "\n\n```json\n" + validJSON + "\n```\n\n"},
		{"围栏后有解释文字", "```json\n" + validJSON + "\n```\nHope this helps!"},
	}

	for _, tt := range inputs {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(1, "m", tt.raw)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			if got.Title != "DB pool timeout" {
				t.Errorf("Title = %q", got.Title)
			}
		})
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"非 JSON", "this is not json"},
		{"空字符串", ""},
		{"缺 summary", `{"severity":"HIGH","confidence":0.5}`},
		{"summary 为空白", `{"summary":"   ","severity":"HIGH","confidence":0.5}`},
		{"severity 非法", `{"summary":"x","severity":"URGENT","confidence":0.5}`},
		{"缺 severity", `{"summary":"x","confidence":0.5}`},
		{"confidence 超过 1", `{"summary":"x","severity":"HIGH","confidence":1.5}`},
		{"confidence 为负", `{"summary":"x","severity":"HIGH","confidence":-0.2}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(1, "m", tt.raw)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !errors.Is(err, ErrInvalidResponse) {
				t.Errorf("error should wrap ErrInvalidResponse, got %v", err)
			}
		})
	}
}

// TestParseDowngradesHighConfidenceWithoutEvidence 覆盖那条业务规则，
// 并确认降级后 RawResponse 仍保留模型给出的原值。
func TestParseDowngradesHighConfidenceWithoutEvidence(t *testing.T) {
	raw := `{
	  "title": "t", "severity": "HIGH", "category": "c",
	  "summary": "s", "possible_causes": ["a"],
	  "evidence": [], "suggested_actions": ["b"],
	  "confidence": 0.97
	}`

	got, err := Parse(1, "m", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Analysis.Confidence != domain.HighConfidenceWithoutEvidence {
		t.Errorf("Confidence = %v, want downgraded to %v",
			got.Analysis.Confidence, domain.HighConfidenceWithoutEvidence)
	}
	if !strings.Contains(got.Analysis.RawResponse, "0.97") {
		t.Error("RawResponse should keep the model's original confidence for auditing")
	}
}

func TestStripCodeFence(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"无围栏", "{}", "{}"},
		{"json 围栏", "```json\n{}\n```", "{}"},
		{"纯围栏", "```\n{}\n```", "{}"},
		{"缺少结束围栏", "```json\n{}", "{}"},
		{"仅有围栏标记", "```", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripCodeFence(tt.in); got != tt.want {
				t.Errorf("stripCodeFence(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuildRetryUserPromptEscalates(t *testing.T) {
	in := Input{RawLog: "some error"}

	first := buildRetryUserPrompt(in, 1)
	if strings.Contains(first, "ONLY valid JSON") {
		t.Error("first attempt should not carry the strict instruction")
	}

	second := buildRetryUserPrompt(in, 2)
	if !strings.Contains(second, "ONLY valid JSON") {
		t.Error("second attempt should carry the strict instruction")
	}
	if !strings.Contains(second, in.RawLog) {
		t.Error("retry prompt should still contain the log")
	}
}
