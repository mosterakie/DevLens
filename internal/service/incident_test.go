package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mosterakie/DevLens/internal/domain"
	"github.com/mosterakie/DevLens/internal/repository"
)

// --- 假实现 ---

type fakeRepo struct {
	incidents []*domain.Incident
	events    map[int64][]repository.Event

	nextID       int64
	createErr    error
	countErr     error
	similarErr   error
	enqueueNever bool
	statusErr    error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{nextID: 1, events: map[int64][]repository.Event{}}
}

func (f *fakeRepo) Create(_ context.Context, in repository.CreateIncidentInput) (*domain.Incident, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	// 字段要与真实仓储一致：漏掉 Lang 之类的字段会让依赖它的
	// 断言在测假实现而不是生产代码。
	lang := in.Lang
	if !lang.IsValid() {
		lang = domain.DefaultLang()
	}
	inc := &domain.Incident{
		ID:          f.nextID,
		RawLog:      in.RawLog,
		Normalized:  in.Normalized,
		Fingerprint: in.Fingerprint,
		Status:      domain.StatusAnalyzing,
		Lang:        lang,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	f.nextID++
	f.incidents = append(f.incidents, inc)
	return inc, nil
}

func (f *fakeRepo) GetByID(_ context.Context, id int64) (*domain.Incident, error) {
	for _, inc := range f.incidents {
		if inc.ID == id {
			return inc, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (f *fakeRepo) CountByFingerprint(_ context.Context, fp []byte) (int, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	n := 0
	for _, inc := range f.incidents {
		if string(inc.Fingerprint) == string(fp) {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) FindRelated(_ context.Context, fp []byte, excludeID int64, limit int) ([]repository.RelatedIncident, error) {
	var out []repository.RelatedIncident
	for _, inc := range f.incidents {
		// FAILED 的过滤在真实仓储的 SQL 里（status <> 'FAILED'）。
		// 假实现必须同样过滤，否则会测出一个生产环境不存在的"缺陷"，
		// 或者掩盖一个真实缺陷。
		if inc.ID == excludeID || string(inc.Fingerprint) != string(fp) ||
			inc.Status == domain.StatusFailed {
			continue
		}
		out = append(out, repository.RelatedIncident{
			ID: inc.ID, Status: inc.Status, CreatedAt: inc.CreatedAt,
			Match: repository.MatchExact,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// FindSimilarCandidates 模拟粗召回：返回除自己以外的全部未失败记录。
//
// 真实仓储会按 created_at DESC 截断到 SimilarRecallLimit，
// 假实现里的记录数远小于该上限，所以顺序对断言没有影响。
//
// 这里不做相似度过滤 —— 过滤是 service 的职责（Go 侧算 Jaccard），
// 假实现如果自己过滤一遍，就把被测逻辑抄了一份，
// 会让"相似度算错了"这类缺陷测不出来。
func (f *fakeRepo) FindSimilarCandidates(_ context.Context, excludeID int64, limit int) ([]repository.SimilarCandidate, error) {
	if f.similarErr != nil {
		return nil, f.similarErr
	}
	var out []repository.SimilarCandidate
	for _, inc := range f.incidents {
		if inc.ID == excludeID || inc.Status == domain.StatusFailed {
			continue
		}
		out = append(out, repository.SimilarCandidate{
			RelatedIncident: repository.RelatedIncident{
				ID: inc.ID, Status: inc.Status, CreatedAt: inc.CreatedAt,
			},
			Normalized: inc.Normalized,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeRepo) SetRecurring(_ context.Context, id int64, recurring bool) error {
	for _, inc := range f.incidents {
		if inc.ID == id {
			inc.IsRecurring = recurring
			return nil
		}
	}
	return repository.ErrNotFound
}

func (f *fakeRepo) List(_ context.Context, _ repository.ListFilter) ([]*domain.Incident, error) {
	return f.incidents, nil
}

func (f *fakeRepo) UpdateStatus(_ context.Context, id int64, to domain.Status) error {
	if f.statusErr != nil {
		return f.statusErr
	}
	for _, inc := range f.incidents {
		if inc.ID == id {
			inc.Status = to
			return nil
		}
	}
	return repository.ErrNotFound
}

func (f *fakeRepo) ListEvents(_ context.Context, id int64) ([]repository.Event, error) {
	return f.events[id], nil
}

type fakeQueue struct {
	enqueued []int64
	err      error
}

func (q *fakeQueue) EnqueueAnalyze(_ context.Context, id int64) error {
	if q.err != nil {
		return q.err
	}
	q.enqueued = append(q.enqueued, id)
	return nil
}

// --- 测试 ---

func validLog() string {
	return strings.Repeat("e", domain.MinLogBytes+10)
}

func TestSubmitRejectsInvalidLog(t *testing.T) {
	tests := []struct {
		name    string
		log     string
		wantErr error
	}{
		{"空", "", domain.ErrLogEmpty},
		{"纯空白", "    \n  ", domain.ErrLogEmpty},
		{"过短", "short", domain.ErrLogTooShort},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeRepo()
			svc := NewIncidentService(repo, &fakeQueue{})

			_, err := svc.Submit(context.Background(), tt.log)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if len(repo.incidents) != 0 {
				t.Error("no incident should be created for an invalid log")
			}
		})
	}
}

func TestSubmitCreatesAndEnqueues(t *testing.T) {
	repo := newFakeRepo()
	q := &fakeQueue{}
	svc := NewIncidentService(repo, q)

	res, err := svc.Submit(context.Background(), validLog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Incident == nil {
		t.Fatal("expected an incident")
	}
	if res.Incident.Status != domain.StatusAnalyzing {
		t.Errorf("Status = %q, want ANALYZING", res.Incident.Status)
	}
	if res.Recurring {
		t.Error("first submission should not be recurring")
	}
	if len(q.enqueued) != 1 || q.enqueued[0] != res.Incident.ID {
		t.Errorf("expected one enqueue for id %d, got %v", res.Incident.ID, q.enqueued)
	}
	if len(res.Incident.Fingerprint) != 16 {
		t.Errorf("fingerprint length = %d, want 16", len(res.Incident.Fingerprint))
	}
}

// TestSubmitMarksRecurringOnSecondSubmission 是 M2 的核心验收点：
// 同一段日志提交两次，第二次应被识别为重复出现。
func TestSubmitMarksRecurringOnSecondSubmission(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	first, err := svc.Submit(ctx, validLog())
	if err != nil {
		t.Fatal(err)
	}
	if first.Recurring {
		t.Fatal("first submission should not be recurring")
	}

	second, err := svc.Submit(ctx, validLog())
	if err != nil {
		t.Fatal(err)
	}
	if !second.Recurring {
		t.Error("second submission of the same log should be recurring")
	}
	if !second.Incident.IsRecurring {
		t.Error("incident should be flagged as recurring")
	}
	if len(second.Related) != 1 {
		t.Fatalf("expected 1 related incident, got %d", len(second.Related))
	}
	if second.Related[0].ID != first.Incident.ID {
		t.Errorf("related id = %d, want %d", second.Related[0].ID, first.Incident.ID)
	}
}

// TestSubmitExcludesSelfFromRelated 确认刚创建的记录不会匹配到自己。
func TestSubmitExcludesSelfFromRelated(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})

	res, err := svc.Submit(context.Background(), validLog())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Related {
		if r.ID == res.Incident.ID {
			t.Fatalf("incident %d should not be related to itself", res.Incident.ID)
		}
	}
	if len(res.Related) != 0 {
		t.Errorf("expected no related incidents, got %d", len(res.Related))
	}
}

// TestSubmitKeepsIncidentWhenEnqueueFails 确认入队失败时记录仍被保留：
// 记录本身有价值，且 worker 启动时会扫描补入队。
func TestSubmitKeepsIncidentWhenEnqueueFails(t *testing.T) {
	repo := newFakeRepo()
	q := &fakeQueue{err: errors.New("redis down")}
	svc := NewIncidentService(repo, q)

	res, err := svc.Submit(context.Background(), validLog())
	if err == nil {
		t.Fatal("expected an error from enqueue")
	}
	if res == nil || res.Incident == nil {
		t.Fatal("incident should still be returned so the caller can surface it")
	}
	if len(repo.incidents) != 1 {
		t.Errorf("incident should be persisted, got %d records", len(repo.incidents))
	}
}

func TestChangeStatusValidatesTransition(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	res, err := svc.Submit(ctx, validLog())
	if err != nil {
		t.Fatal(err)
	}
	id := res.Incident.ID

	// ANALYZING -> OPEN 合法。
	if _, err := svc.ChangeStatus(ctx, id, domain.StatusOpen); err != nil {
		t.Fatalf("ANALYZING -> OPEN should be allowed: %v", err)
	}
	// OPEN -> RESOLVED 非法，必须先经过 INVESTIGATING。
	if _, err := svc.ChangeStatus(ctx, id, domain.StatusResolved); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("OPEN -> RESOLVED should be rejected, got %v", err)
	}
	// OPEN -> INVESTIGATING 合法。
	if _, err := svc.ChangeStatus(ctx, id, domain.StatusInvestigating); err != nil {
		t.Fatalf("OPEN -> INVESTIGATING should be allowed: %v", err)
	}
	// INVESTIGATING -> RESOLVED 合法。
	if _, err := svc.ChangeStatus(ctx, id, domain.StatusResolved); err != nil {
		t.Fatalf("INVESTIGATING -> RESOLVED should be allowed: %v", err)
	}
	// RESOLVED 是终态。
	if _, err := svc.ChangeStatus(ctx, id, domain.StatusOpen); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("RESOLVED is terminal, got %v", err)
	}
}

func TestChangeStatusRejectsUnknownStatus(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	res, err := svc.Submit(context.Background(), validLog())
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.ChangeStatus(context.Background(), res.Incident.ID, domain.Status("BOGUS"))
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("unknown status should be rejected, got %v", err)
	}
}

func TestChangeStatusMissingIncident(t *testing.T) {
	svc := NewIncidentService(newFakeRepo(), &fakeQueue{})
	_, err := svc.ChangeStatus(context.Background(), 999, domain.StatusOpen)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
