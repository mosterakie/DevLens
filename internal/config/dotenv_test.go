package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeEnv 写一个临时 .env 文件并返回路径。
func writeEnv(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp env: %v", err)
	}
	return path
}

func TestLoadDotEnvBasic(t *testing.T) {
	// 用带前缀的键名，避免和真实环境变量冲突。
	t.Setenv("DEVLENS_TEST_A", "")
	os.Unsetenv("DEVLENS_TEST_A")

	path := writeEnv(t, "DEVLENS_TEST_A=hello\n")
	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := os.Getenv("DEVLENS_TEST_A"); got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

// TestLoadDotEnvDoesNotOverrideExisting 确认真实环境变量优先。
//
// 这条很关键：CI 或生产导出的变量不该被仓库里的 .env 覆盖。
func TestLoadDotEnvDoesNotOverrideExisting(t *testing.T) {
	t.Setenv("DEVLENS_TEST_B", "from-env")

	path := writeEnv(t, "DEVLENS_TEST_B=from-file\n")
	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := os.Getenv("DEVLENS_TEST_B"); got != "from-env" {
		t.Errorf("existing env var was overwritten: got %q", got)
	}
}

func TestLoadDotEnvMissingFileIsNotAnError(t *testing.T) {
	err := LoadDotEnv(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Errorf("missing file should not be an error, got %v", err)
	}
}

func TestLoadDotEnvParsing(t *testing.T) {
	content := `
# 完整注释行

DEVLENS_TEST_C=plain
DEVLENS_TEST_D="quoted value"
DEVLENS_TEST_E='single quoted'
DEVLENS_TEST_F=trailing   # 行尾注释
DEVLENS_TEST_G=  spaced around
DEVLENS_TEST_H=
DEVLENS_TEST_I=value with = sign
`
	path := writeEnv(t, content)
	// 确保这些键一开始不存在。
	for _, k := range []string{
		"DEVLENS_TEST_C", "DEVLENS_TEST_D", "DEVLENS_TEST_E",
		"DEVLENS_TEST_F", "DEVLENS_TEST_G", "DEVLENS_TEST_H", "DEVLENS_TEST_I",
	} {
		os.Unsetenv(k)
	}

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]string{
		"DEVLENS_TEST_C": "plain",
		"DEVLENS_TEST_D": "quoted value",
		"DEVLENS_TEST_E": "single quoted",
		"DEVLENS_TEST_F": "trailing",
		"DEVLENS_TEST_G": "spaced around",
		"DEVLENS_TEST_H": "",
		"DEVLENS_TEST_I": "value with = sign",
	}
	for k, w := range want {
		if got := os.Getenv(k); got != w {
			t.Errorf("%s = %q, want %q", k, got, w)
		}
	}
}

// TestLoadDotEnvKeepsHashInValue 确认未加引号时，紧跟非空格字符的 #
// 不会被当成注释起点。否则形如 P@ss#word 的密码会被截断。
func TestLoadDotEnvKeepsHashInValue(t *testing.T) {
	path := writeEnv(t, "DEVLENS_TEST_J=P@ss#word\n")
	os.Unsetenv("DEVLENS_TEST_J")

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := os.Getenv("DEVLENS_TEST_J"); got != "P@ss#word" {
		t.Errorf("got %q, want P@ss#word", got)
	}
}

// TestLoadDotEnvQuotedKeepsHash 确认引号内的 # 一律保留。
func TestLoadDotEnvQuotedKeepsHash(t *testing.T) {
	path := writeEnv(t, `DEVLENS_TEST_K="has # hash"`+"\n")
	os.Unsetenv("DEVLENS_TEST_K")

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := os.Getenv("DEVLENS_TEST_K"); got != "has # hash" {
		t.Errorf("got %q, want %q", got, "has # hash")
	}
}

func TestLoadDotEnvRejectsMalformedLine(t *testing.T) {
	path := writeEnv(t, "GOOD=1\nTHIS LINE HAS NO EQUALS\n")
	os.Unsetenv("GOOD")

	err := LoadDotEnv(path)
	if err == nil {
		t.Fatal("expected an error for a line without =")
	}
}

func TestLoadDotEnvHandlesCRLF(t *testing.T) {
	// Windows 上编辑的 .env 常常是 CRLF，值末尾不能带上 \r。
	path := writeEnv(t, "DEVLENS_TEST_L=value\r\nDEVLENS_TEST_M=other\r\n")
	os.Unsetenv("DEVLENS_TEST_L")
	os.Unsetenv("DEVLENS_TEST_M")

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := os.Getenv("DEVLENS_TEST_L"); got != "value" {
		t.Errorf("got %q, want value (trailing CR not stripped?)", got)
	}
	if got := os.Getenv("DEVLENS_TEST_M"); got != "other" {
		t.Errorf("got %q, want other", got)
	}
}
