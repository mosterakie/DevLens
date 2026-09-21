// Package service 承担业务编排：事务边界、状态机转移、跨组件协作。
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/mosterakie/DevLens/internal/domain"
	"github.com/mosterakie/DevLens/internal/fingerprint"
	"github.com/mosterakie/DevLens/internal/redact"
	"github.com/mosterakie/DevLens/internal/repository"
)

// ErrInvalidTransition 表示状态机拒绝了这次转移。
var ErrInvalidTransition = errors.New("invalid status transition")

// Analyzer 是异步分析任务的入队接口。
//
// 定义成接口而不是直接依赖 queue 包，是为了让 service 的测试
// 可以注入假实现，不需要真的跑 Redis。
type Analyzer interface {
	EnqueueAnalyze(ctx context.Context, incidentID int64) error
}

// IncidentRepo 是 service 需要的数据访问能力。
type IncidentRepo interface {
	Create(ctx context.Context, in repository.CreateIncidentInput) (*domain.Incident, error)
	GetByID(ctx context.Context, id int64) (*domain.Incident, error)
	CountByFingerprint(ctx context.Context, fp []byte) (int, error)
	FindRelated(ctx context.Context, fp []byte, excludeID int64, limit int) ([]repository.RelatedIncident, error)
	FindSimilarCandidates(ctx context.Context, excludeID int64, limit int) ([]repository.SimilarCandidate, error)
	SetRecurring(ctx context.Context, id int64, recurring bool) error
	List(ctx context.Context, f repository.ListFilter) ([]*domain.Incident, error)
	UpdateStatus(ctx context.Context, id int64, to domain.Status) error
	ListEvents(ctx context.Context, incidentID int64) ([]repository.Event, error)
}

// DemoUserID 是免登录 Demo 使用的固定用户，与迁移里 seed 的那一行对应。
// 用指针是因为 incidents.created_by 允许 NULL。
func DemoUserID() *int64 {
	id := int64(1)
	return &id
}

// IncidentService 是 Incident 相关的业务入口。
type IncidentService struct {
	repo         IncidentRepo
	queue        Analyzer
	relatedLimit int
}

// NewIncidentService 构造服务。
func NewIncidentService(repo IncidentRepo, q Analyzer) *IncidentService {
	return &IncidentService{repo: repo, queue: q, relatedLimit: fingerprint.RelatedLimit}
}

// findRelated 合并"精确指纹"与"相似度"两路，返回最终的相关历史列表。
//
// 这是 service 与 worker 共用的唯一入口 —— 合并规则只实现一次，
// 否则 worker 喂给模型的历史会与用户看到的不一致。
//
// 两路的角色不同：
//   - 精确命中走 fingerprint 索引，是主路，结果确定，不受候选行数上限影响。
//   - 相似命中是补充召回，靠 Go 侧算词集 Jaccard，受粗召回行数上限约束。
//
// normalized 必须是当前记录的归一化文本；similar 一路要以它为基准比较。
func (s *IncidentService) findRelated(ctx context.Context, normalized string, fp []byte, excludeID int64) ([]repository.RelatedIncident, error) {
	exact, err := s.repo.FindRelated(ctx, fp, excludeID, s.relatedLimit)
	if err != nil {
		return nil, fmt.Errorf("find related: %w", err)
	}

	similar, err := s.similarCandidates(ctx, normalized, excludeID)
	if err != nil {
		// 相似召回是补充信号，失败不应该让整个请求失败 ——
		// 精确匹配已经拿到了"确定同类"，那是用户最需要的信息。
		// 这里降级为只用精确结果，而不是把 500 抛给用户。
		return toRelatedIncidents(exact), nil
	}

	return mergeRelated(exact, similar, s.relatedLimit), nil
}

// similarCandidates 粗召回候选并算出相似度达标的那些。
//
// excludeID 排除调用方已知的自己。这里**不**额外按文本排除记录：
// 与当前记录归一化完全相同的记录会以 Jaccard = 1.0 进入这一路，
// 但它们必然也命中精确路，最终由 fingerprint.Merge 去重时保留 exact、
// 丢弃 similar（Merge 先处理精确路）。所以重复提交既不会漏、也不会
// 在结果里出现两次，去重的责任集中在 Merge 一处，而不是分散到两条路。
func (s *IncidentService) similarCandidates(ctx context.Context, normalized string, excludeID int64) ([]repository.RelatedIncident, error) {
	cands, err := s.repo.FindSimilarCandidates(ctx, excludeID, repository.SimilarRecallLimit)
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, nil
	}

	base := fingerprint.Tokens(normalized)
	out := make([]repository.RelatedIncident, 0, len(cands))
	for _, c := range cands {
		score := fingerprint.Jaccard(base, fingerprint.Tokens(c.Normalized))
		if score < fingerprint.SimilarityThreshold {
			continue
		}
		item := c.RelatedIncident
		item.Match = repository.MatchSimilar
		item.Score = score
		out = append(out, item)
	}
	return out, nil
}

