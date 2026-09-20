package migrate

import (
	"context"
	"errors"
	"testing"
)

// TestBaselineAdoptsExistingSchema 覆盖这一版新增的升级路径：
// 库的结构是手动建的，没有迁移记录，baseline 应当把它接入迁移系统。
func TestBaselineAdoptsExistingSchema(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	// 模拟"手动建库"：直接建表，不经过迁移系统。
	_, err := pool.Exec(ctx, `
		CREATE TABLE mig_alpha (id int PRIMARY KEY);
		CREATE TABLE mig_beta (id int PRIMARY KEY);`)
	if err != nil {
		t.Fatal(err)
	}

	migs, err := Load(migDir(t, map[string]string{
		"0001_alpha.up.sql": "CREATE TABLE mig_alpha (id int PRIMARY KEY);",
		"0002_beta.up.sql":  "CREATE TABLE mig_beta (id int PRIMARY KEY);",
	}))
	if err != nil {
		t.Fatal(err)
	}

	runner := New(pool, nil)
	res, err := runner.Baseline(ctx, BaselineInput{
		Version:        2,
		Migrations:     migs,
		RequiredTables: []string{"mig_alpha", "mig_beta"},
	})
	if err != nil {
		t.Fatalf("Baseline 失败: %v", err)
	}
	if len(res.Marked) != 2 {
		t.Errorf("标记了 %d 条, want 2", len(res.Marked))
	}

	// 标记之后，正常 Apply 应当跳过而不是重复建表。
	if err := runner.Apply(ctx, migs); err != nil {
		t.Fatalf("baseline 之后 Apply 应当跳过, 实际失败: %v", err)
	}
}

// TestBaselineRejectsEmptySchema 是 baseline 最重要的安全网。
//
// 把空库标成"已迁移"会让后续迁移建立在错误前提上，
// 而且报错会出现在很久以后，很难追查。
func TestBaselineRejectsEmptySchema(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	migs, err := Load(migDir(t, map[string]string{
		"0001_alpha.up.sql": "CREATE TABLE mig_alpha (id int PRIMARY KEY);",
	}))
	if err != nil {
		t.Fatal(err)
	}

	_, err = New(pool, nil).Baseline(ctx, BaselineInput{
		Version:        1,
		Migrations:     migs,
		RequiredTables: []string{"mig_alpha"},
	})
	if !errors.Is(err, ErrSchemaNotBaselineable) {
		t.Errorf("空库上 baseline 应当被拒绝, got %v", err)
	}

	// 拒绝之后不能留下任何记录。
	applied, _, err := Status(ctx, pool, migs)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 0 {
		t.Errorf("被拒绝的 baseline 不应写入记录, got %d", applied)
	}
}

// TestBaselineForceSkipsCheck 确认 --force 能跳过结构校验。
func TestBaselineForceSkipsCheck(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	migs, err := Load(migDir(t, map[string]string{
		"0001_alpha.up.sql": "CREATE TABLE mig_alpha (id int PRIMARY KEY);",
	}))
	if err != nil {
		t.Fatal(err)
	}

	_, err = New(pool, nil).Baseline(ctx, BaselineInput{
		Version:        1,
		Migrations:     migs,
		RequiredTables: []string{"mig_alpha"},
		Force:          true,
	})
	if err != nil {
		t.Errorf("force 应当跳过校验: %v", err)
	}
}

// TestBaselineRefusesWhenAlreadyTracked 确认已有记录的库不会被重复 baseline。
func TestBaselineRefusesWhenAlreadyTracked(t *testing.T) {
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
		t.Fatal(err)
	}

	_, err = runner.Baseline(ctx, BaselineInput{
		Version:    1,
		Migrations: migs,
		Force:      true,
	})
	if err == nil {
		t.Error("已有迁移记录的库不应允许 baseline")
	}
}

// TestBaselineRejectsUnknownVersion 确认标一个不存在的版本会被拒绝。
func TestBaselineRejectsUnknownVersion(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	migs, err := Load(migDir(t, map[string]string{
		"0001_alpha.up.sql": "CREATE TABLE mig_alpha (id int PRIMARY KEY);",
	}))
	if err != nil {
		t.Fatal(err)
	}

	_, err = New(pool, nil).Baseline(ctx, BaselineInput{
		Version:    99,
		Migrations: migs,
		Force:      true,
	})
	if err == nil {
		t.Error("不存在的版本应当被拒绝")
	}
}

// TestBaselineRejectsZeroVersion 确认版本 0 被拒绝。
func TestBaselineRejectsZeroVersion(t *testing.T) {
	pool := testPool(t)
	_, err := New(pool, nil).Baseline(context.Background(), BaselineInput{Version: 0})
	if err == nil {
		t.Error("版本 0 应当被拒绝")
	}
}
