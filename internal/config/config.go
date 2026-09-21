// Package config 从环境变量读取配置。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 是运行所需的全部配置。
type Config struct {
	DatabaseURL string
	RedisAddr   string
	RedisPass   string
	RedisDB     int

	// BindAddr 是 HTTP 服务监听的主机地址。
	//
	// 默认 127.0.0.1：进程默认只在回环上监听，对外由反向代理承担。
	// 这样进程本身没有公网暴露面 —— 即使云安全组被误开放，
	// 外部也无法直接连到进程。容器或反代场景用 BIND_ADDR=0.0.0.0 显式打开。
	BindAddr string

	Port     string
	LogLevel string

	// TrustedProxies 是允许其提供 X-Forwarded-For 的代理地址（CIDR 或 IP）。
	//
	// 空列表（默认）表示**不信任任何代理**：客户端 IP 一律取 TCP 连接的对端
	// 地址，忽略 X-Forwarded-For。
	//
	// 这不是细枝末节 —— 限流按客户端 IP 分桶，而 X-Forwarded-For 是
	// 客户端可随意伪造的头。默认信任它等于让任何调用方每次请求换一个
	// 伪造 IP 就能绕过限流（实测可复现）。
	//
	// 因此默认 fail-safe：只有在确实部署了反向代理、并在
	// TRUSTED_PROXIES 里显式声明其地址之后，才会采信该头。
	TrustedProxies []string

	// LLM 配置。v1 的 analyzer 尚未接入，但配置项先留好，
	// 且启动时校验，避免跑起来才在第一个请求上失败。
	LLMAPIKey  string
	LLMModel   string
	LLMBaseURL string
	// LLMJSONMode 开启后请求服务端保证输出合法 JSON。
	LLMJSONMode bool
	// LLMMaxTokens 是单次调用的输出上限。设小了会截断 JSON，
	// 截断的内容无法解析且重试也无用。
	LLMMaxTokens int

	AnalyzeTimeout time.Duration

	RateLimitAnalyzePerMin int
	RateLimitGlobalPerMin  int
}

// Load 读取配置并校验必填项。
//
// 缺必填项时立刻返回错误，让进程在启动阶段就失败，
// 而不是等第一个请求才 500。
// DotEnvPath 是默认的 .env 位置。可用 DOTENV_PATH 覆盖，
// 设为空字符串则完全不读文件。
const DotEnvPath = ".env"

// Load 读取配置。它会先尝试加载 .env，再读环境变量。
//
// 顺序很重要：.env 只是本地默认值，真实环境变量优先。
func Load() (*Config, error) {
	path := env("DOTENV_PATH", DotEnvPath)
	if path != "" {
		if err := LoadDotEnv(path); err != nil {
			return nil, err
		}
	}
	return loadFromEnv()
}

func loadFromEnv() (*Config, error) {
	c := &Config{
		DatabaseURL: env("DATABASE_URL", ""),
		RedisAddr:   env("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPass:   env("REDIS_PASSWORD", ""),
		BindAddr:    env("BIND_ADDR", "127.0.0.1"),
		Port:        env("PORT", "8080"),
		LogLevel:    env("LOG_LEVEL", "info"),

		// 空 = 不信任任何代理（默认）。见 Config.TrustedProxies 的说明。
		TrustedProxies: envList("TRUSTED_PROXIES"),

		LLMAPIKey:   env("LLM_API_KEY", ""),
		LLMModel:    env("LLM_MODEL", "deepseek-flash"),
		LLMBaseURL:  env("LLM_BASE_URL", "https://api.deepseek.com"),
		LLMJSONMode: envBool("LLM_JSON_MODE", true),
	}

	var err error
	if c.RedisDB, err = envInt("REDIS_DB", 0); err != nil {
		return nil, err
	}
	if c.RateLimitAnalyzePerMin, err = envInt("RATE_LIMIT_ANALYZE_PER_MIN", 10); err != nil {
		return nil, err
	}
	if c.RateLimitGlobalPerMin, err = envInt("RATE_LIMIT_GLOBAL_PER_MIN", 200); err != nil {
		return nil, err
	}
	if c.AnalyzeTimeout, err = envDuration("ANALYZE_TIMEOUT", 25*time.Second); err != nil {
		return nil, err
	}
	if c.LLMMaxTokens, err = envInt("LLM_MAX_TOKENS", 4096); err != nil {
		return nil, err
	}

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// envList 按逗号拆分成列表，忽略空白项。
//
// 未设置或为空时返回 nil（而不是空切片）：调用方用 len() == 0 判断
// "没有配置"，nil 与空切片在这点上等价，但 nil 更明确地表示"没配"。
//
// 不做去重与语法校验：地址是否合法交给 net 层解析，
// 这里只负责把字符串切开，保持配置层薄。
func envList(key string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}

	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func envInt(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return n, nil
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return d, nil
}
