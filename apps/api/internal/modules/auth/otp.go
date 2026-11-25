package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"insightflow/api/internal/platform/cache"
	"insightflow/api/internal/platform/token"
)

// OTP purposes.
const (
	PurposeLogin    = "login"
	PurposeRegister = "register_verify"
)

// OTP store errors.
var (
	ErrOTPUnavailable      = errors.New("auth: otp store unavailable")
	ErrOTPExpired          = errors.New("auth: otp challenge expired")
	ErrOTPAttemptsExceeded = errors.New("auth: otp attempts exceeded")
	ErrOTPResendTooSoon    = errors.New("auth: otp resend too soon")
)

// InvalidCodeError carries the remaining attempts after a wrong code.
type InvalidCodeError struct{ AttemptsLeft int }

func (e *InvalidCodeError) Error() string {
	return fmt.Sprintf("auth: invalid otp code (%d attempts left)", e.AttemptsLeft)
}

// OTPConfig tunes the challenge lifecycle.
type OTPConfig struct {
	TTL             time.Duration
	ResendInterval  time.Duration
	MaxAttempts     int
	MaxSendsPerHour int
	Secret          string
	Env             string
}

// OTPStore keeps HMAC'd OTP challenges in Redis. The plaintext code never
// touches Redis, the database, or the logs.
type OTPStore struct {
	cache *cache.Client
	cfg   OTPConfig
}

// NewOTPStore builds the store.
func NewOTPStore(c *cache.Client, cfg OTPConfig) *OTPStore { return &OTPStore{cache: c, cfg: cfg} }

type challengePayload struct {
	UserID   string `json:"user_id"`
	Purpose  string `json:"purpose"`
	CodeHMAC string `json:"code_hmac"`
}

func (s *OTPStore) challengeKey(id string) string {
	return cache.Key(s.cfg.Env, cache.PrefixOTP, "chal", id)
}
func (s *OTPStore) attemptsKey(id string) string {
	return cache.Key(s.cfg.Env, cache.PrefixOTP, "att", id)
}
func (s *OTPStore) resendKey(id string) string {
	return cache.Key(s.cfg.Env, cache.PrefixOTP, "resend", id)
}
func (s *OTPStore) sendsKey(userID string) string {
	return cache.Key(s.cfg.Env, cache.PrefixOTP, "sends", userID)
}

// Create starts a challenge and returns the id plus the plaintext code to email.
func (s *OTPStore) Create(ctx context.Context, userID, purpose string) (string, string, error) {
	if s.cache == nil {
		return "", "", ErrOTPUnavailable
	}
	id, err := token.RandomToken()
	if err != nil {
		return "", "", ErrOTPUnavailable
	}
	code, err := generateCode()
	if err != nil {
		return "", "", ErrOTPUnavailable
	}
	if err := s.store(ctx, id, userID, purpose, code); err != nil {
		return "", "", err
	}
	// Open the resend window now so an immediate resend is throttled.
	_, _ = s.cache.AcquireLock(ctx, s.resendKey(id), s.cfg.ResendInterval)
	if err := s.countSend(ctx, userID); err != nil {
		return "", "", err
	}
	return id, code, nil
}

// Verify consumes a challenge, returning the user and purpose on success.
func (s *OTPStore) Verify(ctx context.Context, id, code string) (string, string, error) {
	if s.cache == nil {
		return "", "", ErrOTPUnavailable
	}
	raw, ok, err := s.cache.Get(ctx, s.challengeKey(id))
	if err != nil {
		return "", "", ErrOTPUnavailable
	}
	if !ok {
		return "", "", ErrOTPExpired
	}
	var payload challengePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", "", ErrOTPExpired
	}

	// Count the attempt atomically before comparing so brute force is bounded
	// even under concurrency.
	n, err := s.cache.IncrementWithTTL(ctx, s.attemptsKey(id), s.cfg.TTL)
	if err != nil {
		return "", "", ErrOTPUnavailable
	}
	if int(n) > s.cfg.MaxAttempts {
		_ = s.cache.Delete(ctx, s.challengeKey(id))
		_ = s.cache.Delete(ctx, s.attemptsKey(id))
		return "", "", ErrOTPAttemptsExceeded
	}
	if subtle.ConstantTimeCompare([]byte(s.hmac(id, code)), []byte(payload.CodeHMAC)) != 1 {
		left := s.cfg.MaxAttempts - int(n) + 1
		if left <= 0 {
			_ = s.cache.Delete(ctx, s.challengeKey(id))
			_ = s.cache.Delete(ctx, s.attemptsKey(id))
			return "", "", ErrOTPAttemptsExceeded
		}
		return "", "", &InvalidCodeError{AttemptsLeft: left}
	}

	// Single use: drop the challenge and its attempt counter.
	_ = s.cache.Delete(ctx, s.challengeKey(id))
	_ = s.cache.Delete(ctx, s.attemptsKey(id))
	return payload.UserID, payload.Purpose, nil
}

// Resend issues a fresh code for an existing challenge, enforcing the resend
// throttle and the hourly per-account cap. The old code is invalidated because
// the stored HMAC is overwritten.
func (s *OTPStore) Resend(ctx context.Context, id string) (string, string, string, error) {
	if s.cache == nil {
		return "", "", "", ErrOTPUnavailable
	}
	raw, ok, err := s.cache.Get(ctx, s.challengeKey(id))
	if err != nil {
		return "", "", "", ErrOTPUnavailable
	}
	if !ok {
		return "", "", "", ErrOTPExpired
	}
	var payload challengePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", "", "", ErrOTPExpired
	}

	acquired, err := s.cache.AcquireLock(ctx, s.resendKey(id), s.cfg.ResendInterval)
	if err != nil {
		return "", "", "", ErrOTPUnavailable
	}
	if !acquired {
		return "", "", "", ErrOTPResendTooSoon
	}
	if err := s.countSend(ctx, payload.UserID); err != nil {
		return "", "", "", err
	}

	code, err := generateCode()
	if err != nil {
		return "", "", "", ErrOTPUnavailable
	}
	if err := s.store(ctx, id, payload.UserID, payload.Purpose, code); err != nil {
		return "", "", "", err
	}
	_ = s.cache.Delete(ctx, s.attemptsKey(id))
	return payload.UserID, payload.Purpose, code, nil
}

func (s *OTPStore) store(ctx context.Context, id, userID, purpose, code string) error {
	payload, err := json.Marshal(challengePayload{
		UserID:   userID,
		Purpose:  purpose,
		CodeHMAC: s.hmac(id, code),
	})
	if err != nil {
		return ErrOTPUnavailable
	}
	if err := s.cache.SetWithTTL(ctx, s.challengeKey(id), payload, s.cfg.TTL); err != nil {
		return ErrOTPUnavailable
	}
	return nil
}

func (s *OTPStore) countSend(ctx context.Context, userID string) error {
	n, err := s.cache.IncrementWithTTL(ctx, s.sendsKey(userID), time.Hour)
	if err != nil {
		return ErrOTPUnavailable
	}
	if int(n) > s.cfg.MaxSendsPerHour {
		return ErrOTPResendTooSoon
	}
	return nil
}

// hmac derives the stored digest: HMAC-SHA256(secret, challengeID + ":" + code).
func (s *OTPStore) hmac(challengeID, code string) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.Secret))
	mac.Write([]byte(challengeID))
	mac.Write([]byte(":"))
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

// generateCode returns a uniformly random 6-digit code.
func generateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
