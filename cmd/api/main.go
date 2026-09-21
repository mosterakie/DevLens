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
	"github.com/mosterakie/DevLens/internal/metrics"
	"github.com/mosterakie/DevLens/internal/middleware"
	"github.com/mosterakie/DevLens/internal/migrate"
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

	// 启动时应用迁移。api 与 worker 都可能先启动，所以两边都调用；
	// migrate 内部用 advisory lock 保证只有一个真正执行。
	if err := applyMigrations(ctx, db, log); err != nil {
		return err
	}

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

	reg := metrics.New()
	gauges := metrics.NewGauges()
	// 队列积压：唯一的"当前状态"类指标，在渲染时才去问 Redis。
	gauges.Register("devlens_queue_depth", func() float64 {
		d, err := q.Depth(context.Background())
		if err != nil {
			return -1
		}
		return float64(d)
	})

	router := buildRouter(cfg, log, h, db, q, reg, gauges)

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

// migrationDir 是迁移文件所在目录。
//
// 相对路径：容器里 WORKDIR 是 /app，migrations 一并复制到那里；
// 本地开发时工作目录就是仓库根。
const migrationDir = "migrations"

// applyMigrations 读取并应用迁移。
//
// 目录不存在时跳过，便于只跑集成测试的场景。
func applyMigrations(ctx context.Context, db *repository.DB, log *slog.Logger) error {
	if _, err := os.Stat(migrationDir); err != nil {
		log.Warn("未找到迁移目录，跳过迁移", "dir", migrationDir)
		return nil
	}

	migrations, err := migrate.Load(migrationDir)
	if err != nil {
		return err
	}

	runner := migrate.New(db.Pool(), log)
	start := time.Now()
	if err := runner.Apply(ctx, migrations); err != nil {
		return err
	}
	log.Info("迁移检查完成",
		"total", len(migrations),
		"duration_ms", time.Since(start).Milliseconds())
	return nil
}

func buildRouter(
	cfg *config.Config,
	log *slog.Logger,
	h *handler.IncidentHandler,
	db *repository.DB,
	q *queue.Queue,
	reg *metrics.Registry,
	gauges *metrics.Gauges,
) *gin.Engine {
	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(log))
	r.Use(middleware.Recovery(log))
	r.Use(middleware.Metrics(reg))

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
	// 请求体上限放在最前面：超大的请求在进入限流与解析之前就被挡掉，
	// 不占用限流配额，也不会先在内存里展开。
	bodyLimit := middleware.BodyLimit(middleware.MaxBodyBytes)
	v1.POST("/incidents/analyze", bodyLimit, analyzeLimit, analyzeGlobal, h.Analyze)

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
	v1.GET("/incidents/:id/related", readLimit, h.Related)
	v1.PATCH("/incidents/:id/status", readLimit, h.ChangeStatus)

	// 健康检查放在限流之外：编排系统探测不该被限流挡住，
	// 而且/readyz 失败会导致摘流量，被限流误判的代价很大。
	// 指标端点不鉴权也不限流：它需要被监控系统高频抓取。
	// 真实部署里应当只在内网暴露。
	r.GET("/metrics", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		c.String(http.StatusOK, reg.Render()+gauges.Render())
	})

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

	mountWeb(r)

	return r
}

// webFiles 是允许直接访问的前端文件。
//
// 用白名单而不是把整个目录挂到根路径：web 目录里不该有任何
// 需要保密的文件，但显式列出能避免以后误把源文件或配置暴露出去。
var webFiles = []string{
	"index.html",
	"analyze.html",
	"incidents.html",
	"detail.html",
	"app.js",
	"i18n.js",
	"select.js",
	"header.js",
	"style.css",
}

// mountWeb 注册前端静态资源。web 目录不存在时跳过，
// 便于只跑 API 的场景（例如跑集成测试）。
func mountWeb(r *gin.Engine) {
	if _, err := os.Stat("web"); err != nil {
		return
	}

	serve := func(file string) gin.HandlerFunc {
		return func(c *gin.Context) { c.File("./web/" + file) }
	}

	// 根路径返回首页。
	r.GET("/", serve("index.html"))
	r.HEAD("/", serve("index.html"))

	for _, f := range webFiles {
		file := f
		r.GET("/"+file, serve(file))
		r.HEAD("/"+file, serve(file))
	}
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
