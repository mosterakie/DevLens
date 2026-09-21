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
	// Lang 记录这条记录应当用哪种语言生成诊断。
	Lang domain.Lang
}

// Create 插入一条 Incident，状态为 ANALYZING，并写入一条创建事件。
//
// 插入和事件写入放在同一个事务里：只写前者会让 Timeline 缺一条，
// 而 Timeline 是详情页的核心。
func (r *IncidentRepo) Create(ctx context.Context, in CreateIncidentInput) (*domain.Incident, error) {
	var out *domain.Incident

	err := r.db.InTx(ctx, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO incidents (raw_log, normalized, fingerprint, created_by, status, lang)
			VALUES ($1, $2, $3, $4, 'ANALYZING', $5)
			RETURNING id, title, raw_log, normalized, fingerprint, severity,
			          category, status, is_recurring, lang, created_by, created_at, updated_at`

		// 语言在这里兜底，而不是只依赖调用方。
		// 空字符串会绕过数据库的 DEFAULT，写进一条语言未定义的记录。
		lang := in.Lang
		if !lang.IsValid() {
			lang = domain.DefaultLang()
		}

		inc, err := scanIncident(tx.QueryRow(ctx, q,
			in.RawLog, in.Normalized, in.Fingerprint, in.CreatedBy, string(lang)))
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
		       category, status, is_recurring, lang, created_by, created_at, updated_at
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

// MatchExact 表示这条相关历史与当前记录指纹完全相同，是"确定同类"。
const MatchExact = "exact"

// MatchSimilar 表示这条相关历史只是词集相似度达标，是"可能同类"。
// 与 MatchExact 的语义强度不同，必须让 UI 能区分。
const MatchSimilar = "similar"

// RelatedIncident 是"同类问题"列表里的一项。
type RelatedIncident struct {
	ID        int64
	Title     string
	Severity  *domain.Severity
	Status    domain.Status
	CreatedAt time.Time

	// Match 说明这项是怎么被找出来的：exact 或 similar。
	//
	// 两者语义强度不同，必须让 UI 能如实区分 —— 05-related-incidents.md
	// 的核心诉求是不能让用户看到"错误的相关历史"，所以"确定同类"和
	// "可能同类"不能在界面上长得一样。
	Match string

	// Score 是相似度得分，仅对 MatchSimilar 有意义。
	//
	// 不用来排序（排序仍按 created_at DESC），只用于日志和调试：
	// 线上出现可疑召回时，需要知道它到底有多接近阈值。
	Score float64
}

// FindRelated 查找同指纹的其他 Incident。
//
// excludeID 用来排除自己，否则刚插入的记录会匹配到自己。
// FAILED 的记录没有有效分析，也排除掉。
//
// 这是同类召回的主路：走 fingerprint 索引，一次查询、结果确定，
// 与其他记录的规模无关。相似度召回（FindSimilarCandidates）只是它的
// 补充，不改变这里的语义与实现。
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
		it.Match = MatchExact
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate related: %w", err)
	}
	return out, nil
}

// SimilarCandidate 是粗召回调出来的一条候选，附带它归一化后的文本。
//
// Normalized 是给 Go 侧算相似度用的，不返回给 API ——
// 它可能包含日志片段，而相关历史列表只需要展示元信息。
type SimilarCandidate struct {
	RelatedIncident
	Normalized string
}

// SimilarRecallLimit 是相似度粗召回的取数上限。
//
// 为什么必须有这个上限：相似度只能在 Go 侧算（SQL 里没有 Jaccard，
// 把文本拉回来算就意味着要读全表）。LIMIT 200 是一个明确的取舍：
//
//   - 召回率：只覆盖最近 200 条未失败记录。更久远的历史即使相似也
//     召不回来。对 Demo 与中小规模库足够；真要做到全量召回需要
//     pg_trgm 之类的 SQL 侧索引，那是 v2 的事（见 05-related-incidents.md）。
//   - 成本：无论库里有多少数据，单次查询最多读 200 行、固定量的
//     内存与 CPU，不会因为库变大而拖慢提交路径。提交路径是同步的，
//     用户正在等这个响应。
//   - 交互：精确匹配是主路且不受此上限影响，所以"确定同类"永远不会
//     因为行数上限而丢失，丢的只是补充信号。
//
// 按 created_at DESC 取最近的是有意的：同类问题在时间上聚集，
// 最近的历史比远古记录更可能相关，用户也更关心。
const SimilarRecallLimit = 200

// FindSimilarCandidates 粗召回用于算相似度的候选记录。
//
// 只做 SQL 侧的粗召回（取最近的若干条未 FAILED 记录），相似度计算
// 留给 Go 侧 —— 相似度要复用 fingerprint 包里的归一化语义，用 SQL
// 重写一遍必然产生偏差，且无法给 Jaccard 建索引。
//
// 必须排除自己（excludeID），否则刚插入的记录会以 1.0 的相似度
// 匹配到自己。
func (r *IncidentRepo) FindSimilarCandidates(ctx context.Context, excludeID int64, limit int) ([]SimilarCandidate, error) {
	if limit <= 0 || limit > SimilarRecallLimit {
		limit = SimilarRecallLimit
	}

	const q = `
		SELECT id, title, severity, status, created_at, normalized
		FROM incidents
		WHERE id <> $1 AND status <> 'FAILED'
		ORDER BY created_at DESC
		LIMIT $2`

	rows, err := r.db.pool.Query(ctx, q, excludeID, limit)
	if err != nil {
		return nil, fmt.Errorf("find similar candidates: %w", err)
	}
	defer rows.Close()

	var out []SimilarCandidate
	for rows.Next() {
		var it SimilarCandidate
		if err := rows.Scan(&it.ID, &it.Title, &it.Severity, &it.Status, &it.CreatedAt, &it.Normalized); err != nil {
			return nil, fmt.Errorf("scan similar candidate: %w", err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate similar candidates: %w", err)
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
		       category, status, is_recurring, lang, created_by, created_at, updated_at
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
	var lang string
	err := s.Scan(
		&inc.ID, &title, &inc.RawLog, &inc.Normalized, &inc.Fingerprint,
		&severity, &category, &inc.Status, &inc.IsRecurring,
		&lang, &inc.CreatedBy, &inc.CreatedAt, &inc.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	inc.Title = title
	inc.Lang = domain.Lang(lang)
	if severity != nil {
		inc.Severity = domain.Severity(*severity)
	}
	if category != nil {
		inc.Category = *category
	}
	return &inc, nil
}
