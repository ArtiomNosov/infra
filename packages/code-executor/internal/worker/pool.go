package worker

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)


type Pool struct {
	workers     int
	tasks       chan task
	wg          sync.WaitGroup
	logger      *zap.Logger
	once        sync.Once
	started     bool
	startMutex  sync.Mutex
	pendingWg   sync.WaitGroup 
}


type task struct {
	ctx      context.Context
	fn       func(context.Context) (interface{}, error)
	callback func(Result)
}


type Result struct {
	Data  interface{}
	Error error
}


func NewPool(workers int, logger *zap.Logger) *Pool {
	if workers <= 0 {
		workers = 10 
	}

	pool := &Pool{
		workers: workers,
		tasks:   make(chan task, workers*2), 
		logger:  logger,
	}

	return pool
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
		go p.worker(i)
	}
}


func (p *Pool) worker(id int) {
	defer p.wg.Done()

	for task := range p.tasks {
		
		if task.ctx.Err() != nil {
			if task.callback != nil {
				task.callback(Result{
					Data:  nil,
					Error: task.ctx.Err(),
				})
			}
			continue
		}

		
		result := Result{}
		result.Data, result.Error = task.fn(task.ctx)

		
		if task.callback != nil {
			task.callback(result)
		}
	}
}


func (p *Pool) Execute(ctx context.Context, fn func(context.Context) (interface{}, error)) Result {
	p.once.Do(p.start)

	
	resultChan := make(chan Result, 1)

	
	select {
	case p.tasks <- task{
		ctx: ctx,
		fn:  fn,
		callback: func(result Result) {
			resultChan <- result
		},
	}:
		
	case <-ctx.Done():
		return Result{
			Data:  nil,
			Error: ctx.Err(),
		}
	case <-time.After(5 * time.Second):
		
		return Result{
			Data:  nil,
			Error: context.DeadlineExceeded,
		}
	}

	
	select {
	case result := <-resultChan:
		return result
	case <-ctx.Done():
		return Result{
			Data:  nil,
			Error: ctx.Err(),
		}
	}
}


func (p *Pool) ExecuteAsync(ctx context.Context, fn func(context.Context) (interface{}, error), callback func(Result)) {
	p.once.Do(p.start)
	p.pendingWg.Add(1)

	select {
	case p.tasks <- task{
		ctx:      ctx,
		fn:       fn,
		callback: func(result Result) {
			defer p.pendingWg.Done()
			if callback != nil {
				callback(result)
			}
		},
	}:
		
	case <-ctx.Done():
		p.pendingWg.Done()
		if callback != nil {
			callback(Result{
				Data:  nil,
				Error: ctx.Err(),
			})
		}
	case <-time.After(5 * time.Second):
		
		p.pendingWg.Done()
		if callback != nil {
			callback(Result{
				Data:  nil,
				Error: context.DeadlineExceeded,
			})
		}
	}
}


func (p *Pool) Wait() {
	p.pendingWg.Wait()
}

