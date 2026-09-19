// Command worker 消费分析队列。
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mosterakie/DevLens/internal/analyzer"
	"github.com/mosterakie/DevLens/internal/config"
	"github.com/mosterakie/DevLens/internal/queue"
	"github.com/mosterakie/DevLens/internal/repository"
	"github.com/mosterakie/DevLens/internal/worker"
)

func main() {
	log := newLogger()
	if err := run(log); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// 收到信号就取消 ctx，让 worker 处理完当前任务后退出。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	db, err := repository.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	q, err := queue.New(ctx, cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB)
	if err != nil {
		return err
	}
	defer func() { _ = q.Close() }()

	incidents := repository.NewIncidentRepo(db)
	analyses := repository.NewAnalysisRepo(db)

	a := chooseAnalyzer(cfg, log)

	w := worker.New(db, incidents, analyses, q, a, log)

	// 启动时把长时间停留在 ANALYZING 的任务捡回来。
	// 这是队列没有消费确认的兜底，让系统在 worker 崩溃后能自愈。
	requeueCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	n, err := w.RequeueStale(requeueCtx, 5*time.Minute)
	cancel()
	if err != nil {
		log.Error("requeue stale failed", "error", err)
	} else if n > 0 {
		log.Info("requeued stale incidents", "count", n)
	}

	log.Info("worker starting", "analyzer", a.Name())
	return w.Run(ctx)
}

// chooseAnalyzer 选择分析实现。
//
// 配了 API key 就用真实模型；没配就退回本地启发式实现。
// 后者让整条链路在没有外部依赖时也能端到端跑通，
// 只是判断依据是关键词匹配，覆盖面有限。
func chooseAnalyzer(cfg *config.Config, log *slog.Logger) analyzer.Analyzer {
	if cfg.LLMAPIKey == "" {
		log.Warn("LLM_API_KEY not set, falling back to local heuristic analyzer")
		return analyzer.NewHeuristic()
	}
	log.Info("using llm analyzer", "model", cfg.LLMModel)
	return analyzer.NewHTTP(analyzer.HTTPConfig{
		APIKey:      cfg.LLMAPIKey,
		Model:       cfg.LLMModel,
		BaseURL:     cfg.LLMBaseURL,
		Timeout:     cfg.AnalyzeTimeout,
		MaxAttempts: 3,
	})
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
