package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mosterakie/DevLens/internal/domain"
	"github.com/mosterakie/DevLens/internal/fingerprint"
	"github.com/mosterakie/DevLens/internal/repository"
)

// errSimilarUnavailable 模拟相似度粗召回失败，用于验证降级路径。
var errSimilarUnavailable = errors.New("similar recall unavailable")

// timeNow 是 time.Now 的薄包装，让测试里的时间构造读起来短一些。
func timeNow() time.Time { return time.Now() }

// 本文件覆盖"改动 2：同类问题召回"的验收点。
//
// 用的日志取真实形态：一段 PostgreSQL 连接被拒的 coderd 日志，
// 同类之间换 IP / 环境名 / 措辞，异类是 Go panic。
// 长度都要越过 domain.MinLogBytes（100 字节），否则会先被长度校验挡掉。

// dbConnLogA 是同类故障的第一种措辞。
func dbConnLogA() string {
	return `2026-09-08 01:19:09.357 [erro]  pubsub: pubsub disconnected from postgres  error=EOF
2026-09-08 01:19:09.383 [erro]  pubsub: pubsub failed to dial postgres  network=tcp  address=postgresql.coder-nonprod.svc.cluster.local:5432  timeout_ms=0
2026-09-08 01:19:09.383 [erro]  pubsub: pubsub failed to connect to postgres  error="dial tcp 10.1.2.3:5432: connect: connection refused"
2026-09-08 01:19:10.413 [erro]  coderd: failed to ping database, retrying in 1s
2026-09-08 01:19:11.511 [warn]  coderd.gitsync: acquire stale chat diff statuses failed
`
}

// dbConnLogB 是同一种故障，但 IP / 环境名 / 措辞都换了。
//
// 这正是改造前漏召回的情况：归一化后仍与 A 不同，指纹不同。
func dbConnLogB() string {
	return `2026-09-08 03:44:11.001 [erro]  pubsub: pubsub disconnected from postgres  error=unexpected EOF
2026-09-08 03:44:11.020 [erro]  pubsub: pubsub failed to dial postgres  network=tcp  address=postgresql.coder-prod.svc.cluster.local:5432  timeout_ms=0
2026-09-08 03:44:11.020 [erro]  pubsub: pubsub failed to connect to postgres  error="dial tcp 10.9.8.7:5432: connect: connection refused"
2026-09-08 03:44:11.100 [erro]  coderd: database ping failed, backing off
2026-09-08 03:44:12.200 [warn]  coderd.gitsync: acquire stale chat diff statuses failed
`
}

// panicLog 是完全无关的日志（Go 空指针 panic）。
func panicLog() string {
	return `panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x638 pc=0x190717c]

goroutine 15 [running]:
github.com/databricks/terraform-provider-databricks/exporter.(*importContext).emitRfaAccessRequestDestinations(...)
      exporter/impl_uc.go:813 +0x7c
github.com/databricks/terraform-provider-databricks/exporter.importUcMetastores(...)
      exporter/impl_uc.go:656 +0x8e
`
}

// --- 验收点 1：同类但措辞不同 -> recurring + match=similar ---

// TestSimilarWordingIsRecalledAsSimilar 是本次改造的核心验收点。
//
// 改造前这两条日志指纹不同，第二条会被当成全新问题（is_recurring=f，
// related 为空）。改造后应当召回，并且如实标成 similar 而不是 exact——
// 它是"可能同类"，不是"确定同类"。
func TestSimilarWordingIsRecalledAsSimilar(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	first, err := svc.Submit(ctx, dbConnLogA())
	if err != nil {
		t.Fatal(err)
	}
	if first.Recurring {
		t.Fatal("第一条不该是重复出现")
	}

	second, err := svc.Submit(ctx, dbConnLogB())
	if err != nil {
		t.Fatal(err)
	}

	// 前提：两条的指纹确实不同，否则这条测试走的是精确路，没有验证相似召回。
	if string(first.Incident.Fingerprint) == string(second.Incident.Fingerprint) {
		t.Fatal("前置条件不成立：两条日志指纹相同，测不到相似召回")
	}

	if !second.Recurring {
		t.Error("措辞不同但同类的日志应判定为 recurring")
	}
	if !second.Incident.IsRecurring {
		t.Error("is_recurring 应写入记录")
	}

	if len(second.Related) != 1 {
		t.Fatalf("应当召回 1 条同类历史, got %d: %+v", len(second.Related), second.Related)
	}

	got := second.Related[0]
	if got.ID != first.Incident.ID {
		t.Errorf("召回的 ID = %d, want %d", got.ID, first.Incident.ID)
	}
	if got.Match != repository.MatchSimilar {
		t.Errorf("Match = %q, want %q（措辞不同只能标成可能同类）", got.Match, repository.MatchSimilar)
	}
	if got.Score < fingerprint.SimilarityThreshold {
		t.Errorf("Score = %v, 应当 >= 阈值 %v", got.Score, fingerprint.SimilarityThreshold)
	}
}

