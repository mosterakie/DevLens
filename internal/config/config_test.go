package config

import (
	"os"
	"testing"
)

// loadEnvOnly 在隔离的环境里跑 loadFromEnv。
//
// 用 DOTENV_PATH="" 让 Load 完全跳过 .env 读取，避免依赖工作目录下
// 是否存在 .env —— 测试不应该因为开发机上的文件内容而变化。
func loadEnvOnly(t *testing.T, kv map[string]string) (*Config, error) {
	t.Helper()

	for k, v := range kv {
		t.Setenv(k, v)
	}
	// DATABASE_URL 是必填项，缺失会让 Load 直接失败。
	// 不在这里给默认值是为了让「缺必填项」的测试也能复用本函数。
	if _, ok := kv["DATABASE_URL"]; !ok {
		t.Setenv("DATABASE_URL", "postgres://u:p@127.0.0.1:5432/db")
	}
	t.Setenv("DOTENV_PATH", "")

	return Load()
}

// TestBindAddrDefaultsToLoopback 是本改动的核心断言。
//
// 默认必须是回环：进程默认不带公网暴露面。改成 0.0.0.0 之类的默认值
// 会让进程直接暴露，安全组一旦误开放就可达。
func TestBindAddrDefaultsToLoopback(t *testing.T) {
	cfg, err := loadEnvOnly(t, map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BindAddr != "127.0.0.1" {
		t.Errorf("default BindAddr = %q, want 127.0.0.1", cfg.BindAddr)
	}
}

// TestBindAddrOverride 确认容器/反代场景可以显式打开。
func TestBindAddrOverride(t *testing.T) {
	cfg, err := loadEnvOnly(t, map[string]string{"BIND_ADDR": "0.0.0.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BindAddr != "0.0.0.0" {
		t.Errorf("BindAddr = %q, want 0.0.0.0", cfg.BindAddr)
	}
}

// TestBindAddrEmptyFallsBackToDefault 记录一个刻意的行为：
// env() 把空字符串当作「没设置」，所以 BIND_ADDR= 会退回默认回环，
// 而不是变成空 host（空 host 在 JoinHostPort 下等于监听所有网卡）。
//
// 这条是有意为之的失败安全方向：配错了宁可只监听回环，也不要意外全开。
func TestBindAddrEmptyFallsBackToDefault(t *testing.T) {
	cfg, err := loadEnvOnly(t, map[string]string{"BIND_ADDR": ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BindAddr != "127.0.0.1" {
		t.Errorf("BindAddr = %q, want 127.0.0.1 (empty must not mean all interfaces)", cfg.BindAddr)
	}
}

// TestIPv6BindAddrSurvivesLoad 确认 IPv6 原样读入。
//
// 地址拼接的安全性由 cmd/api 里的 net.JoinHostPort 保证，
// 这里只确认配置层没有做会破坏 IPv6 的处理（比如按 : 切分）。
func TestIPv6BindAddrSurvivesLoad(t *testing.T) {
	cfg, err := loadEnvOnly(t, map[string]string{"BIND_ADDR": "::1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BindAddr != "::1" {
		t.Errorf("BindAddr = %q, want ::1", cfg.BindAddr)
	}
}

// TestTrustedProxiesDefaultsToNone 是本改动最关键的安全断言。
//
// 默认必须是"不信任任何代理"。如果默认信任，ClientIP() 就会采信
// 客户端自带的 X-Forwarded-For，而限流按该值分桶 —— 任何调用方
// 每次换一个伪造 IP 即可绕过限流。
//
// 这条测试失败意味着默认值被改成了不安全的方向，属于安全回归。
func TestTrustedProxiesDefaultsToNone(t *testing.T) {
	cfg, err := loadEnvOnly(t, map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("default TrustedProxies = %v, want empty (must not trust XFF)", cfg.TrustedProxies)
	}
}

// TestTrustedProxiesParsing 确认逗号分隔、空白容忍与空项剔除。
func TestTrustedProxiesParsing(t *testing.T) {
	cfg, err := loadEnvOnly(t, map[string]string{
		"TRUSTED_PROXIES": " 127.0.0.1 , 10.0.0.0/8 ,, ",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"127.0.0.1", "10.0.0.0/8"}
	if len(cfg.TrustedProxies) != len(want) {
		t.Fatalf("got %v, want %v", cfg.TrustedProxies, want)
	}
	for i := range want {
		if cfg.TrustedProxies[i] != want[i] {
			t.Errorf("index %d = %q, want %q", i, cfg.TrustedProxies[i], want[i])
		}
	}
}

// TestTrustedProxiesEmptyMeansNone 确认 TRUSTED_PROXIES= 退回"不信任"，
// 而不是变成"空字符串元素"（那会让 SetTrustedProxies 解析失败）。
func TestTrustedProxiesEmptyMeansNone(t *testing.T) {
	for _, v := range []string{"", "   ", ",", " , "} {
		cfg, err := loadEnvOnly(t, map[string]string{"TRUSTED_PROXIES": v})
		if err != nil {
			t.Fatalf("value %q: unexpected error: %v", v, err)
		}
		if len(cfg.TrustedProxies) != 0 {
			t.Errorf("value %q: got %v, want nil/empty", v, cfg.TrustedProxies)
		}
	}
}

// TestPortStillDefaults 确认本改动没有影响既有配置项。
func TestPortStillDefaults(t *testing.T) {
	cfg, err := loadEnvOnly(t, map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("default Port = %q, want 8080", cfg.Port)
	}
}

// TestDatabaseURLRequired 确认必填校验没有被改动破坏。
func TestDatabaseURLRequired(t *testing.T) {
	t.Setenv("DOTENV_PATH", "")
	os.Unsetenv("DATABASE_URL")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error when DATABASE_URL is missing")
	}
}
