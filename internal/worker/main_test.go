package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosterakie/DevLens/internal/analyzer"
	"github.com/mosterakie/DevLens/internal/domain"
	"github.com/mosterakie/DevLens/internal/repository"
)

// 集成测试需要真实 PostgreSQL。
//
// 库名必须以 _test 结尾：测试会清空全部表。go test -short 跳过。

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://devlens:devlens@127.0.0.1:5432/devlens_test?sslmode=disable"
	}
	return dsn
}

// setupDB 建立连接、建表、清空数据。
func setupDB(t *testing.T) *repository.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("跳过集成测试（-short）")
	}

	dsn := testDSN(t)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("解析连接串: %v", err)
	}
	if db := cfg.ConnConfig.Database; len(db) < 5 || db[len(db)-5:] != "_test" {
		t.Fatalf("拒绝在库 %q 上运行：库名必须以 _test 结尾（测试会清空所有表）", db)
	}

	ctx := context.Background()
	db, err := repository.New(ctx, dsn)
	if err != nil {
		t.Skipf("连接测试库失败，跳过: %v", err)
	}
	t.Cleanup(db.Close)

	applyMigrations(t, db)
	reset(t, db)
	return db
}

// applyMigrations 按文件名顺序执行 migrations 下的 up 脚本。
func applyMigrations(t *testing.T, db *repository.DB) {
	t.Helper()
	dir := findMigrationsDir(t)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取迁移目录: %v", err)
	}

	var ups []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			ups = append(ups, e.Name())
		}
	}
	sort.Strings(ups)

	ctx := context.Background()
	// 先清掉，让迁移能从零执行。
	dropAll(t, db)

	for _, name := range ups {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("读取 %s: %v", name, err)
		}
		if _, err := db.Pool().Exec(ctx, string(body)); err != nil {
			t.Fatalf("执行 %s: %v", name, err)
		}
	}
}

func dropAll(t *testing.T, db *repository.DB) {
	t.Helper()
	const sql = `
		DROP TABLE IF EXISTS comments, incident_events, incident_analysis, incidents, users, schema_migrations CASCADE;
		DROP TYPE IF EXISTS status_t, severity_t CASCADE;`
	if _, err := db.Pool().Exec(context.Background(), sql); err != nil {
		t.Fatalf("清理对象: %v", err)
	}
}

func reset(t *testing.T, db *repository.DB) {
	t.Helper()
	const sql = `TRUNCATE incidents, incident_analysis, incident_events, comments RESTART IDENTITY CASCADE;`
	if _, err := db.Pool().Exec(context.Background(), sql); err != nil {
		t.Fatalf("清空数据: %v", err)
	}
}

func findMigrationsDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "migrations")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("未找到 migrations 目录（从 %s 向上查找）", wd)
	return ""
}

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
		return 0, errors.New("queue empty")
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