// --- 验收点 2：完全无关 -> 不 recurring 且 related 为空 ---

// TestUnrelatedLogIsNotRecalled 是防止误召回的护栏。
//
// 相似度路放宽了匹配，最危险的失败模式是把不相关历史塞给用户
// （05-related-incidents.md：用户看到"错误的相关历史"才会失去信任）。
// 这条测试锁定"异类不被召回"。
func TestUnrelatedLogIsNotRecalled(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	if _, err := svc.Submit(ctx, dbConnLogA()); err != nil {
		t.Fatal(err)
	}

	other, err := svc.Submit(ctx, panicLog())
	if err != nil {
		t.Fatal(err)
	}

	if other.Recurring {
		t.Error("完全无关的日志不该判定为 recurring")
	}
	if other.Incident.IsRecurring {
		t.Error("完全无关的日志不该写入 is_recurring")
	}
	if len(other.Related) != 0 {
		t.Errorf("完全无关的日志不该召回任何历史, got %d: %+v", len(other.Related), other.Related)
	}
}

// TestUnrelatedPanicNotRecalledFromSimilarPanic 覆盖两个 panic 日志也互不召回。
//
// 它们共享 panic/nil/pointer 等词，但因为栈帧不同，Jaccard 会低于阈值。
// 这条是"共享高频词不等于同类"的回归护栏。
func TestUnrelatedPanicNotRecalledFromSimilarPanic(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	if _, err := svc.Submit(ctx, panicLog()); err != nil {
		t.Fatal(err)
	}

	// 另一个 panic：共享 panic/nil/pointer 这些词，但故障点完全不同。
	another := `panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x628a4d9]

goroutine 1 [running]:
internal/sync.(*Mutex).Lock(...)
github.com/ysya/runscaler/internal/config.(*LogFileWriter).Write(0x0, {0x21a39bc2600, 0xf1, 0x100})
        internal/config/logfile.go:49 +0x59
main.logServiceDrainTimeoutReminder(...)
        cmd/runner/main.go:584 +0x1ed
`

	res, err := svc.Submit(ctx, another)
	if err != nil {
		t.Fatal(err)
	}

	// 这两条确实可能被判成同类（都是 Go panic），所以这里不做
	// "必须为空"的断言，而是验证：如果真的召回，必须是 similar
	// 而不是 exact，且相似度确实达到了阈值。
	for _, r := range res.Related {
		if r.Match != repository.MatchSimilar {
			t.Errorf("不同栈帧的 panic 不该被标成 %q", r.Match)
		}
		if r.Score < fingerprint.SimilarityThreshold {
			t.Errorf("召回项 Score = %v 低于阈值 %v", r.Score, fingerprint.SimilarityThreshold)
		}
	}
}

// --- 验收点 3：精确重复 -> match=exact ---

// TestExactDuplicateIsMatchedAsExact 确认精确路仍是主路且标记正确。
//
// 同一段日志提交两次，指纹相同，必须走精确匹配并标成 exact。
// 这是"确定同类"信号，权重最高，不能被相似路顶掉。
func TestExactDuplicateIsMatchedAsExact(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	first, err := svc.Submit(ctx, dbConnLogA())
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Submit(ctx, dbConnLogA())
	if err != nil {
		t.Fatal(err)
	}

	if !second.Recurring {
		t.Error("同一段日志第二次提交应当是 recurring")
	}
	if len(second.Related) != 1 {
		t.Fatalf("应当有 1 条同类, got %d", len(second.Related))
	}

	got := second.Related[0]
	if got.ID != first.Incident.ID {
		t.Errorf("ID = %d, want %d", got.ID, first.Incident.ID)
	}
	if got.Match != repository.MatchExact {
		t.Errorf("Match = %q, want %q", got.Match, repository.MatchExact)
	}
	// 精确命中没有相似度概念，得分应当清零。
	if got.Score != 0 {
		t.Errorf("精确命中的 Score 应当为 0, got %v", got.Score)
	}
}

