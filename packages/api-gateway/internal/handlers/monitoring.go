package handlers

import (
	"context"
	"net/http"
	"time"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"github.com/e2b-dev/infra/packages/api-gateway/internal/storage"
	"github.com/e2b-dev/infra/packages/api-gateway/internal/worker"
)

type MonitoringHandler struct {
	redisClient  interface{}
	envStorage   *storage.EnvironmentStorage
	jobStorage   *storage.JobStorage
	workerClient *worker.Client
	startTime    time.Time
}

func NewMonitoringHandler(redisClient interface{}, envStorage *storage.EnvironmentStorage, jobStorage *storage.JobStorage, workerClient *worker.Client) *MonitoringHandler {
	return &MonitoringHandler{
		redisClient:  redisClient,
		envStorage:   envStorage,
		jobStorage:   jobStorage,
		workerClient: workerClient,
		startTime:    time.Now(),
	}
}

type HealthResponse struct {
	Status        string `json:"status"`
	Redis         string `json:"redis"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

func (h *MonitoringHandler) Health(c *gin.Context) {
	ctx := c.Request.Context()

	redisStatus := "disconnected"
	if h.redisClient != nil {
		rdb := h.envStorage.GetClient()
		if err := rdb.Ping(ctx).Err(); err == nil {
			redisStatus = "connected"
		}
	}

	uptime := time.Since(h.startTime).Seconds()

	c.JSON(http.StatusOK, HealthResponse{
		Status:        "ok",
		Redis:         redisStatus,
		UptimeSeconds: int64(uptime),
	})
}

type StatsResponse struct {
	UptimeSeconds int64                    `json:"uptime_seconds"`
	Jobs          StatsJobs                 `json:"jobs"`
	Environments  StatsEnvironments         `json:"environments"`
	Workers       StatsWorkers              `json:"workers"`
	CurrentJobs   []StatsCurrentJob         `json:"current_jobs"`
}

type StatsJobs struct {
	Code  JobTypeStats `json:"code"`
	Files JobTypeStats `json:"files"`
}

type JobTypeStats struct {
	Queued         int `json:"queued"`
	Running        int `json:"running"`
	CompletedTotal int `json:"completed_total"`
	FailedTotal    int `json:"failed_total"`
	TimeoutTotal   int `json:"timeout_total"`
}

type StatsEnvironments struct {
	Total int `json:"total"`
}

type StatsWorkers struct {
	Connected int `json:"connected"`
}

type StatsCurrentJob struct {
	JobID          string `json:"job_id"`
	Type           string `json:"type"`
	Environment    string `json:"environment"`
	Status         string `json:"status"`
	RunningSeconds int    `json:"running_seconds"`
}

func (h *MonitoringHandler) Stats(c *gin.Context) {
	ctx := c.Request.Context()

	uptime := time.Since(h.startTime).Seconds()

	codeJobs, _, _ := h.jobStorage.List(ctx, storage.JobTypeCode, "", "", 10000, 0)
	filesJobs, _, _ := h.jobStorage.List(ctx, storage.JobTypeFiles, "", "", 10000, 0)

	codeStats := calculateJobStats(codeJobs)
	filesStats := calculateJobStats(filesJobs)

	envs, _ := h.envStorage.List(ctx, "")
	workers, _ := h.workerClient.GetAvailableWorkers(ctx)

	runningJobs, _, _ := h.jobStorage.List(ctx, storage.JobTypeCode, "running", "", 100, 0)
	filesRunningJobs, _, _ := h.jobStorage.List(ctx, storage.JobTypeFiles, "running", "", 100, 0)
	runningJobs = append(runningJobs, filesRunningJobs...)

	currentJobs := make([]StatsCurrentJob, 0, len(runningJobs))
	for _, job := range runningJobs {
		if job.StartedAt != nil {
			runningSeconds := int(time.Since(*job.StartedAt).Seconds())
			currentJobs = append(currentJobs, StatsCurrentJob{
				JobID:          job.JobID,
				Type:           string(job.Type),
				Environment:    job.Environment,
				Status:         string(job.Status),
				RunningSeconds: runningSeconds,
			})
		}
	}

	c.JSON(http.StatusOK, StatsResponse{
		UptimeSeconds: int64(uptime),
		Jobs: StatsJobs{
			Code:  codeStats,
			Files: filesStats,
		},
		Environments: StatsEnvironments{
			Total: len(envs),
		},
		Workers: StatsWorkers{
			Connected: workers,
		},
		CurrentJobs: currentJobs,
	})
}

func calculateJobStats(jobs []*storage.Job) JobTypeStats {
	stats := JobTypeStats{}
	for _, job := range jobs {
		switch job.Status {
		case storage.StatusQueued:
			stats.Queued++
		case storage.StatusRunning:
			stats.Running++
		case storage.StatusCompleted:
			stats.CompletedTotal++
		case storage.StatusFailed:
			stats.FailedTotal++
		case storage.StatusTimeout:
			stats.TimeoutTotal++
		}
	}
	return stats
}

