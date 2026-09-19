package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mosterakie/DevLens/internal/domain"
)

func sampleAnalysis(incidentID int64) *domain.Analysis {
	return &domain.Analysis{
		IncidentID:       incidentID,
		Summary:          "连接池在等待可用连接时超时。",
		PossibleCauses:   []string{"连接池耗尽", "慢查询占住连接"},
		Evidence:         []domain.Evidence{{Key: "超时", Value: "5s", SourceLine: 12}},
		SuggestedActions: []string{"检查连接池使用率"},
		Model:            "deepseek-flash",
		PromptVersion:    "v2",
		Confidence:       0.8,
		RawResponse:      `{"summary":"连接池在等待可用连接时超时。"}`,
	}
}

func TestSaveAndGetAnalysis(t *testing.T) {
	db := setupDB(t)
	repo := NewAnalysisRepo(db)
	ctx := context.Background()

	inc := mustCreate(t, db, "log", "norm", fp(1))

	if err := repo.Save(ctx, sampleAnalysis(inc.ID)); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}

	got, err := repo.GetByIncidentID(ctx, inc.ID)
	if err != nil {
		t.Fatalf("GetByIncidentID 失败: %v", err)
	}

	if got.Summary != "连接池在等待可用连接时超时。" {
		t.Errorf("Summary = %q", got.Summary)
	}
	if len(got.PossibleCauses) != 2 {
		t.Errorf("PossibleCauses 数量 = %d, want 2", len(got.PossibleCauses))
	}
	// JSONB 往返必须保真，否则证据的行号会丢。
	if len(got.Evidence) != 1 || got.Evidence[0].SourceLine != 12 {
		t.Errorf("Evidence 往返后不正确: %+v", got.Evidence)
	}
	if got.Confidence != 0.8 {
		t.Errorf("Confidence = %v", got.Confidence)
	}
	if got.Model != "deepseek-flash" || got.PromptVersion != "v2" {
		t.Errorf("模型与版本号未正确保存: %q %q", got.Model, got.PromptVersion)
	}
	if got.RawResponse == "" {
		t.Error("RawResponse 应被保留，用于排查")
	}
}

func TestGetAnalysisNotFound(t *testing.T) {
	db := setupDB(t)
	repo := NewAnalysisRepo(db)

	_, err := repo.GetByIncidentID(context.Background(), 1234)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("应返回 ErrNotFound, got %v", err)
	}
}

