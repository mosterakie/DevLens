// Package migrate 在启动时应用数据库迁移。
//
// 手写而不引入迁移库：需求很窄——按文件名顺序执行 up 脚本并记录版本。
// 引入 golang-migrate 会多一个依赖和它自己的一套约定（如 dirty 状态处理），
// 对这个规模不值得。
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// advisoryLockID 是迁移专用的锁标识。
//
// 多实例同时启动时靠它保证只有一个执行迁移，其余等待。
// 数值任取，只要全项目一致。
const advisoryLockID int64 = 0x4465764c656e73

// upFilePattern 匹配迁移文件名，如 0001_init.up.sql。
var upFilePattern = regexp.MustCompile(`^(\d{4})_.+\.up\.sql$`)

// Migration 是一个待应用的迁移。
type Migration struct {
	Version int
	Name    string
	SQL     string
	// Checksum 用于发现"已应用的迁移被改过"。
	// 改了历史迁移又不改版本号，会让不同环境的结构悄悄分叉。
	Checksum string
}

// Load 从目录读取所有 up 迁移并校验命名。
func Load(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取迁移目录 %s: %w", dir, err)
	}

	var out []Migration
	seen := map[int]string{}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		m := upFilePattern.FindStringSubmatch(name)
		if m == nil {
			// down 脚本是合法文件，只是不在这里执行。
			if strings.HasSuffix(name, ".down.sql") {
				continue
			}
			// 其它 .sql 说明命名不规范：静默忽略会让"以为加了迁移
			// 但其实没生效"变成很难查的问题。
			if strings.HasSuffix(name, ".sql") {
				return nil, fmt.Errorf(
					"迁移文件名不合规范：%s（应形如 0001_init.up.sql）", name)
			}
			continue
		}

		version := 0
		if _, err := fmt.Sscanf(m[1], "%d", &version); err != nil {
			return nil, fmt.Errorf("解析版本号 %s: %w", name, err)
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("版本号 %d 重复：%s 与 %s", version, prev, name)
		}
		seen[version] = name

		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("读取 %s: %w", name, err)
		}

		sum := sha256.Sum256(body)
		out = append(out, Migration{
			Version:  version,
			Name:     name,
			SQL:      string(body),
			Checksum: hex.EncodeToString(sum[:8]),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Logger 是 Runner 需要的日志能力。
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// Runner 负责应用迁移。
type Runner struct {
	pool *pgxpool.Pool
	log  Logger
}

// New 构造 Runner。log 可以为 nil。
func New(pool *pgxpool.Pool, log Logger) *Runner {
	return &Runner{pool: pool, log: log}
}

func (r *Runner) info(msg string, args ...any) {
	if r.log != nil {
		r.log.Info(msg, args...)
	}
}

// ErrChecksumMismatch 表示某个已应用的迁移内容被改动过。
var ErrChecksumMismatch = errors.New("已应用的迁移内容被改动")

// Apply 应用所有尚未执行的迁移。
//
// 整个过程持有 advisory lock：多实例同时启动时只有第一个真正执行，
// 其余阻塞等待，拿到锁后发现无待应用项直接返回。
func (r *Runner) Apply(ctx context.Context, migrations []Migration) error {
	if len(migrations) == 0 {
		return nil
	}

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("获取连接: %w", err)
	}
	defer conn.Release()

	// 用会话级锁而不是事务级：迁移里可能有多条语句，
	// 且并非所有 DDL 都适合放在一个长事务里。
	// 锁在连接释放时自动释放，不需要显式 unlock。
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockID); err != nil {
		return fmt.Errorf("获取迁移锁: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(),
			`SELECT pg_advisory_unlock($1)`, advisoryLockID)
	}()

	if err := r.ensureTable(ctx, conn); err != nil {
		return err
	}

	applied, err := r.appliedSet(ctx, conn)
	if err != nil {
		return err
	}
	pending := 0
	for _, m := range migrations {
		if prev, ok := applied[m.Version]; ok {
			// 已应用过：确认内容没被改过。
			if prev != m.Checksum {
				return fmt.Errorf("%w：%s（记录 %s，当前 %s）。"+
					"不要修改已应用的迁移，应新增一个迁移文件",
					ErrChecksumMismatch, m.Name, prev, m.Checksum)
			}
			continue
		}
		pending++
	}

	if pending == 0 {
		r.info("数据库结构已是最新", "applied", len(applied))
		return nil
	}

	r.info("开始应用迁移", "pending", pending)

	for _, m := range migrations {
		if _, ok := applied[m.Version]; ok {
			continue
		}
		if err := r.applyOne(ctx, conn, m); err != nil {
			return err
		}
		r.info("已应用迁移", "version", m.Version, "name", m.Name)
	}

	return nil
}

// applyOne 在一个事务里执行迁移并登记版本。
//
// PostgreSQL 支持事务性 DDL，所以迁移失败时结构不会被改一半、
// 版本也不会被误记。
func (r *Runner) applyOne(ctx context.Context, conn *pgxpool.Conn, m Migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return fmt.Errorf("执行 %s 失败: %w", m.Name, err)
	}

	const ins = `
		INSERT INTO schema_migrations (version, name, checksum)
		VALUES ($1, $2, $3)`
	if _, err := tx.Exec(ctx, ins, m.Version, m.Name, m.Checksum); err != nil {
		return fmt.Errorf("登记 %s 失败: %w", m.Name, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交 %s 失败: %w", m.Name, err)
	}
	return nil
}

func (r *Runner) ensureTable(ctx context.Context, conn *pgxpool.Conn) error {
	const ddl = `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version     INTEGER PRIMARY KEY,
			name        TEXT NOT NULL,
			checksum    TEXT NOT NULL,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)`
	if _, err := conn.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("建立 schema_migrations 表: %w", err)
	}
	return nil
}

// appliedSet 读取已应用的版本与校验和。
func (r *Runner) appliedSet(ctx context.Context, conn *pgxpool.Conn) (map[int]string, error) {
	rows, err := conn.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("读取已应用迁移: %w", err)
	}
	defer rows.Close()

	out := map[int]string{}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			return nil, fmt.Errorf("扫描迁移记录: %w", err)
		}
		out[v] = sum
	}
	return out, rows.Err()
}

// Status 报告已应用与总数，供环境检查工具使用。
func Status(ctx context.Context, pool *pgxpool.Pool, migrations []Migration) (applied, total int, err error) {
	total = len(migrations)

	// 用 current_schema() 而不是硬编码 'public'。
	//
	// 表未必要建在 public 上：部署时可能用独立 schema 做权限隔离。
	// 写死 public 会让那种环境下永远报"还没有迁移记录"——
	// 而这个问题只在非 public schema 下才暴露。
	var exists bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = current_schema()
			  AND table_name = 'schema_migrations'
		)`).Scan(&exists)
	if err != nil {
		return 0, total, fmt.Errorf("检查 schema_migrations: %w", err)
	}
	if !exists {
		return 0, total, nil
	}

	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil {
		return 0, total, fmt.Errorf("统计已应用迁移: %w", err)
	}
	return applied, total, nil
}
