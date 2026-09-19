package usecases

import (
	"testing"

	"github.com/mosterakie/DevLens/internal/fingerprint"
)

// TestSamplesAreUsable 确认每条样本都能通过日志校验，
// 并且归一化不会 panic、不会产出空串。
func TestSamplesAreUsable(t *testing.T) {
	if len(Samples) == 0 {
		t.Fatal("样本集为空")
	}

	for _, s := range Samples {
		t.Run(s.Name, func(t *testing.T) {
			if s.Log == "" {
				t.Fatal("日志内容为空")
			}
			if s.Source == "" {
				t.Error("缺少来源链接")
			}

			norm := fingerprint.Normalize(s.Log)
			if norm == "" {
				t.Error("归一化结果为空")
			}
			if fp := fingerprint.Compute(norm); len(fp) != fingerprint.Size {
				t.Errorf("指纹长度 = %d, want %d", len(fp), fingerprint.Size)
			}
		})
	}
}

// TestSamplesAreDistinct 确认这些样本互相不同。
//
// 它们是同一个人的真实故障记录，如果两条撞到同一个指纹，
// 说明归一化规则过松——那会让"同类问题"把无关故障混在一起。
func TestSamplesAreDistinct(t *testing.T) {
	seen := map[string]string{}

	for _, s := range Samples {
		h := fingerprint.ComputeHex(fingerprint.Normalize(s.Log))
		if prev, ok := seen[h]; ok {
			t.Errorf("%s 与 %s 归一化后相同，规则可能过松", s.Name, prev)
			continue
		}
		seen[h] = s.Name
	}
}

// TestNormalizeIsIdempotentOnSamples 在真实日志上验证幂等性。
//
// 单元测试用的是构造的字符串，这里换成真实的、含多行缩进和
// 各种噪声的日志。
func TestNormalizeIsIdempotentOnSamples(t *testing.T) {
	for _, s := range Samples {
		once := fingerprint.Normalize(s.Log)
		twice := fingerprint.Normalize(once)
		if once != twice {
			t.Errorf("%s 归一化不幂等:\n once:  %q\n twice: %q",
				s.Name, truncate(once, 120), truncate(twice, 120))
		}
	}
}

// TestSamplesVaryInNormalizedForm 确认时间戳等噪声确实被抹掉了。
//
// 取一条含时间戳的样本，改掉时间戳后归一化结果应当不变，
// 否则"同类判定"对时间变化不成立。
func TestSamplesVaryInNormalizedForm(t *testing.T) {
	var target *Sample
	for i := range Samples {
		if fingerprintsTimestamp(Samples[i].Log) {
			target = &Samples[i]
			break
		}
	}
	if target == nil {
		t.Skip("样本里没有含时间戳的日志")
	}

	original := target.Log
	modified := replaceFirstTimestamp(original)
	if modified == original {
		t.Skip("未能替换时间戳")
	}

	a := fingerprint.ComputeHex(fingerprint.Normalize(original))
	b := fingerprint.ComputeHex(fingerprint.Normalize(modified))
	if a != b {
		t.Errorf("%s: 时间戳变化影响了指纹，归一化规则可能漏了某种时间格式", target.Name)
	}
}

func fingerprintsTimestamp(s string) bool {
	return replaceFirstTimestamp(s) != s
}

// replaceFirstTimestamp 把第一处 ISO 风格的时间戳换成年份更早的值。
func replaceFirstTimestamp(s string) string {
	// 与 fingerprint 包的规则保持一致：匹配 yyyy-mm-dd HH:MM:SS。
	for i := 0; i+19 <= len(s); i++ {
		seg := s[i : i+19]
		if looksLikeTimestamp(seg) {
			// 只改年份，保持长度不变，避免影响偏移。
			repl := "2000" + seg[4:]
			return s[:i] + repl + s[i+19:]
		}
	}
	return s
}

func looksLikeTimestamp(s string) bool {
	if len(s) != 19 {
		return false
	}
	digits := []int{0, 1, 2, 3, 5, 6, 8, 9, 11, 12, 14, 15, 17, 18}
	for _, i := range digits {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	if s[4] != '-' || s[7] != '-' || s[13] != ':' || s[16] != ':' {
		return false
	}
	if s[10] != ' ' && s[10] != 'T' {
		return false
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
