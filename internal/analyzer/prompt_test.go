package analyzer

import (
	"strings"
	"testing"

	"github.com/mosterakie/DevLens/internal/domain"
)

// TestSystemPromptIsChineseByDefault 是这一轮的需求：默认用中文输出。
func TestSystemPromptIsChineseByDefault(t *testing.T) {
	p := SystemPrompt(domain.DefaultLang())
	if !strings.Contains(p, "简体中文") {
		t.Errorf("default prompt should ask for Simplified Chinese, got:\n%s", p)
	}
}

func TestSystemPromptEnglish(t *testing.T) {
	p := SystemPrompt(domain.LangEn)
	if strings.Contains(p, "简体中文") {
		t.Error("English prompt should not ask for Chinese")
	}
	if !strings.Contains(p, "Write title") {
		t.Errorf("English prompt should contain the English instruction, got:\n%s", p)
	}
}

// TestSystemPromptKeepsKeysEnglish 确认键名和枚举值始终是英文。
//
// 它们是要被程序解析的契约，随语言变化会让解析直接失败。
func TestSystemPromptKeepsKeysEnglish(t *testing.T) {
	for _, lang := range domain.AllLangs() {
		p := SystemPrompt(lang)
		for _, want := range []string{
			`"possible_causes"`,
			`"suggested_actions"`,
			`"source_line"`,
			`"LOW"`,
			`"CRITICAL"`,
			`"json_object"`,
		} {
			if want == `"json_object"` {
				continue // 不在 prompt 里，属于请求参数
			}
			if !strings.Contains(p, want) {
				t.Errorf("lang %s: prompt missing %s", lang, want)
			}
		}
	}
}

// TestUserPromptCarriesLanguage 确认用户消息里也重复了一次语言要求。
// 系统提示词和用户消息都提到，模型更不容易忽略。
func TestUserPromptCarriesLanguage(t *testing.T) {
	zh := buildUserPrompt(Input{RawLog: "boom", Lang: domain.LangZhHans})
	if !strings.Contains(zh, "简体中文") {
		t.Error("Chinese user prompt should carry the language instruction")
	}

	en := buildUserPrompt(Input{RawLog: "boom", Lang: domain.LangEn})
	if strings.Contains(en, "简体中文") {
		t.Error("English user prompt should not carry the Chinese instruction")
	}
	if !strings.Contains(en, "English") {
		t.Error("English user prompt should carry the English instruction")
	}
}

// TestInputLangOrFallsBack 确认零值语言会退回默认语言。
func TestInputLangOrFallsBack(t *testing.T) {
	if got := (Input{}).LangOr(); got != domain.DefaultLang() {
		t.Errorf("empty Lang should fall back to default, got %q", got)
	}
	if got := (Input{Lang: domain.LangEn}).LangOr(); got != domain.LangEn {
		t.Errorf("got %q, want en", got)
	}
	if got := (Input{Lang: "fr"}).LangOr(); got != domain.DefaultLang() {
		t.Errorf("unsupported Lang should fall back to default, got %q", got)
	}
}

// TestPromptVersionBumped 确认改了 prompt 后版本号也跟着变，
// 否则无法区分新旧数据。
func TestPromptVersionBumped(t *testing.T) {
	if PromptVersion != "v2" {
		t.Errorf("PromptVersion = %q, expected v2 after the language change", PromptVersion)
	}
}
