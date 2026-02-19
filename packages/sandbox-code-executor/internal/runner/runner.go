package runner

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/sandbox-code-executor/internal/piston"
	"github.com/e2b-dev/infra/packages/sandbox-code-executor/internal/store"
)

type Runner struct {
	store   *store.Store
	piston  *piston.Client
	log     *zap.Logger
	timeout time.Duration
	maxOut  int
}

func NewRunner(s *store.Store, p *piston.Client, log *zap.Logger, timeout time.Duration, maxOut int) *Runner {
	if maxOut <= 0 {
		maxOut = 2 * 1024 * 1024 // 2 MB
	}
	return &Runner{store: s, piston: p, log: log, timeout: timeout, maxOut: maxOut}
}

func (r *Runner) RunCodeJob(ctx context.Context, id string) {
	job, err := r.store.GetJobByID(ctx, store.JobKindCode, id)
	if err != nil || job == nil {
		r.log.Warn("job not found", zap.String("id", id))
		return
	}
	if job.Status != store.StatusPending {
		return
	}
	_ = r.store.SetStatus(ctx, store.JobKindCode, id, store.StatusRunning, nil)

	var payload store.JobPayloadCode
	if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
		_ = r.store.FailCodeJob(ctx, id, "invalid payload")
		return
	}
	timeoutMs := payload.Timeout * 1000
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}
	if timeoutMs > 300000 {
		timeoutMs = 300000
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(payload.Timeout+2)*time.Second)
	defer cancel()

	fileName := getFileName(payload.Lang)
	req := piston.ExecuteRequest{
		Language: payload.Lang,
		Files:    []piston.File{{Name: fileName, Content: payload.Code}},
		Timeout:  timeoutMs,
	}
	resp, err := r.piston.Execute(runCtx, req)
	if err != nil {
		if ctx.Err() != nil {
			_ = r.store.SetStatus(ctx, store.JobKindCode, id, store.StatusCancelled, &store.JobResult{Error: "cancelled"})
			return
		}
		_ = r.store.FailCodeJob(ctx, id, err.Error())
		return
	}
	stdout := resp.Run.Stdout
	if stdout == "" {
		stdout = resp.Run.Output
	}
	stdout = truncate(stdout, r.maxOut)
	stderr := truncate(resp.Run.Stderr, r.maxOut)
	if resp.Compile.Code != 0 {
		compileErr := resp.Compile.Stderr
		if compileErr == "" {
			compileErr = resp.Compile.Stdout
		}
		_ = r.store.CompleteCodeJob(ctx, id, &store.JobResult{Stdout: "", Stderr: compileErr})
		return
	}
	if resp.Run.Code != 0 && resp.Run.Signal == "SIGKILL" {
		_ = r.store.CompleteCodeJob(ctx, id, &store.JobResult{Stdout: stdout, Stderr: "Execution timeout exceeded"})
		return
	}
	_ = r.store.CompleteCodeJob(ctx, id, &store.JobResult{Stdout: stdout, Stderr: stderr})
}

func (r *Runner) RunFilesJob(ctx context.Context, id string) {
	job, err := r.store.GetJobByID(ctx, store.JobKindFiles, id)
	if err != nil || job == nil {
		r.log.Warn("job not found", zap.String("id", id))
		return
	}
	if job.Status != store.StatusPending {
		return
	}
	_ = r.store.SetStatus(ctx, store.JobKindFiles, id, store.StatusRunning, nil)

	var payload store.JobPayloadFiles
	if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
		_ = r.store.FailFilesJob(ctx, id, "invalid payload")
		return
	}
	timeoutMs := payload.Timeout * 1000
	if timeoutMs <= 0 {
		timeoutMs = 300000
	}
	if timeoutMs > 300000 {
		timeoutMs = 300000
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(payload.Timeout+2)*time.Second)
	defer cancel()

	files := make([]piston.File, len(payload.Files))
	for i, f := range payload.Files {
		files[i] = piston.File{Name: f.Name, Content: f.Content}
	}
	req := piston.ExecuteRequest{
		Language: payload.Lang,
		Files:    files,
		Timeout:  timeoutMs,
	}
	resp, err := r.piston.Execute(runCtx, req)
	if err != nil {
		if ctx.Err() != nil {
			_ = r.store.SetStatus(ctx, store.JobKindFiles, id, store.StatusCancelled, &store.JobResult{Error: "cancelled"})
			return
		}
		_ = r.store.FailFilesJob(ctx, id, err.Error())
		return
	}
	stdout := resp.Run.Stdout
	if stdout == "" {
		stdout = resp.Run.Output
	}
	stdout = truncate(stdout, r.maxOut)
	stderr := truncate(resp.Run.Stderr, r.maxOut)
	if resp.Compile.Code != 0 {
		compileErr := resp.Compile.Stderr
		if compileErr == "" {
			compileErr = resp.Compile.Stdout
		}
		_ = r.store.CompleteFilesJob(ctx, id, &store.JobResult{Stdout: "", Stderr: compileErr})
		return
	}
	if resp.Run.Code != 0 && resp.Run.Signal == "SIGKILL" {
		_ = r.store.CompleteFilesJob(ctx, id, &store.JobResult{Stdout: stdout, Stderr: "Execution timeout exceeded"})
		return
	}
	_ = r.store.CompleteFilesJob(ctx, id, &store.JobResult{Stdout: stdout, Stderr: stderr})
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
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
