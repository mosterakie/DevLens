// Package repository 封装数据访问。
//
// 直接用 pgx 手写 SQL，没有引入 sqlc 代码生成：当前查询数量很少，
// 生成器带来的构建步骤和维护成本还换不回收益。等查询变多再引入。
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound 表示查询目标不存在。
var ErrNotFound = errors.New("not found")

// DB 持有连接池。
type DB struct {
	pool *pgxpool.Pool
}

// New 建立连接池并验证连通性。
func New(ctx context.Context, dsn string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}

	// 连接池上限压低一些：本地 PostgreSQL 是共享实例，
	// 不该被开发期的压测占满。
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return &DB{pool: pool}, nil
}

// NewWithPool 用一个已有的连接池构造 DB。
//
// 用于调用方已经持有连接池的场景：集成测试要把连接的 search_path
// 指向自己的 schema，所以需要先建池再交给这里的场景。
func NewWithPool(pool *pgxpool.Pool) *DB {
	return &DB{pool: pool}
}

// Close 释放连接池。
func (db *DB) Close() {
	if db != nil && db.pool != nil {
		db.pool.Close()
	}
}

// Ping 用于健康检查。
func (db *DB) Ping(ctx context.Context) error {
	return db.pool.Ping(ctx)
}

// Pool 返回底层连接池。
//
// 暴露它是为了让 migrate 包能在同一个池上执行迁移，
// 避免为迁移另外建一个连接。常规数据访问仍应走各 Repo。
func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

// Query 执行查询，供 worker 的兜底扫描等场景使用。
// 常规的数据访问仍应放在各 Repo 里。
func (db *DB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return db.pool.Query(ctx, sql, args...)
}

// InTx 在一个事务中执行 fn。fn 返回错误时回滚。
func (db *DB) InTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		// 回滚已提交的事务会返回 ErrTxClosed，忽略即可。
		_ = tx.Rollback(ctx)
	}()

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
