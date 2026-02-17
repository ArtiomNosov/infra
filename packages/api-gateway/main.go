package main

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api-gateway/internal/cfg"
	"github.com/e2b-dev/infra/packages/api-gateway/internal/handlers"
	"github.com/e2b-dev/infra/packages/api-gateway/internal/redis"
	"github.com/e2b-dev/infra/packages/api-gateway/internal/storage"
	"github.com/e2b-dev/infra/packages/api-gateway/internal/worker"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log := zap.Must(logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:   "api-gateway",
		IsInternal:    true,
		IsDebug:       false,
		EnableConsole: true,
	}))
	defer log.Sync()
	zap.ReplaceGlobals(log)

	config, err := cfg.Parse()
	if err != nil {
		log.Fatal("Failed to parse config", zap.Error(err))
	}

	redisClient, err := redis.NewClient(config.RedisAddress, config.RedisPassword, config.RedisDB)
	if err != nil {
		log.Fatal("Failed to connect to Redis", zap.Error(err))
	}
	defer redisClient.Close()

	rdb := redisClient.GetClient()

	envStorage := storage.NewEnvironmentStorage(rdb)
	jobStorage := storage.NewJobStorage(rdb)
	envFile := storage.NewEnvironmentsFile(config.EnvironmentsConfigPath)

	configYAML, err := envFile.Load()
	if err != nil {
		log.Fatal("Failed to load environments config", zap.Error(err))
	}

	for _, env := range configYAML.Environments {
		if err := envStorage.Set(ctx, env); err != nil {
			log.Warn("Failed to load environment from config", zap.String("id", env.ID), zap.Error(err))
		}
	}

	workerClient := worker.NewClient(rdb)

	handlerConfig := handlers.Config{
		SyncMaxTimeout: config.ExecutionSyncMaxTimeout,
		MaxTimeout:     config.ExecutionMaxTimeout,
		MaxFiles:       config.LimitsMaxFiles,
		MaxFileSize:    config.LimitsMaxFileSize,
		MaxTotalSize:   config.LimitsMaxTotalSize,
	}

	envHandler := handlers.NewEnvironmentsHandler(envStorage, envFile)
	executeHandler := handlers.NewExecuteHandler(envStorage, jobStorage, workerClient, handlerConfig)
	jobsCodeHandler := handlers.NewJobsCodeHandler(envStorage, jobStorage, handlerConfig)
	jobsFilesHandler := handlers.NewJobsFilesHandler(envStorage, jobStorage, handlerConfig)
	monitoringHandler := handlers.NewMonitoringHandler(redisClient, envStorage, jobStorage, workerClient)

	r := gin.New()
	r.Use(gin.Recovery())

	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	r.Use(cors.New(corsConfig))

	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"service": "API Gateway",
			"version": "1.0",
			"endpoints": gin.H{
				"health": "/health",
				"stats": "/stats",
				"environments": "/environments",
				"execute": "/execute",
				"jobs_code": "/jobs/code",
				"jobs_files": "/jobs/files",
			},
		})
	})

	r.GET("/health", monitoringHandler.Health)
	r.GET("/stats", monitoringHandler.Stats)

	api := r.Group("/")
	{
		api.GET("/environments", envHandler.GetEnvironments)
		api.POST("/environments", envHandler.CreateEnvironment)
		api.PUT("/environments/:id", envHandler.UpdateEnvironment)
		api.DELETE("/environments/:id", envHandler.DeleteEnvironment)

		api.POST("/execute", executeHandler.Execute)

		api.POST("/jobs/code", jobsCodeHandler.CreateJob)
		api.GET("/jobs/code", jobsCodeHandler.ListJobs)
		api.GET("/jobs/code/:id", jobsCodeHandler.GetJob)
		api.DELETE("/jobs/code/:id", jobsCodeHandler.CancelJob)

		api.POST("/jobs/files", jobsFilesHandler.CreateJob)
		api.GET("/jobs/files", jobsFilesHandler.ListJobs)
		api.GET("/jobs/files/:id", jobsFilesHandler.GetJob)
		api.DELETE("/jobs/files/:id", jobsFilesHandler.CancelJob)
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", config.ServerPort),
		Handler: r,
	}

	signalCtx, sigCancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer sigCancel()

	go func() {
		log.Info("Starting API Gateway", zap.Int("port", config.ServerPort))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Failed to start server", zap.Error(err))
		}
	}()

	<-signalCtx.Done()

	log.Info("Shutting down server")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("Server shutdown error", zap.Error(err))
	}

	log.Info("Server stopped")
}

