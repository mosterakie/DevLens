package domain

import "fmt"

// Severity 是 Incident 的严重程度。
type Severity string

const (
	SeverityLow      Severity = "LOW"
	SeverityMedium   Severity = "MEDIUM"
	SeverityHigh     Severity = "HIGH"
	SeverityCritical Severity = "CRITICAL"
)

var allSeverities = []Severity{
	SeverityLow,
	SeverityMedium,
	SeverityHigh,
	SeverityCritical,
}

// AllSeverities 返回全部合法严重程度。返回副本。
func AllSeverities() []Severity {
	out := make([]Severity, len(allSeverities))
	copy(out, allSeverities)
	return out
}

// IsValid 报告 s 是否是已定义的严重程度。
func (s Severity) IsValid() bool {
	for _, v := range allSeverities {
		if v == s {
			return true
		}
	}
	return false
}

// ParseSeverity 把字符串转成 Severity，并校验。
func ParseSeverity(s string) (Severity, error) {
	v := Severity(s)
	if !v.IsValid() {
		return "", fmt.Errorf("invalid severity %q", s)
	}
	return v, nil
}
