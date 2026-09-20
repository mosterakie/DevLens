// Command devcheck 检查本机开发环境是否就绪。
//
// 用 Go 而不是 shell 脚本：Windows PowerShell 5.1 会用系统 ANSI 代码页
// 解码没有 BOM 的脚本，中文注释和输出会变成乱码并破坏解析。Go 源文件
// 统一是 UTF-8，没有这个问题，而本项目本来就依赖 Go 工具链。
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosterakie/DevLens/internal/migrate"
)

const (
	pgPort    = 5432
	redisPort = 6379
)

func main() {
	flagBaseline := flag.Int("baseline", 0,
		"把当前库标记为已应用指定版本（用于从手动建库升级）。"+
			"传 0 表示不执行，只做检查")
	flagForce := flag.Bool("force", false, "baseline 时跳过结构校验，仅在你确认结构无误时使用")
	flag.Parse()

	os.Exit(run(*flagBaseline, *flagForce))
}

func run(baselineVersion int, force bool) int {
	fmt.Println()
	fmt.Println("DevLens 开发环境检查")
	fmt.Println(strings.Repeat("=", 44))

	problems := 0

	head("依赖服务")

	if probe("127.0.0.1", pgPort) {
		ok(fmt.Sprintf("PostgreSQL 在 %d 端口可连接", pgPort))
	} else {
		bad(fmt.Sprintf("PostgreSQL 未在 %d 端口监听", pgPort))
		hint("原生安装：启动 Windows 服务 postgresql-x64-18")
		hint("或用容器：docker compose up -d postgres")
		problems++
	}

	if probe("127.0.0.1", redisPort) {
		ok(fmt.Sprintf("Redis 在 %d 端口可连接", redisPort))
	} else {
		bad(fmt.Sprintf("Redis 未在 %d 端口监听", redisPort))
		if exe := findRedis(); exe != "" {
			hint("原生安装：启动 " + exe)
		}
		hint("或用容器：docker compose up -d redis")
		problems++
	}

	head("配置")

	env, err := readEnvFile(".env")
	switch {
	case os.IsNotExist(err):
		warn(".env 不存在，将使用环境变量或默认值")
		hint("可以从 .env.example 复制一份")
	case err != nil:
		bad("读取 .env 失败：" + err.Error())
		problems++
	default:
		ok(".env 已存在")
		if v := lookup(env, "DATABASE_URL"); v != "" {
			detail("DATABASE_URL", v)
		}
		if v := lookup(env, "LLM_MODEL"); v != "" {
			detail("LLM_MODEL", v)
		}
		key := lookup(env, "LLM_API_KEY")
		if strings.TrimSpace(key) == "" {
			warn("LLM_API_KEY 为空，worker 会退回本地启发式分析器")
			hint("整条链路仍可跑通，只是判断依据变成关键词匹配")
		} else {
			ok("LLM_API_KEY 已配置 (" + mask(key) + ")")
		}
	}

	head("数据库迁移")

	// 连上数据库看迁移状态。连不上时前面已经报过连接问题，这里跳过。
	if probe("127.0.0.1", pgPort) {
		if baselineVersion > 0 {
			if err := runBaseline(lookup(env, "DATABASE_URL"), baselineVersion, force); err != nil {
				bad("baseline 失败：" + err.Error())
				return 1
			}
		} else if err := checkMigrations(lookup(env, "DATABASE_URL")); err != nil {
			bad("检查迁移状态失败：" + err.Error())
		}
	}

	head("api 端口")

	desired := 8080
	if v := lookup(env, "PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			desired = n
		}
	}

	chosen := desired
	if probe("127.0.0.1", desired) {
		owner := describePortOwner(desired)
		warn(fmt.Sprintf("%d 已被占用 %s", desired, owner))
		chosen = 0
		for p := desired + 1; p <= desired+20; p++ {
			if !probe("127.0.0.1", p) {
				chosen = p
				break
			}
		}
		if chosen == 0 {
			bad("往后 20 个端口都被占用")
			problems++
		} else {
			ok(fmt.Sprintf("改用空闲端口 %d", chosen))
		}
	} else {
		ok(fmt.Sprintf("%d 可用", desired))
	}

	if problems > 0 {
		head(fmt.Sprintf("发现 %d 个问题，先处理再启动", problems))
		return 1
	}

	head("检查通过")
	fmt.Printf("  启动 api   :  go run ./cmd/api      (端口 %d)\n", chosen)
	fmt.Println("  启动 worker:  go run ./cmd/worker")
	fmt.Println()
	return 0
}

