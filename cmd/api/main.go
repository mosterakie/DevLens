// Command api 提供 HTTP 接口。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mosterakie/DevLens/internal/config"
	"github.com/mosterakie/DevLens/internal/handler"
	"github.com/mosterakie/DevLens/internal/middleware"
	"github.com/mosterakie/DevLens/internal/queue"
	"github.com/mosterakie/DevLens/internal/repository"
	"github.com/mosterakie/DevLens/internal/service"
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

	ctx := context.Background()

	db, err := repository.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	log.Info("connected to postgres")

	q, err := queue.New(ctx, cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB)
	if err != nil {
		return err
	}
	defer func() { _ = q.Close() }()
	log.Info("connected to redis")

	incidentRepo := repository.NewIncidentRepo(db)
	analysisRepo := repository.NewAnalysisRepo(db)
	svc := service.NewIncidentService(incidentRepo, q)
	h := handler.NewIncidentHandler(svc, analysisRepo)

	router := buildRouter(cfg, log, h, db, q)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// 优雅关闭：先停止接受新请求，再等在途请求完成。
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig

		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("shutdown error", "error", err)
		}
	}()

	log.Info("api listening", "port", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	<-shutdownDone
	log.Info("stopped")
	return nil
}

func buildRouter(
	cfg *config.Config,
	log *slog.Logger,
	h *handler.IncidentHandler,
	db *repository.DB,
	q *queue.Queue,
) *gin.Engine {
	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(log))
	r.Use(middleware.Recovery(log))

	v1 := r.Group("/api/v1")

	// 提交分析最贵，且会触发 LLM 调用，限制收得最紧。
	analyzeLimit := middleware.RateLimit(q, middleware.RateLimitConfig{
		Name:   "analyze:ip",
		Limit:  cfg.RateLimitAnalyzePerMin,
		Window: time.Minute,
		ByIP:   true,
	}, log)
	// 全局计数是兜底：IP 可以被换掉。
	analyzeGlobal := middleware.RateLimit(q, middleware.RateLimitConfig{
		Name:   "analyze:global",
		Limit:  cfg.RateLimitGlobalPerMin,
		Window: time.Minute,
		ByIP:   false,
	}, log)
	v1.POST("/incidents/analyze", analyzeLimit, analyzeGlobal, h.Analyze)

	// 轮询用的读取接口，限额要明显宽于提交：1 秒一次轮询即 60 次/分钟。
	// 这个数字必须和提交限额一起设计，否则轮询会把自己限流掉。
	readLimit := middleware.RateLimit(q, middleware.RateLimitConfig{
		Name:   "read:ip",
		Limit:  120,
		Window: time.Minute,
		ByIP:   true,
	}, log)

	v1.GET("/incidents", readLimit, h.List)
	v1.GET("/incidents/:id", readLimit, h.Get)
	v1.GET("/incidents/:id/events", readLimit, h.Events)
	v1.PATCH("/incidents/:id/status", readLimit, h.ChangeStatus)

	// 健康检查放在限流之外：编排系统探测不该被限流挡住，
	// 而且/readyz 失败会导致摘流量，被限流误判的代价很大。
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db unavailable", "error": err.Error()})
			return
		}
		if err := q.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "redis unavailable", "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	// 前端是静态文件，没有构建步骤，直接由 api 进程托管。
	// 这样本地只需启动一个进程就能打开页面。
	if _, err := os.Stat("web"); err == nil {
		r.Static("/static", "./web")
		r.StaticFile("/", "./web/index.html")
		r.StaticFile("/analyze.html", "./web/analyze.html")
		r.StaticFile("/incidents.html", "./web/incidents.html")
		r.StaticFile("/detail.html", "./web/detail.html")
		r.StaticFile("/app.js", "./web/app.js")
		r.StaticFile("/style.css", "./web/style.css")
	}

	return r
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
