package migrate

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrSchemaNotBaselineable 表示当前库的结构与要标记的版本不匹配。
var ErrSchemaNotBaselineable = errors.New("数据库结构与目标版本不匹配")

// BaselineInput 描述一次 baseline 操作的输入。
type BaselineInput struct {
	// Version 是要标记为"已应用"的最高版本。
	Version int
	// Migrations 是全部迁移，用于校验版本存在。
	Migrations []Migration
	// Force 跳过结构校验。仅在明确知道自己在做什么时使用。
	Force bool
	// RequiredTables 是判断结构是否到位的依据表名。
	RequiredTables []string
}

// BaselineResult 是一次 baseline 的结果。
type BaselineResult struct {
	// Marked 是被标记为已应用的迁移版本。
	Marked []int
}

// Baseline 把已有数据库标记为"已应用某个版本"，不执行迁移内容。
//
// 用于从手动建库升级到自动迁移的场景：库的结构已经存在，
// 但没有 schema_migrations 记录，直接跑迁移会因为对象已存在而失败。
//
// 这个操作有风险：如果标记的版本高于实际结构，后续迁移会建立在
// 错误的前提上。所以默认会校验结构——检查若干必须存在的表——
// 只有校验通过才写入记录。
func (r *Runner) Baseline(ctx context.Context, in BaselineInput) (*BaselineResult, error) {
	if in.Version <= 0 {
		return nil, fmt.Errorf("baseline 版本必须大于 0")
	}

	// 确认这个版本确实存在。
	found := false
	var toMark []Migration
	for _, m := range in.Migrations {
		if m.Version <= in.Version {
			toMark = append(toMark, m)
		}
		if m.Version == in.Version {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("版本 %d 不存在于迁移目录中", in.Version)
	}
	if len(toMark) == 0 {
		return nil, fmt.Errorf("没有需要标记的迁移")
	}

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取连接: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockID); err != nil {
		return nil, fmt.Errorf("获取迁移锁: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(),
			`SELECT pg_advisory_unlock($1)`, advisoryLockID)
	}()

	if err := r.ensureTable(ctx, conn); err != nil {
		return nil, err
	}

	existing, err := r.appliedSet(ctx, conn)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return nil, fmt.Errorf(
			"这个库已经有 %d 条迁移记录，不需要 baseline。"+
				"baseline 只用于把手动建好的库接入迁移系统", len(existing))
	}

	// 结构校验：要标记的版本所对应的表必须已存在。
	// 这道检查挡不住所有错误，但能挡住"标了个超前版本"这类最危险的误操作。
	if !in.Force {
		if err := r.verifySchema(ctx, conn, in.RequiredTables); err != nil {
			return nil, err
		}
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("开启事务: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const ins = `
		INSERT INTO schema_migrations (version, name, checksum)
		VALUES ($1, $2, $3)
		ON CONFLICT (version) DO NOTHING`

	marked := make([]int, 0, len(toMark))
	for _, m := range toMark {
		if _, err := tx.Exec(ctx, ins, m.Version, m.Name, m.Checksum); err != nil {
			return nil, fmt.Errorf("标记 %s 失败: %w", m.Name, err)
		}
		marked = append(marked, m.Version)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("提交 baseline 失败: %w", err)
	}

	sort.Ints(marked)
	return &BaselineResult{Marked: marked}, nil
}

// verifySchema 检查给定的表是否存在。
//
// 只检查"存在"不检查"结构一致"：完整比对列类型等于重写一遍迁移解析，
// 收益不抵复杂度。存在性检查足以挡住最常见的误操作——把空库标成
// 已迁移。
func (r *Runner) verifySchema(ctx context.Context, conn *pgxpool.Conn, tables []string) error {
	if len(tables) == 0 {
		tables = []string{"incidents", "incident_analysis", "incident_events"}
	}

	var missing []string
	for _, t := range tables {
		var exists bool
		err := conn.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = current_schema() AND table_name = $1
			)`, t).Scan(&exists)
		if err != nil {
			return fmt.Errorf("检查表 %s: %w", t, err)
		}
		if !exists {
			missing = append(missing, t)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("%w：以下表不存在：%v。"+
			"如果这个库确实是空的，不要 baseline，直接用正常迁移；"+
			"如果确定要跳过校验，加 --force",
			ErrSchemaNotBaselineable, missing)
	}
	return nil
}

// BaselineRequiredTables 返回结构校验默认检查的表。
func BaselineRequiredTables() []string {
	return []string{"incidents", "incident_analysis", "incident_events", "users"}
}
