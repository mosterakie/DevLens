package repository

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosterakie/DevLens/internal/domain"
)

// 集成测试需要真实的 PostgreSQL。
//
// 默认连接指向本地专用的 devlens_test 库，绝不会碰其他库：
// 每个测试开始前都会清空全部表。库名由 DATABASE_URL 覆盖，
// 但如果指向的库名不以 _test 结尾，测试会拒绝运行。
//
// 用 go test -short 跳过这些测试。

// testDSN 返回集成测试使用的连接串。
func testDSN(t *testing.T) string {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://devlens:devlens@127.0.0.1:5432/devlens_test?sslmode=disable"
	}
	return dsn
}

// guardTestDatabase 确保连接的库名以 _test 结尾。
//
// 清空表是不可逆的，如果配置写错指到了开发库甚至真实业务库，
// 损失无法挽回。所以宁可让测试直接失败。
func guardTestDatabase(t *testing.T, dsn string) {
	t.Helper()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("解析连接串失败: %v", err)
	}
	// URL 形式下库名在 Path 里，去掉前导斜杠。
	dbName := cfg.ConnConfig.Database
	if len(dbName) < 5 || dbName[len(dbName)-5:] != "_test" {
		t.Fatalf("拒绝在库 %q 上运行集成测试：库名必须以 _test 结尾，"+
			"因为测试会清空所有表。请设置 TEST_DATABASE_URL 指向一个测试库。", dbName)
	}
}

// setupDB 建立连接、应用迁移、清空数据，返回可用的 DB。
func setupDB(t *testing.T) *DB {
	t.Helper()

	if testing.Short() {
		t.Skip("跳过集成测试（-short）")
	}

	dsn := testDSN(t)
	guardTestDatabase(t, dsn)

	ctx := context.Background()
	db, err := New(ctx, dsn)
	if err != nil {
		t.Skipf("连接测试库失败，跳过：%v", err)
	}
	t.Cleanup(db.Close)

	applyMigrations(t, db)
	truncateAll(t, db)

	return db
}

// applyMigrations 按文件名顺序执行 migrations 目录下的 up 脚本。
//
// 用简单的分割执行而不是引入迁移库：测试里只需要"把表建起来"，
// 而 migrations 目录的脚本本身就是幂等不了的（CREATE TABLE 会报错），
// 所以先全部 drop。
func applyMigrations(t *testing.T, db *DB) {
	t.Helper()

	dir := findMigrationsDir(t)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取迁移目录失败: %v", err)
	}

	var ups []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" &&
			len(e.Name()) > 3 && e.Name()[len(e.Name())-7:] == ".up.sql" {
			ups = append(ups, e.Name())
		}
	}
	sort.Strings(ups)

	ctx := context.Background()

	// 先全部清掉，让脚本可以从头执行。
	dropAll(t, db)

	for _, name := range ups {
		sql, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		if _, err := db.pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("执行 %s 失败: %v", name, err)
		}
	}
}

// dropAll 删除本项目的全部对象，让迁移可以重新执行。
func dropAll(t *testing.T, db *DB) {
	t.Helper()

	const sql = `
		DROP TABLE IF EXISTS comments, incident_events, incident_analysis, incidents, users CASCADE;
		DROP TYPE IF EXISTS status_t, severity_t CASCADE;`

	if _, err := db.pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("清理对象失败: %v", err)
	}
}

// truncateAll 清空数据但保留表结构，在每个测试前调用。
// 用 RESTART IDENTITY 让 ID 从 1 开始，断言里可以写死具体 ID。
func truncateAll(t *testing.T, db *DB) {
	t.Helper()

	const sql = `
		TRUNCATE incidents, incident_analysis, incident_events, comments
		RESTART IDENTITY CASCADE;
		DELETE FROM users WHERE email <> 'demo@devlens.local';`

	if _, err := db.pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("清空数据失败: %v", err)
	}
}

// findMigrationsDir 从当前包目录往上找到 migrations 目录。
func findMigrationsDir(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("获取工作目录失败: %v", err)
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

// helper：断言用的简短包装。

func mustCreate(t *testing.T, db *DB, rawLog, normalized string, fp []byte) *domain.Incident {
	t.Helper()
	repo := NewIncidentRepo(db)
	inc, err := repo.Create(context.Background(), CreateIncidentInput{
		RawLog:      rawLog,
		Normalized:  normalized,
		Fingerprint: fp,
		CreatedBy:   demoUserID(),
	})
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	return inc
}

func fp(seed byte) []byte {
	b := make([]byte, 16)
	for i := range b {
		b[i] = seed
	}
	return b
}

func demoUserID() *int64 {
	id := int64(1)
	return &id
}
