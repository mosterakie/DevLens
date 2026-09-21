package migrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosterakie/DevLens/internal/testsupport"
)

// 集成测试需要真实 PostgreSQL。
//
// 用独立 schema：go test ./... 按包并行，各包都在 public 上建表删表
// 会互相踩踏，表现为随机失败。详见 internal/testsupport。
const testSchema = "test_migrate"

// testPool 返回限定在该 schema 上的连接池。
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool := testsupport.Pool(t, testSchema)
	reset(t, pool)
	return pool
}

// reset 删掉迁移测试可能创建的对象，让每次测试互不影响。
//
// 只动自己的 schema，所以并行跑的其它包不受影响。
func reset(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	sql := "DROP TABLE IF EXISTS " + testSchema + ".schema_migrations, " +
		testSchema + ".mig_alpha, " + testSchema + ".mig_beta, " +
		testSchema + ".mig_gamma CASCADE"
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

	// 表建出来了。只查自己的 schema，避免被 public 里的同名表误导。
	for _, table := range []string{"mig_alpha", "mig_beta"} {
		var exists bool
		err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = $1 AND table_name = $2
			)`, testSchema, table).Scan(&exists)
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("表 %s.%s 未创建", testSchema, table)
		}
	}

	applied, total, err := Status(ctx, pool, migs)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 2 || total != 2 {
		t.Errorf("Status = %d/%d, want 2/2", applied, total)
	}
}

// TestApplyIsIdempotent 是最重要的一条：重复启动不能重复执行。
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
// 关键点：失败那条里已经执行成功的语句也必须撤销，
// 否则会留下半成品结构——而版本号又没登记，重试时会再撞一次。
func TestFailedMigrationRollsBack(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	dir := migDir(t, map[string]string{
		"0001_alpha.up.sql": "CREATE TABLE mig_alpha (id int PRIMARY KEY);",
		"0002_beta.up.sql": "CREATE TABLE mig_beta (id int PRIMARY KEY);\n" +
			"INSERT INTO table_that_does_not_exist VALUES (1);",
	})

	migs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := New(pool, nil).Apply(ctx, migs); err == nil {
		t.Fatal("迁移应当失败")
	}

	// 第一条已提交，表应存在。
	var hasAlpha bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.tables
		WHERE table_schema = $1 AND table_name = 'mig_alpha')`,
		testSchema).Scan(&hasAlpha); err != nil {
		t.Fatal(err)
	}
	if !hasAlpha {
		t.Error("0001 应当已提交")
	}

	// 第二条回滚，它建的表不应存在。
	var hasBeta bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.tables
		WHERE table_schema = $1 AND table_name = 'mig_beta')`,
		testSchema).Scan(&hasBeta); err != nil {
		t.Fatal(err)
	}
	if hasBeta {
		t.Error("0002 失败后 mig_beta 应当被回滚")
	}

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

	if err := New(pool, nil).Apply(ctx, changed); !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("应当报 ErrChecksumMismatch, got %v", err)
	}
}

func TestApplyEmptyIsNoop(t *testing.T) {
	pool := testPool(t)
	if err := New(pool, nil).Apply(context.Background(), nil); err != nil {
		t.Errorf("空迁移集不应报错: %v", err)
	}
}

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
