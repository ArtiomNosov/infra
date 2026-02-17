package handlers

import (
	"net/http"
	"time"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"github.com/e2b-dev/infra/packages/api-gateway/internal/storage"
	"github.com/e2b-dev/infra/packages/api-gateway/internal/worker"
)

type ExecuteHandler struct {
	envStorage *storage.EnvironmentStorage
	jobStorage *storage.JobStorage
	workerClient *worker.Client
	config Config
}

func NewExecuteHandler(envStorage *storage.EnvironmentStorage, jobStorage *storage.JobStorage, workerClient *worker.Client, config Config) *ExecuteHandler {
	return &ExecuteHandler{
		envStorage: envStorage,
		jobStorage: jobStorage,
		workerClient: workerClient,
		config: config,
	}
}

type ExecuteRequest struct {
	Environment string `json:"environment" binding:"required"`
	Code        string `json:"code" binding:"required"`
	Stdin       string `json:"stdin"`
	Timeout     int    `json:"timeout"`
	Network     bool   `json:"network"`
}

type ExecuteResponse struct {
	Status          string `json:"status"`
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	ExecutionTimeMs int64  `json:"execution_time_ms"`
}

func (h *ExecuteHandler) Execute(c *gin.Context) {
	var req ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	env, err := h.envStorage.Get(ctx, req.Environment)
	if err != nil {
		zap.L().Error("Failed to get environment", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	if env == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Environment not found"})
		return
	}

	timeout := req.Timeout
	if timeout == 0 {
		timeout = 30
	}
	if timeout > h.config.SyncMaxTimeout {
		timeout = h.config.SyncMaxTimeout
	}

	workers, err := h.workerClient.GetAvailableWorkers(ctx)
	if err != nil {
		zap.L().Warn("Failed to check workers", zap.Error(err))
	}
	if workers == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "No available workers"})
		return
	}

	execReq := &worker.ExecutionRequest{
		JobID:      "",
		Type:       storage.JobTypeCode,
		Environment: req.Environment,
		Code:       req.Code,
		Stdin:      req.Stdin,
		Timeout:    timeout,
		Network:    req.Network,
	}

	startTime := time.Now()
	resp, err := h.workerClient.ExecuteSync(ctx, execReq)
	if err != nil {
		zap.L().Error("Execution failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Execution failed"})
		return
	}

	executionTime := time.Since(startTime).Milliseconds()

	status := "completed"
	if resp.ExitCode != 0 {
		status = "failed"
	}
	if executionTime > int64(timeout*1000) {
		status = "timeout"
	}

	result := ExecuteResponse{
		Status:          status,
		ExitCode:        resp.ExitCode,
		Stdout:          resp.Stdout,
		Stderr:          resp.Stderr,
		ExecutionTimeMs: executionTime,
	}

	c.JSON(http.StatusOK, result)
}


