package fingerprint

import (
	"strings"
	"testing"
)

// TestNormalizeSameFingerprint 覆盖"应该视为同类"的情况。
// 表里的每一行都是 04-fingerprint.md 规则表的一条规格。
func TestNormalizeSameFingerprint(t *testing.T) {
	tests := []struct {
		name string
		a, b string
	}{
		{
			name: "IP 不同应视为同类",
			a:    "Error connecting to DB at 10.0.1.4",
			b:    "Error connecting to DB at 10.0.2.7",
		},
		{
			name: "时间戳不同应视为同类",
			a:    "2026-09-18 14:32:51 ERROR timeout",
			b:    "2026-09-19 09:11:02 ERROR timeout",
		},
		{
			name: "goroutine 号不同应视为同类",
			a:    "goroutine 231 [IO wait]:\ncontext deadline exceeded",
			b:    "goroutine 88 [IO wait]:\ncontext deadline exceeded",
		},
		{
			name: "堆栈行号不同应视为同类",
			a:    "db/db.go:142",
			b:    "db/db.go:98",
		},
		{
			name: "UUID 不同应视为同类",
			a:    "request id 550e8400-e29b-41d4-a716-446655440000 failed",
			b:    "request id 6ba7b810-9dad-11d1-80b4-00c04fd430c8 failed",
		},
		{
			name: "内存地址不同应视为同类",
			a:    "panic: nil map at 0xc000123456",
			b:    "panic: nil map at 0xc000abcdef",
		},
		{
			name: "端口不同应视为同类",
			a:    "dial tcp 10.0.0.1:5432: i/o timeout",
			b:    "dial tcp 10.0.0.1:6379: i/o timeout",
		},
		{
			name: "耗时时长不同应视为同类",
			a:    "context deadline exceeded after 5s",
			b:    "context deadline exceeded after 200ms",
		},
		{
			name: "大小写和空白差异应忽略",
			a:    "  CONNECTION   Refused  ",
			b:    "connection refused",
		},
		{
			name: "常见日志时间格式应视为同类",
			a:    "09/18/2026 14:32:51 error",
			b:    "09/19/2026 09:11:02 error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			na, nb := Normalize(tt.a), Normalize(tt.b)
			if na != nb {
				t.Fatalf("normalized forms differ:\n  a: %q\n  b: %q", na, nb)
			}
			if ha, hb := ComputeHex(na), ComputeHex(nb); ha != hb {
				t.Fatalf("fingerprints differ: %s vs %s", ha, hb)
			}
		})
	}
}

// TestNormalizeDistinctFingerprint 是反向断言：不同的错误不应该被合并。
//
// 只测"相同的相同"会漏掉归一化过松的 bug，那样所有日志会收敛成同一个指纹，
// 找同类功能表面可用实际失效。这类测试是防止规则退化的主要机制。
func TestNormalizeDistinctFingerprint(t *testing.T) {
	// 每一组内部的成员都必须互不相同。
	groups := [][]string{
		{
			"connection refused",
			"connection reset",
			"context deadline exceeded",
			"context canceled",
		},
		{
			"no such host",
			"no route to host",
			"network is unreachable",
		},
		{
			"permission denied",
			"file not found",
			"disk quota exceeded",
		},
	}

	for _, group := range groups {
		seen := make(map[string]string, len(group))
		for _, s := range group {
			h := ComputeHex(Normalize(s))
			if prev, ok := seen[h]; ok {
				t.Errorf("%q and %q collapsed to the same fingerprint", prev, s)
				continue
			}
			seen[h] = s
		}
	}
}

// TestNormalizeIdempotent 保证归一化是幂等的。
//
// 不幂等意味着规则之间会互相干扰，同一段日志跑两次得到不同指纹，
// 那"找同类"就完全不可靠了。
func TestNormalizeIdempotent(t *testing.T) {
	inputs := []string{
		"2026-09-18 14:32:51 ERROR dial tcp 10.0.1.4:5432: i/o timeout after 5s",
		"goroutine 231 [IO wait]:\n\tdb/db.go:142 +0x1a",
		"request 550e8400-e29b-41d4-a716-446655440000 failed at 0xc000123456",
		"CONNECTION   REFUSED",
		"",
		"   ",
		"纯中文日志啊啊啊",
	}

	for _, in := range inputs {
		once := Normalize(in)
		twice := Normalize(once)
		if once != twice {
			t.Errorf("Normalize is not idempotent for %q:\n  once:  %q\n  twice: %q", in, once, twice)
		}
	}
}

