package engine_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/engine"
)

func TestWorkerPool_BasicTasks(t *testing.T) {
	ctx := context.Background()
	concurrency := 4
	totalTasks := 20

	var count atomic.Int32
	pool := engine.NewWorkerPool(ctx, concurrency)

	for i := 0; i < totalTasks; i++ {
		err := pool.Submit(func(taskCtx context.Context) error {
			time.Sleep(5 * time.Millisecond)
			count.Add(1)
			return nil
		})
		if err != nil {
			t.Fatalf("unexpected submit error: %v", err)
		}
	}

	errs := pool.Wait()
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d", len(errs))
	}
	if int(count.Load()) != totalTasks {
		t.Fatalf("expected %d completed tasks, got %d", totalTasks, count.Load())
	}
	if pool.Concurrency() != concurrency {
		t.Fatalf("expected concurrency %d, got %d", concurrency, pool.Concurrency())
	}
}

func TestWorkerPool_ConcurrencyLimit(t *testing.T) {
	ctx := context.Background()
	concurrency := 3
	pool := engine.NewWorkerPool(ctx, concurrency)

	var active atomic.Int32
	var maxActive atomic.Int32

	for i := 0; i < 15; i++ {
		_ = pool.Submit(func(taskCtx context.Context) error {
			current := active.Add(1)
			for {
				max := maxActive.Load()
				if current <= max || maxActive.CompareAndSwap(max, current) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			active.Add(-1)
			return nil
		})
	}

	pool.Close()
	if maxActive.Load() > int32(concurrency) {
		t.Fatalf("concurrency limit exceeded: maxActive=%d > concurrency=%d", maxActive.Load(), concurrency)
	}
}

func TestWorkerPool_PanicRecovery(t *testing.T) {
	ctx := context.Background()
	var errCallbackCount atomic.Int32
	pool := engine.NewWorkerPool(ctx, 2, engine.WithErrorHandler(func(err error) {
		errCallbackCount.Add(1)
	}))

	var successCount atomic.Int32

	// Task 1: panics
	_ = pool.Submit(func(taskCtx context.Context) error {
		panic("simulated fatal crash in worker")
	})

	// Task 2: succeeds
	_ = pool.Submit(func(taskCtx context.Context) error {
		successCount.Add(1)
		return nil
	})

	errs := pool.Wait()
	if len(errs) != 1 {
		t.Fatalf("expected 1 error from recovered panic, got %d", len(errs))
	}
	if successCount.Load() != 1 {
		t.Fatalf("expected 1 task to succeed despite peer panic, got %d", successCount.Load())
	}
	if errCallbackCount.Load() != 1 {
		t.Fatalf("expected error callback invoked once, got %d", errCallbackCount.Load())
	}
}

func TestWorkerPool_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pool := engine.NewWorkerPool(ctx, 2)

	var executed atomic.Int32

	for i := 0; i < 10; i++ {
		_ = pool.Submit(func(taskCtx context.Context) error {
			time.Sleep(50 * time.Millisecond)
			executed.Add(1)
			return nil
		})
	}

	time.Sleep(10 * time.Millisecond)
	cancel() // Cancel early
	pool.Close()

	if executed.Load() == 10 {
		t.Fatalf("expected tasks to be cancelled before all 10 finished")
	}
}

func TestWorkerPool_CloseAndSubmit(t *testing.T) {
	ctx := context.Background()
	pool := engine.NewWorkerPool(ctx, 2)
	pool.Close()

	err := pool.Submit(func(taskCtx context.Context) error { return nil })
	if !errors.Is(err, engine.ErrPoolClosed) {
		t.Fatalf("expected ErrPoolClosed, got %v", err)
	}

	err = pool.Submit(nil)
	if !errors.Is(err, engine.ErrTaskNil) {
		t.Fatalf("expected ErrTaskNil, got %v", err)
	}

	err = pool.SubmitWithTimeout(func(taskCtx context.Context) error { return nil }, 10*time.Millisecond)
	if !errors.Is(err, engine.ErrPoolClosed) {
		t.Fatalf("expected ErrPoolClosed on timeout submit, got %v", err)
	}

	err = pool.SubmitWithTimeout(nil, 10*time.Millisecond)
	if !errors.Is(err, engine.ErrTaskNil) {
		t.Fatalf("expected ErrTaskNil on timeout submit, got %v", err)
	}
}

func TestWorkerPool_Stop(t *testing.T) {
	ctx := context.Background()
	pool := engine.NewWorkerPool(ctx, 2)
	pool.Stop()

	err := pool.Submit(func(taskCtx context.Context) error { return nil })
	if err == nil {
		t.Fatalf("expected error after Stop, got nil")
	}
}

func TestExecuteTasks(t *testing.T) {
	ctx := context.Background()
	var count atomic.Int32

	tasks := []engine.Task{
		func(ctx context.Context) error {
			count.Add(1)
			return nil
		},
		func(ctx context.Context) error {
			count.Add(1)
			return errors.New("task 2 failed")
		},
		func(ctx context.Context) error {
			count.Add(1)
			return nil
		},
	}

	errs := engine.ExecuteTasks(ctx, 2, tasks)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if count.Load() != 3 {
		t.Fatalf("expected 3 tasks executed, got %d", count.Load())
	}

	// Empty tasks
	errsEmpty := engine.ExecuteTasks(ctx, 2, nil)
	if len(errsEmpty) != 0 {
		t.Fatalf("expected 0 errors for empty tasks, got %d", len(errsEmpty))
	}
}

func TestParallelMap(t *testing.T) {
	ctx := context.Background()
	items := []int{1, 2, 3, 4, 5, 6, 7, 8}

	results, errs := engine.ParallelMap(ctx, 3, items, func(taskCtx context.Context, item int) (int, error) {
		if item == 5 {
			return 0, errors.New("error on item 5")
		}
		return item * 2, nil
	})

	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if results[0] != 2 || results[1] != 4 || results[2] != 6 || results[3] != 8 || results[5] != 12 {
		t.Fatalf("unexpected results array: %v", results)
	}

	// Empty items
	resEmpty, errsEmpty := engine.ParallelMap(ctx, 3, []int{}, func(taskCtx context.Context, item int) (int, error) {
		return item, nil
	})
	if resEmpty != nil || errsEmpty != nil {
		t.Fatalf("expected nil results for empty items")
	}
}

func TestParallelMap_PanicRecovery(t *testing.T) {
	ctx := context.Background()
	items := []int{10, 20, 30}

	results, errs := engine.ParallelMap(ctx, 2, items, func(taskCtx context.Context, item int) (int, error) {
		if item == 20 {
			panic("panic on item 20")
		}
		return item * 3, nil
	})

	if len(errs) != 1 {
		t.Fatalf("expected 1 error from recovered panic, got %d", len(errs))
	}
	if results[0] != 30 || results[2] != 90 {
		t.Fatalf("expected other tasks to finish successfully, got %v", results)
	}
}
