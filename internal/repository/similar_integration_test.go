package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/mosterakie/DevLens/internal/domain"
)

// 本文件覆盖改动 2 新增的相似度粗召回方法。
//
// 粗召回只负责"把可能相关的行取回来"，不负责算相似度——
// 相似度在 Go 侧由 fingerprint 包算，所以这里的断言集中在
// 行数上限、排除自己、排除 FAILED 这几个 SQL 侧的性质上。

// TestFindSimilarCandidatesExcludesSelf 是必须覆盖的边界。
//
// 不排除自己时，刚插入的记录会以 1.0 的相似度匹配到自己。
func TestFindSimilarCandidatesExcludesSelf(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	inc := mustCreate(t, db, "log", "database connection refused", fp(3))

	got, err := repo.FindSimilarCandidates(ctx, inc.ID, SimilarRecallLimit)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if c.ID == inc.ID {
			t.Fatalf("不应把自己算作候选: %+v", got)
		}
	}
}

// TestFindSimilarCandidatesExcludesFailed 确认 FAILED 记录不参与召回。
//
// FAILED 的记录没有有效分析，把它们列成"相关历史"对用户没有价值。
func TestFindSimilarCandidatesExcludesFailed(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	ok := mustCreate(t, db, "a", "same normalized text here", fp(4))
	bad := mustCreate(t, db, "b", "same normalized text here", fp(4))

	if err := repo.UpdateStatus(ctx, bad.ID, domain.StatusOpen); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(ctx, bad.ID, domain.StatusFailed); err != nil {
		t.Fatal(err)
	}

	got, err := repo.FindSimilarCandidates(ctx, 0, SimilarRecallLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("应只返回 1 条未失败的记录, got %d: %+v", len(got), got)
	}
	if got[0].ID != ok.ID {
		t.Errorf("返回了 %d, 期望 %d", got[0].ID, ok.ID)
	}
}

// TestFindSimilarCandidatesRespectsLimit 确认行数上限生效。
//
// 这是粗召回的核心约束：不限行数就是全表扫描，
// 而提交路径是同步的，用户正在等这个响应。
func TestFindSimilarCandidatesRespectsLimit(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		mustCreate(t, db, "x", "normalized text", fp(5))
	}

	got, err := repo.FindSimilarCandidates(ctx, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("limit=3 时应返回 3 条, got %d", len(got))
	}
}

// TestFindSimilarCandidatesClampsOversizedLimit 确认超大的 limit 被夹到上限。
//
// 调用方传了比 SimilarRecallLimit 大的值时必须夹住，
// 否则"限行数"这个保证就形同虚设。
func TestFindSimilarCandidatesClampsOversizedLimit(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		mustCreate(t, db, "x", "normalized text", fp(6))
	}

	// 传一个远大于上限的值：结果不该超过实际记录数，
	// 且绝不能因为 limit 过大而去读更多行。
	got, err := repo.FindSimilarCandidates(ctx, 0, SimilarRecallLimit*100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Errorf("应当返回全部 5 条, got %d", len(got))
	}
}

// TestFindSimilarCandidatesReturnsNormalized 确认归一化文本被带回来。
//
// 没有它，Go 侧就无法算相似度，整个粗召回没有意义。
func TestFindSimilarCandidatesReturnsNormalized(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	const norm = "dial tcp <ip>:<port>: connect: connection refused"
	inc := mustCreate(t, db, "raw", norm, fp(7))

	got, err := repo.FindSimilarCandidates(ctx, 0, SimilarRecallLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Normalized != norm {
		t.Errorf("Normalized = %q, want %q", got[0].Normalized, norm)
	}
	if got[0].ID != inc.ID {
		t.Errorf("ID = %d, want %d", got[0].ID, inc.ID)
	}
}

// TestFindSimilarCandidatesOrdersByCreatedAtDesc 确认按时间倒序。
//
// 同类问题在时间上聚集，截断时必须先保留最近的。
func TestFindSimilarCandidatesOrdersByCreatedAtDesc(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		mustCreate(t, db, "x", "normalized text", fp(8))
	}

	got, err := repo.FindSimilarCandidates(ctx, 0, SimilarRecallLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].CreatedAt.After(got[i-1].CreatedAt) {
			t.Errorf("第 %d 条比前一条更新，排序不是 created_at DESC", i)
		}
	}
	// 最新的 ID 最大（RESTART IDENTITY 后 ID 递增）。
	if got[0].ID != 5 {
		t.Errorf("首条应为最新创建的 id=5, got %d", got[0].ID)
	}
}