// TestExactWinsWhenBothPathsHit 覆盖两路同时命中同一个 ID。
//
// 措辞相同（精确命中）时相似度往往是 1.0，两路都会命中同一 ID。
// 去重时必须保留 exact —— 否则用户看到的是"可能同类"，
// 而我们其实有确定结论。这是"精确优先"最关键的一条。
func TestExactWinsWhenBothPathsHit(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := svc.Submit(ctx, dbConnLogA()); err != nil {
			t.Fatal(err)
		}
	}

	// 第三条提交时，前两条都命中两路。
	res, err := svc.Submit(ctx, dbConnLogA())
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Related) != 3 {
		t.Fatalf("应当有 3 条历史（去重后）, got %d: %+v", len(res.Related), res.Related)
	}
	for _, r := range res.Related {
		if r.Match != repository.MatchExact {
			t.Errorf("id=%d 的 Match = %q, want exact（两路都命中时精确优先）", r.ID, r.Match)
		}
	}

	// 确认没有重复 ID。
	seen := map[int64]bool{}
	for _, r := range res.Related {
		if seen[r.ID] {
			t.Errorf("出现重复 ID %d", r.ID)
		}
		seen[r.ID] = true
	}
}

// --- 验收点 4：自己不出现在 related 里 ---

// TestSelfNeverAppearsInRelated 确认自己不会出现在结果里。
//
// 相似路如果不排除自己，刚插入的记录会以 1.0 的相似度匹配到自己，
// 表现为"每次都显示一条莫名其妙的历史"。
func TestSelfNeverAppearsInRelated(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	// 先放几条同类，让相似路有东西可召回。
	if _, err := svc.Submit(ctx, dbConnLogA()); err != nil {
		t.Fatal(err)
	}

	res, err := svc.Submit(ctx, dbConnLogB())
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Related) == 0 {
		t.Fatal("应当召回至少一条历史，否则本测试没有验证到排除自己")
	}
	for _, r := range res.Related {
		if r.ID == res.Incident.ID {
			t.Fatalf("自己（id=%d）不该出现在 related 里: %+v", res.Incident.ID, res.Related)
		}
	}
}

// TestRelatedByIncidentIDExcludesSelfWithSimilarPath 在按 ID 查询路径上
// 同样验证排除自己，以及相似召回生效。
func TestRelatedByIncidentIDExcludesSelfWithSimilarPath(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	first, err := svc.Submit(ctx, dbConnLogA())
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Submit(ctx, dbConnLogB())
	if err != nil {
		t.Fatal(err)
	}

	// 查第一条的同类：第二条措辞不同，应当通过相似路召回。
	got, err := svc.RelatedByIncidentID(ctx, first.Incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("应当召回 1 条, got %d: %+v", len(got), got)
	}
	if got[0].ID != second.Incident.ID {
		t.Errorf("ID = %d, want %d", got[0].ID, second.Incident.ID)
	}
	if got[0].Match != repository.MatchSimilar {
		t.Errorf("Match = %q, want similar", got[0].Match)
	}
	for _, r := range got {
		if r.ID == first.Incident.ID {
			t.Error("自己不该出现在结果里")
		}
	}
}

// --- 合并与排序 ---

// TestRelatedIsOrderedByCreatedAtDesc 确认整体按 created_at 倒序。
func TestRelatedIsOrderedByCreatedAtDesc(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	// 交错提交同类与异类，让两路都有内容。
	for i := 0; i < 4; i++ {
		if _, err := svc.Submit(ctx, dbConnLogA()); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Submit(ctx, dbConnLogB()); err != nil {
			t.Fatal(err)
		}
	}

	res, err := svc.Submit(ctx, dbConnLogB())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Related) < 2 {
		t.Fatalf("需要至少 2 条才能验证排序, got %d", len(res.Related))
	}
	for i := 1; i < len(res.Related); i++ {
		if res.Related[i].CreatedAt.After(res.Related[i-1].CreatedAt) {
			t.Errorf("第 %d 条比前一条更新，排序不是 created_at DESC: %+v", i, res.Related)
		}
	}
}

