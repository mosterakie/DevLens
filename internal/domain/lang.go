package domain

import "strings"

// Lang 是诊断内容使用的语言。
//
// 做成显式类型而不是裸字符串，是为了让"语言"这个概念在
// handler、service、analyzer 之间传递时有明确约束。
type Lang string

const (
	// LangZhHans 是简体中文，也是默认语言。
	LangZhHans Lang = "zh-Hans"
	// LangEn 是英文。
	LangEn Lang = "en"
)

var allLangs = []Lang{LangZhHans, LangEn}

// AllLangs 返回支持的语言。返回副本。
func AllLangs() []Lang {
	out := make([]Lang, len(allLangs))
	copy(out, allLangs)
	return out
}

// IsValid 报告 l 是否是支持的语言。
func (l Lang) IsValid() bool {
	for _, v := range allLangs {
		if v == l {
			return true
		}
	}
	return false
}

// DefaultLang 是未指定语言时使用的值。
func DefaultLang() Lang { return LangZhHans }

// ParseLang 解析语言标识，兼容几种常见的写法。
//
// 接受 "zh"、"zh-CN"、"zh-Hans"、"en"、"en-US" 等，
// 都归一到受支持的两种之一。无法识别时返回默认语言，
// 而不是报错——语言偏好不该让整个请求失败。
func ParseLang(s string) Lang {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return DefaultLang()
	}

	// 取主语言标签，例如 "zh-cn" -> "zh"。
	primary := v
	if i := strings.IndexAny(v, "-_"); i >= 0 {
		primary = v[:i]
	}

	switch primary {
	case "zh":
		return LangZhHans
	case "en":
		return LangEn
	default:
		return DefaultLang()
	}
}

// DisplayName 返回该语言的自称，用于语言选择菜单。
func (l Lang) DisplayName() string {
	switch l {
	case LangEn:
		return "English"
	case LangZhHans:
		return "中文"
	default:
		return string(l)
	}
}
