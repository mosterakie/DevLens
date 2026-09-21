package fingerprint

import "sort"

// RelatedLimit 是"同类问题"结果的整体条数上限。
//
// 上限 5 是沿用的产品约束（05-related-incidents.md：UI 上超过 5 条
// 没有意义）。精确命中与相似命中合并之后共用这一个上限，
// 不是各 5 条 —— 否则混合召回会让列表长度翻倍。
const RelatedLimit = 5

// Match 说明一条相关历史是怎么被找出来的。
type Match string

const (
	// MatchExact 是精确指纹命中，语义为"确定同类"。
	MatchExact Match = "exact"
	// MatchSimilar 是词集相似度命中，语义为"可能同类"。
	MatchSimilar Match = "similar"
)

// Candidate 是参与合并的一条相关历史候选。
//
// 这里用本包自己的类型而不是 repository.RelatedIncident，是为了让
// fingerprint 保持叶子包（不 import 任何本项目内的包）。合并规则属于
// 相似度语义的一部分，由本包定义，调用方负责把仓储的返回值映射进来。
type Candidate struct {
	ID        int64
	Title     string
	CreatedAt int64 // UnixNano，仅用于排序
	Match     Match
	Score     float64
}

// Merge 合并"精确指纹命中"与"相似度命中"两路结果。
//
// 规则（与设计说明一致）：
//
//  1. 去重：同一个 ID 只保留一次。
//  2. 精确优先：同一个 ID 两路都命中时保留 MatchExact。
//     这不只是排序偏好 —— 精确命中是确定同类，把它降级成"可能同类"
//     会让 UI 少给用户一个确定的信号，属于信息损失。
//  3. 排序：created_at DESC。最新的在前，与仓库层单路查询的排序一致。
//  4. 截断：整体 limit 条。
//
// limit <= 0 时使用 RelatedLimit。
//
// 确定性：排序键相同时用 ID 兜底，保证同一组输入永远得到同一个顺序。
// 只按 CreatedAt 排序时，时间戳相同的两条记录顺序取决于 sort 的实现
// 细节（sort.Slice 不稳定），会让接口输出在两次调用间抖动。
func Merge(exact, similar []Candidate, limit int) []Candidate {
	if limit <= 0 || limit > RelatedLimit {
		limit = RelatedLimit
	}

	seen := make(map[int64]int, len(exact)+len(similar))
	out := make([]Candidate, 0, len(exact)+len(similar))

	add := func(c Candidate) {
		if idx, ok := seen[c.ID]; ok {
			// 已存在：只在"已有的是相似、新来的是精确"时升级。
			// 反向（精确被相似覆盖）必须禁止，否则精确信号会丢。
			if out[idx].Match == MatchSimilar && c.Match == MatchExact {
				out[idx].Match = MatchExact
				out[idx].Score = 0
			}
			return
		}
		seen[c.ID] = len(out)
		out = append(out, c)
	}

	// 先放精确命中，再放相似命中。顺序不影响最终排序，
	// 但让"精确优先"在去重阶段就天然成立。
	for _, c := range exact {
		c.Match = MatchExact
		add(c)
	}
	for _, c := range similar {
		c.Match = MatchSimilar
		add(c)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})

	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
