package worker

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Result — результат выполнения задачи в пуле
type Result struct {
	Data  interface{}
	Error error
}

type poolTask struct {
	ctx      context.Context
	fn       func(context.Context) (interface{}, error)
	callback func(Result)
}

// Pool — пул воркеров для синхронного выполнения (POST /execute)
type Pool struct {
	workers    int
	tasks      chan poolTask
	wg         sync.WaitGroup
	logger     *zap.Logger
	once       sync.Once
	started    bool
	startMutex sync.Mutex
}

func NewPool(workers int, logger *zap.Logger) *Pool {
	if workers <= 0 {
		workers = 10
	}
	return &Pool{
		workers: workers,
		tasks:   make(chan poolTask, workers*2),
		logger:  logger,
	}
}

func (p *Pool) start() {
	p.startMutex.Lock()
	defer p.startMutex.Unlock()
	if p.started {
		return
	}
	p.started = true
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.runWorker(i)
	}
}

func (p *Pool) runWorker(id int) {
	defer p.wg.Done()
	for task := range p.tasks {
		if task.ctx.Err() != nil {
			if task.callback != nil {
				task.callback(Result{Error: task.ctx.Err()})
			}
			continue
		}
		var res Result
		res.Data, res.Error = task.fn(task.ctx)
		if task.callback != nil {
			task.callback(res)
		}
	}
}

func (p *Pool) Execute(ctx context.Context, fn func(context.Context) (interface{}, error)) Result {
	p.once.Do(p.start)
	resultChan := make(chan Result, 1)
	select {
	case p.tasks <- poolTask{
		ctx: ctx,
		fn:  fn,
		callback: func(r Result) { resultChan <- r },
	}:
	case <-ctx.Done():
		return Result{Error: ctx.Err()}
	case <-time.After(5 * time.Second):
		return Result{Error: context.DeadlineExceeded}
	}
	select {
	case r := <-resultChan:
		return r
	case <-ctx.Done():
		return Result{Error: ctx.Err()}
	}
}
