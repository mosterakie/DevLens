package domain

import "testing"

// TestStatusTransitions 穷举状态机的合法与非法转移。
func TestStatusTransitions(t *testing.T) {
	tests := []struct {
		from, to Status
		want     bool
	}{
		{StatusAnalyzing, StatusOpen, true},
		{StatusAnalyzing, StatusFailed, true},
		{StatusOpen, StatusInvestigating, true},
		{StatusInvestigating, StatusResolved, true},
		{StatusFailed, StatusAnalyzing, true}, // 重试

		{StatusAnalyzing, StatusInvestigating, false}, // 不能跳过 OPEN
		{StatusAnalyzing, StatusResolved, false},
		{StatusOpen, StatusAnalyzing, false},
		{StatusOpen, StatusResolved, false}, // 必须先 INVESTIGATING
		{StatusInvestigating, StatusOpen, false},
		{StatusResolved, StatusAnalyzing, false}, // 终态
		{StatusResolved, StatusOpen, false},
		{StatusResolved, StatusInvestigating, false},
		{StatusFailed, StatusOpen, false}, // 只能回到 ANALYZING
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			if got := tt.from.CanTransitionTo(tt.to); got != tt.want {
				t.Errorf("CanTransitionTo = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStatusTransitionFromUnknown(t *testing.T) {
	var unknown Status = "NOPE"
	if unknown.CanTransitionTo(StatusOpen) {
		t.Error("unknown source status should not transition")
	}
	if StatusAnalyzing.CanTransitionTo(unknown) {
		t.Error("transition to unknown status should be rejected")
	}
}

func TestStatusIsValid(t *testing.T) {
	for _, s := range AllStatuses() {
		if !s.IsValid() {
			t.Errorf("%q should be valid", s)
		}
	}
	if Status("WHATEVER").IsValid() {
		t.Error("unexpected status should be invalid")
	}
}

// TestAllStatusesReturnsCopy 确认返回的是副本，调用方改不到内部切片。
func TestAllStatusesReturnsCopy(t *testing.T) {
	first := AllStatuses()
	if len(first) == 0 {
		t.Fatal("expected non-empty")
	}
	first[0] = "MUTATED"

	second := AllStatuses()
	if second[0] == "MUTATED" {
		t.Error("AllStatuses leaked internal state")
	}
}

func TestNextStatusesReturnsCopy(t *testing.T) {
	got := StatusAnalyzing.NextStatuses()
	if len(got) != 2 {
		t.Fatalf("want 2 successors, got %d", len(got))
	}
	got[0] = "MUTATED"
	if StatusAnalyzing.NextStatuses()[0] == "MUTATED" {
		t.Error("NextStatuses leaked internal state")
	}
}

func TestResolvedIsTerminal(t *testing.T) {
	if !StatusResolved.IsTerminal() {
		t.Error("RESOLVED should be terminal")
	}
	// FAILED 不是终态，它可以重试。
	if StatusFailed.IsTerminal() {
		t.Error("FAILED should not be terminal")
	}
	for _, s := range []Status{StatusAnalyzing, StatusOpen, StatusInvestigating} {
		if s.IsTerminal() {
			t.Errorf("%q should not be terminal", s)
		}
	}
}

// TestEveryStatusHasTransitionEntry 确认每个合法状态都在转移表里。
//
// 漏掉一个会让该状态的 CanTransitionTo 恒为 false，
// 表现为"卡住不动"，很难从日志上看出来。
func TestEveryStatusHasTransitionEntry(t *testing.T) {
	for _, s := range AllStatuses() {
		if _, ok := transitions[s]; !ok {
			t.Errorf("status %q missing from transitions table", s)
		}
	}
}

// TestTransitionsPointToValidStatuses 确认转移表里没有拼错的目标状态。
func TestTransitionsPointToValidStatuses(t *testing.T) {
	for from, tos := range transitions {
		if !from.IsValid() {
			t.Errorf("transitions contains unknown source %q", from)
		}
		for _, to := range tos {
			if !to.IsValid() {
				t.Errorf("transitions[%q] contains unknown target %q", from, to)
			}
		}
	}
}