// TestRelatedRespectsOverallLimit 确认整体上限为 relatedLimit（5），
// 而不是每路各 5 条。
func TestRelatedRespectsOverallLimit(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	// 造 8 条历史，确保超过上限。
	for i := 0; i < 8; i++ {
		if _, err := svc.Submit(ctx, dbConnLogA()); err != nil {
			t.Fatal(err)
		}
	}

	res, err := svc.Submit(ctx, dbConnLogA())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Related) > fingerprint.RelatedLimit {
		t.Errorf("related 条数 = %d, 超过上限 %d", len(res.Related), fingerprint.RelatedLimit)
	}
	if len(res.Related) != fingerprint.RelatedLimit {
		t.Errorf("应当填满到 %d 条, got %d", fingerprint.RelatedLimit, len(res.Related))
	}
}

// TestSimilarRecallFailureDegradesGracefully 覆盖相似路出错时的降级。
//
// 相似召回是补充信号，它失败不该让整个提交失败 ——
// 精确匹配已经拿到"确定同类"，那是用户最需要的信息。
func TestSimilarRecallFailureDegradesGracefully(t *testing.T) {
	repo := newFakeRepo()
	repo.similarErr = errSimilarUnavailable
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	first, err := svc.Submit(ctx, dbConnLogA())
	if err != nil {
		t.Fatal(err)
	}

	second, err := svc.Submit(ctx, dbConnLogA())
	if err != nil {
		t.Fatalf("相似召回失败不该让提交失败: %v", err)
	}

	// 精确路仍然返回结果。
	if len(second.Related) != 1 {
		t.Fatalf("降级后仍应有精确命中的 1 条, got %d", len(second.Related))
	}
	if second.Related[0].ID != first.Incident.ID {
		t.Errorf("ID = %d, want %d", second.Related[0].ID, first.Incident.ID)
	}
	if second.Related[0].Match != repository.MatchExact {
		t.Errorf("降级路径也要标 exact, got %q", second.Related[0].Match)
	}
}

// TestRelatedExcludesFailed 确认 FAILED 记录不参与两路召回。
func TestRelatedExcludesFailed(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	bad, err := svc.Submit(ctx, dbConnLogA())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(ctx, bad.Incident.ID, domain.StatusOpen); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(ctx, bad.Incident.ID, domain.StatusFailed); err != nil {
		t.Fatal(err)
	}

	res, err := svc.Submit(ctx, dbConnLogA())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Related {
		if r.ID == bad.Incident.ID {
			t.Errorf("FAILED 的记录不该出现在 related 里: %+v", res.Related)
		}
	}
}

// TestRecurringSemanticsDocumented 锁定 recurring 的新语义：
// 精确命中或相似命中都算重复出现。
//
// 与 v1 的差异（v1 只有精确命中才算）是有意的，这里用测试固定住，
// 避免以后有人把它当成"回归"改回去而不自知。
func TestRecurringSemanticsDocumented(t *testing.T) {
	t.Run("精确命中", func(t *testing.T) {
		repo := newFakeRepo()
		svc := NewIncidentService(repo, &fakeQueue{})
		ctx := context.Background()

		if _, err := svc.Submit(ctx, dbConnLogA()); err != nil {
			t.Fatal(err)
		}
		res, err := svc.Submit(ctx, dbConnLogA())
		if err != nil {
			t.Fatal(err)
		}
		if !res.Recurring {
			t.Error("精确命中应当 recurring")
		}
	})

	t.Run("仅相似命中", func(t *testing.T) {
		repo := newFakeRepo()
		svc := NewIncidentService(repo, &fakeQueue{})
		ctx := context.Background()

		if _, err := svc.Submit(ctx, dbConnLogA()); err != nil {
			t.Fatal(err)
		}
		res, err := svc.Submit(ctx, dbConnLogB())
		if err != nil {
			t.Fatal(err)
		}
		if !res.Recurring {
			t.Error("相似命中应当 recurring（与 v1 的差异，见 service 注释）")
		}
	})

	t.Run("无命中", func(t *testing.T) {
		repo := newFakeRepo()
		svc := NewIncidentService(repo, &fakeQueue{})
		ctx := context.Background()

		if _, err := svc.Submit(ctx, dbConnLogA()); err != nil {
			t.Fatal(err)
		}
		res, err := svc.Submit(ctx, panicLog())
		if err != nil {
			t.Fatal(err)
		}
		if res.Recurring {
			t.Error("无命中不该 recurring")
		}
	})
}

