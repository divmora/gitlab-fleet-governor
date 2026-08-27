package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Common worker pool errors.
var (
	ErrPoolClosed   = errors.New("worker pool is closed")
	ErrPoolFull     = errors.New("worker pool task queue is full")
	ErrTaskNil      = errors.New("task cannot be nil")
	ErrPoolCanceled = errors.New("worker pool context was canceled")
)

// Task represents a discrete unit of executable work.
type Task func(ctx context.Context) error

// taskEnvelope wraps a Task with optional error propagation.
type taskEnvelope struct {
	task    Task
	errChan chan<- error
}

// WorkerPoolOption configures a WorkerPool instance.
type WorkerPoolOption func(*WorkerPool)

// WithWorkerCount sets the number of concurrent worker goroutines.
func WithWorkerCount(n int) WorkerPoolOption {
	return func(p *WorkerPool) {
		if n > 0 {
			p.concurrency = n
		}
	}
}

// WithQueueSize sets the capacity of the task submission queue.
func WithQueueSize(n int) WorkerPoolOption {
	return func(p *WorkerPool) {
		if n > 0 {
			p.queueSize = n
		}
	}
}

// WithErrorHandler sets an asynchronous error callback.
func WithErrorHandler(fn func(error)) WorkerPoolOption {
	return func(p *WorkerPool) {
		p.onErr = fn
	}
}

// WorkerPool provides a bounded concurrency task execution pool with channel dispatch,
// panic recovery, error collection, and graceful context-aware shutdown.
type WorkerPool struct {
	concurrency int
	queueSize   int
	tasks       chan taskEnvelope
	errors      []error
	errMu       sync.Mutex
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	closed      atomic.Bool
	started     atomic.Bool
	onErr       func(error)
}

// NewWorkerPool creates and initializes a new bounded WorkerPool.
func NewWorkerPool(ctx context.Context, concurrency int, opts ...WorkerPoolOption) *WorkerPool {
	if concurrency <= 0 {
		concurrency = 10
	}

	poolCtx, cancel := context.WithCancel(ctx)
	p := &WorkerPool{
		concurrency: concurrency,
		queueSize:   concurrency * 4,
		errors:      make([]error, 0),
		ctx:         poolCtx,
		cancel:      cancel,
	}

	for _, opt := range opts {
		opt(p)
	}

	if p.queueSize <= 0 {
		p.queueSize = p.concurrency * 4
	}

	p.tasks = make(chan taskEnvelope, p.queueSize)
	p.start()
	return p
}

// start launches the worker goroutines.
func (p *WorkerPool) start() {
	if p.started.Swap(true) {
		return
	}

	for i := 0; i < p.concurrency; i++ {
		p.wg.Add(1)
		go p.worker()
	}
}

// worker is the long-lived goroutine loop consuming and executing tasks.
func (p *WorkerPool) worker() {
	defer p.wg.Done()

	for {
		select {
		case <-p.ctx.Done():
			return
		case env, ok := <-p.tasks:
			if !ok {
				return
			}
			p.executeTask(env)
		}
	}
}

// executeTask executes a single task with panic recovery and error recording.
func (p *WorkerPool) executeTask(env taskEnvelope) {
	var err error
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, false)
			err = fmt.Errorf("worker panic recovered: %v\nstack:\n%s", r, string(buf[:n]))
			p.recordError(err)
		}
		if env.errChan != nil {
			env.errChan <- err
		}
	}()

	if env.task == nil {
		return
	}

	select {
	case <-p.ctx.Done():
		err = p.ctx.Err()
		p.recordError(err)
		return
	default:
	}

	err = env.task(p.ctx)
	if err != nil {
		p.recordError(err)
	}
}

// recordError safely appends an error to the pool's error list.
func (p *WorkerPool) recordError(err error) {
	if err == nil {
		return
	}
	p.errMu.Lock()
	p.errors = append(p.errors, err)
	p.errMu.Unlock()

	if p.onErr != nil {
		p.onErr(err)
	}
}

