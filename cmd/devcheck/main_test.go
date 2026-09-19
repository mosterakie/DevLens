package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestProbeFindsListener(t *testing.T) {
	// 起一个临时监听者，确认 probe 能发现它。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("无法建立监听: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	if !probe("127.0.0.1", port) {
		t.Errorf("probe 应当发现 %d 上的监听者", port)
	}
}

func TestProbeClosedPort(t *testing.T) {
	// 先占一个端口再关掉，确保它是空闲的。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	if probe("127.0.0.1", port) {
		t.Errorf("probe 不应对已关闭的 %d 返回 true", port)
	}
}

func TestReadEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	content := "" +
		"# 注释行\n" +
		"\n" +
		"DATABASE_URL=postgres://localhost/db\n" +
		"LLM_MODEL=deepseek-flash\n" +
		"EMPTY=\n" +
		"  SPACED  =  value  \n"

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	env, err := readEnvFile(path)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	cases := map[string]string{
		"DATABASE_URL": "postgres://localhost/db",
		"LLM_MODEL":    "deepseek-flash",
		"EMPTY":        "",
		"SPACED":       "value",
	}
	for k, want := range cases {
		if got := env[k]; got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	// 注释行不应产生键。
	if _, ok := env["# 注释行"]; ok {
		t.Error("注释行不应被解析成键值对")
	}
}

func TestReadEnvFileMissing(t *testing.T) {
	_, err := readEnvFile(filepath.Join(t.TempDir(), "nope"))
	if !os.IsNotExist(err) {
		t.Errorf("文件不存在时应返回 IsNotExist 错误, got %v", err)
	}
}

// TestReadEnvFileKeepsHashInValue 覆盖一个容易出错的点：
// 值里的 # 不该被当成注释。密码常见这种情况。
func TestReadEnvFileKeepsHashInValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("PASSWORD=P@ss#word\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	env, err := readEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := env["PASSWORD"]; got != "P@ss#word" {
		t.Errorf("值里的 # 被截断了: %q", got)
	}
}

func TestMask(t *testing.T) {
	tests := []struct {
		in       string
		wantFull bool // true 表示不检查具体内容，只确认不含完整原文
	}{
		{"sk-1234567890abcdef", false},
		{"short", false},
		{"", false},
	}

	for _, tt := range tests {
		got := mask(tt.in)
		if len(tt.in) > 12 && got == tt.in {
			t.Errorf("长的密钥不应原样返回: %q", got)
		}
		if len(tt.in) > 12 && got != "" {
			// 确认打码后仍能辨认出是同一把 key。
			prefix := tt.in[:7]
			if got[:7] != prefix {
				t.Errorf("打码应保留前缀: %q", got)
			}
		}
	}
}

func TestFindRedisReturnsPathOrEmpty(t *testing.T) {
	// 不做断言：这台机器上可能装也可能没装。
	// 只确认不会 panic，且返回值要么是空串要么指向存在的文件。
	got := findRedis()
	if got != "" {
		if _, err := os.Stat(got); err != nil {
			t.Errorf("返回了不存在的路径: %q", got)
		}
	}
}

func TestProbeInvalidPort(t *testing.T) {
	// 超出范围的端口应当安全返回 false 而不是 panic。
	if probe("127.0.0.1", 99999) {
		t.Error("非法端口不应返回 true")
	}
}
