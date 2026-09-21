// Command redactbackfill 把存量记录的 raw_log 脱敏并重算指纹。
//
// 为什么需要它：脱敏与指纹都加入了新逻辑，但已有记录的 raw_log
// 是明文、指纹基于明文计算。不处理的话：
//  1. 服务端仍存有凭据，与新逻辑的承诺不符
//  2. 旧记录与新提交的同类日志匹配不上（指纹算法不同）
//
// 为什么用 Go 而不是 SQL：脱敏规则和归一化规则都在 Go 里，用 SQL
// 重写一遍必然产生偏差，导致迁移后的指纹与运行时算的对不上。
//
// 这个操作不可逆——旧数据里的凭据会被永久替换掉。默认是试运行，
// 加 -apply 才真正写入。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosterakie/DevLens/internal/fingerprint"
	"github.com/mosterakie/DevLens/internal/redact"
)

func main() {
	apply := flag.Bool("apply", false, "真正写入。不加这个参数只做试运行")
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "数据库连接串")
	flag.Parse()

	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "需要 -dsn 或 DATABASE_URL")
		os.Exit(2)
	}

	if err := run(*dsn, *apply); err != nil {
		fmt.Fprintln(os.Stderr, "失败:", err)
		os.Exit(1)
	}
}

type record struct {
	id      int64
	rawLog  string
	oldFP   string
	redact  bool
	newLen  int
	newNorm string
	newFP   string
}

func run(dsn string, apply bool) error {
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `SELECT id, raw_log, encode(fingerprint, 'hex') FROM incidents ORDER BY id`)
	if err != nil {
		return err
	}

	var recs []record
	for rows.Next() {
		var r record
		if err := rows.Scan(&r.id, &r.rawLog, &r.oldFP); err != nil {
			rows.Close()
			return err
		}
		recs = append(recs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	fmt.Printf("共 %d 条记录\n\n", len(recs))

	// 逐条计算脱敏后的结果，先全部算完再决定是否写入。
	// 这样试运行时也能看到完整的影响面。
	for i := range recs {
		r := &recs[i]
		redacted := redact.Apply(r.rawLog)
		r.redact = redact.Changed(r.rawLog, redacted)
		r.newLen = len(redacted)
		r.newNorm = fingerprint.Normalize(redacted)
		r.newFP = fmt.Sprintf("%x", fingerprint.Compute(r.newNorm))
	}

	var (
		redactedCount int
		fpChanged     int
	)
	for _, r := range recs {
		if r.redact {
			redactedCount++
		}
		if r.oldFP != r.newFP {
			fpChanged++
		}
	}

	fmt.Printf("需要脱敏: %d 条\n", redactedCount)
	fmt.Printf("指纹变化: %d 条\n\n", fpChanged)

	if redactedCount > 0 {
		fmt.Println("将被脱敏的记录:")
		for _, r := range recs {
			if r.redact {
				fmt.Printf("  id=%-4d 长度 %d -> %d\n", r.id, len(r.rawLog), r.newLen)
			}
		}
		fmt.Println()
	}

	if fpChanged > 0 {
		fmt.Println("指纹发生变化的记录:")
		for _, r := range recs {
			if r.oldFP != r.newFP {
				fmt.Printf("  id=%-4d %s -> %s\n", r.id, r.oldFP[:16], r.newFP[:16])
			}
		}
		fmt.Println()
	}

	// 报告重算后的同类分组。
	//
	// 重点是"是否发生了新的归并"——如果两条原本不同的记录重算后
	// 撞到同一个指纹，说明脱敏把它们抹成了同样的文本，这可能不是
	// 想要的，需要人工看一眼。
	oldGroups := map[string][]int64{}
	for _, r := range recs {
		oldGroups[r.oldFP] = append(oldGroups[r.oldFP], r.id)
	}
	newGroups := map[string][]int64{}
	for _, r := range recs {
		newGroups[r.newFP] = append(newGroups[r.newFP], r.id)
	}

	countGroups := func(g map[string][]int64) (withDup, recordsInDup int) {
		for _, ids := range g {
			if len(ids) > 1 {
				withDup++
				recordsInDup += len(ids)
			}
		}
		return
	}

	oldDup, oldInDup := countGroups(oldGroups)
	newDup, newInDup := countGroups(newGroups)

	fmt.Printf("同类分组：\n")
	fmt.Printf("  迁移前 %d 个不同指纹，其中 %d 组有多条（涉及 %d 条记录）\n",
		len(oldGroups), oldDup, oldInDup)
	fmt.Printf("  迁移后 %d 个不同指纹，其中 %d 组有多条（涉及 %d 条记录）\n",
		len(newGroups), newDup, newInDup)

	// 列出迁移后的同类分组，让执行者能直接看到归并结果。
	if newDup > 0 {
		fmt.Printf("\n迁移后的同类分组：\n")
		for fp, ids := range newGroups {
			if len(ids) > 1 {
				fmt.Printf("  %s -> %v\n", fp[:16], ids)
			}
		}
	}

	if newInDup > oldInDup {
		fmt.Printf("\n注意：归并后参与同类的记录从 %d 条增加到 %d 条，"+
			"说明有原本不同的记录被脱敏抹成了同样的文本\n", oldInDup, newInDup)
	}

	if !apply {
		fmt.Println("\n这是试运行。加 -apply 才会写入。")
		return nil
	}

	// 写入。
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const upd = `
		UPDATE incidents
		SET raw_log = $2, normalized = $3, fingerprint = $4, updated_at = now()
		WHERE id = $1`

	start := time.Now()
	for _, r := range recs {
		if _, err := tx.Exec(ctx, upd, r.id, redact.Apply(r.rawLog), r.newNorm, fingerprint.Compute(r.newNorm)); err != nil {
			return fmt.Errorf("更新 id=%d: %w", r.id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	fmt.Printf("\n已写入 %d 条，耗时 %v\n", len(recs), time.Since(start).Round(time.Millisecond))
	return nil
}
