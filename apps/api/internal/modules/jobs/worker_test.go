package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProcessor struct{ calls int32 }

func (f *fakeProcessor) ProcessOne(context.Context) (bool, error) {
	atomic.AddInt32(&f.calls, 1)
	return false, nil
}

type fakeWaiter struct {
	calls int32
	err   error
	block bool
}

func (f *fakeWaiter) Wait(ctx context.Context, _ time.Duration) error {
	atomic.AddInt32(&f.calls, 1)
	if f.err != nil {
		return f.err
	}
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func runPool(t *testing.T, p *WorkerPool) (context.CancelFunc, chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()
	return cancel, done
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func TestWorkerRepollsAfterSignal(t *testing.T) {
	proc := &fakeProcessor{}
	waiter := &fakeWaiter{}
	pool := NewWorkerPool(proc, 1, time.Hour, waiter)
	cancel, done := runPool(t, pool)

	waitFor(t, func() bool { return atomic.LoadInt32(&waiter.calls) >= 3 })
	if atomic.LoadInt32(&proc.calls) < 3 {
		t.Fatalf("processor calls = %d; want >= 3", proc.calls)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancel")
	}
}

func TestWorkerFallsBackToPollingOnSignalError(t *testing.T) {
	proc := &fakeProcessor{}
	waiter := &fakeWaiter{err: errors.New("redis down")}
	pool := NewWorkerPool(proc, 1, 20*time.Millisecond, waiter)
	cancel, done := runPool(t, pool)

	waitFor(t, func() bool { return atomic.LoadInt32(&waiter.calls) >= 2 })

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancel")
	}
}

func TestWorkerStopsWhileWaitingForSignal(t *testing.T) {
	proc := &fakeProcessor{}
	waiter := &fakeWaiter{block: true}
	pool := NewWorkerPool(proc, 1, time.Hour, waiter)
	cancel, done := runPool(t, pool)

	waitFor(t, func() bool { return atomic.LoadInt32(&waiter.calls) >= 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked waiter prevented shutdown")
	}
}

func TestWorkerPollsWithoutWaiter(t *testing.T) {
	proc := &fakeProcessor{}
	pool := NewWorkerPool(proc, 1, 10*time.Millisecond, nil)
	cancel, done := runPool(t, pool)

	waitFor(t, func() bool { return atomic.LoadInt32(&proc.calls) >= 2 })
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancel")
	}
}