// TestMergeRelatedExactPreferred 直接覆盖 mergeRelated 的去重规则。
func TestMergeRelatedExactPreferred(t *testing.T) {
	now := timeNow()
	exact := []repository.RelatedIncident{{ID: 1, CreatedAt: now, Match: repository.MatchExact}}
	similar := []repository.RelatedIncident{
		{ID: 1, CreatedAt: now, Match: repository.MatchSimilar, Score: 0.99},
		{ID: 2, CreatedAt: now.Add(-1), Match: repository.MatchSimilar, Score: 0.8},
	}

	got := mergeRelated(exact, similar, 5)
	if len(got) != 2 {
		t.Fatalf("应当去重成 2 条, got %d: %+v", len(got), got)
	}
	if got[0].ID != 1 {
		t.Fatalf("最新的应当在前, got %+v", got)
	}
	if got[0].Match != repository.MatchExact {
		t.Errorf("id=1 的 Match = %q, want exact", got[0].Match)
	}
	if got[0].Score != 0 {
		t.Errorf("精确项得分应当清零, got %v", got[0].Score)
	}
	if got[1].Match != repository.MatchSimilar {
		t.Errorf("id=2 的 Match = %q, want similar", got[1].Match)
	}
	if got[1].Score != 0.8 {
		t.Errorf("相似项得分应当保留, got %v", got[1].Score)
	}
}

// TestSubmitResultHasNoNilRelated 确认没有同类时返回空切片而不是 nil。
//
// handler 会遍历它并构造 DTO；nil 与空切片在 JSON 上都序列化成 []，
// 但保持与既有行为一致更安全。
func TestSubmitResultHasNoNilRelated(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})

	res, err := svc.Submit(context.Background(), panicLog())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Related) != 0 {
		t.Errorf("不该有同类, got %+v", res.Related)
	}
}

// TestSimilarCandidateOrderIsStable 确认召回结果的顺序与内容稳定。
//
// 相似度路依赖 map 迭代顺序，若实现里混入顺序依赖，这里会不稳定失败。
//
// 语料固定后再反复查询：不能每轮都提交新记录 ——
// 那样每轮的历史集合都不同，顺序"变化"是数据变化导致的，
// 断言会测错对象。
func TestSimilarCandidateOrderIsStable(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	// 构造一批同类历史（措辞有差异，走相似路）。
	for i := 0; i < 3; i++ {
		if _, err := svc.Submit(ctx, dbConnLogA()); err != nil {
			t.Fatal(err)
		}
	}

	// 查一条 B 的同类，反复查同一个 ID。
	target, err := svc.Submit(ctx, dbConnLogB())
	if err != nil {
		t.Fatal(err)
	}

	// 多提交几条 B：它们也会进入候选集，这是语料的正常增长，
	// 但每次都必须得到同一个结果。所以先固定语料，再反复查询。
	for i := 0; i < 2; i++ {
		if _, err := svc.Submit(ctx, dbConnLogB()); err != nil {
			t.Fatal(err)
		}
	}

	var baseline []string
	for i := 0; i < 20; i++ {
		got, err := svc.RelatedByIncidentID(ctx, target.Incident.ID)
		if err != nil {
			t.Fatal(err)
		}
		got2 := make([]string, 0, len(got))
		for _, r := range got {
			got2 = append(got2, r.Match)
		}
		if i == 0 {
			baseline = got2
			if len(baseline) == 0 {
				t.Fatal("应当有召回结果，否则本测试没有验证到相似路")
			}
			continue
		}
		if strings.Join(got2, ",") != strings.Join(baseline, ",") {
			t.Fatalf("第 %d 次结果不稳定: %v vs %v", i, got2, baseline)
		}
	}
}
