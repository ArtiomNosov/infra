package worker

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/sandbox-code-executor/internal/runner"
	"github.com/e2b-dev/infra/packages/sandbox-code-executor/internal/store"
)

// QueueWorker потребляет задачи из Redis и выполняет их через Runner
type QueueWorker struct {
	store  *store.Store
	runner *runner.Runner
	log    *zap.Logger
	n      int
}

func NewQueueWorker(s *store.Store, r *runner.Runner, log *zap.Logger, n int) *QueueWorker {
	return &QueueWorker{store: s, runner: r, log: log, n: n}
}

func (w *QueueWorker) Start(ctx context.Context) {
	for i := 0; i < w.n; i++ {
		go w.loopCode(ctx, i)
		go w.loopFiles(ctx, i)
	}
}

func (w *QueueWorker) loopCode(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		jobID, err := w.store.DequeueCode(ctx, 2*time.Second)
		if err != nil {
			w.log.Warn("dequeue code error", zap.Error(err))
			continue
		}
		if jobID == "" {
			continue
		}
		w.runner.RunCodeJob(ctx, jobID)
	}
}

func (w *QueueWorker) loopFiles(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		jobID, err := w.store.DequeueFiles(ctx, 2*time.Second)
		if err != nil {
			w.log.Warn("dequeue files error", zap.Error(err))
			continue
		}
		if jobID == "" {
			continue
		}
		w.runner.RunFilesJob(ctx, jobID)
	}
}
