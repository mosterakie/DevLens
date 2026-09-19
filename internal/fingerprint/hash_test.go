package fingerprint

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestComputeDeterministic(t *testing.T) {
	in := Normalize("2026-09-18 ERROR timeout at 10.0.1.4")
	first := Compute(in)
	for i := 0; i < 10; i++ {
		if got := Compute(in); !bytes.Equal(got, first) {
			t.Fatalf("Compute is not deterministic on call %d", i)
		}
	}
}

func TestComputeSize(t *testing.T) {
	got := Compute("anything")
	if len(got) != Size {
		t.Errorf("want %d bytes, got %d", Size, len(got))
	}
}

func TestComputeHexLength(t *testing.T) {
	got := ComputeHex("anything")
	if len(got) != Size*2 {
		t.Errorf("want %d hex chars, got %d", Size*2, len(got))
	}
	if _, err := hex.DecodeString(got); err != nil {
		t.Errorf("ComputeHex produced invalid hex: %v", err)
	}
}

// TestOfNormalizesBeforeHashing 确认 Of 确实先归一化。
// 如果 Of 直接对原始日志做 hash，两个只差 IP 的日志会得到不同指纹。
func TestOfNormalizesBeforeHashing(t *testing.T) {
	a := Of("Error connecting to DB at 10.0.1.4")
	b := Of("Error connecting to DB at 10.0.2.7")
	if !bytes.Equal(a, b) {
		t.Error("Of should normalize before hashing")
	}
}

// TestComputeDistinctInputs 确认不同的规范化输入产生不同指纹。
func TestComputeDistinctInputs(t *testing.T) {
	inputs := []string{"connection refused", "connection reset", "timeout"}
	seen := make(map[string]string, len(inputs))
	for _, in := range inputs {
		h := ComputeHex(in)
		if prev, ok := seen[h]; ok {
			t.Errorf("%q and %q produced the same fingerprint", prev, in)
			continue
		}
		seen[h] = in
	}
}

func TestNormalizeThenComputeStableAcrossFormats(t *testing.T) {
	// 同一类错误的两种写法，经过完整链路后指纹应一致。
	a := Of("2026-09-18 14:32:51 ERROR dial tcp 10.0.1.4:5432: i/o timeout after 5s")
	b := Of("2026-09-19 09:11:02 Error dial tcp 10.0.2.7:6432: i/o timeout after 200ms")
	if !bytes.Equal(a, b) {
		t.Errorf("expected same fingerprint across formats:\n  %s\n  %s",
			hex.EncodeToString(a), hex.EncodeToString(b))
	}
}
