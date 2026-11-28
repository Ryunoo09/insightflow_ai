package jobs

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Processor claims and handles one job. Dispatcher implements it; the
// interface keeps the worker loop testable without a database.
type Processor interface {
	ProcessOne(ctx context.Context) (bool, error)
}

// Waiter blocks until a wake-up signal arrives or timeout elapses. A nil
// Waiter disables the optimization and the pool polls on a fixed interval.
type Waiter interface {
	Wait(ctx context.Context, timeout time.Duration) error
}

// WorkerPool runs a fixed number of goroutines that drain the job queue. When
// the queue is empty each worker waits for a Redis signal (or idleInterval
// when no signal adapter is configured) before polling again.
type WorkerPool struct {
	processor    Processor
	workers      int
	idleInterval time.Duration
	waiter       Waiter
}

// NewWorkerPool builds the pool (PRD 27.10: 2 concurrent jobs).
func NewWorkerPool(p Processor, workers int, idleInterval time.Duration, waiter Waiter) *WorkerPool {
	if workers < 1 {
		workers = 1
	}
	return &WorkerPool{processor: p, workers: workers, idleInterval: idleInterval, waiter: waiter}
}

// Run starts the pool and blocks until ctx is cancelled (graceful shutdown).
func (p *WorkerPool) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < p.workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p.loop(ctx, id)
		}(i)
	}
	wg.Wait()
}

func (p *WorkerPool) loop(ctx context.Context, id int) {
	for {
		if ctx.Err() != nil {
			return
		}
		worked, err := p.processor.ProcessOne(ctx)
		if err != nil {
			slog.Error("job dispatch failed", "worker", id, "error", err)
		}
		if worked {
			continue
		}
		if p.waiter != nil {
			// A nil error means either a signal or the BRPOP timeout; both
			// are followed by a cheap claim query.
			if err := p.waiter.Wait(ctx, p.idleInterval); err == nil {
				continue
			} else if ctx.Err() != nil {
				return
			} else {
				slog.Warn("job wake wait failed; falling back to polling", "worker", id, "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.idleInterval):
		}
	}
}
