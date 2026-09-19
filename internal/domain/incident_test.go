package domain

import (
	"strings"
	"testing"
)

func TestSeverityIsValid(t *testing.T) {
	for _, s := range AllSeverities() {
		if !s.IsValid() {
			t.Errorf("%q should be valid", s)
		}
	}
	if Severity("URGENT").IsValid() {
		t.Error("unexpected severity should be invalid")
	}
}

func TestParseSeverity(t *testing.T) {
	if got, err := ParseSeverity("HIGH"); err != nil || got != SeverityHigh {
		t.Errorf("ParseSeverity(HIGH) = %q, %v", got, err)
	}
	if _, err := ParseSeverity("high"); err == nil {
		t.Error("ParseSeverity should be case-sensitive, lowercase should fail")
	}
	if _, err := ParseSeverity("NOPE"); err == nil {
		t.Error("ParseSeverity should reject unknown values")
	}
}

func TestSeverityAllReturnsCopy(t *testing.T) {
	first := AllSeverities()
	first[0] = "MUTATED"
	if AllSeverities()[0] == "MUTATED" {
		t.Error("AllSeverities leaked internal state")
	}
}

func TestValidateLog(t *testing.T) {
	// 构造一个长度恰好合法的日志。
	valid := strings.Repeat("e", MinLogBytes)

	tests := []struct {
		name    string
		in      string
		wantErr error
	}{
		{"空字符串", "", ErrLogEmpty},
		{"纯空白", "   \n\t ", ErrLogEmpty},
		{"过短", strings.Repeat("e", MinLogBytes-1), ErrLogTooShort},
		{"正好下限", valid, nil},
		{"超出上限", strings.Repeat("e", MaxLogBytes+1), ErrLogTooLong},
		{"正好上限", strings.Repeat("e", MaxLogBytes), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLog(tt.in)
			if err != tt.wantErr {
				t.Errorf("ValidateLog() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateAnalysisDowngradesUnsupportedConfidence 覆盖那条业务规则：
// 高置信度必须有证据支撑，否则降级。
func TestValidateAnalysisDowngradesUnsupportedConfidence(t *testing.T) {
	t.Run("无证据时高置信度被降级", func(t *testing.T) {
		a := &Analysis{
			Summary:    "looks like a pool exhaustion",
			Confidence: 0.99,
			Evidence:   nil,
		}
		if err := a.Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.Confidence != HighConfidenceWithoutEvidence {
			t.Errorf("confidence = %v, want downgraded to %v",
				a.Confidence, HighConfidenceWithoutEvidence)
		}
	})

	t.Run("有证据时保留高置信度", func(t *testing.T) {
		a := &Analysis{
			Summary:    "pool exhausted",
			Confidence: 0.99,
			Evidence: []Evidence{
				{Key: "active connections", Value: "20", SourceLine: 18},
			},
		}
		if err := a.Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.Confidence != 0.99 {
			t.Errorf("confidence = %v, want 0.99 preserved", a.Confidence)
		}
	})

	t.Run("低置信度不受影响", func(t *testing.T) {
		a := &Analysis{Summary: "unclear", Confidence: 0.3}
		if err := a.Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.Confidence != 0.3 {
			t.Errorf("confidence = %v, want 0.3", a.Confidence)
		}
	})

	t.Run("边界值恰好等于阈值不被降级", func(t *testing.T) {
		a := &Analysis{Summary: "x", Confidence: HighConfidenceWithoutEvidence}
		if err := a.Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.Confidence != HighConfidenceWithoutEvidence {
			t.Errorf("confidence = %v, want unchanged", a.Confidence)
		}
	})
}

func TestValidateAnalysisRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		in   Analysis
	}{
		{"summary 为空", Analysis{Summary: "", Confidence: 0.5}},
		{"summary 纯空白", Analysis{Summary: "   ", Confidence: 0.5}},
		{"confidence 为负", Analysis{Summary: "x", Confidence: -0.1}},
		{"confidence 超过 1", Analysis{Summary: "x", Confidence: 1.1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := tt.in
			if err := a.Validate(); err == nil {
				t.Error("expected an error")
			}
		})
	}
}
