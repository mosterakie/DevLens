package repository

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosterakie/DevLens/internal/domain"
	"github.com/mosterakie/DevLens/internal/testsupport"
)

// 集成测试需要真实 PostgreSQL。
//
// 使用独立的 schema：go test ./... 会按包并行，如果各包都在 public
// 上建表删表，一个包的 DROP 会删掉另一个包的表，表现为随机失败
// （"关系 incidents 不存在"）。每个包用自己的 schema 后互不干扰。
const testSchema = "test_repository"

// setupDB 建连接、应用迁移、清空数据。
func setupDB(t *testing.T) *DB {
	t.Helper()

	pool := testsupport.Pool(t, testSchema)
	db := &DB{pool: pool}

	applyMigrations(t, db)
	testsupport.Truncate(t, pool, testSchema)

	return db
}

// applyMigrations 按文件名顺序执行 migrations 下的 up 脚本。
func applyMigrations(t *testing.T, db *DB) {
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
	testsupport.DropAll(t, db.pool, testSchema)

	for _, name := range ups {
		sql, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("读取 %s: %v", name, err)
		}
		if _, err := db.pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("执行 %s: %v", name, err)
		}
	}
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

var _ = pgxpool.Pool{}
