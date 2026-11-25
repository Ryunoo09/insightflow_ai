// Package cache provides namespaced Redis access for ephemeral state.
// Redis holds only soft state (OTP/OAuth challenges, rate counters, locks,
// quotas, deterministic result cache); PostgreSQL and storage are the sources
// of truth.
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Key prefixes keep Go- and Engine-owned keys apart.
const (
	PrefixOTP   = "otp"
	PrefixOAuth = "oauth"
	PrefixRate  = "rate"
	PrefixLock  = "lock"
	PrefixQuota = "quota"
	PrefixCache = "rc"
)

// Client wraps a Redis connection pool.
type Client struct {
	rdb *redis.Client
}

// New connects to Redis and verifies connectivity.
func New(ctx context.Context, addr, password string, db int) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

// Close releases the underlying pool.
func (c *Client) Close() error { return c.rdb.Close() }

// NewFromURL connects using a redis:// connection URL.
func NewFromURL(ctx context.Context, rawURL string) (*Client, error) {
	opt, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	return New(ctx, opt.Addr, opt.Password, opt.DB)
}

// Key builds an environment-namespaced key: if:env:prefix:part:part.
// The "if:{env}:" root keeps development, test, and production state apart on a
// shared Redis instance.
func Key(env, prefix string, parts ...string) string {
	k := "if:" + env + ":" + prefix
	for _, p := range parts {
		k += ":" + p
	}
	return k
}

// SetWithTTL stores value. ttl must be positive: keys never live forever.
func (c *Client) SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("cache: ttl must be positive, got %s", ttl)
	}
	return c.rdb.Set(ctx, key, value, ttl).Err()
}

// Get returns the value and whether the key existed.
func (c *Client) Get(ctx context.Context, key string) ([]byte, bool, error) {
	b, err := c.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// Delete removes a key.
func (c *Client) Delete(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, key).Err()
}

// IncrementWithTTL increments a fixed-window counter. The TTL is applied when
// the key is first created, so the window does not slide on every increment.
// ponytail: SetNX guarantees a TTL even if the process dies before the INCR.
func (c *Client) IncrementWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	if ttl <= 0 {
		return 0, fmt.Errorf("cache: ttl must be positive, got %s", ttl)
	}
	if err := c.rdb.SetNX(ctx, key, 0, ttl).Err(); err != nil {
		return 0, err
	}
	return c.rdb.Incr(ctx, key).Result()
}

// AcquireLock sets key only if absent. It returns true when the caller owns the
// lock, false when another holder already has it.
func (c *Client) AcquireLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, fmt.Errorf("cache: ttl must be positive, got %s", ttl)
	}
	return c.rdb.SetNX(ctx, key, "1", ttl).Result()
}

// ReleaseLock drops a lock held by the caller.
func (c *Client) ReleaseLock(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, key).Err()
}