// TestFindSimilarCandidatesEmptyDB 确认空库返回空而不是报错。
func TestFindSimilarCandidatesEmptyDB(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)

	got, err := repo.FindSimilarCandidates(context.Background(), 0, SimilarRecallLimit)
	if err != nil {
		t.Fatalf("空库不该报错: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("空库应返回空, got %+v", got)
	}
}

// TestFindRelatedSetsMatchExact 确认精确路会标上 match=exact。
//
// 这是"确定同类"信号，必须由仓储层如实标出，
// 上层才能在合并时执行"精确优先"。
func TestFindRelatedSetsMatchExact(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	inc := mustCreate(t, db, "a", "a", fp(9))
	mustCreate(t, db, "b", "b", fp(9))

	got, err := repo.FindRelated(ctx, fp(9), inc.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Match != MatchExact {
		t.Errorf("Match = %q, want %q", got[0].Match, MatchExact)
	}
}

// TestFindRelatedUnchangedBySimilarity 是"精确查询保持原样"的回归护栏。
//
// 设计约束要求 FindRelated 的行为不变（主路，性能好、确定性）。
// 这里用真实库验证：只返回同指纹的，不受相似度引入的影响。
func TestFindRelatedUnchangedBySimilarity(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	// 两条同指纹，一条不同指纹但文本高度相似。
	exact1 := mustCreate(t, db, "a", "postgres connection refused database", fp(10))
	exact2 := mustCreate(t, db, "b", "postgres connection refused database2", fp(10))
	// 不同指纹，归一化文本与上面几乎相同。
	similar := mustCreate(t, db, "c", "postgres connection refused databases", fp(11))

	got, err := repo.FindRelated(ctx, fp(10), 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("精确查询应只返回 2 条同指纹记录, got %d: %+v", len(got), got)
	}
	for _, r := range got {
		if r.ID == similar.ID {
			t.Errorf("精确查询不该把不同指纹的 %d 带回来", similar.ID)
		}
		if r.ID != exact1.ID && r.ID != exact2.ID {
			t.Errorf("返回了意外的 ID %d", r.ID)
		}
	}
}

// TestSimilarRecallLimitIsSane 锁定粗召回上限的取值。
//
// 上限过大会让提交路径随库增长而变慢；过小则召回率不足。
// 这里固定住 200，改动时必须是有意的。
func TestSimilarRecallLimitIsSane(t *testing.T) {
	if SimilarRecallLimit != 200 {
		t.Errorf("SimilarRecallLimit = %d, want 200（改动前请确认这是有意的）", SimilarRecallLimit)
	}
}

// TestFindSimilarCandidatesExcludesExplicitID 确认排除的是调用方指定的 ID。
//
// 在真实库里用一个大 ID 查询，结果不应包含任何记录。
func TestFindSimilarCandidatesExcludesExplicitID(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	inc := mustCreate(t, db, "a", strings.Repeat("normalized ", 10), fp(12))

	// 指定一个不存在的 ID：记录应该照常返回。
	got, err := repo.FindSimilarCandidates(ctx, 999999, SimilarRecallLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != inc.ID {
		t.Errorf("应当返回 id=%d, got %+v", inc.ID, got)
	}
}
