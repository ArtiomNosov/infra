package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/sandbox-code-executor/internal/config"
	"github.com/e2b-dev/infra/packages/sandbox-code-executor/internal/piston"
	"github.com/e2b-dev/infra/packages/sandbox-code-executor/internal/store"
	"github.com/e2b-dev/infra/packages/sandbox-code-executor/internal/worker"
)

type Handler struct {
	cfg          *config.Config
	store        *store.Store
	piston       *piston.Client
	workerPool   *worker.Pool
	log          *zap.Logger
	syncTimeout  time.Duration
	stdoutLimit  int
}

func NewHandler(cfg *config.Config, s *store.Store, p *piston.Client, wp *worker.Pool, log *zap.Logger) *Handler {
	return &Handler{
		cfg:         cfg,
		store:       s,
		piston:      p,
		workerPool:  wp,
		log:         log,
		syncTimeout: time.Duration(cfg.DefaultTimeout+2) * time.Second,
		stdoutLimit: cfg.StdoutMaxSize,
	}
}

// Environment — окружение (runtime) из Piston
type Environment struct {
	ID       string   `json:"id"`
	Language string   `json:"language"`
	Version  string   `json:"version"`
	Aliases  []string `json:"aliases,omitempty"`
}

func (h *Handler) ListEnvironments(c *gin.Context) {
	ctx := c.Request.Context()
	runtimes, err := h.piston.GetRuntimes(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var list []Environment
	for lang, vers := range runtimes {
		for _, v := range vers {
			list = append(list, Environment{
				ID:       lang + "-" + v.Version,
				Language: lang,
				Version:  v.Version,
				Aliases:  v.Aliases,
			})
		}
	}
	c.JSON(http.StatusOK, list)
}

func (h *Handler) CreateEnvironment(c *gin.Context) {
	// По ТЗ — создание окружения. Используем Piston, явное создание не требуется.
	c.JSON(http.StatusOK, gin.H{"message": "Use GET /environments for available runtimes"})
}

func (h *Handler) UpdateEnvironment(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id required"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "message": "updated"})
}

func (h *Handler) DeleteEnvironment(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id required"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "message": "deleted"})
}

// ExecuteRequest — синхронное исполнение (макс. 30 сек по ТЗ)
type ExecuteRequest struct {
	Lang    string `json:"lang" binding:"required"`
	Code    string `json:"code" binding:"required"`
	Timeout int    `json:"timeout"`
}

type ExecuteResponse struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

func (h *Handler) Execute(c *gin.Context) {
	var req ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Timeout <= 0 {
		req.Timeout = h.cfg.DefaultTimeout
	}
	if req.Timeout > h.cfg.DefaultTimeout {
		req.Timeout = h.cfg.DefaultTimeout // sync max 30 sec
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), h.syncTimeout)
	defer cancel()

	result := h.workerPool.Execute(ctx, func(ctx context.Context) (interface{}, error) {
		return h.runCode(ctx, req.Lang, req.Code, "", req.Timeout)
	})
	if result.Error != nil {
		h.log.Error("execute failed", zap.Error(result.Error))
		c.JSON(http.StatusInternalServerError, ExecuteResponse{Stderr: result.Error.Error()})
		return
	}
	res := result.Data.(*ExecuteResponse)
	c.JSON(http.StatusOK, res)
}

func (h *Handler) runCode(ctx context.Context, lang, code, stdin string, timeoutSec int) (*ExecuteResponse, error) {
	fileName := getFileName(lang)
	timeoutMs := timeoutSec * 1000
	if timeoutMs > 300000 {
		timeoutMs = 300000
	}
	req := piston.ExecuteRequest{
		Language: lang,
		Files:    []piston.File{{Name: fileName, Content: code}},
		Stdin:    stdin,
		Timeout:  timeoutMs,
	}
	resp, err := h.piston.Execute(ctx, req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &ExecuteResponse{Stderr: "Execution timeout exceeded"}, nil
		}
		return nil, err
	}
	stdout := resp.Run.Stdout
	if stdout == "" {
		stdout = resp.Run.Output
	}
	stdout = truncate(stdout, h.stdoutLimit)
	stderr := truncate(resp.Run.Stderr, h.stdoutLimit)
	if resp.Compile.Code != 0 {
		compileErr := resp.Compile.Stderr
		if compileErr == "" {
			compileErr = resp.Compile.Stdout
		}
		return &ExecuteResponse{Stdout: "", Stderr: compileErr}, nil
	}
	if resp.Run.Code != 0 && resp.Run.Signal == "SIGKILL" {
		return &ExecuteResponse{Stdout: stdout, Stderr: "Execution timeout exceeded"}, nil
	}
	return &ExecuteResponse{Stdout: stdout, Stderr: stderr}, nil
}

// POST /jobs/code — асинхронная задача (inline код, до 5 мин)
type JobCodeRequest struct {
	Lang    string `json:"lang" binding:"required"`
	Code    string `json:"code" binding:"required"`
	Timeout int    `json:"timeout"`
}