// Submit enqueues a task for execution. Blocks if the queue is full unless context is canceled.
func (p *WorkerPool) Submit(task Task) error {
	if task == nil {
		return ErrTaskNil
	}
	if p.closed.Load() {
		return ErrPoolClosed
	}

	select {
	case <-p.ctx.Done():
		return p.ctx.Err()
	case p.tasks <- taskEnvelope{task: task}:
		return nil
	}
}

// SubmitWithTimeout attempts to enqueue a task within the specified timeout.
func (p *WorkerPool) SubmitWithTimeout(task Task, timeout time.Duration) error {
	if task == nil {
		return ErrTaskNil
	}
	if p.closed.Load() {
		return ErrPoolClosed
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-p.ctx.Done():
		return p.ctx.Err()
	case <-timer.C:
		return ErrPoolFull
	case p.tasks <- taskEnvelope{task: task}:
		return nil
	}
}

// Close closes the task submission queue and waits for all workers to complete pending tasks.
func (p *WorkerPool) Close() {
	if p.closed.Swap(true) {
		p.wg.Wait()
		return
	}
	close(p.tasks)
	p.wg.Wait()
}

// Stop cancels the pool context immediately and terminates workers.
func (p *WorkerPool) Stop() {
	p.cancel()
	if !p.closed.Swap(true) {
		close(p.tasks)
	}
	p.wg.Wait()
}

// Wait waits for all workers to finish processing and returns all accumulated errors.
func (p *WorkerPool) Wait() []error {
	p.Close()
	return p.Errors()
}

// Errors returns a copy of all errors collected during task execution.
func (p *WorkerPool) Errors() []error {
	p.errMu.Lock()
	defer p.errMu.Unlock()
	copied := make([]error, len(p.errors))
	copy(copied, p.errors)
	return copied
}

// Concurrency returns the configured worker count.
func (p *WorkerPool) Concurrency() int {
	return p.concurrency
}

// ----------------------------------------------------------------------------
// Batch Execution Helpers
// ----------------------------------------------------------------------------

// ExecuteTasks executes a slice of tasks using a temporary bounded worker pool.
func ExecuteTasks(ctx context.Context, concurrency int, tasks []Task) []error {
	if len(tasks) == 0 {
		return nil
	}
	if concurrency <= 0 {
		concurrency = 10
	}

	pool := NewWorkerPool(ctx, concurrency, WithQueueSize(len(tasks)))
	for _, task := range tasks {
		if err := pool.Submit(task); err != nil {
			pool.recordError(err)
		}
	}
	return pool.Wait()
}

// ParallelMap applies a transformation function across items concurrently, preserving result indices.
func ParallelMap[T any, R any](ctx context.Context, concurrency int, items []T, fn func(ctx context.Context, item T) (R, error)) ([]R, []error) {
	if len(items) == 0 {
		return nil, nil
	}
	if concurrency <= 0 {
		concurrency = 10
	}

	results := make([]R, len(items))
	errs := make([]error, len(items))
	var hasError atomic.Bool

	pool := NewWorkerPool(ctx, concurrency, WithQueueSize(len(items)))
	for idx, itm := range items {
		i := idx
		itemVal := itm
		if err := pool.Submit(func(taskCtx context.Context) error {
			res, err := fn(taskCtx, itemVal)
			if err != nil {
				errs[i] = err
				hasError.Store(true)
				return err
			}
			results[i] = res
			return nil
		}); err != nil {
			errs[i] = err
			hasError.Store(true)
			pool.recordError(err)
		}
	}
	pool.Close()

	poolErrors := pool.Errors()
	if !hasError.Load() && len(poolErrors) == 0 {
		return results, nil
	}

	collectedErrs := make([]error, 0, len(items))
	for _, err := range errs {
		if err != nil {
			collectedErrs = append(collectedErrs, err)
		}
	}
	for _, pErr := range poolErrors {
		found := false
		for _, cErr := range collectedErrs {
			if cErr == pErr || (cErr != nil && pErr != nil && cErr.Error() == pErr.Error()) {
				found = true
				break
			}
		}
		if !found {
			collectedErrs = append(collectedErrs, pErr)
		}
	}

	return results, collectedErrs
}

