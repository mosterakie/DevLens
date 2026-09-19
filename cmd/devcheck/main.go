// Command devcheck 检查本机开发环境是否就绪。
//
// 用 Go 而不是 shell 脚本：Windows PowerShell 5.1 会用系统 ANSI 代码页
// 解码没有 BOM 的脚本，中文注释和输出会变成乱码并破坏解析。Go 源文件
// 统一是 UTF-8，没有这个问题，而本项目本来就依赖 Go 工具链。
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	pgPort    = 5432
	redisPort = 6379
)

func main() {
	os.Exit(run())
}

func run() int {
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
		if v := env["DATABASE_URL"]; v != "" {
			detail("DATABASE_URL", v)
		}
		if v := env["LLM_MODEL"]; v != "" {
			detail("LLM_MODEL", v)
		}
		key := env["LLM_API_KEY"]
		if strings.TrimSpace(key) == "" {
			warn("LLM_API_KEY 为空，worker 会退回本地启发式分析器")
			hint("整条链路仍可跑通，只是判断依据变成关键词匹配")
		} else {
			ok("LLM_API_KEY 已配置 (" + mask(key) + ")")
		}
	}

	head("api 端口")

	desired := 8080
	if v := env["PORT"]; v != "" {
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
