package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mosterakie/DevLens/internal/domain"
)

// IncidentRepo 提供 Incident 相关的查询。
type IncidentRepo struct {
	db *DB
}

// NewIncidentRepo 构造仓储。
func NewIncidentRepo(db *DB) *IncidentRepo {
	return &IncidentRepo{db: db}
}

// CreateIncidentInput 是创建 Incident 所需的字段。
type CreateIncidentInput struct {
	RawLog      string
	Normalized  string
	Fingerprint []byte
	CreatedBy   *int64
}

// Create 插入一条 Incident，状态为 ANALYZING，并写入一条创建事件。
//
// 插入和事件写入放在同一个事务里：只写前者会让 Timeline 缺一条，
// 而 Timeline 是详情页的核心。
func (r *IncidentRepo) Create(ctx context.Context, in CreateIncidentInput) (*domain.Incident, error) {
	var out *domain.Incident

	err := r.db.InTx(ctx, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO incidents (raw_log, normalized, fingerprint, created_by, status)
			VALUES ($1, $2, $3, $4, 'ANALYZING')
			RETURNING id, title, raw_log, normalized, fingerprint, severity,
			          category, status, is_recurring, created_by, created_at, updated_at`

		inc, err := scanIncident(tx.QueryRow(ctx, q,
			in.RawLog, in.Normalized, in.Fingerprint, in.CreatedBy))
		if err != nil {
			return fmt.Errorf("insert incident: %w", err)
		}

		const eq = `
			INSERT INTO incident_events (incident_id, event_type, to_status)
			VALUES ($1, 'created', 'ANALYZING')`
		if _, err := tx.Exec(ctx, eq, inc.ID); err != nil {
			return fmt.Errorf("insert created event: %w", err)
		}

		out = inc
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetByID 按 ID 读取 Incident。
func (r *IncidentRepo) GetByID(ctx context.Context, id int64) (*domain.Incident, error) {
	const q = `
		SELECT id, title, raw_log, normalized, fingerprint, severity,
		       category, status, is_recurring, created_by, created_at, updated_at
		FROM incidents WHERE id = $1`

	inc, err := scanIncident(r.db.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get incident: %w", err)
	}
	return inc, nil
}

// CountByFingerprint 统计同指纹的历史记录数，用于判断是否重复出现。
func (r *IncidentRepo) CountByFingerprint(ctx context.Context, fp []byte) (int, error) {
	const q = `
		SELECT count(*) FROM incidents
		WHERE fingerprint = $1 AND status <> 'FAILED'`

	var n int
	if err := r.db.pool.QueryRow(ctx, q, fp).Scan(&n); err != nil {
		return 0, fmt.Errorf("count by fingerprint: %w", err)
	}
	return n, nil
}

// RelatedIncident 是"同类问题"列表里的一项。
type RelatedIncident struct {
	ID        int64
	Title     string
	Severity  *domain.Severity
	Status    domain.Status
	CreatedAt time.Time
}

// FindRelated 查找同指纹的其他 Incident。
//
// excludeID 用来排除自己，否则刚插入的记录会匹配到自己。
// FAILED 的记录没有有效分析，也排除掉。
func (r *IncidentRepo) FindRelated(ctx context.Context, fp []byte, excludeID int64, limit int) ([]RelatedIncident, error) {
	const q = `
		SELECT id, title, severity, status, created_at
		FROM incidents
		WHERE fingerprint = $1 AND id <> $2 AND status <> 'FAILED'
		ORDER BY created_at DESC
		LIMIT $3`

	rows, err := r.db.pool.Query(ctx, q, fp, excludeID, limit)
	if err != nil {
		return nil, fmt.Errorf("find related: %w", err)
	}
	defer rows.Close()

	var out []RelatedIncident
	for rows.Next() {
		var it RelatedIncident
		if err := rows.Scan(&it.ID, &it.Title, &it.Severity, &it.Status, &it.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan related: %w", err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate related: %w", err)
	}
	return out, nil
}

// SetRecurring 标记该 Incident 是否属于重复出现的问题。
func (r *IncidentRepo) SetRecurring(ctx context.Context, id int64, recurring bool) error {
	const q = `UPDATE incidents SET is_recurring = $2, updated_at = now() WHERE id = $1`
	if _, err := r.db.pool.Exec(ctx, q, id, recurring); err != nil {
		return fmt.Errorf("set recurring: %w", err)
	}
	return nil
}

// ListFilter 是列表查询的过滤条件。
type ListFilter struct {
	Status   *domain.Status
	Severity *domain.Severity
	Limit    int
	// CursorCreatedAt / CursorID 构成游标。用游标而不是 offset，
	// 因为 Incidents 持续新增，offset 分页在列表头部插入时会漏记录。
	CursorCreatedAt *time.Time
	CursorID        *int64
}

// List 返回 Incident 列表。
func (r *IncidentRepo) List(ctx context.Context, f ListFilter) ([]*domain.Incident, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}

	const q = `
		SELECT id, title, raw_log, normalized, fingerprint, severity,
		       category, status, is_recurring, created_by, created_at, updated_at
		FROM incidents
		WHERE ($1::status_t IS NULL OR status = $1::status_t)
		  AND ($2::severity_t IS NULL OR severity = $2::severity_t)
		  AND ($3::timestamptz IS NULL OR (created_at, id) < ($3::timestamptz, $4::bigint))
		ORDER BY created_at DESC, id DESC
		LIMIT $5`

	rows, err := r.db.pool.Query(ctx, q, f.Status, f.Severity, f.CursorCreatedAt, f.CursorID, f.Limit)
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()

	var out []*domain.Incident
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incidents: %w", err)
	}
	return out, nil
}

// UpdateStatus 在事务内转移状态并写入事件。
//
// 状态校验在 service 层完成；这里只负责原子写入，
// 并把旧状态读出来记录到事件里。
func (r *IncidentRepo) UpdateStatus(ctx context.Context, id int64, to domain.Status) error {
	return r.db.InTx(ctx, func(tx pgx.Tx) error {
		var from domain.Status
		const sel = `SELECT status FROM incidents WHERE id = $1 FOR UPDATE`
		if err := tx.QueryRow(ctx, sel, id).Scan(&from); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock incident: %w", err)
		}

		const upd = `UPDATE incidents SET status = $2, updated_at = now() WHERE id = $1`
		if _, err := tx.Exec(ctx, upd, id, to); err != nil {
			return fmt.Errorf("update status: %w", err)
		}

		const ev = `
			INSERT INTO incident_events (incident_id, event_type, from_status, to_status)
			VALUES ($1, 'status_changed', $2, $3)`
		if _, err := tx.Exec(ctx, ev, id, from, to); err != nil {
			return fmt.Errorf("insert status event: %w", err)
		}
		return nil
	})
}

// Event 是 Timeline 上的一条记录。
type Event struct {
	ID         int64
	EventType  string
	FromStatus *domain.Status
	ToStatus   *domain.Status
	CreatedAt  time.Time
}

// ListEvents 读取某个 Incident 的事件流。
func (r *IncidentRepo) ListEvents(ctx context.Context, incidentID int64) ([]Event, error) {
	const q = `
		SELECT id, event_type, from_status, to_status, created_at
		FROM incident_events
		WHERE incident_id = $1
		ORDER BY created_at, id`

	rows, err := r.db.pool.Query(ctx, q, incidentID)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.EventType, &e.FromStatus, &e.ToStatus, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return out, nil
}

// rowScanner 抽象 pgx.Row 和 pgx.Rows 的公共部分。
type rowScanner interface {
	Scan(dest ...any) error
}

func scanIncident(s rowScanner) (*domain.Incident, error) {
	var (
		inc      domain.Incident
		severity *string
		category *string
		title    string
	)
	// severity 和 category 在分析完成前是 NULL，用 *string 接收可空值，
	// 再由 domain 层转成枚举。
	err := s.Scan(
		&inc.ID, &title, &inc.RawLog, &inc.Normalized, &inc.Fingerprint,
		&severity, &category, &inc.Status, &inc.IsRecurring,
		&inc.CreatedBy, &inc.CreatedAt, &inc.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	inc.Title = title
	if severity != nil {
		inc.Severity = domain.Severity(*severity)
	}
	if category != nil {
		inc.Category = *category
	}
	return &inc, nil
}
