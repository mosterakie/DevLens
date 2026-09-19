package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mosterakie/DevLens/internal/domain"
)

// AnalysisRepo 提供分析结果的读写。
type AnalysisRepo struct {
	db *DB
}

// NewAnalysisRepo 构造仓储。
func NewAnalysisRepo(db *DB) *AnalysisRepo {
	return &AnalysisRepo{db: db}
}

// Save 写入分析结果。
//
// 用 ON CONFLICT 保证幂等：worker 可能重复消费同一条消息，
// 第二次写入应当覆盖而不是报错。这也是重试能安全进行的前提。
func (r *AnalysisRepo) Save(ctx context.Context, a *domain.Analysis) error {
	causes, err := json.Marshal(a.PossibleCauses)
	if err != nil {
		return fmt.Errorf("marshal possible_causes: %w", err)
	}
	evidence, err := json.Marshal(a.Evidence)
	if err != nil {
		return fmt.Errorf("marshal evidence: %w", err)
	}
	actions, err := json.Marshal(a.SuggestedActions)
	if err != nil {
		return fmt.Errorf("marshal suggested_actions: %w", err)
	}

	const q = `
		INSERT INTO incident_analysis (
			incident_id, summary, possible_causes, evidence,
			suggested_actions, model, prompt_version, confidence, raw_response
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (incident_id) DO UPDATE SET
			summary           = EXCLUDED.summary,
			possible_causes   = EXCLUDED.possible_causes,
			evidence          = EXCLUDED.evidence,
			suggested_actions = EXCLUDED.suggested_actions,
			model             = EXCLUDED.model,
			prompt_version    = EXCLUDED.prompt_version,
			confidence        = EXCLUDED.confidence,
			raw_response      = EXCLUDED.raw_response`

	_, err = r.db.pool.Exec(ctx, q,
		a.IncidentID, a.Summary, causes, evidence, actions,
		a.Model, a.PromptVersion, a.Confidence, a.RawResponse)
	if err != nil {
		return fmt.Errorf("save analysis: %w", err)
	}
	return nil
}

// GetByIncidentID 读取分析结果。没有结果时返回 ErrNotFound。
func (r *AnalysisRepo) GetByIncidentID(ctx context.Context, incidentID int64) (*domain.Analysis, error) {
	const q = `
		SELECT incident_id, summary, possible_causes, evidence,
		       suggested_actions, model, prompt_version, confidence,
		       raw_response, created_at
		FROM incident_analysis WHERE incident_id = $1`

	var (
		a        domain.Analysis
		causes   []byte
		evidence []byte
		actions  []byte
	)
	err := r.db.pool.QueryRow(ctx, q, incidentID).Scan(
		&a.IncidentID, &a.Summary, &causes, &evidence, &actions,
		&a.Model, &a.PromptVersion, &a.Confidence, &a.RawResponse, &a.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get analysis: %w", err)
	}

	if err := json.Unmarshal(causes, &a.PossibleCauses); err != nil {
		return nil, fmt.Errorf("unmarshal possible_causes: %w", err)
	}
	if err := json.Unmarshal(evidence, &a.Evidence); err != nil {
		return nil, fmt.Errorf("unmarshal evidence: %w", err)
	}
	if err := json.Unmarshal(actions, &a.SuggestedActions); err != nil {
		return nil, fmt.Errorf("unmarshal suggested_actions: %w", err)
	}
	return &a, nil
}