// probe 尝试建立 TCP 连接，判断端口是否有服务在监听。
// 本机连接要么立刻成功要么立刻被拒，用短超时即可。
func probe(host string, port int) bool {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// lookup 按与 config.Load 相同的优先级取值：真实环境变量优先于 .env。
//
// devcheck 原先只读 .env，与主程序的约定不一致，
// 会导致用环境变量覆盖配置时检查了错误的库。
func lookup(env map[string]string, key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return env[key]
}

// readEnvFile 解析 .env，只做够用的解析，不引入依赖。
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, sc.Err()
}

// checkMigrations 报告迁移是否已全部应用。
//
// api 与 worker 启动时会自动应用迁移，所以这里只是提前告知，
// 不把它算作阻塞问题。
func checkMigrations(dsn string) error {
	if dsn == "" {
		dsn = "postgres://devlens:devlens@127.0.0.1:5432/devlens?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	migs, err := migrate.Load("migrations")
	if err != nil {
		return err
	}

	applied, total, err := migrate.Status(ctx, pool, migs)
	if err != nil {
		return err
	}

	switch {
	case applied < total:
		pending := total - applied
		warn(fmt.Sprintf("还有 %d 条迁移未应用（%d/%d）", pending, applied, total))
		hint("api 或 worker 启动时会自动应用")
	default:
		ok(fmt.Sprintf("迁移已全部应用（%d/%d）", applied, total))
	}
	return nil
}

// runBaseline 把已有库接入迁移系统。
//
// 用于从"手动执行迁移脚本"升级到"启动时自动迁移"的场景：
// 库的结构已存在但没有 schema_migrations 记录。
func runBaseline(dsn string, version int, force bool) error {
	if dsn == "" {
		dsn = "postgres://devlens:devlens@127.0.0.1:5432/devlens?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	migs, err := migrate.Load("migrations")
	if err != nil {
		return err
	}

	res, err := migrate.New(pool, nil).Baseline(ctx, migrate.BaselineInput{
		Version:        version,
		Migrations:     migs,
		Force:          force,
		RequiredTables: migrate.BaselineRequiredTables(),
	})
	if err != nil {
		return err
	}

	ok(fmt.Sprintf("已把数据库标记为应用到 v%d", version))
	hint(fmt.Sprintf("登记了 %d 条迁移：%v", len(res.Marked), res.Marked))
	hint("之后启动 api 或 worker 时会自动应用更新的迁移")
	return nil
}

// findRedis 在本机常见位置找 redis-server。
func findRedis() string {
	for _, c := range []string{
		`D:\Env\Redis-8.10.1\redis-server.exe`,
		`C:\Program Files\Redis\redis-server.exe`,
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func describePortOwner(port int) string {
	return fmt.Sprintf("(netstat -ano | findstr :%d 可查看具体进程)", port)
}

// mask 只保留前后几位，避免完整密钥出现在终端历史里。
func mask(s string) string {
	if len(s) <= 12 {
		return "****"
	}
	return s[:7] + "****" + s[len(s)-4:]
}

func head(s string) {
	fmt.Println()
	fmt.Println(s)
}

func ok(s string)   { fmt.Println("  [ok]   " + s) }
func warn(s string) { fmt.Println("  [warn] " + s) }
func bad(s string)  { fmt.Println("  [fail] " + s) }
func hint(s string) { fmt.Println("         " + s) }
func detail(k, v string) {
	fmt.Printf("         %-13s %s\n", k+":", v)
}