func (h *Handler) CreateJobCode(c *gin.Context) {
	var req JobCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Timeout <= 0 {
		req.Timeout = h.cfg.DefaultTimeout
	}
	if req.Timeout > h.cfg.MaxTimeout {
		req.Timeout = h.cfg.MaxTimeout
	}
	id, err := h.store.EnqueueCode(c.Request.Context(), &store.JobPayloadCode{
		Lang: req.Lang, Code: req.Code, Timeout: req.Timeout,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"id": id, "status": "pending"})
}

func (h *Handler) ListJobsCode(c *gin.Context) {
	jobs, err := h.store.ListJobs(c.Request.Context(), store.JobKindCode)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, jobs)
}

func (h *Handler) GetJobCode(c *gin.Context) {
	id := c.Param("id")
	job, err := h.store.GetJobByID(c.Request.Context(), store.JobKindCode, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if job == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}
	c.JSON(http.StatusOK, job)
}

func (h *Handler) CancelJobCode(c *gin.Context) {
	id := c.Param("id")
	if err := h.store.CancelJob(c.Request.Context(), store.JobKindCode, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "status": "cancelled"})
}

// POST /jobs/files — асинхронная задача (файлы + команда)
type JobFilesRequest struct {
	Lang    string              `json:"lang" binding:"required"`
	Files   []store.FilePayload `json:"files" binding:"required"`
	Command string              `json:"command"`
	Timeout int                 `json:"timeout"`
}

func (h *Handler) CreateJobFiles(c *gin.Context) {
	var req JobFilesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Files) > config.MaxFilesPerJob {
		c.JSON(http.StatusBadRequest, gin.H{"error": "too many files"})
		return
	}
	var total int
	for i := range req.Files {
		if len(req.Files[i].Content) > config.MaxFileSize {
			c.JSON(http.StatusBadRequest, gin.H{"error": "file too large"})
			return
		}
		total += len(req.Files[i].Content)
	}
	if total > config.MaxTotalFilesSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "total files size exceeded"})
		return
	}
	if req.Timeout <= 0 {
		req.Timeout = h.cfg.DefaultTimeout
	}
	if req.Timeout > h.cfg.MaxTimeout {
		req.Timeout = h.cfg.MaxTimeout
	}
	id, err := h.store.EnqueueFiles(c.Request.Context(), &store.JobPayloadFiles{
		Lang: req.Lang, Files: req.Files, Command: req.Command, Timeout: req.Timeout,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"id": id, "status": "pending"})
}

func (h *Handler) ListJobsFiles(c *gin.Context) {
	jobs, err := h.store.ListJobs(c.Request.Context(), store.JobKindFiles)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, jobs)
}

func (h *Handler) GetJobFiles(c *gin.Context) {
	id := c.Param("id")
	job, err := h.store.GetJobByID(c.Request.Context(), store.JobKindFiles, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if job == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}
	c.JSON(http.StatusOK, job)
}

func (h *Handler) CancelJobFiles(c *gin.Context) {
	id := c.Param("id")
	if err := h.store.CancelJob(c.Request.Context(), store.JobKindFiles, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "status": "cancelled"})
}

func (h *Handler) Health(c *gin.Context) {
	// Проверка Redis
	if err := h.store.Ping(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) Stats(c *gin.Context) {
	ctx := c.Request.Context()
	codeJobs, _ := h.store.ListJobs(ctx, store.JobKindCode)
	filesJobs, _ := h.store.ListJobs(ctx, store.JobKindFiles)
	var pending, running, completed, failed int
	for _, j := range codeJobs {
		switch j.Status {
		case store.StatusPending:
			pending++
		case store.StatusRunning:
			running++
		case store.StatusCompleted:
			completed++
		case store.StatusFailed, store.StatusCancelled:
			failed++
		}
	}
	for _, j := range filesJobs {
		switch j.Status {
		case store.StatusPending:
			pending++
		case store.StatusRunning:
			running++
		case store.StatusCompleted:
			completed++
		case store.StatusFailed, store.StatusCancelled:
			failed++
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"jobs": gin.H{
			"pending":   pending,
			"running":   running,
			"completed": completed,
			"failed":    failed,
		},
		"workers": h.cfg.WorkerPoolSize,
	})
}

func getFileName(lang string) string {
	m := map[string]string{
		"python": "main.py", "node": "main.js", "javascript": "main.js", "typescript": "main.ts",
		"java": "Main.java", "gcc": "main.c", "cpp": "main.cpp", "c": "main.c",
		"go": "main.go", "rust": "main.rs", "ruby": "main.rb", "php": "main.php",
	}
	if name, ok := m[lang]; ok {
		return name
	}
	if idx := strings.Index(lang, "."); idx > 0 {
		lang = lang[:idx]
	}
	return "main." + lang
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max]
}
