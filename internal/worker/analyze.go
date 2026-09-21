// Package worker 消费分析队列并把结果落库。
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mosterakie/DevLens/internal/analyzer"
	"github.com/mosterakie/DevLens/internal/domain"
	"github.com/mosterakie/DevLens/internal/fingerprint"
	"github.com/mosterakie/DevLens/internal/queue"
	"github.com/mosterakie/DevLens/internal/repository"
)

// Queue 是消费队列所需的能力。
type Queue interface {
	DequeueAnalyze(ctx context.Context, timeout time.Duration) (int64, error)
	EnqueueAnalyze(ctx context.Context, incidentID int64) error
}

// AnalyzerWorker 处理单个 Incident 的分析。
type AnalyzerWorker struct {
	db        *repository.DB
	incidents *repository.IncidentRepo
	analyses  *repository.AnalysisRepo
	queue     Queue
	analyzer  analyzer.Analyzer
	log       *slog.Logger

	// pollTimeout 是阻塞取任务的超时，同时也是检查退出的间隔。
	//
	// 它决定了收到退出信号后最坏要等多久——go-redis 的 BRPOP 不会
	// 因 context 取消而立即返回，命令要等服务端超时才结束。
	// 取 1 秒：关闭延迟可接受，代价只是空队列时每秒多一次 Redis 往返。
	pollTimeout time.Duration
}

// New 构造 worker。
func New(
	db *repository.DB,
	incidents *repository.IncidentRepo,
	analyses *repository.AnalysisRepo,
	q Queue,
	a analyzer.Analyzer,
	log *slog.Logger,
) *AnalyzerWorker {
	return &AnalyzerWorker{
		db:          db,
		incidents:   incidents,
		analyses:    analyses,
		queue:       q,
		analyzer:    a,
		log:         log,
		pollTimeout: time.Second,
	}
}

// RequeueStale 把长时间停留在 ANALYZING 的记录重新入队。
//
// 这是队列没有消费确认的兜底：任务如果因为 worker 崩溃而丢失，
// 启动时能靠这个扫描捡回来，让系统可以自愈。
func (w *AnalyzerWorker) RequeueStale(ctx context.Context, olderThan time.Duration) (int, error) {
	const q = `
		SELECT id FROM incidents
		WHERE status = 'ANALYZING'
		  AND created_at < now() - $1::interval
		ORDER BY created_at`

	rows, err := w.db.Query(ctx, q, olderThan.String())
	if err != nil {
		return 0, fmt.Errorf("scan stale: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan stale id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate stale: %w", err)
	}

	for _, id := range ids {
		if err := w.queue.EnqueueAnalyze(ctx, id); err != nil {
			return 0, fmt.Errorf("requeue %d: %w", id, err)
		}
	}
	return len(ids), nil
}

// Run 持续消费，直到 ctx 取消。
func (w *AnalyzerWorker) Run(ctx context.Context) error {
	w.log.Info("worker started", "analyzer", w.analyzer.Name())

	for {
		if ctx.Err() != nil {
			return nil
		}

		id, err := w.queue.DequeueAnalyze(ctx, w.pollTimeout)

		// 队列为空是正常的：阻塞等待超时后回来继续等。
		if errors.Is(err, queue.ErrQueueEmpty) {
			continue
		}

		// ctx 被取消说明要退出了。BRPOP 在等待中被打断也会报错，
		// 所以先查 ctx 再判断错误，避免把正常退出记成故障。
		if ctx.Err() != nil {
			return nil
		}

		if err != nil {
			w.log.Error("dequeue failed", "error", err)
			// Redis 不可用时不要忙等。
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
			continue
		}

		if err := w.Handle(ctx, id); err != nil {
			// Handle 内部已经把失败状态写库，这里只记录。
			w.log.Error("handle failed", "incident_id", id, "error", err)
		}
	}
}

