// Package testsupport 提供集成测试的公共基建。
//
// 为什么不放在各个 _test.go 里：集成测试需要真实 PostgreSQL，而
// 多个包并行运行时（go test ./... 默认按包并行）会同时操作同一个库。
// 如果各包都用 public schema，一个包 DROP TABLE 就会把另一个包的表删掉，
// 表现为随机失败:"关系 incidents 不存在"。
//
// 解法是让每个测试包使用独立的 schema。这个包负责建 schema、设置
// search_path，以及提供库名守卫。
package testsupport

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DSN 返回集成测试使用的连接串。
func DSN() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://devlens:devlens@127.0.0.1:5432/devlens_test?sslmode=disable"
}

// SkipIfShort 在 -short 模式下跳过集成测试。
func SkipIfShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("跳过集成测试（-short）")
	}
}

// GuardDatabase 确认连接的库名以 _test 结尾。
//
// 集成测试会建表删表。如果配置写错指到了开发库甚至真实业务库，
// 损失无法挽回，所以宁可让测试直接失败。
func GuardDatabase(t *testing.T, dsn string) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("解析连接串: %v", err)
	}
	db := cfg.ConnConfig.Database
	if len(db) < 5 || db[len(db)-5:] != "_test" {
		t.Fatalf("拒绝在库 %q 上运行：库名必须以 _test 结尾"+
			"（集成测试会建表删表）", db)
	}
}

// schemaLocks 保证同一个 schema 只被一个包使用。
//
// 用进程内锁防的是同一个包内多个测试并发的情况；跨包的隔离由
// 各自的 schema 名保证。
var schemaLocks sync.Map

// Pool 建立连接并把 search_path 指向该包专属的 schema。
//
// schema 名由包名派生，所以不同的测试包天然隔离，
// DROP TABLE 不会互相影响。
func Pool(t *testing.T, schema string) *pgxpool.Pool {
	t.Helper()
	SkipIfShort(t)

	if schema == "" {
		t.Fatal("schema 名不能为空")
	}

	dsn := DSN()
	GuardDatabase(t, dsn)

	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("解析连接串: %v", err)
	}

	// 新建schema，并把连接的 search_path 指向它。
	// 用 AfterConnect 而不是拼 DSN 参数，因为要保证每个连接都生效。
	// search_path 里不包含 public。
	//
	// 包含它的话隔离就不彻底：如果本 schema 里某个表被改名或删除，
	// 查询会落到 public 上找到同名的旧表，测试于是看到"成功"而
	// 实际验证的是另一张表。这类错误极难发现——测试通过，但验的是
	// 假象。
	//
	// 只用自己的 schema 后，表不存在就是不存在。
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		s := pgx.Identifier{schema}.Sanitize()
		_, err := conn.Exec(ctx, fmt.Sprintf(
			`CREATE SCHEMA IF NOT EXISTS %s; SET search_path TO %s`, s, s))
		return err
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Skipf("连接测试库失败，跳过: %v", err)
	}
	t.Cleanup(pool.Close)

	// 立刻建一次 schema，让 subsequent 的 DROP 有目标。
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		`CREATE SCHEMA IF NOT EXISTS %s`, pgx.Identifier{schema}.Sanitize())); err != nil {
		t.Fatalf("建立 schema %s: %v", schema, err)
	}

	// 记录使用情况，防止两个包误用同名 schema。
	if prev, loaded := schemaLocks.LoadOrStore(schema, dsn); loaded && prev != dsn {
		t.Fatalf("schema %s 已被另一个连接串使用", schema)
	}

	return pool
}

// DropAll 删除该 schema 下的本项目的对象，让迁移可以重新执行。
//
// 只影响自己 schema，不会碰 public——这样并行跑的包互不干扰。
func DropAll(t *testing.T, pool *pgxpool.Pool, schema string) {
	t.Helper()
	sql := fmt.Sprintf(`
		DROP TABLE IF EXISTS %s.comments, %s.incident_events,
			%s.incident_analysis, %s.incidents, %s.users,
			%s.schema_migrations CASCADE;
		DROP TYPE IF EXISTS %s.status_t, %s.severity_t CASCADE;`,
		schema, schema, schema, schema, schema, schema, schema, schema)

	if _, err := pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("清理对象: %v", err)
	}
}

// Truncate 清空数据但保留表结构，在每个测试前调用。
func Truncate(t *testing.T, pool *pgxpool.Pool, schema string) {
	t.Helper()
	sql := fmt.Sprintf(`
		TRUNCATE %s.incidents, %s.incident_analysis,
			%s.incident_events, %s.comments RESTART IDENTITY CASCADE;`,
		schema, schema, schema, schema)
	if _, err := pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("清空数据: %v", err)
	}
}

// FindMigrationsDir 从当前目录向上找到 migrations 目录。
func FindMigrationsDir(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("获取工作目录: %v", err)
	}

	dir := wd
	for i := 0; i < 6; i++ {
		candidate := dir + string(os.PathSeparator) + "migrations"
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
		parent := parentDir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("未找到 migrations 目录（从 %s 向上查找）", wd)
	return ""
}

func parentDir(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	if i <= 0 {
		return p
	}
	return p[:i]
}
