package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosterakie/DevLens/internal/analyzer"
	"github.com/mosterakie/DevLens/internal/domain"
	"github.com/mosterakie/DevLens/internal/repository"
	"github.com/mosterakie/DevLens/internal/testsupport"
)

// 集成测试需要真实 PostgreSQL。
//
// 用独立 schema：go test ./... 按包并行，各包都在 public 上建表删表
// 会互相踩踏，表现为随机失败（"关系 incidents 不存在"）。
// 详见 internal/testsupport。
const testSchema = "test_worker"

// setupDB 建连接、应用迁移、清空数据。
func setupDB(t *testing.T) *repository.DB {
	t.Helper()

	pool := testsupport.Pool(t, testSchema)
	applyMigrations(t, pool)
	testsupport.Truncate(t, pool, testSchema)

	return repository.NewWithPool(pool)
}

// applyMigrations 按文件名顺序执行 migrations 下的 up 脚本。
func applyMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	dir := testsupport.FindMigrationsDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取迁移目录: %v", err)
	}

	var ups []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" &&
			len(e.Name()) > 7 && e.Name()[len(e.Name())-7:] == ".up.sql" {
			ups = append(ups, e.Name())
		}
	}
	sort.Strings(ups)

	ctx := context.Background()
	testsupport.DropAll(t, pool, testSchema)

	for _, name := range ups {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("读取 %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("执行 %s: %v", name, err)
		}
	}
}

// errQueueEmpty 供假队列使用。队列包有自己的 ErrQueueEmpty，
// 但这里不需要依赖它——worker 只判断是不是错误。
var errQueueEmpty = errors.New("queue empty")

// --- 假实现 ---

// fakeAnalyzer 返回预设结果，避免测试依赖外部 API。
type fakeAnalyzer struct {
	result analyzer.Result
	err    error

	// 记录收到的输入，用于验证 worker 传对了什么。
	calls []analyzer.Input
}

func (f *fakeAnalyzer) Name() string { return "fake-model" }

func (f *fakeAnalyzer) Analyze(_ context.Context, in analyzer.Input) (analyzer.Result, error) {
	f.calls = append(f.calls, in)
	if f.err != nil {
		return analyzer.Result{}, f.err
	}
	return f.result, nil
}

// okResult 构造一份合法的分析结果。
func okResult(title string) analyzer.Result {
	return analyzer.Result{
		Analysis: &domain.Analysis{
			Summary:          "连接池在等待可用连接时超时。",
			PossibleCauses:   []string{"连接池耗尽", "慢查询占住连接"},
			Evidence:         []domain.Evidence{{Key: "超时", Value: "5s", SourceLine: 12}},
			SuggestedActions: []string{"检查连接池使用率"},
			Model:            "fake-model",
			PromptVersion:    "v2",
			Confidence:       0.8,
			RawResponse:      `{"summary":"..."}`,
		},
		Title:    title,
		Severity: domain.SeverityHigh,
		Category: "Database / Timeout",
	}
}

// fakeQueue 记录入队，不做真实队列操作。
type fakeQueue struct {
	enqueued []int64
	ids      []int64
	idx      int
	err      error
}

func (q *fakeQueue) DequeueAnalyze(_ context.Context, _ time.Duration) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}
	if q.idx >= len(q.ids) {
		return 0, errQueueEmpty
	}
	id := q.ids[q.idx]
	q.idx++
	return id, nil
}

func (q *fakeQueue) EnqueueAnalyze(_ context.Context, id int64) error {
	if q.err != nil {
		return q.err
	}
	q.enqueued = append(q.enqueued, id)
	return nil
}

// --- 辅助 ---

func newWorker(t *testing.T, db *repository.DB, a analyzer.Analyzer, q Queue) *AnalyzerWorker {
	t.Helper()
	log := slog.New(slog.NewTextHandler(&discard{}, &slog.HandlerOptions{Level: slog.LevelError}))
	return New(db, repository.NewIncidentRepo(db), repository.NewAnalysisRepo(db), q, a, log)
}

// discard 丢弃日志输出，避免测试刷屏。
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func seedIncident(t *testing.T, db *repository.DB, raw, norm string, fp byte) *domain.Incident {
	t.Helper()
	buf := make([]byte, 16)
	for i := range buf {
		buf[i] = fp
	}
	uid := int64(1)
	inc, err := repository.NewIncidentRepo(db).Create(context.Background(), repository.CreateIncidentInput{
		RawLog:      raw,
		Normalized:  norm,
		Fingerprint: buf,
		CreatedBy:   &uid,
		Lang:        domain.LangZhHans,
	})
	if err != nil {
		t.Fatalf("建记录失败: %v", err)
	}
	return inc
}