// Handle 处理一个 Incident 的分析任务。
func (w *AnalyzerWorker) Handle(ctx context.Context, incidentID int64) error {
	log := w.log.With("incident_id", incidentID)

	inc, err := w.incidents.GetByID(ctx, incidentID)
	if err != nil {
		return fmt.Errorf("load incident: %w", err)
	}

	// 已经分析过的直接跳过。worker 可能重复消费同一条消息，
	// 这一步让重复消费变成无操作。
	if exist, err := w.analyses.GetByIncidentID(ctx, incidentID); err == nil && exist != nil {
		log.Info("analysis already exists, skipping")
		return nil
	} else if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return fmt.Errorf("check existing analysis: %w", err)
	}

	related, err := w.findRelated(ctx, inc)
	if err != nil {
		return fmt.Errorf("load related: %w", err)
	}
	summaries := make([]string, 0, len(related))
	for _, r := range related {
		// 把匹配原因一并写进摘要：模型需要知道哪条是"确定同类"、
		// 哪条只是"措辞相近"，否则它会把推测当事实来参考。
		mark := "确定同类"
		if r.Match == repository.MatchSimilar {
			mark = "可能同类"
		}
		summaries = append(summaries, fmt.Sprintf("#%d %s [%s]", r.ID, r.Title, mark))
	}

	in := analyzer.Input{
		RawLog:           inc.RawLog,
		Normalized:       inc.Normalized,
		IncidentID:       inc.ID,
		RelatedSummaries: summaries,
		// 语言取自提交时记录的值，所以同一条记录无论何时重跑
		// 都会用同一种语言生成。
		Lang: inc.Lang,
	}

	started := time.Now()
	result, err := w.analyzer.Analyze(ctx, in)
	if err != nil {
		log.Error("analyze failed", "error", err, "duration_ms", time.Since(started).Milliseconds())
		return w.markFailed(ctx, incidentID, err)
	}
	log.Info("analyzed",
		"duration_ms", time.Since(started).Milliseconds(),
		"confidence", result.Analysis.Confidence)

	return w.persist(ctx, incidentID, result)
}

// findRelated 合并"精确指纹"与"相似度"两路，供模型参考。
//
// 为什么 worker 自己实现合并而不是复用 service：依赖方向是
// worker → repository，worker 拿不到 service（cmd/worker 里也没构造它）。
// 反向依赖会让 service 成为 worker 的必需前置，而 worker 的逻辑上
// 并不需要任何业务编排能力。
//
// 合并规则本身不在这里重复实现 —— 去重、精确优先、排序都由
// fingerprint.Merge 提供，service 与 worker 共用同一份规则，
// 保证喂给模型的历史和用户看到的是同一批。
func (w *AnalyzerWorker) findRelated(ctx context.Context, inc *domain.Incident) ([]repository.RelatedIncident, error) {
	exact, err := w.incidents.FindRelated(ctx, inc.Fingerprint, inc.ID, fingerprint.RelatedLimit)
	if err != nil {
		return nil, err
	}

	exactC := make([]fingerprint.Candidate, 0, len(exact))
	byID := make(map[int64]repository.RelatedIncident, len(exact))
	for _, r := range exact {
		exactC = append(exactC, candidateOf(r, fingerprint.MatchExact))
		byID[r.ID] = r
	}

	// 相似召回失败时降级为只用精确结果：给模型的补充上下文缺一块，
	// 远好过整条分析任务因为召回失败而走进 markFailed。
	cands, err := w.incidents.FindSimilarCandidates(ctx, inc.ID, repository.SimilarRecallLimit)
	similarC := make([]fingerprint.Candidate, 0, len(cands))
	if err == nil {
		base := fingerprint.Tokens(inc.Normalized)
		for _, c := range cands {
			score := fingerprint.Jaccard(base, fingerprint.Tokens(c.Normalized))
			if score < fingerprint.SimilarityThreshold {
				continue
			}
			similarC = append(similarC, candidateOf(c.RelatedIncident, fingerprint.MatchSimilar))
			if _, ok := byID[c.ID]; !ok {
				byID[c.ID] = c.RelatedIncident
			}
		}
	}

	merged := fingerprint.Merge(exactC, similarC, fingerprint.RelatedLimit)
	out := make([]repository.RelatedIncident, 0, len(merged))
	for _, c := range merged {
		item := byID[c.ID]
		item.Match = string(c.Match)
		out = append(out, item)
	}
	return out, nil
}

