// Package redact 从日志文本里移除凭据。
//
// 目的不是"日志文件里别出现敏感信息"，而是**敏感数据不流向第三方**：
// 日志会被发给外部模型，一旦发出去就收不回来。所以脱敏发生在
// 交给模型之前，并且存库的就是脱敏后的版本。
//
// 规则刻意保守：只匹配"键值对"形式的凭据，不碰裸字符串。
// 过度脱敏会让模型失去诊断线索——比如把 IP 全脱掉，就看不出
// 网络问题了，而 IP 本身并不是凭据。
package redact

import (
	"regexp"
	"strings"
)

// Placeholder 是替换后的占位符。
//
// 保留键名、只替换值：这样模型仍能看出"这里是个认证字段"，
// 从而判断出这是认证问题，而不是丢掉整条线索。
const Placeholder = "***"

// rule 是一条脱敏规则。
type rule struct {
	name string
	re   *regexp.Regexp
	// repl 是替换模板，用 $1 引用要保留的部分（通常是键名）。
	repl string
}

// 键名的通用形式。要求后面必须跟分隔符，避免匹配到
// "passwordless" 这类不含值的词。
const keySep = `[\s"']*[:=][\s"']*`

// keyStart 匹配键名的左边界。
//
// 不能用 \b：下划线也是单词字符，所以 "client_secret" 里的
// "secret" 前面没有词边界，\b 会漏掉它——而带前缀的键名
// （client_secret、aws_secret_access_key、db_password）
// 恰恰是最常见的形式。
//
// 改为显式要求：行首、空白、引号，或任意非字母数字字符。
const keyStart = `(^|[\s"',;{[(])`

// rules 按顺序应用。
//
// 顺序有影响：DSN 里的密码要在通用规则之前处理，否则会被
// 后面的规则切碎。
var rules = []rule{
	// 连接串里的密码：scheme://user:password@host
	// 这是凭据泄漏最常见的形式，且极难用通用规则覆盖。
	{
		name: "url_credentials",
		re:   regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]+):([^/\s@]+)@`),
		repl: "$1:" + Placeholder + "@",
	},

	// Authorization 头。Bearer/Basic/Token 后面跟凭据。
	{
		name: "authorization_header",
		re:   regexp.MustCompile(`(?i)(authorization` + keySep + `(?:bearer|basic|token)\s+)[^\s"',;]+`),
		repl: "${1}${2}" + Placeholder,
	},

	// 带 sk- 前缀的密钥。OpenAI、DeepSeek 等都用这个形式。
	// 即使它不以 key= 的形式出现（比如粘在 URL 里）也是明确特征，
	// 误伤概率极低。
	{
		name: "sk_prefixed_key",
		re:   regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`),
		repl: "sk-" + Placeholder,
	},

	// password / passwd / pwd
	{
		name: "password",
		re:   regexp.MustCompile(`(?i)` + keyStart + `(pass(?:word|wd)?` + keySep + `)[^\s"',;=&:]+`),
		repl: "${1}${2}" + Placeholder,
	},

	// token / secret / api_key / access_key / private_key
	{
		name: "token_secret",
		re:   regexp.MustCompile(`(?i)` + keyStart + `((?:access[_-]?|api[_-]?|auth[_-]?|client[_-]?|private[_-]?|refresh[_-]?|secret[_-]?)?(?:token|secret|key)` + keySep + `)[^\s"',;=&:]+`),
		repl: "${1}${2}" + Placeholder,
	},

	// credential
	{
		name: "credential",
		re:   regexp.MustCompile(`(?i)` + keyStart + `(credentials?` + keySep + `)[^\s"',;=&:]+`),
		repl: "${1}${2}" + Placeholder,
	},
}

// Apply 返回脱敏后的文本。
//
// 除了替换凭据，还会统一换行符并去掉尾随空白：
// 日志可能来自 Windows，带 \r 会让后续按行处理时出错。
func Apply(s string) string {
	out := s

	// 先规范化换行，让规则的 \s 匹配行为一致。
	out = strings.ReplaceAll(out, "\r\n", "\n")
	out = strings.ReplaceAll(out, "\r", "\n")

	for _, r := range rules {
		out = r.re.ReplaceAllString(out, r.repl)
	}

	return out
}

// Changed 报告 Apply 是否改动过文本。
//
// 用于记录"这条日志含敏感信息"的事实，而不记录具体内容。
func Changed(original, redacted string) bool {
	return original != redacted
}

// RuleNames 返回规则名，供测试与文档使用。
func RuleNames() []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.name)
	}
	return out
}
