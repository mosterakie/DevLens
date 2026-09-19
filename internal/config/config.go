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

	Port     string
	LogLevel string

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
		Port:        env("PORT", "8080"),
		LogLevel:    env("LOG_LEVEL", "info"),
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
