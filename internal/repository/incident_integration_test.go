package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/mosterakie/DevLens/internal/domain"
)

func TestCreatePersistsAllFields(t *testing.T) {
	db := setupDB(t)

	inc := mustCreate(t, db, "原始日志内容", "归一化后的内容", fp(0xAB))

	if inc.ID != 1 {
		t.Errorf("ID = %d, want 1（TRUNCATE 后应重新从 1 开始）", inc.ID)
	}
	if inc.RawLog != "原始日志内容" {
		t.Errorf("RawLog 未正确回读: %q", inc.RawLog)
	}
	if inc.Normalized != "归一化后的内容" {
		t.Errorf("Normalized 未正确回读: %q", inc.Normalized)
	}
	if string(inc.Fingerprint) != string(fp(0xAB)) {
		t.Errorf("Fingerprint 未正确回读: %x", inc.Fingerprint)
	}
	if inc.Status != domain.StatusAnalyzing {
		t.Errorf("新建状态应为 ANALYZING, got %q", inc.Status)
	}
	if inc.Lang != domain.DefaultLang() {
		t.Errorf("Lang = %q, want %q", inc.Lang, domain.DefaultLang())
	}
	if inc.CreatedAt.IsZero() {
		t.Error("CreatedAt 不应为零值")
	}
}

// TestCreateWritesInitialEvent 确认建记录时同时写入创建事件。
//
// 只写 incidents 不写事件，详情页的时间线就会缺第一条，
// 而这个问题在只看接口响应时发现不了。
func TestCreateWritesInitialEvent(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)

	inc := mustCreate(t, db, "log", "norm", fp(1))

	events, err := repo.ListEvents(context.Background(), inc.ID)
	if err != nil {
		t.Fatalf("ListEvents 失败: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("应写入 1 条创建事件, got %d", len(events))
	}
	if events[0].EventType != "created" {
		t.Errorf("事件类型 = %q, want created", events[0].EventType)
	}
	if events[0].ToStatus == nil || *events[0].ToStatus != domain.StatusAnalyzing {
		t.Errorf("事件目标状态不正确: %v", events[0].ToStatus)
	}
}

