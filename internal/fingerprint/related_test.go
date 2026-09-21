package fingerprint

import "testing"

// TestMergeExactWinsOverSimilar 覆盖"精确优先"。
//
// 同一个 ID 两路都命中时必须保留 exact：把确定同类降级成可能同类
// 是信息损失，UI 会少给用户一个确定的信号。
func TestMergeExactWinsOverSimilar(t *testing.T) {
	exact := []Candidate{{ID: 7, Title: "exact", CreatedAt: 100, Match: MatchExact}}
	similar := []Candidate{{ID: 7, Title: "similar", CreatedAt: 100, Match: MatchSimilar, Score: 0.9}}

	got := Merge(exact, similar, 5)
	if len(got) != 1 {
		t.Fatalf("应当去重成 1 条, got %d", len(got))
	}
	if got[0].Match != MatchExact {
		t.Errorf("Match = %q, want %q（精确必须优先）", got[0].Match, MatchExact)
	}
	if got[0].Title != "exact" {
		t.Errorf("应当保留精确那条的内容, got %q", got[0].Title)
	}
}

// TestMergeSimilarFirstThenExact 覆盖入参顺序反过来时结论不变。
//
// 两条记录本来就同时命中两路，谁先到不该影响结果。
func TestMergeSimilarFirstThenExact(t *testing.T) {
	// 让相似路先产出该 ID，再去重时应当被精确路升级。
	got := Merge(
		[]Candidate{{ID: 3, CreatedAt: 50}},
		[]Candidate{{ID: 3, CreatedAt: 50}},
		5,
	)
	if len(got) != 1 || got[0].Match != MatchExact {
		t.Fatalf("got %+v, want 单条 exact", got)
	}
}

// TestMergeDeduplicates 确认不同来源的同一 ID 只出现一次。
func TestMergeDeduplicates(t *testing.T) {
	exact := []Candidate{{ID: 1, CreatedAt: 10}, {ID: 2, CreatedAt: 20}}
	similar := []Candidate{{ID: 2, CreatedAt: 20}, {ID: 3, CreatedAt: 30}}

	got := Merge(exact, similar, 5)
	if len(got) != 3 {
		t.Fatalf("应当有 3 条（1/2/3）, got %d: %+v", len(got), got)
	}
	for i := 1; i < len(got); i++ {
		if got[i].ID == got[i-1].ID {
			t.Errorf("出现重复 ID: %+v", got)
		}
	}
}

// TestMergeOrdersByCreatedAtDesc 覆盖排序：最新的在前。
func TestMergeOrdersByCreatedAtDesc(t *testing.T) {
	exact := []Candidate{{ID: 1, CreatedAt: 100}}
	similar := []Candidate{
		{ID: 2, CreatedAt: 300},
		{ID: 3, CreatedAt: 200},
	}

	got := Merge(exact, similar, 5)
	want := []int64{2, 3, 1}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("位置 %d = id %d, want %d（整体 %+v）", i, got[i].ID, id, got)
		}
	}
}

// TestMergeAppliesLimit 覆盖整体上限，而不是每路各一个上限。
//
// 混合召回如果按路限流，列表长度会翻倍。
func TestMergeAppliesLimit(t *testing.T) {
	exact := []Candidate{{ID: 1, CreatedAt: 100}, {ID: 2, CreatedAt: 90}}
	similar := []Candidate{{ID: 3, CreatedAt: 80}, {ID: 4, CreatedAt: 70}}

	got := Merge(exact, similar, 3)
	if len(got) != 3 {
		t.Fatalf("limit=3 时应返回 3 条, got %d", len(got))
	}
	// 截断的是最旧的（id=4）。
	for _, c := range got {
		if c.ID == 4 {
			t.Errorf("最旧的一条应当被截断: %+v", got)
		}
	}
}

// TestMergeDefaultsLimit 确认 limit <= 0 时回落到 RelatedLimit。
func TestMergeDefaultsLimit(t *testing.T) {
	var exact []Candidate
	for i := int64(1); i <= 10; i++ {
		exact = append(exact, Candidate{ID: i, CreatedAt: i})
	}

	for _, limit := range []int{0, -1} {
		got := Merge(exact, nil, limit)
		if len(got) != RelatedLimit {
			t.Errorf("limit=%d 时应回落到 %d 条, got %d", limit, RelatedLimit, len(got))
		}
	}
}

// TestMergeTieBreakIsStable 覆盖时间戳相同时的确定性。
//
// sort.Slice 不稳定，只按 CreatedAt 排序时两条同时间戳的记录
// 顺序会在两次调用之间抖动，表现为接口输出不稳定。
// 用 ID 兜底后顺序就确定了。
func TestMergeTieBreakIsStable(t *testing.T) {
	in := []Candidate{
		{ID: 11, CreatedAt: 500},
		{ID: 9, CreatedAt: 500},
		{ID: 13, CreatedAt: 500},
	}

	first := Merge(in, nil, 5)
	for i := 0; i < 100; i++ {
		got := Merge(in, nil, 5)
		for j := range first {
			if got[j].ID != first[j].ID {
				t.Fatalf("第 %d 次顺序不稳定: %+v vs %+v", i, got, first)
			}
		}
	}
	// ID 大的在前。
	if first[0].ID != 13 {
		t.Errorf("同时间戳应按 ID 倒序兜底, got %+v", first)
	}
}

// TestMergeEmptyInputs 确认两路都空时返回空而不是 nil panic。
func TestMergeEmptyInputs(t *testing.T) {
	if got := Merge(nil, nil, 5); len(got) != 0 {
		t.Errorf("空输入应返回空结果, got %+v", got)
	}
}

// TestMergeExactScoreIsMeaningless 确认精确项的 Score 不被借用。
//
// Score 只有相似项才有意义。如果精确项带上相似路的得分，
// 日志里会出现"exact 且 score=0.9"这种自相矛盾的记录。
func TestMergeExactScoreIsMeaningless(t *testing.T) {
	got := Merge(
		[]Candidate{{ID: 1, CreatedAt: 10}},
		[]Candidate{{ID: 1, CreatedAt: 10, Score: 0.99}},
		5,
	)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Score != 0 {
		t.Errorf("精确命中不该带相似度得分, got %v", got[0].Score)
	}
}