// TestSaveAnalysisIsIdempotent 是重试能安全进行的前提。
//
// worker 可能重复消费同一条消息，第二次写入必须覆盖而不是报错，
// 也不能产生两行。
func TestSaveAnalysisIsIdempotent(t *testing.T) {
	db := setupDB(t)
	repo := NewAnalysisRepo(db)
	ctx := context.Background()

	inc := mustCreate(t, db, "log", "norm", fp(2))

	first := sampleAnalysis(inc.ID)
	if err := repo.Save(ctx, first); err != nil {
		t.Fatalf("首次 Save 失败: %v", err)
	}

	second := sampleAnalysis(inc.ID)
	second.Summary = "第二次的结论"
	second.Confidence = 0.6
	if err := repo.Save(ctx, second); err != nil {
		t.Fatalf("重复 Save 应当成功（幂等），实际失败: %v", err)
	}

	var count int
	if err := db.pool.QueryRow(ctx,
		`SELECT count(*) FROM incident_analysis WHERE incident_id = $1`, inc.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("应只有 1 行分析记录, got %d", count)
	}

	got, err := repo.GetByIncidentID(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "第二次的结论" {
		t.Errorf("重复写入应覆盖, Summary = %q", got.Summary)
	}
}

// TestEmptyListsRoundTripAsEmptyNotNil 覆盖 JSONB 的边界。
//
// 空切片序列化成 [] 而不是 null，前端拿到的是空数组，
// 不必额外判空。
func TestEmptyListsRoundTripAsEmpty(t *testing.T) {
	db := setupDB(t)
	repo := NewAnalysisRepo(db)
	ctx := context.Background()

	inc := mustCreate(t, db, "log", "norm", fp(3))

	a := sampleAnalysis(inc.ID)
	a.PossibleCauses = []string{}
	a.Evidence = []domain.Evidence{}
	a.SuggestedActions = []string{}
	if err := repo.Save(ctx, a); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByIncidentID(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PossibleCauses == nil {
		t.Error("空的 possible_causes 应回读为空切片而不是 nil")
	}
	if len(got.PossibleCauses) != 0 {
		t.Errorf("possible_causes 应为空, got %v", got.PossibleCauses)
	}
}

// TestAnalysisCascadeDelete 确认删除 Incident 时分析结果一并删除。
func TestAnalysisCascadeDelete(t *testing.T) {
	db := setupDB(t)
	repo := NewAnalysisRepo(db)
	ctx := context.Background()

	inc := mustCreate(t, db, "log", "norm", fp(4))
	if err := repo.Save(ctx, sampleAnalysis(inc.ID)); err != nil {
		t.Fatal(err)
	}

	if _, err := db.pool.Exec(ctx, `DELETE FROM incidents WHERE id = $1`, inc.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.GetByIncidentID(ctx, inc.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("级联删除后应查不到, got %v", err)
	}
}

func TestListFiltersAndCursor(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	// 建 5 条，分别设置状态和严重程度。
	for i := 0; i < 5; i++ {
		inc := mustCreate(t, db, "log", "norm", fp(byte(10+i)))
		if err := repo.UpdateStatus(ctx, inc.ID, domain.StatusOpen); err != nil {
			t.Fatal(err)
		}
		sev := domain.SeverityHigh
		if i%2 == 0 {
			sev = domain.SeverityLow
		}
		if _, err := db.pool.Exec(ctx,
			`UPDATE incidents SET severity = $2 WHERE id = $1`, inc.ID, sev); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("按严重程度过滤", func(t *testing.T) {
		sev := domain.SeverityLow
		got, err := repo.List(ctx, ListFilter{Severity: &sev, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Errorf("LOW 应有 3 条, got %d", len(got))
		}
	})

	t.Run("按状态过滤", func(t *testing.T) {
		st := domain.StatusOpen
		got, err := repo.List(ctx, ListFilter{Status: &st, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 5 {
			t.Errorf("OPEN 应有 5 条, got %d", len(got))
		}
	})

	t.Run("游标分页不重复不遗漏", func(t *testing.T) {
		seen := map[int64]bool{}

		page1, err := repo.List(ctx, ListFilter{Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(page1) != 2 {
			t.Fatalf("第一页应有 2 条, got %d", len(page1))
		}
		for _, inc := range page1 {
			seen[inc.ID] = true
		}

		last := page1[len(page1)-1]
		page2, err := repo.List(ctx, ListFilter{
			Limit:           2,
			CursorCreatedAt: &last.CreatedAt,
			CursorID:        &last.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, inc := range page2 {
			if seen[inc.ID] {
				t.Errorf("游标分页返回了重复记录 %d", inc.ID)
			}
			seen[inc.ID] = true
		}
		if len(seen) != 4 {
			t.Errorf("两页合计应有 4 条不同记录, got %d", len(seen))
		}
	})
}

func TestListDefaultAndMaxLimit(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		mustCreate(t, db, "log", "norm", fp(byte(20+i)))
	}

	// limit <= 0 应当走默认值 20，而不是返回空。
	got, err := repo.List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("默认 limit 下应返回全部 3 条, got %d", len(got))
	}

	// 超出上限（100）应被夹到 100，这里只验证不报错。
	if _, err := repo.List(ctx, ListFilter{Limit: 1000}); err != nil {
		t.Errorf("超大的 limit 不应报错: %v", err)
	}
}

var _ = time.Now
