package analyzer

import (
	"context"
	"strings"
	"testing"

	"github.com/mosterakie/DevLens/internal/domain"
)

func TestHeuristicClassifiesKnownPatterns(t *testing.T) {
	tests := []struct {
		name         string
		log          string
		wantCategory string
		wantSeverity domain.Severity
	}{
		{
			name:         "数据库连接池超时",
			log:          "ERROR: context deadline exceeded\ngoroutine 231 [IO wait]:\ndatabase/sql.(*DB).conn(...)",
			wantCategory: "Database / Timeout",
			wantSeverity: domain.SeverityHigh,
		},
		{
			name:         "panic",
			log:          "panic: runtime error: invalid memory address or nil pointer dereference",
			wantCategory: "Concurrency / Nil",
			wantSeverity: domain.SeverityCritical,
		},
		{
			name:         "DNS 解析失败",
			log:          "dial tcp: lookup payment.internal on 10.0.0.2:53: no such host",
			wantCategory: "Network / DNS",
			wantSeverity: domain.SeverityHigh,
		},
		{
			name:         "鉴权失败",
			log:          "401 Unauthorized: token expired",
			wantCategory: "Auth",
			wantSeverity: domain.SeverityMedium,
		},
		{
			name:         "内存不足",
			log:          "fatal error: runtime: out of memory",
			wantCategory: "Resource / Memory",
			wantSeverity: domain.SeverityCritical,
		},
	}

	h := NewHeuristic()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := h.Analyze(context.Background(), Input{RawLog: tt.log, IncidentID: 1})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Category != tt.wantCategory {
				t.Errorf("Category = %q, want %q", got.Category, tt.wantCategory)
			}
			if got.Severity != tt.wantSeverity {
				t.Errorf("Severity = %q, want %q", got.Severity, tt.wantSeverity)
			}
			if got.Analysis.Summary == "" {
				t.Error("summary should not be empty")
			}
			if len(got.Analysis.PossibleCauses) == 0 {
				t.Error("possible_causes should not be empty")
			}
		})
	}
}

// TestHeuristicDefaultsToChinese 是这一轮的需求：默认语言是中文。
func TestHeuristicDefaultsToChinese(t *testing.T) {
	h := NewHeuristic()
	got, err := h.Analyze(context.Background(), Input{
		RawLog:     "ERROR: context deadline exceeded",
		IncidentID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsHan(got.Analysis.Summary) {
		t.Errorf("default summary should be Chinese, got %q", got.Analysis.Summary)
	}
	if !containsHan(got.Title) {
		t.Errorf("default title should be Chinese, got %q", got.Title)
	}
}

// TestHeuristicHonoursEnglish 确认显式指定英文时输出英文。
func TestHeuristicHonoursEnglish(t *testing.T) {
	h := NewHeuristic()
	got, err := h.Analyze(context.Background(), Input{
		RawLog:     "ERROR: context deadline exceeded",
		IncidentID: 1,
		Lang:       domain.LangEn,
	})
	if err != nil {
		t.Fatal(err)
	}
	if containsHan(got.Analysis.Summary) {
		t.Errorf("English summary should not contain Han characters, got %q", got.Analysis.Summary)
	}
	if containsHan(got.Title) {
		t.Errorf("English title should not contain Han characters, got %q", got.Title)
	}
}

// TestHeuristicCategoryStaysEnglish 确认分类标识不随语言变化。
// 它是过滤和聚合的依据，翻译会让同一种错误产生两个不同的 key。
func TestHeuristicCategoryStaysEnglish(t *testing.T) {
	h := NewHeuristic()
	for _, lang := range domain.AllLangs() {
		got, err := h.Analyze(context.Background(), Input{
			RawLog:     "ERROR: context deadline exceeded",
			IncidentID: 1,
			Lang:       lang,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.Category != "Database / Timeout" {
			t.Errorf("lang %s: category = %q, want the stable English key", lang, got.Category)
		}
	}
}

// TestHeuristicAdmitsUncertainty 确认无法判断时返回低置信度并说明，
// 而不是编造一个分类。
func TestHeuristicAdmitsUncertainty(t *testing.T) {
	h := NewHeuristic()
	got, err := h.Analyze(context.Background(), Input{
		RawLog:     "something happened\nnot sure what",
		IncidentID: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Analysis.Confidence >= 0.5 {
		t.Errorf("confidence = %v, want below 0.5 for an unrecognized log", got.Analysis.Confidence)
	}
	if len(got.Analysis.Evidence) != 0 {
		t.Error("no evidence should be claimed for an unrecognized log")
	}
	if got.Category != "Unknown" {
		t.Errorf("Category = %q, want Unknown", got.Category)
	}
}

// TestHeuristicEvidenceHasLineNumbers 确认证据带行号且指向原文。
func TestHeuristicEvidenceHasLineNumbers(t *testing.T) {
	raw := strings.Join([]string{
		"2026-09-18 14:32:51 ERROR request failed",
		"context deadline exceeded",
		"goroutine 231 [IO wait]:",
	}, "\n")

	h := NewHeuristic()
	got, err := h.Analyze(context.Background(), Input{RawLog: raw, IncidentID: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Analysis.Evidence) == 0 {
		t.Fatal("expected at least one evidence item")
	}

	lines := strings.Split(raw, "\n")
	for _, e := range got.Analysis.Evidence {
		if e.SourceLine < 1 || e.SourceLine > len(lines) {
			t.Errorf("source_line %d out of range", e.SourceLine)
			continue
		}
		actual := strings.ToLower(lines[e.SourceLine-1])
		if !strings.Contains(actual, strings.ToLower(e.Key)) {
			t.Errorf("evidence line %d does not contain %q: %q",
				e.SourceLine, e.Key, lines[e.SourceLine-1])
		}
		// value 必须是日志原文，任何语言下都不翻译。
		if !strings.Contains(lines[e.SourceLine-1], e.Value) {
			t.Errorf("evidence value %q is not verbatim from the log", e.Value)
		}
	}
}

// TestHeuristicConfidenceVaries 确认 confidence 会随证据数量变化。
func TestHeuristicConfidenceVaries(t *testing.T) {
	h := NewHeuristic()
	a, err := h.Analyze(context.Background(), Input{
		RawLog: "context deadline exceeded", IncidentID: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.Analyze(context.Background(), Input{
		RawLog: "context deadline exceeded\ncontext deadline exceeded", IncidentID: 2})
	if err != nil {
		t.Fatal(err)
	}
	if a.Analysis.Confidence == b.Analysis.Confidence {
		t.Errorf("confidence should vary with evidence: both %v", a.Analysis.Confidence)
	}
}

func TestHeuristicName(t *testing.T) {
	if got := NewHeuristic().Name(); got != "heuristic-v1" {
		t.Errorf("Name() = %q", got)
	}
}

// containsHan 报告字符串里是否含汉字。
func containsHan(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}
