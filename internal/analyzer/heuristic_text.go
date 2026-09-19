package analyzer

import "github.com/mosterakie/DevLens/internal/domain"

// ruleText 是一组规则的本地化文案。
//
// 与规则本身分开：规则是"怎么判断"，文案是"怎么表述"，
// 两者的变化原因不同，放在一起会让结构变乱。
type ruleText struct {
	summary string
	titles  string
	causes  []string
	actions []string
}

// textFor 返回该规则在指定语言下的文案。
func textFor(category string, lang domain.Lang) ruleText {
	if lang == domain.LangEn {
		return enText[category]
	}
	return zhText[category]
}

var zhText = map[string]ruleText{
	"Database / Timeout": {
		summary: "请求在等待数据库连接时超出了配置的超时时间。",
		titles:  "数据库连接超时",
		causes: []string{
			"数据库连接池已耗尽",
			"慢查询长期占用连接",
			"连接泄漏，未归还连接池",
			"服务与数据库之间的网络延迟",
		},
		actions: []string{
			"检查连接池的使用率",
			"排查慢查询",
			"确认连接在使用后是否被释放",
		},
	},
	"Timeout": {
		summary: "某个操作超出了配置的超时时间。",
		titles:  "操作超时",
		causes: []string{
			"下游依赖比预期慢",
			"超时阈值设置过低",
			"网络延迟",
		},
		actions: []string{
			"定位变慢的依赖",
			"对比当前耗时与配置的预算",
		},
	},
	"Concurrency / Nil": {
		summary: "进程在处理请求时发生了 panic。",
		titles:  "进程 panic",
		causes: []string{
			"空指针解引用",
			"依赖未初始化",
			"并发读写 map",
		},
		actions: []string{
			"从堆栈中找到 panic 发生的位置",
			"为触发问题的输入补一个回归测试",
		},
	},
	"Network / DNS": {
		summary: "域名无法解析。",
		titles:  "DNS 解析失败",
		causes: []string{
			"DNS 解析失败",
			"服务名配置错误",
			"解析服务不可达",
		},
		actions: []string{
			"确认域名是否正确",
			"检查 DNS 解析服务的健康状态",
		},
	},
	"Auth": {
		summary: "鉴权或授权校验失败。",
		titles:  "鉴权失败",
		causes: []string{
			"凭据已过期",
			"调用方缺少所需权限",
			"时钟偏移导致令牌失效",
		},
		actions: []string{
			"检查凭据的有效期",
			"确认调用方具备所需的权限范围",
		},
	},
	"Resource / Memory": {
		summary: "进程内存不足。",
		titles:  "内存不足",
		causes: []string{
			"内存泄漏",
			"工作集超过容器上限",
			"缓冲区无界增长",
		},
		actions: []string{
			"查看内存使用趋势",
			"对比配置上限与实际工作集",
		},
	},
}

var enText = map[string]ruleText{
	"Database / Timeout": {
		summary: "The request exceeded its deadline while waiting on the database.",
		titles:  "Database timeout",
		causes: []string{
			"Database connection pool exhausted",
			"Slow query holding connections",
			"Connection leak",
			"Network latency between service and database",
		},
		actions: []string{
			"Inspect connection pool utilization",
			"Check for slow queries",
			"Verify connections are released back to the pool",
		},
	},
	"Timeout": {
		summary: "An operation exceeded its configured timeout.",
		titles:  "Operation timeout",
		causes: []string{
			"Downstream dependency slower than expected",
			"Timeout threshold set too low",
			"Network latency",
		},
		actions: []string{
			"Identify the slow dependency",
			"Compare current latency with the configured budget",
		},
	},
	"Concurrency / Nil": {
		summary: "The process panicked while handling a request.",
		titles:  "Process panic",
		causes: []string{
			"Nil pointer dereference",
			"Uninitialized dependency",
			"Concurrent map access",
		},
		actions: []string{
			"Locate the panic site in the stack trace",
			"Add a regression test for the failing input",
		},
	},
	"Network / DNS": {
		summary: "A hostname could not be resolved.",
		titles:  "DNS resolution failure",
		causes: []string{
			"DNS resolution failure",
			"Misconfigured service name",
			"Resolver unreachable",
		},
		actions: []string{
			"Verify the hostname is correct",
			"Check DNS resolver health",
		},
	},
	"Auth": {
		summary: "An authentication or authorization check failed.",
		titles:  "Authentication failure",
		causes: []string{
			"Expired credentials",
			"Missing permission on the caller",
			"Clock skew invalidating tokens",
		},
		actions: []string{
			"Check credential expiry",
			"Verify the caller has the required scope",
		},
	},
	"Resource / Memory": {
		summary: "The process ran out of memory.",
		titles:  "Out of memory",
		causes: []string{
			"Memory leak",
			"Working set larger than the container limit",
			"Unbounded buffer growth",
		},
		actions: []string{
			"Check memory usage trend",
			"Compare the limit with the actual working set",
		},
	},
}

// unknownText 是无法归类时的文案。
func unknownText(lang domain.Lang) ruleText {
	if lang == domain.LangEn {
		return ruleText{
			summary: "The log does not contain enough information to identify a likely cause.",
			titles:  "Unclassified error",
			causes:  []string{"Insufficient detail in the submitted log"},
			actions: []string{"Include the full stack trace and surrounding context"},
		}
	}
	return ruleText{
		summary: "日志中的信息不足以判断可能的原因。",
		titles:  "无法归类的错误",
		causes:  []string{"提交的日志缺少足够细节"},
		actions: []string{"补充完整的堆栈和上下文信息"},
	}
}
