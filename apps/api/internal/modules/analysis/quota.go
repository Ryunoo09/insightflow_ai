package analysis

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"insightflow/api/internal/platform/cache"
)

// DailyQuota enforces the per-workspace daily run limit from Plan 06 §6: the
// counter is checked before the engine is called, so a runaway client cannot
// spend provider credits indefinitely.
//
// The key is `quota:analysis:{workspace}:{yyyymmdd}`. The fixed date part is
// what makes the window a calendar day rather than a sliding one.
type DailyQuota struct {
	cache *cache.Client
	env   string
	limit int64
}

// NewDailyQuota builds the quota counter. A nil cache or a non-positive limit
// disables enforcement, which is what tests and single-tenant dev runs want.
func NewDailyQuota(cacheClient *cache.Client, env string, limit int64) *DailyQuota {
	return &DailyQuota{cache: cacheClient, env: env, limit: limit}
}

// Enabled reports whether the quota is actually enforced.
func (q *DailyQuota) Enabled() bool {
	return q != nil && q.cache != nil && q.limit > 0
}

// ConsumeDaily increments the workspace's counter and reports whether the run
// may proceed. A Redis failure is treated as "allowed": analysis must not be
// blocked by soft state (the provider itself is the hard limit).
func (q *DailyQuota) ConsumeDaily(ctx context.Context, workspaceID uuid.UUID, now time.Time) (bool, error) {
	if !q.Enabled() {
		return true, nil
	}
	key := cache.Key(q.env, cache.PrefixQuota, "analysis", workspaceID.String(), now.UTC().Format("20060102"))
	used, err := q.cache.IncrementWithTTL(ctx, key, untilEndOfDay(now))
	if err != nil {
		return true, nil
	}
	return used <= q.limit, nil
}

// untilEndOfDay returns the TTL for a calendar-day counter: always positive, so
// the cache wrapper never rejects it.
func untilEndOfDay(now time.Time) time.Duration {
	utc := now.UTC()
	next := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC).Add(24 * time.Hour)
	ttl := next.Sub(utc)
	if ttl < time.Minute {
		return time.Minute
	}
	return ttl
}

// quotaError carries the remaining quota in the message so the UI can say
// something more useful than "forbidden".
func quotaError(limit int64) error {
	return fmt.Errorf("%w: %d run per hari", ErrQuotaExceeded, limit)
}
