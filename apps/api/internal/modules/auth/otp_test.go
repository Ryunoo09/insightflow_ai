package auth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"insightflow/api/internal/platform/cache"
)

func testRedis(t *testing.T) *cache.Client {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	c, err := cache.New(context.Background(), addr, "", 0)
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func newTestOTPStore(c *cache.Client, maxAttempts int) *OTPStore {
	return NewOTPStore(c, OTPConfig{
		TTL:             5 * time.Minute,
		ResendInterval:  time.Minute,
		MaxAttempts:     maxAttempts,
		MaxSendsPerHour: 5,
		Secret:          "test-otp-secret-16-bytes",
		Env:             "test",
	})
}

func TestOTPChallengeSingleUse(t *testing.T) {
	store := newTestOTPStore(testRedis(t), 3)
	ctx := context.Background()
	userID := "user-" + uuid.NewString()

	id, code, err := store.Create(ctx, userID, PurposeLogin)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("code length = %d, want 6", len(code))
	}
	gotUser, purpose, err := store.Verify(ctx, id, code)
	if err != nil || gotUser != userID || purpose != PurposeLogin {
		t.Fatalf("Verify = %q, %q, %v", gotUser, purpose, err)
	}
	if _, _, err := store.Verify(ctx, id, code); !errors.Is(err, ErrOTPExpired) {
		t.Fatalf("second Verify err = %v, want ErrOTPExpired (single use)", err)
	}
}

func TestOTPWrongCodeIsBounded(t *testing.T) {
	store := newTestOTPStore(testRedis(t), 3)
	ctx := context.Background()

	id, code, err := store.Create(ctx, "user-"+uuid.NewString(), PurposeLogin)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	var last *InvalidCodeError
	for i := 0; i < 3; i++ {
		_, _, err := store.Verify(ctx, id, wrong)
		if !errors.As(err, &last) {
			t.Fatalf("attempt %d err = %v, want InvalidCodeError", i+1, err)
		}
	}
	if _, _, err := store.Verify(ctx, id, wrong); !errors.Is(err, ErrOTPAttemptsExceeded) {
		t.Fatalf("4th attempt err = %v, want ErrOTPAttemptsExceeded", err)
	}
	// The challenge is burned; even the correct code no longer works.
	if _, _, err := store.Verify(ctx, id, code); !errors.Is(err, ErrOTPExpired) {
		t.Fatalf("verify after lockout err = %v, want ErrOTPExpired", err)
	}
}

func TestOTPResendThrottledAndInvalidatesOldCode(t *testing.T) {
	store := newTestOTPStore(testRedis(t), 5)
	ctx := context.Background()

	id, oldCode, err := store.Create(ctx, "user-"+uuid.NewString(), PurposeLogin)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, _, err := store.Resend(ctx, id); !errors.Is(err, ErrOTPResendTooSoon) {
		t.Fatalf("immediate resend err = %v, want ErrOTPResendTooSoon", err)
	}
	// Force the throttle window open by removing the lock key.
	if err := store.cache.Delete(ctx, store.resendKey(id)); err != nil {
		t.Fatalf("clear resend lock: %v", err)
	}
	_, _, newCode, err := store.Resend(ctx, id)
	if err != nil {
		t.Fatalf("Resend: %v", err)
	}
	if newCode == oldCode {
		t.Fatal("resend reused the previous code")
	}
	if _, _, err := store.Verify(ctx, id, oldCode); err == nil {
		t.Fatal("old code still valid after resend")
	}
}

func TestOTPUnknownChallengeExpires(t *testing.T) {
	store := newTestOTPStore(testRedis(t), 3)
	if _, _, err := store.Verify(context.Background(), "does-not-exist", "123456"); !errors.Is(err, ErrOTPExpired) {
		t.Fatalf("err = %v, want ErrOTPExpired", err)
	}
}

func TestMaskEmail(t *testing.T) {
	if got := maskEmail("rakha@example.com"); got != "r***@example.com" {
		t.Fatalf("maskEmail = %q", got)
	}
}
