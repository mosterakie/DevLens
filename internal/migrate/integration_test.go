package migrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 集成测试需要真实 PostgreSQL，用与 repository 相同的 TEST_DATABASE_URL。
// 库名不以 _test 结尾时拒绝运行：测试会建表删表。

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("跳过集成测试（-short）")
	}

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://devlens:devlens@127.0.0.1:5432/devlens_test?sslmode=disable"
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("解析连接串: %v", err)
	}
	if db := cfg.ConnConfig.Database; len(db) < 5 || db[len(db)-5:] != "_test" {
		t.Fatalf("拒绝在库 %q 上运行：库名必须以 _test 结尾（测试会建表删表）", db)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("连接测试库失败，跳过: %v", err)
	}
	t.Cleanup(pool.Close)

	// 每次测试从干净状态开始。只删本包会创建的对象。
	reset(t, pool)
	return pool
}

// reset 删掉迁移测试可能创建的表，让每次测试互不影响。
func reset(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	const sql = `
		DROP TABLE IF EXISTS schema_migrations, mig_alpha, mig_beta, mig_gamma CASCADE;`
	if _, err := pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("清理失败: %v", err)
	}
}

func migDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestApplyCreatesSchema(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	dir := migDir(t, map[string]string{
		"0001_alpha.up.sql": "CREATE TABLE mig_alpha (id int PRIMARY KEY);",
		"0002_beta.up.sql":  "CREATE TABLE mig_beta (id int PRIMARY KEY);",
	})

	migs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := New(pool, nil).Apply(ctx, migs); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}

	// 表建出来了。
	for _, table := range []string{"mig_alpha", "mig_beta"} {
		var exists bool
		err := pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM information_schema.tables
			WHERE table_name = $1)`, table).Scan(&exists)
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("表 %s 未创建", table)
		}
	}

	// 版本登记了。
	applied, total, err := Status(ctx, pool, migs)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 2 || total != 2 {
		t.Errorf("Status = %d/%d, want 2/2", applied, total)
	}
}

// TestApplyIsIdempotent 是这一版最重要的性质：重复启动不能重复执行。
//
// 迁移里的 CREATE TABLE 没有 IF NOT EXISTS，第二次执行必然报错。
func TestApplyIsIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	dir := migDir(t, map[string]string{
		"0001_alpha.up.sql": "CREATE TABLE mig_alpha (id int PRIMARY KEY);",
	})
	migs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	runner := New(pool, nil)
	if err := runner.Apply(ctx, migs); err != nil {
		t.Fatalf("首次 Apply 失败: %v", err)
	}
	// 第二次必须跳过而不是重复执行。
	if err := runner.Apply(ctx, migs); err != nil {
		t.Fatalf("重复 Apply 应当跳过，实际失败: %v", err)
	}

	applied, _, err := Status(ctx, pool, migs)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Errorf("已应用 %d 条, want 1（不应重复登记）", applied)
	}
}

// TestFailedMigrationRollsBack 验证失败的迁移完全回滚。
//
// 关键点：失败的那条里已经执行成功的语句也必须撤销，
// 否则会留下半成品结构——而版本号又没登记，重试时会再撞一次。
func TestFailedMigrationRollsBack(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	dir := migDir(t, map[string]string{
		"0001_alpha.up.sql": "CREATE TABLE mig_alpha (id int PRIMARY KEY);",
		// 第二条先建表再引用不存在的表，整条应当回滚。
		"0002_beta.up.sql": "CREATE TABLE mig_beta (id int PRIMARY KEY);\n" +
			"INSERT INTO table_that_does_not_exist VALUES (1);",
	})

	migs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	err = New(pool, nil).Apply(ctx, migs)
	if err == nil {
		t.Fatal("迁移应当失败")
	}

	// 第一条已提交，表应存在。
	var hasAlpha bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.tables
		WHERE table_name = 'mig_alpha')`).Scan(&hasAlpha); err != nil {
		t.Fatal(err)
	}
	if !hasAlpha {
		t.Error("0001 应当已提交")
	}

	// 第二条回滚，它建的表不应存在。
	var hasBeta bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.tables
		WHERE table_name = 'mig_beta')`).Scan(&hasBeta); err != nil {
		t.Fatal(err)
	}
	if hasBeta {
		t.Error("0002 失败后 mig_beta 应当被回滚")
	}

	// 失败的不登记版本，否则重试会看到"已完成"而跳过。
	applied, _, err := Status(ctx, pool, migs)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Errorf("已登记 %d 条, want 1（失败的不应登记）", applied)
	}
}

// TestChecksumMismatchIsDetected 验证"已应用的迁移被改动"能被发现。
//
// 这类改动不会报错，只会让不同环境的结构悄悄分叉，所以必须主动检查。
func TestChecksumMismatchIsDetected(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	dir := migDir(t, map[string]string{
		"0001_alpha.up.sql": "CREATE TABLE mig_alpha (id int PRIMARY KEY);",
	})
	migs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := New(pool, nil).Apply(ctx, migs); err != nil {
		t.Fatal(err)
	}

	// 改动已应用的迁移内容后重新加载。
	if err := os.WriteFile(
		filepath.Join(dir, "0001_alpha.up.sql"),
		[]byte("CREATE TABLE mig_alpha (id bigint PRIMARY KEY);"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	changed, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	err = New(pool, nil).Apply(ctx, changed)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("应当报 ErrChecksumMismatch, got %v", err)
	}
}

// TestApplyEmptyIsNoop 确认没有迁移时不报错。
func TestApplyEmptyIsNoop(t *testing.T) {
	pool := testPool(t)
	if err := New(pool, nil).Apply(context.Background(), nil); err != nil {
		t.Errorf("空迁移集不应报错: %v", err)
	}
}

// TestStatusBeforeAnyMigration 确认在从未迁移过的库上 Status 不报错。
func TestStatusBeforeAnyMigration(t *testing.T) {
	pool := testPool(t)
	applied, total, err := Status(context.Background(), pool, []Migration{{Version: 1}})
	if err != nil {
		t.Fatalf("Status 失败: %v", err)
	}
	if applied != 0 || total != 1 {
		t.Errorf("Status = %d/%d, want 0/1", applied, total)
	}
}