// TestCreateRollsBackWhenEventFails 确认建记录与写事件在同一事务内。
//
// 用故意超长的 event_type 触发约束错误，验证 incidents 也不该留下。
// 如果只写失败的那半，数据库里会多出一条没有时间线的孤儿记录。
func TestCreateRollsBackWhenEventFails(t *testing.T) {
	db := setupDB(t)
	ctx := context.Background()

	// 先删掉创建事件所需的表，让事务中途失败。
	// 这是模拟"第二步出错"最直接的方式。
	if _, err := db.pool.Exec(ctx, `ALTER TABLE incident_events RENAME TO incident_events_hidden`); err != nil {
		t.Fatalf("准备失败: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.pool.Exec(context.Background(), `ALTER TABLE incident_events_hidden RENAME TO incident_events`)
	})

	repo := NewIncidentRepo(db)
	_, err := repo.Create(ctx, CreateIncidentInput{
		RawLog:      "log",
		Normalized:  "norm",
		Fingerprint: fp(2),
	})
	if err == nil {
		t.Fatal("事件表不存在时 Create 应当报错")
	}

	// 关键断言：incidents 里不能留下记录。
	var count int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM incidents`).Scan(&count); err != nil {
		t.Fatalf("计数失败: %v", err)
	}
	if count != 0 {
		t.Errorf("事务未回滚，incidents 留下了 %d 条记录", count)
	}
}

func TestGetByIDNotFound(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)

	_, err := repo.GetByID(context.Background(), 9999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的 ID 应返回 ErrNotFound, got %v", err)
	}
}

func TestCountByFingerprint(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		mustCreate(t, db, "same", "same", fp(7))
	}
	mustCreate(t, db, "other", "other", fp(8))

	n, err := repo.CountByFingerprint(ctx, fp(7))
	if err != nil {
		t.Fatalf("CountByFingerprint 失败: %v", err)
	}
	if n != 3 {
		t.Errorf("count = %d, want 3", n)
	}
}

// TestCountByFingerprintExcludesFailed 覆盖部分索引的语义：
// FAILED 的记录没有有效分析，不该计入"同类"。
func TestCountByFingerprintExcludesFailed(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	inc := mustCreate(t, db, "log", "norm", fp(9))
	if err := repo.UpdateStatus(ctx, inc.ID, domain.StatusOpen); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(ctx, inc.ID, domain.StatusFailed); err != nil {
		t.Fatal(err)
	}

	n, err := repo.CountByFingerprint(ctx, fp(9))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("FAILED 的记录不应计入, got %d", n)
	}
}

// TestFindRelatedExcludesSelf 是必须覆盖的边界。
//
// 不排除自己时，刚插入的记录会立刻匹配到自己，
// 表现为"每次都显示一条无关的同类问题"。
func TestFindRelatedExcludesSelf(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	inc := mustCreate(t, db, "log", "norm", fp(3))

	related, err := repo.FindRelated(ctx, fp(3), inc.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 0 {
		t.Errorf("不应把自己算作同类, got %d 条", len(related))
	}
}

func TestFindRelatedExcludesFailed(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	ok := mustCreate(t, db, "a", "a", fp(4))
	bad := mustCreate(t, db, "b", "b", fp(4))

	for _, st := range []domain.Status{domain.StatusOpen, domain.StatusFailed} {
		if err := repo.UpdateStatus(ctx, bad.ID, st); err != nil {
			t.Fatal(err)
		}
	}

	related, err := repo.FindRelated(ctx, fp(4), 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 1 {
		t.Fatalf("应只返回 1 条未失败的记录, got %d", len(related))
	}
	if related[0].ID != ok.ID {
		t.Errorf("返回了 %d, 期望 %d", related[0].ID, ok.ID)
	}
}

func TestFindRelatedRespectsLimit(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	for i := 0; i < 8; i++ {
		mustCreate(t, db, "x", "x", fp(5))
	}

	related, err := repo.FindRelated(ctx, fp(5), 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 3 {
		t.Errorf("limit=3 时应返回 3 条, got %d", len(related))
	}
	// 按创建时间倒序，最新的在前。
	if related[0].ID < related[1].ID {
		t.Error("结果应按 created_at 倒序")
	}
}

// TestUpdateStatusWritesEvent 确认状态转移会留下事件。
func TestUpdateStatusWritesEvent(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	inc := mustCreate(t, db, "log", "norm", fp(6))
	if err := repo.UpdateStatus(ctx, inc.ID, domain.StatusOpen); err != nil {
		t.Fatal(err)
	}

	events, err := repo.ListEvents(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("应共有 2 条事件（created + status_changed）, got %d", len(events))
	}

	last := events[len(events)-1]
	if last.EventType != "status_changed" {
		t.Errorf("事件类型 = %q", last.EventType)
	}
	if last.FromStatus == nil || *last.FromStatus != domain.StatusAnalyzing {
		t.Errorf("from_status 不正确: %v", last.FromStatus)
	}
	if last.ToStatus == nil || *last.ToStatus != domain.StatusOpen {
		t.Errorf("to_status 不正确: %v", last.ToStatus)
	}
}

func TestUpdateStatusMissingIncident(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)

	err := repo.UpdateStatus(context.Background(), 4242, domain.StatusOpen)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("应返回 ErrNotFound, got %v", err)
	}
}

func TestSetRecurring(t *testing.T) {
	db := setupDB(t)
	repo := NewIncidentRepo(db)
	ctx := context.Background()

	inc := mustCreate(t, db, "log", "norm", fp(7))
	if inc.IsRecurring {
		t.Fatal("新建时不应标记为重复出现")
	}

	if err := repo.SetRecurring(ctx, inc.ID, true); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsRecurring {
		t.Error("SetRecurring 未生效")
	}
}
