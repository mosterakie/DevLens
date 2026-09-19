package domain

import "testing"

func TestParseLang(t *testing.T) {
	tests := []struct {
		in   string
		want Lang
	}{
		{"", LangZhHans},
		{"zh", LangZhHans},
		{"zh-CN", LangZhHans},
		{"zh-Hans", LangZhHans},
		{"zh_CN", LangZhHans},
		{"ZH-CN", LangZhHans},
		{"  zh  ", LangZhHans},
		{"en", LangEn},
		{"en-US", LangEn},
		{"EN", LangEn},
		{"fr", LangZhHans},    // 不支持，退回默认
		{"de-DE", LangZhHans}, // 同上
		{"xx", LangZhHans},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := ParseLang(tt.in); got != tt.want {
				t.Errorf("ParseLang(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLangIsValid(t *testing.T) {
	for _, l := range AllLangs() {
		if !l.IsValid() {
			t.Errorf("%q should be valid", l)
		}
	}
	if Lang("fr").IsValid() {
		t.Error("unsupported language should be invalid")
	}
}

func TestDefaultLangIsChinese(t *testing.T) {
	if got := DefaultLang(); got != LangZhHans {
		t.Errorf("DefaultLang() = %q, want %q", got, LangZhHans)
	}
}

func TestAllLangsReturnsCopy(t *testing.T) {
	first := AllLangs()
	first[0] = "MUTATED"
	if AllLangs()[0] == "MUTATED" {
		t.Error("AllLangs leaked internal state")
	}
}

func TestLangDisplayName(t *testing.T) {
	if got := LangZhHans.DisplayName(); got != "中文" {
		t.Errorf("got %q", got)
	}
	if got := LangEn.DisplayName(); got != "English" {
		t.Errorf("got %q", got)
	}
}
