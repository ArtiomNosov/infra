package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/sandbox-code-executor/internal/config"
	"github.com/e2b-dev/infra/packages/sandbox-code-executor/internal/handlers"
	"github.com/e2b-dev/infra/packages/sandbox-code-executor/internal/piston"
	"github.com/e2b-dev/infra/packages/sandbox-code-executor/internal/runner"
	"github.com/e2b-dev/infra/packages/sandbox-code-executor/internal/store"
	"github.com/e2b-dev/infra/packages/sandbox-code-executor/internal/worker"
)

func main() {
	cfg := config.FromEnv()

	var logger *zap.Logger
	var err error
	if cfg.Debug {
		logger, err = zap.NewDevelopment()
	} else {
		logger, err = zap.NewProduction()
	}
	if err != nil {
		log.Fatalf("logger: %v", err)
	}
	defer logger.Sync()

	// Redis
	opt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Fatal("redis url", zap.Error(err))
	}
	rdb := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := rdb.Ping(ctx).Err(); err != nil {
		cancel()
		logger.Fatal("redis ping", zap.Error(err))
	}
	cancel()

	st := store.NewStore(rdb, logger, config.ResultTTLHours*time.Hour)
	pc := piston.NewClient(cfg.PistonURL, logger)

	// Пул для синхронного POST /execute
	workerPool := worker.NewPool(cfg.WorkerPoolSize, logger)

	// Runner и воркер очереди для асинхронных задач
	run := runner.NewRunner(st, pc, logger, time.Duration(cfg.MaxTimeout)*time.Second, cfg.StdoutMaxSize)
	queueWorker := worker.NewQueueWorker(st, run, logger, cfg.WorkerPoolSize)
	ctxBg := context.Background()
	queueWorker.Start(ctxBg)

	handler := handlers.NewHandler(cfg, st, pc, workerPool, logger)
	router := setupRouter(handler, logger)

	port, err := choosePort(cfg.APIPort, logger)
	if err != nil {
		logger.Fatal("port", zap.Error(err))
	}
	if port != cfg.APIPort {
		logger.Warn("port in use, using alternative", zap.Int("requested", cfg.APIPort), zap.Int("actual", port))
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%d", port),
		Handler: router,
	}
	go func() {
		logger.Info("http server", zap.Int("port", port), zap.Int("workers", cfg.WorkerPoolSize))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Fatal("shutdown", zap.Error(err))
	}
	logger.Info("exited")
}

func setupRouter(h *handlers.Handler, logger *zap.Logger) *gin.Engine {
	if !gin.IsDebugging() {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	corsConfig.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type"}
	r.Use(cors.New(corsConfig))

	r.GET("/health", h.Health)
	r.GET("/stats", h.Stats)

	r.GET("/environments", h.ListEnvironments)
	r.POST("/environments", h.CreateEnvironment)
	r.PUT("/environments/:id", h.UpdateEnvironment)
	r.DELETE("/environments/:id", h.DeleteEnvironment)

	r.POST("/execute", h.Execute)

	r.POST("/jobs/code", h.CreateJobCode)
	r.GET("/jobs/code", h.ListJobsCode)
	r.GET("/jobs/code/:id", h.GetJobCode)
	r.DELETE("/jobs/code/:id", h.CancelJobCode)

	r.POST("/jobs/files", h.CreateJobFiles)
	r.GET("/jobs/files", h.ListJobsFiles)
	r.GET("/jobs/files/:id", h.GetJobFiles)
	r.DELETE("/jobs/files/:id", h.CancelJobFiles)

	return r
}

func choosePort(requested int, logger *zap.Logger) (int, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", requested))
	if err != nil {
		logger.Warn("requested port busy", zap.Int("port", requested))
		listener, err = net.Listen("tcp", ":0")
		if err != nil {
			return 0, err
		}
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
