package cache

import (
	"context"
	"os"
	"testing"
	"time"
)

func testClient(t *testing.T) *Client {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	c, err := New(context.Background(), addr, "", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestSetGetDelete(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	key := Key("test", "test", "setget", "1")
	t.Cleanup(func() { _ = c.Delete(ctx, key) })

	if err := c.SetWithTTL(ctx, key, []byte("v"), 30*time.Second); err != nil {
		t.Fatalf("SetWithTTL: %v", err)
	}
	got, ok, err := c.Get(ctx, key)
	if err != nil || !ok || string(got) != "v" {
		t.Fatalf("Get = %q, %v, %v; want \"v\", true, nil", got, ok, err)
	}
	if err := c.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := c.Get(ctx, key); ok {
		t.Fatal("key still present after Delete")
	}
}

func TestSetWithTTLRejectsNonPositiveTTL(t *testing.T) {
	c := testClient(t)
	if err := c.SetWithTTL(context.Background(), Key("test", "test", "bad"), []byte("v"), 0); err == nil {
		t.Fatal("expected error for ttl=0")
	}
}

func TestKeyExpires(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	key := Key("test", "test", "ttl", "1")

	if err := c.SetWithTTL(ctx, key, []byte("v"), 150*time.Millisecond); err != nil {
		t.Fatalf("SetWithTTL: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, ok, _ := c.Get(ctx, key); ok {
		t.Fatal("key survived past its TTL")
	}
}

func TestIncrementWithTTLDoesNotSlide(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	key := Key("test", "test", "counter", "1")
	t.Cleanup(func() { _ = c.Delete(ctx, key) })

	// The window opens with a short TTL.
	first, err := c.IncrementWithTTL(ctx, key, 150*time.Millisecond)
	if err != nil || first != 1 {
		t.Fatalf("first increment = %d, %v; want 1, nil", first, err)
	}

	// A later call must not extend the window.
	second, err := c.IncrementWithTTL(ctx, key, time.Hour)
	if err != nil || second != 2 {
		t.Fatalf("second increment = %d, %v; want 2, nil", second, err)
	}

	time.Sleep(400 * time.Millisecond)
	if _, ok, _ := c.Get(ctx, key); ok {
		t.Fatal("window slid: key still present after the original TTL")
	}
}

func TestAcquireLockIsExclusive(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	key := Key("test", "test", "lock", "1")
	t.Cleanup(func() { _ = c.Delete(ctx, key) })

	got, err := c.AcquireLock(ctx, key, 30*time.Second)
	if err != nil || !got {
		t.Fatalf("first AcquireLock = %v, %v; want true, nil", got, err)
	}
	got, err = c.AcquireLock(ctx, key, 30*time.Second)
	if err != nil || got {
		t.Fatalf("second AcquireLock = %v, %v; want false, nil", got, err)
	}
}
