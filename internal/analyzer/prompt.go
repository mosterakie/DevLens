package analyzer

import (
	"fmt"
	"strings"
)

// PromptVersion 记录 prompt 的版本。
//
// 改了 prompt 之后，旧数据的 confidence 还能不能和新数据比较？
// 把版本号存进库才能回答这个问题。
const PromptVersion = "v1"

// systemPrompt 约束模型的输出格式和边界。
const systemPrompt = `You are a production incident analyst.

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

Rules:
- possible_causes must be a list of candidates, never a single definitive cause.
- evidence must quote values that actually appear in the log, with their line number.
- If the log does not contain enough information, set confidence below 0.5
  and state what is missing. Do NOT invent details not present in the log.
- Do not include any text outside the JSON object.`

// buildUserPrompt 拼接用户消息。
func buildUserPrompt(in Input) string {
	var b strings.Builder

	b.WriteString("Analyze the following error log.\n\n")

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
