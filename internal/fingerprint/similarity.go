package fingerprint

import "strings"

// SimilarityThreshold 是判定"可能同类"的词集相似度下限。
//
// 取值 0.5 的依据是实测分离度，而不是拍脑袋：
//
//	同类（措辞不同，改 IP/环境名/retrying 换 backing off）: Jaccard ≈ 0.50 - 0.78
//	异类（数据库连接失败 vs panic 日志）                     : Jaccard ≈ 0.00 - 0.05
//
// 两类之间几乎是空的，0.5 落在中间，离两侧都有余量。
//
// 定得太低会把只是共享 "error" / "failed" / "database" 这类高频词
// 的不相关日志召回进来 —— 用户看到"错误的相关历史"才会失去信任，
// 这正是 05-related-incidents.md 强调要避免的。定得太高则同类也召不回，
// 改造就失去意义。
//
// 已知局限：Jaccard 对**长度不对称**敏感。同一种故障，一条日志 25 个词、
// 另一条 43 个词（多出十几行重复的 heartbeat/processor 噪声行）时，
// 分数会被分母稀释到 0.35 左右而漏召回。实测真实库中的对照：
// 4 行摘录对（词数 25/27）得 0.625 可命中；把其中一条换成 18 行的完整
// 记录（词数 27/43）只剩 0.346 就漏掉了。
//
// 不为此下调阈值：实测全库 171 个配对的分布里，>= 0.5 恰好落在
// 真正相关的簇上（见 05-related-incidents.md 的取舍——确定性优先于召回率），
// 而 0.3-0.5 区间里混着 5 对只是共享 panic/goroutine 字样的记录。
// 要解决长度不对称，正确做法是换相似度度量（如 Dice 系数、或对长日志
// 做去重/分块），而不是把阈值调低到会引入误召回的位置。
// 这属于 v2 的范围。
const SimilarityThreshold = 0.5

// minTokenLen 是参与相似度计算的 token 最小长度。
//
// 忽略长度 < 3 的 token，理由：
//
//  1. 归一化后残留大量 `= ` `: ` `[` `+` `<n>` 之类的碎片（例如
//     `error=eof`、`error="dial ...`、`<ts> [erro]`），它们几乎出现在
//     每条日志里，对区分贡献为零，却会同时抬高分子和分母 —— 结果是
//     把两条毫不相干的日志的相似度抬到阈值之上。
//  2. 真正的判别信息来自有意义的单词（database / deadline / exceeded /
//     refused / panic / nil），它们都 >= 3 个字符。
//  3. Go 的包路径、函数名、状态码在归一化后也是长 token，不受影响。
//
// 这是一个明确的取舍：宁可漏掉 2 字符的短标识符，也不要为了它们
// 引入误召回。需要区分 "IO" 这类短词时应当改归一化规则，
// 而不是把阈值调低。
const minTokenLen = 3

// tokenSplitter 用空格切词。
//
// Normalize 的最后一步已经把连续空白折叠成单个空格并 trim，
// 所以按空格切是安全的；这里不用正则，热路径上能省一次扫描。
//
// 只切空格而不是保留标点：标点本身没有判别力，且归一化已经把
// 有判别力的结构（时间戳/IP/端口/行号）换成了占位符。
const tokenSplitter = " "

// Tokens 把归一化后的日志切成去重的词集。
//
// 输入是 Normalize 的输出，而不是原始日志 —— 相似度的语义必须建立在
// 与指纹相同的归一化之上，否则同一对日志会因为计算入口不同而不同。
// 传原始日志也能跑，但时间戳和 IP 会各算一个 token，结果没有意义。
//
// 返回 map 而不是切片：Jaccard 是集合运算，用集合表达最自然，
// 也天然去重（同一条日志里 `database` 出现五次只该算一个）。
//
// 必须是确定性的：同一个输入永远得到同一个集合内容。
// 出参是 map，Go 的 map 迭代顺序随机，所以调用方不能用遍历顺序
// 做任何判断 —— 本包只把它喂给 Jaccard，而 Jaccard 只做集合交并，
// 与顺序无关。
func Tokens(normalized string) map[string]struct{} {
	out := make(map[string]struct{})

	// 空串走 normal 路径即可，不需要特判：Split 返回单个空串，
	// 下面长度检查会把它丢掉，得到空集。
	for _, t := range strings.Split(normalized, tokenSplitter) {
		if len(t) < minTokenLen {
			continue
		}
		out[t] = struct{}{}
	}
	return out
}

// Jaccard 计算两个词集的 Jaccard 相似度，取值 [0, 1]。
//
// J = |A ∩ B| / |A ∪ B|
//
// 两个空集的定义：返回 0 而不是 1。
// 数学上两个空集的 Jaccard 是未定义的（0/0），而 1 表示"完全一致"，
// 会让两条都归一化成空串的日志被判为同类 —— 这正是最危险的误召回。
// 返回 0 表示"没有相似度证据"，与调用方的语义一致。
//
// 结果是确定的：只做集合交并，不依赖 map 迭代顺序。
// 计算是只读的，不会修改入参。
func Jaccard(a, b map[string]struct{}) float64 {
	// 空集早返回：既避免除零，也省掉无意义的遍历。
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	// 从小的那个遍历，交集计数与遍历方向无关，但能少走几步。
	small, large := a, b
	if len(small) > len(large) {
		small, large = large, small
	}

	inter := 0
	for t := range small {
		if _, ok := large[t]; ok {
			inter++
		}
	}

	union := len(a) + len(b) - inter
	if union == 0 {
		// 到这里只可能是两个集合都为空，而上面已经返回 0。
		// 留作防御：宁可返回 0（不判定同类）也不要 panic。
		return 0
	}

	return float64(inter) / float64(union)
}
