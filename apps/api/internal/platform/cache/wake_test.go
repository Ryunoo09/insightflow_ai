package cache

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func testWake(t *testing.T, workers int) *WakeSignal {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	env := fmt.Sprintf("test-wake-%d", time.Now().UnixNano())
	w, err := NewWakeSignal(context.Background(), "redis://"+addr+"/0", env, workers)
	if err != nil {
		t.Fatalf("NewWakeSignal: %v", err)
	}
	t.Cleanup(func() {
		_ = w.rdb.Del(context.Background(), w.key).Err()
		_ = w.Close()
	})
	return w
}

func TestSignalWakesWaiterAndSetsTTL(t *testing.T) {
	w := testWake(t, 2)
	ctx := context.Background()

	if err := w.Signal(ctx); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	if ttl := w.rdb.TTL(ctx, w.key).Val(); ttl <= 0 {
		t.Fatalf("signal key has no TTL: %v", ttl)
	}

	start := time.Now()
	if err := w.Wait(ctx, 2*time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("wake latency = %s; want < 200ms", elapsed)
	}
}

func TestSignalCapsListLength(t *testing.T) {
	w := testWake(t, 1) // cap = 4
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if err := w.Signal(ctx); err != nil {
			t.Fatalf("Signal %d: %v", i, err)
		}
	}
	if n := w.rdb.LLen(ctx, w.key).Val(); n != w.cap {
		t.Fatalf("list length = %d; want cap %d", n, w.cap)
	}
}

func TestWaitReturnsNilOnTimeout(t *testing.T) {
	w := testWake(t, 1)
	start := time.Now()
	if err := w.Wait(context.Background(), 100*time.Millisecond); err != nil {
		t.Fatalf("Wait timeout must not be an error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond {
		t.Fatalf("Wait returned after %s; want ~100ms", elapsed)
	}
}

func TestWaitBoundedByContextDeadline(t *testing.T) {
	w := testWake(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_ = w.Wait(ctx, 5*time.Second) // a 5s block must be capped by the deadline
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Wait ignored the context deadline: %s", elapsed)
	}
}