// mergeRelated 去重、精确优先、按 created_at DESC 排序并截断。
//
// 去重与排序的实现在 fingerprint.Merge 里，这里只做仓储类型与
// 合并类型之间的映射。
func mergeRelated(exact, similar []repository.RelatedIncident, limit int) []repository.RelatedIncident {
	exactC := make([]fingerprint.Candidate, 0, len(exact))
	for _, r := range exact {
		exactC = append(exactC, toCandidate(r, fingerprint.MatchExact))
	}
	similarC := make([]fingerprint.Candidate, 0, len(similar))
	for _, r := range similar {
		similarC = append(similarC, toCandidate(r, fingerprint.MatchSimilar))
	}

	// 用 ID -> 原记录查回标题等内容，避免在 Candidate 里重复一份。
	// 同时命中两路的 ID 保留精确那条的 Score（Candidate.Score 为 0）。
	byID := make(map[int64]repository.RelatedIncident, len(exact)+len(similar))
	for _, r := range exact {
		byID[r.ID] = r
	}
	for _, r := range similar {
		if _, ok := byID[r.ID]; !ok {
			byID[r.ID] = r
		}
	}

	merged := fingerprint.Merge(exactC, similarC, limit)
	out := make([]repository.RelatedIncident, 0, len(merged))
	for _, c := range merged {
		item := byID[c.ID]
		item.Match = string(c.Match)
		// 精确命中的得分没有意义，统一清零，避免 UI 或日志误读。
		if c.Match == fingerprint.MatchExact {
			item.Score = 0
		}
		out = append(out, item)
	}
	return out
}

func toCandidate(r repository.RelatedIncident, m fingerprint.Match) fingerprint.Candidate {
	return fingerprint.Candidate{
		ID:        r.ID,
		Title:     r.Title,
		CreatedAt: r.CreatedAt.UnixNano(),
		Match:     m,
		Score:     r.Score,
	}
}

// toRelatedIncidents 是 []repository.RelatedIncident 的浅拷贝，
// 用于相似召回失败时的降级路径，避免把仓储返回的切片直接交出去。
func toRelatedIncidents(in []repository.RelatedIncident) []repository.RelatedIncident {
	out := make([]repository.RelatedIncident, len(in))
	copy(out, in)
	return out
}

// AnalyzeResult 是提交分析后的即时返回。
//
// 只有指纹匹配是同步完成的，AI 诊断在后台进行，
// 所以这里能立刻给出 related，但拿不到 analysis。
type AnalyzeResult struct {
	Incident  *domain.Incident
	Related   []repository.RelatedIncident
	Recurring bool
}

// Submit 校验日志、计算指纹、查找同类并入库，最后把分析任务入队。
//
// 顺序上先算指纹再入库很重要：等 AI 分析完再找同类的话，
// 用户要等几十秒才能看到"这问题以前出现过"。指纹是纯计算，
// 所以能放在同步路径上。
func (s *IncidentService) Submit(ctx context.Context, rawLog string) (*AnalyzeResult, error) {
	return s.SubmitLang(ctx, rawLog, domain.DefaultLang())
}

