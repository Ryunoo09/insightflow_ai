package analysis

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"insightflow/api/internal/platform/cache"
)

func TestDailyQuotaDisabledWithoutCache(t *testing.T) {
	quota := NewDailyQuota(nil, "test", 10)
	if quota.Enabled() {
		t.Fatal("quota without a cache must be disabled")
	}
	allowed, err := quota.ConsumeDaily(context.Background(), uuid.New(), time.Now())
	if err != nil || !allowed {
		t.Fatalf("disabled quota = %v, %v", allowed, err)
	}
}

func TestDailyQuotaConsumesOncePerWorkspacePerDay(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	ctx := context.Background()
	client, err := cache.New(ctx, addr, "", 0)
	if err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	defer func() { _ = client.Close() }()

	// A unique env keeps this test's counters out of every other run.
	env := "test-" + uuid.NewString()[:8]
	ws := uuid.New()
	quota := NewDailyQuota(client, env, 2)
	now := time.Now()

	for i := 1; i <= 2; i++ {
		allowed, err := quota.ConsumeDaily(ctx, ws, now)
		if err != nil {
			t.Fatalf("consume %d: %v", i, err)
		}
		if !allowed {
			t.Fatalf("run %d rejected under the limit", i)
		}
	}
	allowed, err := quota.ConsumeDaily(ctx, ws, now)
	if err != nil {
		t.Fatalf("consume over limit: %v", err)
	}
	if allowed {
		t.Fatal("third run allowed past a limit of 2")
	}

	// A different workspace has its own counter.
	other, err := quota.ConsumeDaily(ctx, uuid.New(), now)
	if err != nil || !other {
		t.Fatalf("other workspace = %v, %v", other, err)
	}
}

func TestUntilEndOfDayIsAlwaysPositive(t *testing.T) {
	// Just before midnight the TTL would round to zero without the floor.
	now := time.Date(2026, 1, 2, 23, 59, 59, 900_000_000, time.UTC)
	if ttl := untilEndOfDay(now); ttl <= 0 {
		t.Fatalf("ttl = %s, want positive", ttl)
	}
}
