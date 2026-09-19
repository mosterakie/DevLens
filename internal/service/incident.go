// Package service 承担业务编排：事务边界、状态机转移、跨组件协作。
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/mosterakie/DevLens/internal/domain"
	"github.com/mosterakie/DevLens/internal/fingerprint"
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
	return &IncidentService{repo: repo, queue: q, relatedLimit: 5}
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
	if err := domain.ValidateLog(rawLog); err != nil {
		return nil, err
	}

	normalized := fingerprint.Normalize(rawLog)
	fp := fingerprint.Compute(normalized)

	// 先查历史，用于判断是否重复出现。此时库里还没有本条记录，
	// 所以不需要排除自己。
	count, err := s.repo.CountByFingerprint(ctx, fp)
	if err != nil {
		return nil, fmt.Errorf("count by fingerprint: %w", err)
	}
	recurring := count > 0

	inc, err := s.repo.Create(ctx, repository.CreateIncidentInput{
		RawLog:      rawLog,
		Normalized:  normalized,
		Fingerprint: fp,
		CreatedBy:   DemoUserID(),
	})
	if err != nil {
		return nil, fmt.Errorf("create incident: %w", err)
	}

	if recurring {
		if err := s.repo.SetRecurring(ctx, inc.ID, true); err != nil {
			return nil, fmt.Errorf("mark recurring: %w", err)
		}
		inc.IsRecurring = true
	}

	// 查找同类时排除自己。
	related, err := s.repo.FindRelated(ctx, fp, inc.ID, s.relatedLimit)
	if err != nil {
		return nil, fmt.Errorf("find related: %w", err)
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
func (s *IncidentService) Related(ctx context.Context, fp []byte, excludeID int64) ([]repository.RelatedIncident, error) {
	return s.repo.FindRelated(ctx, fp, excludeID, s.relatedLimit)
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
