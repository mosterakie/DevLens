package domain

// Status 是 Incident 的生命周期状态。
type Status string

const (
	// StatusAnalyzing 表示已创建，等待或正在执行 AI 分析。
	StatusAnalyzing Status = "ANALYZING"
	// StatusOpen 表示分析完成，等待人工排查。
	StatusOpen Status = "OPEN"
	// StatusInvestigating 表示已有人开始排查。
	StatusInvestigating Status = "INVESTIGATING"
	// StatusResolved 表示问题已解决。
	StatusResolved Status = "RESOLVED"
	// StatusFailed 表示分析失败。可以重试回到 ANALYZING。
	StatusFailed Status = "FAILED"
)

// allStatuses 定义了合法状态集合，也用于校验输入。
var allStatuses = []Status{
	StatusAnalyzing,
	StatusOpen,
	StatusInvestigating,
	StatusResolved,
	StatusFailed,
}

// AllStatuses 返回全部合法状态。返回副本，避免调用方改到内部切片。
func AllStatuses() []Status {
	out := make([]Status, len(allStatuses))
	copy(out, allStatuses)
	return out
}

// IsValid 报告 s 是否是已定义的状态。
func (s Status) IsValid() bool {
	for _, v := range allStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// IsTerminal 报告该状态是否还会继续流转。
// RESOLVED 是终态；FAILED 不是，它可以通过重试回到 ANALYZING。
func (s Status) IsTerminal() bool {
	return s == StatusResolved
}

// transitions 是状态机。键是起点，值是该起点允许到达的状态集合。
//
//	ANALYZING ──┬──> OPEN ──> INVESTIGATING ──> RESOLVED
//	            └──> FAILED ──(重试)──> ANALYZING
var transitions = map[Status][]Status{
	StatusAnalyzing:     {StatusOpen, StatusFailed},
	StatusOpen:          {StatusInvestigating},
	StatusInvestigating: {StatusResolved},
	StatusFailed:        {StatusAnalyzing},
	StatusResolved:      {}, // 终态，不允许再流转
}

// CanTransitionTo 报告从 s 转移到 to 是否合法。
// 未知状态一律返回 false。
func (s Status) CanTransitionTo(to Status) bool {
	if !s.IsValid() || !to.IsValid() {
		return false
	}
	for _, v := range transitions[s] {
		if v == to {
			return true
		}
	}
	return false
}

// NextStatuses 返回 s 允许到达的状态。返回副本。
func (s Status) NextStatuses() []Status {
	src := transitions[s]
	out := make([]Status, len(src))
	copy(out, src)
	return out
}
