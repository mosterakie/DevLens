package analyzer

import (
	"fmt"
	"strings"

	"github.com/mosterakie/DevLens/internal/domain"
)

// PromptVersion 记录 prompt 的版本。
//
// 改了 prompt 之后，旧数据的 confidence 还能不能和新数据比较？
// 把版本号存进库才能回答这个问题。
//
// v2：加入输出语言控制，并压缩输出规模以降低截断风险。
const PromptVersion = "v2"

// systemPromptTemplate 约束模型的输出格式和边界。
//
// 用 %s 占位输出语言的指令。JSON 的键名始终是英文——它们是要被
// 程序解析的契约，不该随界面语言变化；只有值需要翻译。
const systemPromptTemplate = `You are a production incident analyst.

Given a raw error log, return a structured diagnosis as JSON.

Respond with ONLY a JSON object, no markdown fences, matching this schema:
{
  "title": string,
  "severity": "LOW" | "MEDIUM" | "HIGH" | "CRITICAL",
  "category": string,
  "summary": string,
  "possible_causes": string[],
  "evidence": [{"key": string, "value": string, "source_line": number}],
  "suggested_actions": string[],
  "confidence": number
}

Language:
- %s
- Keep the JSON keys exactly as shown in English.
- severity must stay one of LOW, MEDIUM, HIGH, CRITICAL in English.
- In "evidence", "value" must quote the log verbatim; only "key" is translated.

Rules:
- Be concise. Write summary in at most 2 sentences. Provide at most 4
  possible_causes, at most 4 evidence items and at most 4 suggested_actions,
  each on one line.
- possible_causes must be a list of candidates, never a single definitive cause.
- evidence must quote values that actually appear in the log, with their line number.
- If the log does not contain enough information, set confidence below 0.5
  and state what is missing. Do NOT invent details not present in the log.
- Do not include any text outside the JSON object.`

// languageInstruction 返回该语言对应的输出要求。
func languageInstruction(l domain.Lang) string {
	switch l {
	case domain.LangEn:
		return "Write title, category, summary, possible_causes, evidence.key and suggested_actions in English."
	default:
		return "用简体中文写 title、category、summary、possible_causes、evidence.key 和 suggested_actions。"
	}
}

// SystemPrompt 返回给定语言下的系统提示词。
func SystemPrompt(l domain.Lang) string {
	return fmt.Sprintf(systemPromptTemplate, languageInstruction(l))
}

// systemPrompt 是默认语言下的提示词，供不关心语言的调用方和测试使用。
var systemPrompt = SystemPrompt(domain.DefaultLang())

// buildUserPrompt 拼接用户消息。
func buildUserPrompt(in Input) string {
	var b strings.Builder

	b.WriteString(languageInstruction(in.Lang))
	b.WriteString("\n\nAnalyze the following error log.\n\n")

	if len(in.RelatedSummaries) > 0 {
		b.WriteString("Previously seen similar incidents:\n")
		for _, s := range in.RelatedSummaries {
			b.WriteString("- ")
			b.WriteString(s)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("Log:\n")
	b.WriteString(in.RawLog)

	return b.String()
}

// promptFenceRetrySuffix 在第二次尝试时追加，用于压制常见的 markdown 围栏。
const promptFenceRetrySuffix = "\n\nIMPORTANT: Respond with ONLY valid JSON. No markdown fences, no explanation."

// buildRetryUserPrompt 针对第 attempt 次尝试（从 1 开始）构造用户消息。
//
// 非法 JSON 是这类系统特有的失败模式。重试时逐步收紧约束，
// 比原样重试更可能成功。
func buildRetryUserPrompt(in Input, attempt int) string {
	base := buildUserPrompt(in)
	if attempt <= 1 {
		return base
	}
	return base + promptFenceRetrySuffix
}

// describeAttempt 用于日志，说明这次尝试用了什么策略。
func describeAttempt(attempt int) string {
	switch attempt {
	case 1:
		return "standard prompt"
	case 2:
		return "standard prompt with strict JSON instruction"
	default:
		return fmt.Sprintf("temperature=0, attempt %d", attempt)
	}
}
