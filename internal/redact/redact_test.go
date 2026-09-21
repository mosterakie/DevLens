package redact

import (
	"strings"
	"testing"
)

// TestRedactsCredentials 覆盖必须脱掉的模式。
//
// 每条都取自真实的日志形态，而不是构造的玩具样例。
func TestRedactsCredentials(t *testing.T) {
	tests := []struct {
		name string
		in   string
		// leak 是必须消失的敏感串
		leak string
		// keep 是必须保留的内容（通常是键名，模型需要它来理解上下文）
		keep string
	}{
		{
			name: "连接串里的密码",
			in:   "failed to connect: postgres://appuser:S3cretP%40ss@db.internal:5432/prod",
			leak: "S3cretP%40ss",
			keep: "appuser",
		},
		{
			name: "Authorization Bearer",
			in:   "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.sig",
			leak: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
			keep: "Authorization",
		},
		{
			name: "Authorization Basic",
			in:   "authorization: Basic dXNlcjpwYXNzd29yZA==",
			leak: "dXNlcjpwYXNzd29yZA==",
			keep: "authorization",
		},
		{
			name: "password 等号形式",
			in:   "config: password=Hunter2!xyz failed",
			leak: "Hunter2!xyz",
			keep: "password",
		},
		{
			name: "passwd 形式",
			in:   "conn passwd=p@ssw0rd host=db",
			leak: "p@ssw0rd",
			keep: "passwd",
		},
		{
			name: "api_key 冒号形式",
			in:   `{"api_key": "abcdef1234567890"}`,
			leak: "abcdef1234567890",
			keep: "api_key",
		},
		{
			name: "token 等号形式",
			in:   "request token=ghp_ABCDEFGHIJKLMNOPQRSTUV failed",
			leak: "ghp_ABCDEFGHIJKLMNOPQRSTUV",
			keep: "token",
		},
		{
			name: "secret 等号形式",
			in:   "client_secret=verysecretvalue&scope=read",
			leak: "verysecretvalue",
			keep: "secret",
		},
		{
			name: "sk 前缀的密钥",
			in:   "using key sk-7297abcdefghijklmnopqrstuvwxyz4a9c for request",
			leak: "7297abcdefghijklmnopqrstuvwxyz4a9c",
			keep: "using key",
		},
		{
			name: "credentials 形式",
			in:   "credentials=admin:admin123 rejected",
			leak: "admin:admin123",
			keep: "credentials",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Apply(tt.in)
			if strings.Contains(got, tt.leak) {
				t.Errorf("敏感信息未被脱掉:\n  in:  %s\n  out: %s", tt.in, got)
			}
			if tt.keep != "" && !strings.Contains(got, tt.keep) {
				t.Errorf("键名 %q 应当保留，否则模型失去上下文:\n  out: %s", tt.keep, got)
			}
			if !strings.Contains(got, "***") {
				t.Errorf("应当含占位符:\n  out: %s", got)
			}
		})
	}
}

// TestKeepsDiagnosticInfo 是反向断言：不该脱的东西必须留下。
//
// 过度脱敏会让模型失去诊断依据。这里固定住"什么是安全的"。
func TestKeepsDiagnosticInfo(t *testing.T) {
	tests := []struct {
		name string
		in   string
		keep []string
	}{
		{
			name: "IP 地址要保留",
			in:   "dial tcp 10.0.1.4:5432: connection refused",
			keep: []string{"10.0.1.4", "5432"},
		},
		{
			name: "内网域名要保留",
			in:   "lookup payment.internal on 10.0.0.2:53: no such host",
			keep: []string{"payment.internal", "10.0.0.2"},
		},
		{
			name: "普通单词不该被误伤",
			in:   "the secret sauce of this approach is caching",
			keep: []string{"secret sauce"},
		},
		{
			name: "不带值的键名不该被误伤",
			in:   "passwordless authentication is enabled",
			keep: []string{"passwordless"},
		},
		{
			name: "错误信息要保留",
			in:   "context deadline exceeded while waiting for database connection",
			keep: []string{"context deadline exceeded", "database connection"},
		},
		{
			name: "堆栈行号要保留",
			in:   "goroutine 231 [IO wait]:\n\t/payment/db/db.go:142 +0x1a",
			keep: []string{"goroutine 231", "db.go:142", "+0x1a"},
		},
		{
			name: "request id 这类 UUID 要保留",
			in:   "request 550e8400-e29b-41d4-a716-446655440000 failed",
			keep: []string{"550e8400-e29b-41d4-a716-446655440000"},
		},
		{
			name: "状态码与耗时",
			in:   "HTTP 401 after 5s, pool size 20",
			keep: []string{"401", "5s", "20"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Apply(tt.in)
			if got != tt.in {
				t.Errorf("不该被改动:\n  in:  %s\n  out: %s", tt.in, got)
			}
			for _, k := range tt.keep {
				if !strings.Contains(got, k) {
					t.Errorf("应当保留 %q:\n  out: %s", k, got)
				}
			}
		})
	}
}

