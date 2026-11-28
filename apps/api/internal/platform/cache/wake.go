package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// PrefixJob namespaces the job wake-up signal.
const PrefixJob = "jobs"

// wakeTTL bounds the signal list. The list is a hint, never a queue: the job
// itself lives in PostgreSQL, so a lost signal only costs one polling interval.
const wakeTTL = 60 * time.Second

// JobWakeKey returns the environment-namespaced wake-up key.
func JobWakeKey(env string) string { return Key(env, PrefixJob, "wake") }

// WakeSignal is the best-effort bridge between the dataset service (producer)
// and the job worker (consumer). It owns a dedicated Redis pool because BRPOP
// holds a connection for the whole idle wait; sharing the cache pool would
// starve OTP and cache operations.
type WakeSignal struct {
	rdb *redis.Client
	key string
	cap int64
}

// NewWakeSignal connects a dedicated client. workers sizes both the blocking
// pool and the list cap (4 tokens per worker).
func NewWakeSignal(ctx context.Context, rawURL, env string, workers int) (*WakeSignal, error) {
	opt, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	if workers < 1 {
		workers = 1
	}
	opt.PoolSize = workers + 2
	rdb := redis.NewClient(opt)
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &WakeSignal{rdb: rdb, key: JobWakeKey(env), cap: int64(4 * workers)}, nil
}

// Close releases the dedicated pool.
func (w *WakeSignal) Close() error { return w.rdb.Close() }

// Signal wakes one idle worker. The payload is deliberately constant: no
// dataset, workspace, or user identifiers enter Redis.
func (w *WakeSignal) Signal(ctx context.Context) error {
	_, err := w.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		p.LPush(ctx, w.key, "1")
		p.LTrim(ctx, w.key, 0, w.cap-1)
		p.Expire(ctx, w.key, wakeTTL)
		return nil
	})
	return err
}

// Wait blocks until a signal arrives or timeout elapses. A timeout is not a
// failure: the caller polls PostgreSQL next. Other errors are returned so the
// caller can fall back to plain polling instead of spinning. BRPOP cannot be
// interrupted once blocking, so a context deadline caps the wait up front.
func (w *WakeSignal) Wait(ctx context.Context, timeout time.Duration) error {
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		return ctx.Err()
	}
	err := w.rdb.BRPop(ctx, timeout, w.key).Err()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	return err
}
