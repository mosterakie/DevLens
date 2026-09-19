package fingerprint

import (
	"regexp"
	"strings"
)

// rule 是一条归一化规则。
//
// 用编译后的 *regexp.Regexp 而不是每次重新编译：Normalize 是热路径，
// 每条日志都要跑一遍全部规则。
type rule struct {
	name        string
	re          *regexp.Regexp
	replacement string
}

// rules 是归一化规则表，按顺序应用。
//
// 顺序有实际影响，不能随意调整，关键约束见 Normalize 的注释。
var rules = []rule{
	// 时间戳必须先于数字规则，否则 2026-09-18 会先被拆成 <N>-<N>-<N>。
	{
		name:        "iso8601",
		re:          regexp.MustCompile(`\d{4}-\d{2}-\d{2}[t ]\d{2}:\d{2}:\d{2}(\.\d+)?(z|[+-]\d{2}:?\d{2})?`),
		replacement: timePlaceholder,
	},
	{
		name:        "common_datetime",
		re:          regexp.MustCompile(`\d{2}/\d{2}/\d{4} \d{2}:\d{2}:\d{2}`),
		replacement: timePlaceholder,
	},
	// 带单位的耗时要在纯数字规则之前，否则 "5s" 会先变成 "<N>s"。
	{
		name:        "duration",
		re:          regexp.MustCompile(`\b\d+(\.\d+)?(ms|us|µs|ns|s|m|h)\b`),
		replacement: durationPlaceholder,
	},
	{
		name:        "ipv4",
		re:          regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}\b`),
		replacement: ipPlaceholder,
	},
	{
		name:        "ipv6",
		re:          regexp.MustCompile(`\b([0-9a-f]{0,4}:){2,7}[0-9a-f]{0,4}\b`),
		replacement: ipPlaceholder,
	},
	{
		name:        "uuid",
		re:          regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`),
		replacement: uuidPlaceholder,
	},
	{
		name:        "hex_literal",
		re:          regexp.MustCompile(`\b0x[0-9a-f]+\b`),
		replacement: hexPlaceholder,
	},
	{
		name:        "long_hex",
		re:          regexp.MustCompile(`\b[0-9a-f]{16,}\b`),
		replacement: hexPlaceholder,
	},
	{
		name:        "goroutine_id",
		re:          regexp.MustCompile(`goroutine \d+`),
		replacement: "goroutine " + numberPlaceholder,
	},
	// Go 的 file:line 要先于通用 file:line，否则 .go 会被通用规则匹配。
	{
		name:        "go_file_line",
		re:          regexp.MustCompile(`([\w./\\-]+\.go):\d+`),
		replacement: "$1:" + linePlaceholder,
	},
	{
		name:        "file_line",
		re:          regexp.MustCompile(`([\w./\\-]+\.\w{1,8}):\d+`),
		replacement: "$1:" + linePlaceholder,
	},
	{
		name:        "port",
		re:          regexp.MustCompile(`:(\d{2,5})\b`),
		replacement: ":" + portPlaceholder,
	},
	{
		name:        "number",
		re:          regexp.MustCompile(`\b\d+\b`),
		replacement: numberPlaceholder,
	},
}

// 占位符。集中定义，避免各处写错。
//
// 一律小写。这不是风格选择：Normalize 第一步就是 ToLower，
// 如果占位符含大写字符，第二次调用会被再次小写化，幂等性立刻被破坏。
// 04-fingerprint.md 中的规则表与本处保持一致。
const (
	timePlaceholder     = "<ts>"
	durationPlaceholder = "<duration>"
	ipPlaceholder       = "<ip>"
	uuidPlaceholder     = "<uuid>"
	hexPlaceholder      = "<hex>"
	numberPlaceholder   = "<n>"
	linePlaceholder     = "<l>"
	portPlaceholder     = "<port>"
)

var whitespaceRun = regexp.MustCompile(`\s+`)

// maxFingerprintInput 限制参与指纹计算的字符数。
// raw_log 仍然完整存储，这里只是不参与指纹。
const maxFingerprintInput = 2000

// Normalize 把一段原始日志转换成用于计算指纹的规范化形式。
//
// 设计目标是让"本质相同的错误"收敛到同一个字符串，同时尽量不把
// 不同的错误合并到一起。具体取舍见 04-fingerprint.md。
//
// 处理顺序（每一步都有理由）：
//  1. 先截断：stack trace 尾部的帧数和内容都会变化，保留头部已足够区分错误。
//  2. 再转小写：必须在下标替换之前，否则占位符本身也会被转成小写，
//     与 04-fingerprint.md 中记载的 <IP> / <TS> 形式不一致。
//     因此所有规则里的正则都用小写字符类。
//  3. 然后按顺序套用规则表。
//  4. 最后折叠空白并 trim。
//
// Normalize 是幂等的：Normalize(Normalize(x)) == Normalize(x)。
// 幂等性由测试保证，因为规则之间有干扰时很容易破坏它。
func Normalize(raw string) string {
	s := raw

	if len(s) > maxFingerprintInput {
		s = s[:maxFingerprintInput]
	}

	s = strings.ToLower(s)

	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.replacement)
	}

	// 折叠空白，让排版差异不影响指纹。
	s = whitespaceRun.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)

	return s
}