// SubmitLang 与 Submit 相同，但显式指定诊断语言。
func (s *IncidentService) SubmitLang(ctx context.Context, rawLog string, lang domain.Lang) (*AnalyzeResult, error) {
	if !lang.IsValid() {
		lang = domain.DefaultLang()
	}

	// 长度校验在脱敏之前：按用户提交的原始大小判断。
	// 脱敏通常让文本变短，按脱敏后判断等于悄悄放宽了限制。
	if err := domain.ValidateLog(rawLog); err != nil {
		return nil, err
	}

	// 脱敏在指纹计算之前。日志会发给外部模型，一旦发出去就收不回来，
	// 所以凭据必须在离开服务之前去掉；存库的也是脱敏后的版本。
	//
	// 顺序很关键：指纹基于脱敏后的文本。好处是 token 不同的同一类
	// 问题会归到一起（都变成 token=***），符合"同类判定"的语义。
	rawLog = redact.Apply(rawLog)

	normalized := fingerprint.Normalize(rawLog)
	fp := fingerprint.Compute(normalized)

	// 先查历史，用于判断是否重复出现。此时库里还没有本条记录，
	// 所以不需要排除自己。
	count, err := s.repo.CountByFingerprint(ctx, fp)
	if err != nil {
		return nil, fmt.Errorf("count by fingerprint: %w", err)
	}
	exactRecurring := count > 0

	inc, err := s.repo.Create(ctx, repository.CreateIncidentInput{
		RawLog:      rawLog,
		Normalized:  normalized,
		Fingerprint: fp,
		CreatedBy:   DemoUserID(),
		Lang:        lang,
	})
	if err != nil {
		return nil, fmt.Errorf("create incident: %w", err)
	}

	// 查找同类时排除自己。
	related, err := s.findRelated(ctx, normalized, fp, inc.ID)
	if err != nil {
		return nil, err
	}

	// recurring 判定：精确命中 或 相似度命中 都算"重复出现过"。
	//
	// 与 v1 的语义差异必须记在这里：v1 只有精确指纹命中（count > 0）
	// 才置 is_recurring=true，措辞不同但同类的第二条日志会被当成新问题。
	// 改造后相似召回也计入，于是 is_recurring 的含义从"指纹完全相同的
	// 记录已存在"放宽为"极可能有同类历史已存在"。
	//
	// 影响面：这个标记会直接呈现给用户（列表与详情页的 RECURRING 徽标），
	// 也会进入 analyzer 的输入。放宽后可能出现"标了 RECURRING 但 related
	// 里只有 similar 项"的组合 —— 这是刻意的：徽标说的是"可能不是新问题"，
	// 而 related 里的 match 字段会如实告诉用户哪条是确定的、哪条是推测的。
	recurring := exactRecurring || len(related) > 0

	// 落库的 is_recurring 与返回给调用方的值必须一致。
	// v1 只在精确命中时写库，这里改成两者都写。
	if recurring {
		if err := s.repo.SetRecurring(ctx, inc.ID, true); err != nil {
			return nil, fmt.Errorf("mark recurring: %w", err)
		}
		inc.IsRecurring = true
	}

	// 入队失败不回滚已经建好的 Incident：记录本身是有价值的，
	// 而且 worker 启动时会扫描超时任务补入队。
	// 这里把错误返回给调用方，由 handler 决定如何呈现。
	if err := s.queue.EnqueueAnalyze(ctx, inc.ID); err != nil {
		return &AnalyzeResult{Incident: inc, Related: related, Recurring: recurring},
			fmt.Errorf("enqueue analyze: %w", err)
	}

	return &AnalyzeResult{Incident: inc, Related: related, Recurring: recurring}, nil
}

// Get 读取 Incident。
func (s *IncidentService) Get(ctx context.Context, id int64) (*domain.Incident, error) {
	return s.repo.GetByID(ctx, id)
}

// Related 读取同类问题。
//
// normalized 用于相似度路；调用方拿不到它时可传空串，
// 此时相似度路不会命中（空词集返回 0），等价于只用精确匹配。
func (s *IncidentService) Related(ctx context.Context, normalized string, fp []byte, excludeID int64) ([]repository.RelatedIncident, error) {
	return s.findRelated(ctx, normalized, fp, excludeID)
}

// RelatedByIncidentID 按 Incident ID 查找同类问题。
//
// 调用方通常只知道 ID，不知道指纹，所以这里先取记录再匹配。
// 归一化文本与指纹都从记录里取，保证相似度是拿同一套归一化结果算的。
// 找不到记录时返回 repository.ErrNotFound，由 handler 映射成 404。
func (s *IncidentService) RelatedByIncidentID(ctx context.Context, id int64) ([]repository.RelatedIncident, error) {
	inc, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.findRelated(ctx, inc.Normalized, inc.Fingerprint, inc.ID)
}

// List 读取列表。
func (s *IncidentService) List(ctx context.Context, f repository.ListFilter) ([]*domain.Incident, error) {
	return s.repo.List(ctx, f)
}

// Events 读取 Timeline。
func (s *IncidentService) Events(ctx context.Context, id int64) ([]repository.Event, error) {
	return s.repo.ListEvents(ctx, id)
}

// ChangeStatus 校验并执行状态转移。
//
// 校验放在 service 而不是 repository，是因为转移规则属于领域知识，
// 而仓储只该关心怎么存取。
func (s *IncidentService) ChangeStatus(ctx context.Context, id int64, to domain.Status) (*domain.Incident, error) {
	if !to.IsValid() {
		return nil, fmt.Errorf("%w: unknown target status %q", ErrInvalidTransition, to)
	}

	inc, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if !inc.Status.CanTransitionTo(to) {
		return nil, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, inc.Status, to)
	}

	if err := s.repo.UpdateStatus(ctx, id, to); err != nil {
		return nil, err
	}

	// 重新读取以拿到 updated_at 等最新字段。
	return s.repo.GetByID(ctx, id)
}