// TestNormalizeEdgeCases 覆盖边界输入，主要验证不 panic 且行为可预期。
func TestNormalizeEdgeCases(t *testing.T) {
	t.Run("空字符串", func(t *testing.T) {
		if got := Normalize(""); got != "" {
			t.Errorf("want empty, got %q", got)
		}
	})

	t.Run("纯空白", func(t *testing.T) {
		if got := Normalize("  \n\t  "); got != "" {
			t.Errorf("want empty, got %q", got)
		}
	})

	t.Run("超长输入被截断", func(t *testing.T) {
		long := strings.Repeat("a", maxFingerprintInput*3)
		out := Normalize(long)
		if len(out) > maxFingerprintInput {
			t.Errorf("output length %d exceeds limit %d", len(out), maxFingerprintInput)
		}
	})

	t.Run("仅在上限之后不同则同指纹", func(t *testing.T) {
		// 差异完全落在截断点之后，两者应当得到相同指纹。
		head := strings.Repeat("a", maxFingerprintInput)
		long1 := head + "xxxxx"
		long2 := head + "yyyyy"
		if ComputeHex(Normalize(long1)) != ComputeHex(Normalize(long2)) {
			t.Error("differences beyond the truncation limit should not affect the fingerprint")
		}
	})

	t.Run("上限之内不同则不同指纹", func(t *testing.T) {
		// 差异落在上限之内，不能被合并。
		head := strings.Repeat("a", maxFingerprintInput-10)
		long1 := head + "xxxxx" + strings.Repeat("z", 100)
		long2 := head + "yyyyy" + strings.Repeat("z", 100)
		if ComputeHex(Normalize(long1)) == ComputeHex(Normalize(long2)) {
			t.Error("differences within the limit must be preserved")
		}
	})

	t.Run("非 UTF-8 字节不 panic", func(t *testing.T) {
		raw := string([]byte{0xff, 0xfe, 'e', 'r', 'r'})
		_ = Normalize(raw)
	})

	t.Run("只有数字", func(t *testing.T) {
		if got := Normalize("12345"); got != numberPlaceholder {
			t.Errorf("want %q, got %q", numberPlaceholder, got)
		}
	})

	t.Run("占位符为小写形式", func(t *testing.T) {
		// 占位符必须全小写：Normalize 第一步就 ToLower，
		// 含大写的占位符会在第二次调用时被改写，破坏幂等性。
		got := Normalize("host 10.0.0.1 at 2026-09-18T14:32:51Z")
		if !strings.Contains(got, ipPlaceholder) {
			t.Errorf("want %q to contain %q", got, ipPlaceholder)
		}
		if !strings.Contains(got, timePlaceholder) {
			t.Errorf("want %q to contain %q", got, timePlaceholder)
		}
		if strings.ContainsAny(got, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			t.Errorf("normalized form should be lowercase, got %q", got)
		}
	})
}

// TestNormalizePlaceholders 直接断言几条规则的输出，
// 便于在规则被误改时立刻定位。
func TestNormalizePlaceholders(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"IPv4", "host 192.168.1.1 down", "host <ip> down"},
		{"时间戳", "2026-09-18T14:32:51Z error", "<ts> error"},
		{"goroutine", "goroutine 42 [running]:", "goroutine <n> [running]:"},
		{"go 文件行号", "main.go:99", "main.go:<l>"},
		{"端口", "listen :8080 failed", "listen :<port> failed"},
		{"十六进制字面量", "addr=0xdeadbeef", "addr=<hex>"},
		{"耗时", "waited 1500ms for lock", "waited <duration> for lock"},
		{"大写转小写", "TIMEOUT", "timeout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.in); got != tt.want {
				t.Errorf("Normalize(%q)\n got: %q\nwant: %q", tt.in, got, tt.want)
			}
		})
	}
}