// TestIdempotent 确认重复脱敏不会继续改动。
//
// 值已经被替换成 ***，再跑一次不应再变。
func TestIdempotent(t *testing.T) {
	inputs := []string{
		"password=Hunter2!xyz",
		"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.x.y",
		"postgres://user:secret@host:5432/db",
		"api_key=abcdef1234567890 token=ghp_ABCDEFGHIJKLMNOP",
		"nothing sensitive here",
	}

	for _, in := range inputs {
		once := Apply(in)
		twice := Apply(once)
		if once != twice {
			t.Errorf("不幂等:\n  in:    %s\n  once:  %s\n  twice: %s", in, once, twice)
		}
	}
}

// TestHandlesCRLF 确认 Windows 换行被规范化。
//
// 日志常来自 Windows 机器，带 \r 会让按行处理的逻辑出错。
func TestHandlesCRLF(t *testing.T) {
	in := "line1\r\nline2\rline3"
	got := Apply(in)
	if strings.Contains(got, "\r") {
		t.Errorf("应当去掉 \r, got %q", got)
	}
	if !strings.Contains(got, "line1\nline2\nline3") {
		t.Errorf("换行未正确规范化: %q", got)
	}
}

func TestEmptyInput(t *testing.T) {
	if got := Apply(""); got != "" {
		t.Errorf("空输入应返回空, got %q", got)
	}
}

func TestChanged(t *testing.T) {
	if !Changed("password=x", Apply("password=x")) {
		t.Error("应当报告有改动")
	}
	if Changed("plain text", Apply("plain text")) {
		t.Error("无改动时不该报告")
	}
}

func TestRuleNames(t *testing.T) {
	names := RuleNames()
	if len(names) == 0 {
		t.Fatal("应当有规则")
	}
	// 规则名唯一，便于日志里指出是哪条命中。
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			t.Errorf("规则名重复: %s", n)
		}
		seen[n] = true
	}
}

// TestMultipleSecretsInOneLine 确认一行里的多个凭据都会被处理。
func TestMultipleSecretsInOneLine(t *testing.T) {
	in := "password=aaa and token=bbb and api_key=ccc"
	got := Apply(in)

	for _, leak := range []string{"aaa", "bbb", "ccc"} {
		if strings.Contains(got, "="+leak) {
			t.Errorf("%q 未被脱掉: %s", leak, got)
		}
	}
	if strings.Count(got, "***") != 3 {
		t.Errorf("应当有 3 个占位符: %s", got)
	}
}

// TestRealisticDSN 覆盖最常见的凭据泄漏形式。
func TestRealisticDSN(t *testing.T) {
	in := "DATABASE_URL=postgres://devlens:devlens@127.0.0.1:5432/devlens?sslmode=disable"
	got := Apply(in)

	if strings.Contains(got, "devlens:devlens") {
		t.Errorf("DSN 里的密码未被脱掉: %s", got)
	}
	// 主机、端口、库名要保留，它们是诊断需要的信息。
	for _, keep := range []string{"127.0.0.1", "5432", "sslmode=disable"} {
		if !strings.Contains(got, keep) {
			t.Errorf("应当保留 %q: %s", keep, got)
		}
	}
}