func candidateOf(r repository.RelatedIncident, m fingerprint.Match) fingerprint.Candidate {
	return fingerprint.Candidate{
		ID:        r.ID,
		Title:     r.Title,
		CreatedAt: r.CreatedAt.UnixNano(),
		Match:     m,
	}
}

// truncate 截断字符串，避免日志或标题过长。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// persist 在一个事务里写入分析结果、更新 Incident 字段并转移到 OPEN。
//
// 三者必须原子：只写一部分会让状态和内容不一致，
// 而状态是前端轮询的唯一依据。
func (w *AnalyzerWorker) persist(ctx context.Context, incidentID int64, res analyzer.Result) error {
	a := res.Analysis
	return w.db.InTx(ctx, func(tx pgx.Tx) error {
		const upAnalysis = `
			INSERT INTO incident_analysis (
				incident_id, summary, possible_causes, evidence,
				suggested_actions, model, prompt_version, confidence, raw_response
			) VALUES (
				$1, $2, $3::jsonb, $4::jsonb, $5::jsonb, $6, $7, $8, $9
			)
			ON CONFLICT (incident_id) DO UPDATE SET
				summary           = EXCLUDED.summary,
				possible_causes   = EXCLUDED.possible_causes,
				evidence          = EXCLUDED.evidence,
				suggested_actions = EXCLUDED.suggested_actions,
				model             = EXCLUDED.model,
				prompt_version    = EXCLUDED.prompt_version,
				confidence        = EXCLUDED.confidence,
				raw_response      = EXCLUDED.raw_response`

		causes, _ := jsonBytes(a.PossibleCauses)
		evidence, _ := jsonBytes(a.Evidence)
		actions, _ := jsonBytes(a.SuggestedActions)

		if _, err := tx.Exec(ctx, upAnalysis, incidentID,
			a.Summary, causes, evidence, actions,
			a.Model, a.PromptVersion, a.Confidence, a.RawResponse); err != nil {
			return fmt.Errorf("upsert analysis: %w", err)
		}

		// title / severity / category 存在 incidents 表上，不在
		// incident_analysis 里，所以要单独更新。
		const upIncident = `
			UPDATE incidents
			SET status     = 'OPEN',
			    title      = COALESCE(NULLIF($2, ''), title),
			    severity   = $3,
			    category   = NULLIF($4, ''),
			    updated_at = now()
			WHERE id = $1`
		if _, err := tx.Exec(ctx, upIncident, incidentID, res.Title, res.Severity, res.Category); err != nil {
			return fmt.Errorf("update incident: %w", err)
		}

		const ev = `
			INSERT INTO incident_events (incident_id, event_type, from_status, to_status)
			VALUES ($1, 'analysis_completed', 'ANALYZING', 'OPEN')`
		if _, err := tx.Exec(ctx, ev, incidentID); err != nil {
			return fmt.Errorf("insert analysis event: %w", err)
		}
		return nil
	})
}

// markFailed 把 Incident 标为 FAILED 并记录原因。
func (w *AnalyzerWorker) markFailed(ctx context.Context, incidentID int64, cause error) error {
	return w.db.InTx(ctx, func(tx pgx.Tx) error {
		const upd = `UPDATE incidents SET status = 'FAILED', updated_at = now() WHERE id = $1`
		if _, err := tx.Exec(ctx, upd, incidentID); err != nil {
			return fmt.Errorf("mark failed: %w", err)
		}

		const ev = `
			INSERT INTO incident_events (incident_id, event_type, from_status, to_status, payload)
			VALUES ($1, 'analysis_failed', 'ANALYZING', 'FAILED', $2::jsonb)`
		payload, _ := jsonBytes(map[string]string{"reason": cause.Error()})
		if _, err := tx.Exec(ctx, ev, incidentID, payload); err != nil {
			return fmt.Errorf("insert failure event: %w", err)
		}
		return nil
	})
}
