package handlers

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"github.com/gin-gonic/gin"
	"github.com/matoous/go-nanoid/v2"
	"go.uber.org/zap"
	"github.com/e2b-dev/infra/packages/api-gateway/internal/storage"
)

type JobsFilesHandler struct {
	envStorage *storage.EnvironmentStorage
	jobStorage *storage.JobStorage
	config Config
}

func NewJobsFilesHandler(envStorage *storage.EnvironmentStorage, jobStorage *storage.JobStorage, config Config) *JobsFilesHandler {
	return &JobsFilesHandler{
		envStorage: envStorage,
		jobStorage: jobStorage,
		config: config,
	}
}

type CreateJobFilesRequest struct {
	Environment string            `json:"environment" binding:"required"`
	Files       []storage.JobFile `json:"files" binding:"required"`
	Command     string            `json:"command" binding:"required"`
	Stdin       string            `json:"stdin"`
	Timeout     int               `json:"timeout"`
	Network     bool              `json:"network"`
}

type CreateJobFilesResponse struct {
	JobID     string    `json:"job_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func validateFilePath(path string) error {
	if filepath.IsAbs(path) {
		return fmt.Errorf("absolute paths are not allowed")
	}

	cleaned := filepath.Clean(path)
	if strings.HasPrefix(cleaned, "..") {
		return fmt.Errorf("paths outside directory are not allowed")
	}

	return nil
}

func (h *JobsFilesHandler) CreateJob(c *gin.Context) {
	var req CreateJobFilesRequest
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

	if len(req.Files) > h.config.MaxFiles {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Too many files"})
		return
	}

	totalSize := 0
	for i := range req.Files {
		file := &req.Files[i]

		if err := validateFilePath(file.Name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid file path: " + file.Name})
			return
		}

		var content []byte
		if file.Encoding == "base64" {
			decoded, err := base64.StdEncoding.DecodeString(file.Content)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid base64 encoding for file: " + file.Name})
				return
			}
			content = decoded
		} else {
			content = []byte(file.Content)
		}

		if len(content) > h.config.MaxFileSize {
			c.JSON(http.StatusBadRequest, gin.H{"error": "File too large: " + file.Name})
			return
		}

		totalSize += len(content)
		file.Content = string(content)
		file.Encoding = "text"
	}

	if totalSize > h.config.MaxTotalSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Total size exceeds limit"})
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
		Type:       storage.JobTypeFiles,
		Environment: req.Environment,
		Status:     storage.StatusQueued,
		Files:      req.Files,
		Command:    req.Command,
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

	c.JSON(http.StatusAccepted, CreateJobFilesResponse{
		JobID:     jobID,
		Status:    string(storage.StatusQueued),
		CreatedAt: now,
	})
}

type GetJobFilesResponse struct {
	JobID      string             `json:"job_id"`
	Environment string            `json:"environment"`
	Status     string             `json:"status"`
	CreatedAt  time.Time          `json:"created_at"`
	StartedAt  *time.Time          `json:"started_at,omitempty"`
	FinishedAt *time.Time          `json:"finished_at,omitempty"`
	Result     *storage.JobResult  `json:"result,omitempty"`
}

func (h *JobsFilesHandler) GetJob(c *gin.Context) {
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

	if job.Type != storage.JobTypeFiles {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	resp := GetJobFilesResponse{
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

type ListJobsFilesResponse struct {
	Jobs   []GetJobFilesResponse `json:"jobs"`
	Total  int                   `json:"total"`
	Limit  int                   `json:"limit"`
	Offset int                   `json:"offset"`
}

func (h *JobsFilesHandler) ListJobs(c *gin.Context) {
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

	jobs, total, err := h.jobStorage.List(ctx, storage.JobTypeFiles, statusFilter, envFilter, limit, offset)
	if err != nil {
		zap.L().Error("Failed to list jobs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	respJobs := make([]GetJobFilesResponse, len(jobs))
	for i, job := range jobs {
		respJobs[i] = GetJobFilesResponse{
			JobID:      job.JobID,
			Environment: job.Environment,
			Status:     string(job.Status),
			CreatedAt:  job.CreatedAt,
			StartedAt:  job.StartedAt,
			FinishedAt: job.FinishedAt,
			Result:     job.Result,
		}
	}

	c.JSON(http.StatusOK, ListJobsFilesResponse{
		Jobs:   respJobs,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

func (h *JobsFilesHandler) CancelJob(c *gin.Context) {
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

	if job.Type != storage.JobTypeFiles {
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

