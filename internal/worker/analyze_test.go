package worker

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mosterakie/DevLens/internal/domain"
	"github.com/mosterakie/DevLens/internal/repository"
)

// TestHandlePersistsAnalysis 覆盖主路径：分析成功后写入结果并转 OPEN。
func TestHandlePersistsAnalysis(t *testing.T) {
	db := setupDB(t)
	ctx := context.Background()

	inc := seedIncident(t, db, "ERROR: context deadline exceeded\npool exhausted", "norm", 0x11)

	fa := &fakeAnalyzer{result: okResult("数据库连接池超时")}
	w := newWorker(t, db, fa, &fakeQueue{})

	if err := w.Handle(ctx, inc.ID); err != nil {
		t.Fatalf("Handle 失败: %v", err)
	}

	// 分析结果落库。
	a, err := repository.NewAnalysisRepo(db).GetByIncidentID(ctx, inc.ID)
	if err != nil {
		t.Fatalf("读取分析失败: %v", err)
	}
	if a.Summary == "" {
		t.Error("summary 不应为空")
	}
	if len(a.Evidence) != 1 || a.Evidence[0].SourceLine != 12 {
		t.Errorf("evidence 未正确保存: %+v", a.Evidence)
	}
	if a.Model != "fake-model" {
		t.Errorf("model = %q", a.Model)
	}

	// 状态转为 OPEN，且 title / severity / category 写到了 incidents 表。
	got, err := repository.NewIncidentRepo(db).GetByID(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusOpen {
		t.Errorf("status = %q, want OPEN", got.Status)
	}
	if got.Title != "数据库连接池超时" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Severity != domain.SeverityHigh {
		t.Errorf("severity = %q, want HIGH", got.Severity)
	}
	if got.Category != "Database / Timeout" {
		t.Errorf("category = %q", got.Category)
	}
}

// TestHandleIsIdempotent 是队列没有消费确认时的必要保障。
//
// worker 可能重复消费同一条消息。重复处理必须是无操作，
// 否则会产生两份分析、或者把已完成的状态改回去。
func TestHandleIsIdempotent(t *testing.T) {
	db := setupDB(t)
	ctx := context.Background()

	inc := seedIncident(t, db, "log", "norm", 0x22)

	fa := &fakeAnalyzer{result: okResult("第一次")}
	w := newWorker(t, db, fa, &fakeQueue{})

	if err := w.Handle(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}
	firstCalls := len(fa.calls)

	// 第二次处理：应当直接跳过，不再调用模型。
	fa.result = okResult("第二次")
	if err := w.Handle(ctx, inc.ID); err != nil {
		t.Fatalf("重复 Handle 应当成功, got %v", err)
	}

	if len(fa.calls) != firstCalls {
		t.Errorf("重复处理不该再调用模型：调用数 %d -> %d", firstCalls, len(fa.calls))
	}

	// 分析内容保持第一次的结果。
	a, err := repository.NewAnalysisRepo(db).GetByIncidentID(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Summary == "" {
		t.Error("分析记录不应被清空")
	}

	// 仍然只有一份分析。
	var count int
	if err := db.Pool().QueryRow(ctx,
		`SELECT count(*) FROM incident_analysis WHERE incident_id = $1`, inc.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("分析记录数 = %d, want 1", count)
	}
}

// TestHandleMarksFailedOnAnalyzerError 确认分析失败时状态转 FAILED 并留痕。
//
// 用户需要知道"为什么没有结果"，所以失败原因要写进事件里。
func TestHandleMarksFailedOnAnalyzerError(t *testing.T) {
	db := setupDB(t)
	ctx := context.Background()

	inc := seedIncident(t, db, "log", "norm", 0x33)

	fa := &fakeAnalyzer{err: errors.New("模型不可用")}
	w := newWorker(t, db, fa, &fakeQueue{})

	// Handle 会把失败写库后返回错误，这里只关心库里的状态。
	_ = w.Handle(ctx, inc.ID)

	got, err := repository.NewIncidentRepo(db).GetByID(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusFailed {
		t.Errorf("status = %q, want FAILED", got.Status)
	}

	// 失败原因要留在事件里，否则用户只看到 FAILED 而不知为何。
	events, err := repository.NewIncidentRepo(db).ListEvents(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range events {
		if e.EventType == "analysis_failed" {
			found = true
		}
	}
	if !found {
		t.Errorf("应当记录 analysis_failed 事件, 实际事件: %+v", events)
	}

	// 失败时不该写入分析结果。
	if _, err := repository.NewAnalysisRepo(db).GetByIncidentID(ctx, inc.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("失败时不应有分析记录, got %v", err)
	}
}

// TestHandleMissingIncident 确认目标不存在时返回错误而不是 panic。
func TestHandleMissingIncident(t *testing.T) {
	db := setupDB(t)
	w := newWorker(t, db, &fakeAnalyzer{result: okResult("x")}, &fakeQueue{})

	err := w.Handle(context.Background(), 99999)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("应当返回 ErrNotFound, got %v", err)
	}
}

// TestHandlePassesLanguageToAnalyzer 确认语言取自提交时记录的值。
//
// 这样同一条记录无论何时重跑都用同一种语言生成。
func TestHandlePassesLanguageToAnalyzer(t *testing.T) {
	db := setupDB(t)
	ctx := context.Background()

	buf := make([]byte, 16)
	buf[0] = 0x44
	uid := int64(1)
	inc, err := repository.NewIncidentRepo(db).Create(ctx, repository.CreateIncidentInput{
		RawLog:      "log",
		Normalized:  "norm",
		Fingerprint: buf,
		CreatedBy:   &uid,
		Lang:        domain.LangEn,
	})
	if err != nil {
		t.Fatal(err)
	}

	fa := &fakeAnalyzer{result: okResult("English title")}
	w := newWorker(t, db, fa, &fakeQueue{})
	if err := w.Handle(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}

	if len(fa.calls) != 1 {
		t.Fatalf("模型调用次数 = %d, want 1", len(fa.calls))
	}
	if fa.calls[0].Lang != domain.LangEn {
		t.Errorf("传给模型的语言 = %q, want en", fa.calls[0].Lang)
	}
}

// TestHandlePassesRelatedSummaries 确认同类历史会一并交给模型参考。
func TestHandlePassesRelatedSummaries(t *testing.T) {
	db := setupDB(t)
	ctx := context.Background()
	repo := repository.NewIncidentRepo(db)

	// 先建两条同指纹的记录，第一条手动设为 OPEN 并给个标题。
	first := seedIncident(t, db, "log a", "norm a", 0x55)
	if err := repo.UpdateStatus(ctx, first.ID, domain.StatusOpen); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx,
		`UPDATE incidents SET title = '既有的同类问题' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}

	second := seedIncident(t, db, "log b", "norm b", 0x55)

	fa := &fakeAnalyzer{result: okResult("x")}
	w := newWorker(t, db, fa, &fakeQueue{})
	if err := w.Handle(ctx, second.ID); err != nil {
		t.Fatal(err)
	}

	if len(fa.calls) != 1 {
		t.Fatalf("模型调用次数 = %d, want 1", len(fa.calls))
	}
	if len(fa.calls[0].RelatedSummaries) != 1 {
		t.Fatalf("应当带上 1 条同类摘要, got %v", fa.calls[0].RelatedSummaries)
	}
	if !strings.Contains(fa.calls[0].RelatedSummaries[0], "既有的同类问题") {
		t.Errorf("摘要应当包含标题, got %q", fa.calls[0].RelatedSummaries[0])
	}
}

// TestHandlePassesSimilarRelatedToModel 是改动 2 在 worker 侧的验收点。
//
// worker 拿不到 service（依赖方向是 worker→repository），所以它必须
// 自己合并两路。如果漏了这一步，模型看不到"措辞不同但同类"的历史，
// 诊断质量会与用户看到的相关历史脱节。
func TestHandlePassesSimilarRelatedToModel(t *testing.T) {
	db := setupDB(t)
	ctx := context.Background()
	repo := repository.NewIncidentRepo(db)

	// 既有记录：归一化文本与待分析的记录高度相似，但指纹不同。
	const oldNorm = "pubsub failed to dial postgres network tcp connection refused coderd failed to ping database"
	old := seedIncident(t, db, "old raw", oldNorm, 0x71)
	if err := repo.UpdateStatus(ctx, old.ID, domain.StatusOpen); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx,
		`UPDATE incidents SET title = '历史数据库连接故障' WHERE id = $1`, old.ID); err != nil {
		t.Fatal(err)
	}

	// 待分析记录：措辞略变，指纹不同（0x72 != 0x71）。
	const newNorm = "pubsub failed to dial postgres network tcp connection refused coderd database ping failed"
	target := seedIncident(t, db, "new raw", newNorm, 0x72)

	fa := &fakeAnalyzer{result: okResult("x")}
	w := newWorker(t, db, fa, &fakeQueue{})
	if err := w.Handle(ctx, target.ID); err != nil {
		t.Fatal(err)
	}

	if len(fa.calls) != 1 {
		t.Fatalf("模型调用次数 = %d, want 1", len(fa.calls))
	}
	summaries := fa.calls[0].RelatedSummaries
	if len(summaries) != 1 {
		t.Fatalf("应当带上 1 条相似历史摘要, got %v", summaries)
	}
	if !strings.Contains(summaries[0], "历史数据库连接故障") {
		t.Errorf("摘要应当包含标题, got %q", summaries[0])
	}
	// 必须标出这是"可能同类"，否则模型会把推测当事实参考。
	if !strings.Contains(summaries[0], "可能同类") {
		t.Errorf("相似命中应当在摘要里标注为可能同类, got %q", summaries[0])
	}
}

// TestHandlePassesExactRelatedMarkedExact 确认精确命中在摘要里标成"确定同类"。
func TestHandlePassesExactRelatedMarkedExact(t *testing.T) {
	db := setupDB(t)
	ctx := context.Background()
	repo := repository.NewIncidentRepo(db)

	first := seedIncident(t, db, "log a", "norm identical text", 0x73)
	if err := repo.UpdateStatus(ctx, first.ID, domain.StatusOpen); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx,
		`UPDATE incidents SET title = '确定的同类' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}

	second := seedIncident(t, db, "log b", "norm identical text", 0x73)

	fa := &fakeAnalyzer{result: okResult("x")}
	w := newWorker(t, db, fa, &fakeQueue{})
	if err := w.Handle(ctx, second.ID); err != nil {
		t.Fatal(err)
	}

	summaries := fa.calls[0].RelatedSummaries
	if len(summaries) != 1 {
		t.Fatalf("应当带上 1 条摘要, got %v", summaries)
	}
	if !strings.Contains(summaries[0], "确定同类") {
		t.Errorf("精确命中应当标注为确定同类, got %q", summaries[0])
	}
}

// TestHandleExcludesSelfFromRelated 确认 worker 不会把记录自己喂给模型。
func TestHandleExcludesSelfFromRelated(t *testing.T) {
	db := setupDB(t)
	ctx := context.Background()

	target := seedIncident(t, db, "raw", "some normalized text", 0x74)

	fa := &fakeAnalyzer{result: okResult("x")}
	w := newWorker(t, db, fa, &fakeQueue{})
	if err := w.Handle(ctx, target.ID); err != nil {
		t.Fatal(err)
	}

	prefix := "#" + strconv.FormatInt(target.ID, 10) + " "
	for _, s := range fa.calls[0].RelatedSummaries {
		if strings.HasPrefix(s, prefix) {
			t.Errorf("记录自己不该出现在相关历史里: %q", s)
		}
	}
}

// TestHandleUnrelatedHasNoSummaries 确认无关历史不会被硬塞给模型。
func TestHandleUnrelatedHasNoSummaries(t *testing.T) {
	db := setupDB(t)
	ctx := context.Background()
	repo := repository.NewIncidentRepo(db)

	first := seedIncident(t, db,
		"panic: runtime error: invalid memory address or nil pointer dereference",
		"panic runtime error invalid memory address nil pointer dereference goroutine running", 0x75)
	if err := repo.UpdateStatus(ctx, first.ID, domain.StatusOpen); err != nil {
		t.Fatal(err)
	}

	second := seedIncident(t, db, "postgres refused", "postgres connection refused database timeout", 0x76)

	fa := &fakeAnalyzer{result: okResult("x")}
	w := newWorker(t, db, fa, &fakeQueue{})
	if err := w.Handle(ctx, second.ID); err != nil {
		t.Fatal(err)
	}

	if len(fa.calls[0].RelatedSummaries) != 0 {
		t.Errorf("无关历史不该喂给模型, got %v", fa.calls[0].RelatedSummaries)
	}
}

// TestRequeueStale 覆盖自愈机制：卡住的 ANALYZING 记录会被重新入队。
//
// 队列没有消费确认，worker 崩溃会丢任务。这个扫描是兜底。
func TestRequeueStale(t *testing.T) {
	db := setupDB(t)
	ctx := context.Background()

	// 一条正常的 ANALYZING（刚创建）和一条"卡住"的。
	recent := seedIncident(t, db, "recent", "recent", 0x66)

	stuck := seedIncident(t, db, "stuck", "stuck", 0x77)
	// 把创建时间改到 10 分钟前。
	if _, err := db.Pool().Exec(ctx,
		`UPDATE incidents SET created_at = now() - interval '10 minutes' WHERE id = $1`, stuck.ID); err != nil {
		t.Fatal(err)
	}

	q := &fakeQueue{}
	w := newWorker(t, db, &fakeAnalyzer{result: okResult("x")}, q)

	n, err := w.RequeueStale(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("RequeueStale 失败: %v", err)
	}
	if n != 1 {
		t.Errorf("应当重新入队 1 条, got %d", n)
	}
	if len(q.enqueued) != 1 || q.enqueued[0] != stuck.ID {
		t.Errorf("重新入队的是 %v, 期望 [%d]", q.enqueued, stuck.ID)
	}
	// 刚创建的记录不该被误判为卡住。
	for _, id := range q.enqueued {
		if id == recent.ID {
			t.Error("刚创建的记录不该被重新入队")
		}
	}
}

// TestRequeueStaleSkipsNonAnalyzing 确认只处理 ANALYZING 状态。
//
// 已完成的记录重新入队会导致重复分析。
func TestRequeueStaleSkipsNonAnalyzing(t *testing.T) {
	db := setupDB(t)
	ctx := context.Background()

	repo := repository.NewIncidentRepo(db)
	inc := seedIncident(t, db, "done", "done", 0x88)
	if err := repo.UpdateStatus(ctx, inc.ID, domain.StatusOpen); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx,
		`UPDATE incidents SET created_at = now() - interval '10 minutes' WHERE id = $1`, inc.ID); err != nil {
		t.Fatal(err)
	}

	q := &fakeQueue{}
	w := newWorker(t, db, &fakeAnalyzer{result: okResult("x")}, q)

	n, err := w.RequeueStale(ctx, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("不该重新入队任何记录, got %d", n)
	}
}

// TestRequeueStaleEmpty 确认没有卡住的任务时不报错。
func TestRequeueStaleEmpty(t *testing.T) {
	db := setupDB(t)
	q := &fakeQueue{}
	w := newWorker(t, db, &fakeAnalyzer{result: okResult("x")}, q)

	n, err := w.RequeueStale(context.Background(), 5*time.Minute)
	if err != nil {
		t.Fatalf("空集不该报错: %v", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

// TestRunStopsOnContextCancel 确认 Run 能在 context 取消后退出。
func TestRunStopsOnContextCancel(t *testing.T) {
	db := setupDB(t)
	q := &fakeQueue{}
	w := newWorker(t, db, &fakeAnalyzer{result: okResult("x")}, q)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run 应当正常退出, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run 未在 3 秒内退出")
	}
}
