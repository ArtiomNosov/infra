package handlers

import (
	"net/http"
	"strconv"
	"time"
	"github.com/gin-gonic/gin"
	"github.com/matoous/go-nanoid/v2"
	"go.uber.org/zap"
	"github.com/e2b-dev/infra/packages/api-gateway/internal/storage"
)

type JobsCodeHandler struct {
	envStorage *storage.EnvironmentStorage
	jobStorage *storage.JobStorage
	config Config
}

func NewJobsCodeHandler(envStorage *storage.EnvironmentStorage, jobStorage *storage.JobStorage, config Config) *JobsCodeHandler {
	return &JobsCodeHandler{
		envStorage: envStorage,
		jobStorage: jobStorage,
		config: config,
	}
}

type CreateJobCodeRequest struct {
	Environment string `json:"environment" binding:"required"`
	Code        string `json:"code" binding:"required"`
	Stdin       string `json:"stdin"`
	Timeout     int    `json:"timeout"`
	Network     bool   `json:"network"`
}

type CreateJobCodeResponse struct {
	JobID     string    `json:"job_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *JobsCodeHandler) CreateJob(c *gin.Context) {
	var req CreateJobCodeRequest
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
	if timeout > h.config.MaxTimeout {
		timeout = h.config.MaxTimeout
	}

	jobID, err := nanoid.New()
	if err != nil {
		zap.L().Error("Failed to generate job ID", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	now := time.Now()
	job := &storage.Job{
		JobID:      jobID,
		Type:       storage.JobTypeCode,
		Environment: req.Environment,
		Status:     storage.StatusQueued,
		Code:       req.Code,
		Stdin:      req.Stdin,
		Timeout:    timeout,
		Network:    req.Network,
		CreatedAt:  now,
	}

	if err := h.jobStorage.Create(ctx, job); err != nil {
		zap.L().Error("Failed to create job", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	c.JSON(http.StatusAccepted, CreateJobCodeResponse{
		JobID:     jobID,
		Status:    string(storage.StatusQueued),
		CreatedAt: now,
	})
}

type GetJobCodeResponse struct {
	JobID      string             `json:"job_id"`
	Environment string            `json:"environment"`
	Status     string             `json:"status"`
	CreatedAt  time.Time          `json:"created_at"`
	StartedAt  *time.Time          `json:"started_at,omitempty"`
	FinishedAt *time.Time          `json:"finished_at,omitempty"`
	Result     *storage.JobResult  `json:"result,omitempty"`
}

func (h *JobsCodeHandler) GetJob(c *gin.Context) {
	jobID := c.Param("id")

	ctx := c.Request.Context()

	job, err := h.jobStorage.Get(ctx, jobID)
	if err != nil {
		zap.L().Error("Failed to get job", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	if job == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	if job.Type != storage.JobTypeCode {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	resp := GetJobCodeResponse{
		JobID:      job.JobID,
		Environment: job.Environment,
		Status:     string(job.Status),
		CreatedAt:  job.CreatedAt,
		StartedAt:  job.StartedAt,
		FinishedAt: job.FinishedAt,
		Result:     job.Result,
	}

	c.JSON(http.StatusOK, resp)
}

type ListJobsCodeResponse struct {
	Jobs  []GetJobCodeResponse `json:"jobs"`
	Total int                  `json:"total"`
	Limit int                  `json:"limit"`
	Offset int                 `json:"offset"`
}

func (h *JobsCodeHandler) ListJobs(c *gin.Context) {
	statusFilter := c.Query("status")
	envFilter := c.Query("environment")

	limit := 100
	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	offset := 0
	if offsetStr := c.Query("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	ctx := c.Request.Context()

	jobs, total, err := h.jobStorage.List(ctx, storage.JobTypeCode, statusFilter, envFilter, limit, offset)
	if err != nil {
		zap.L().Error("Failed to list jobs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	respJobs := make([]GetJobCodeResponse, len(jobs))
	for i, job := range jobs {
		respJobs[i] = GetJobCodeResponse{
			JobID:      job.JobID,
			Environment: job.Environment,
			Status:     string(job.Status),
			CreatedAt:  job.CreatedAt,
			StartedAt:  job.StartedAt,
			FinishedAt: job.FinishedAt,
			Result:     job.Result,
		}
	}

	c.JSON(http.StatusOK, ListJobsCodeResponse{
		Jobs:   respJobs,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

func (h *JobsCodeHandler) CancelJob(c *gin.Context) {
	jobID := c.Param("id")

	ctx := c.Request.Context()

	job, err := h.jobStorage.Get(ctx, jobID)
	if err != nil {
		zap.L().Error("Failed to get job", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	if job == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	if job.Type != storage.JobTypeCode {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	if job.Status == storage.StatusCompleted || job.Status == storage.StatusFailed || job.Status == storage.StatusTimeout {
		c.JSON(http.StatusConflict, gin.H{
			"error":          "Cannot cancel job",
			"current_status": string(job.Status),
		})
		return
	}

	if err := h.jobStorage.Cancel(ctx, jobID); err != nil {
		zap.L().Error("Failed to cancel job", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Job cancelled"})
}

