package handlers

import (
	"net/http"
	"os/exec"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"github.com/e2b-dev/infra/packages/api-gateway/internal/storage"
)

type EnvironmentsHandler struct {
	envStorage *storage.EnvironmentStorage
	envFile    *storage.EnvironmentsFile
}

func NewEnvironmentsHandler(envStorage *storage.EnvironmentStorage, envFile *storage.EnvironmentsFile) *EnvironmentsHandler {
	return &EnvironmentsHandler{
		envStorage: envStorage,
		envFile:    envFile,
	}
}

type GetEnvironmentsResponse struct {
	Environments []*storage.Environment `json:"environments"`
}

func (h *EnvironmentsHandler) GetEnvironments(c *gin.Context) {
	language := c.Query("language")

	envs, err := h.envStorage.List(c.Request.Context(), language)
	if err != nil {
		zap.L().Error("Failed to list environments", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	c.JSON(http.StatusOK, GetEnvironmentsResponse{Environments: envs})
}

type CreateEnvironmentRequest struct {
	ID       string   `json:"id" binding:"required"`
	Image    string   `json:"image" binding:"required"`
	Language string   `json:"language" binding:"required"`
	Packages []string `json:"packages"`
}

func (h *EnvironmentsHandler) CreateEnvironment(c *gin.Context) {
	var req CreateEnvironmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	exists, err := h.envStorage.Exists(ctx, req.ID)
	if err != nil {
		zap.L().Error("Failed to check environment existence", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	if exists {
		c.JSON(http.StatusConflict, gin.H{"error": "Environment ID already exists"})
		return
	}

	cmd := exec.Command("docker", "image", "inspect", req.Image)
	if err := cmd.Run(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Docker image does not exist"})
		return
	}

	env := &storage.Environment{
		ID:       req.ID,
		Image:    req.Image,
		Language: req.Language,
		Packages: req.Packages,
	}

	if err := h.envStorage.Set(ctx, env); err != nil {
		zap.L().Error("Failed to save environment", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	config, err := h.envFile.Load()
	if err != nil {
		zap.L().Error("Failed to load environments config", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	config.Environments = append(config.Environments, env)
	if err := h.envFile.Save(config); err != nil {
		zap.L().Error("Failed to save environments config", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	c.JSON(http.StatusCreated, env)
}

type UpdateEnvironmentRequest struct {
	Image    *string   `json:"image"`
	Language *string   `json:"language"`
	Packages *[]string `json:"packages"`
}

func (h *EnvironmentsHandler) UpdateEnvironment(c *gin.Context) {
	id := c.Param("id")

	ctx := c.Request.Context()

	env, err := h.envStorage.Get(ctx, id)
	if err != nil {
		zap.L().Error("Failed to get environment", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	if env == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Environment not found"})
		return
	}

	var req UpdateEnvironmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Image != nil {
		cmd := exec.Command("docker", "image", "inspect", *req.Image)
		if err := cmd.Run(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Docker image does not exist"})
			return
		}
		env.Image = *req.Image
	}
	if req.Language != nil {
		env.Language = *req.Language
	}
	if req.Packages != nil {
		env.Packages = *req.Packages
	}

	if err := h.envStorage.Set(ctx, env); err != nil {
		zap.L().Error("Failed to update environment", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	config, err := h.envFile.Load()
	if err != nil {
		zap.L().Error("Failed to load environments config", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	for i, e := range config.Environments {
		if e.ID == id {
			config.Environments[i] = env
			break
		}
	}

	if err := h.envFile.Save(config); err != nil {
		zap.L().Error("Failed to save environments config", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	c.JSON(http.StatusOK, env)
}

func (h *EnvironmentsHandler) DeleteEnvironment(c *gin.Context) {
	id := c.Param("id")

	ctx := c.Request.Context()

	env, err := h.envStorage.Get(ctx, id)
	if err != nil {
		zap.L().Error("Failed to get environment", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	if env == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Environment not found"})
		return
	}

	jobStorage := storage.NewJobStorage(h.envStorage.GetClient())
	jobs, err := jobStorage.FindByEnvironment(ctx, id, []storage.JobStatus{
		storage.StatusQueued,
		storage.StatusRunning,
	})
	if err != nil {
		zap.L().Error("Failed to find jobs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	for _, job := range jobs {
		if err := jobStorage.Cancel(ctx, job.JobID); err != nil {
			zap.L().Warn("Failed to cancel job", zap.String("job_id", job.JobID), zap.Error(err))
		}
	}

	if err := h.envStorage.Delete(ctx, id); err != nil {
		zap.L().Error("Failed to delete environment", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	config, err := h.envFile.Load()
	if err != nil {
		zap.L().Error("Failed to load environments config", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	var filtered []*storage.Environment
	for _, e := range config.Environments {
		if e.ID != id {
			filtered = append(filtered, e)
		}
	}
	config.Environments = filtered

	if err := h.envFile.Save(config); err != nil {
		zap.L().Error("Failed to save environments config", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	rdb := h.envStorage.GetClient()
	rdb.Publish(ctx, "env:updated", `{"action":"delete","id":"`+id+`"}`)

	c.JSON(http.StatusOK, gin.H{"message": "Environment deleted"})
}

