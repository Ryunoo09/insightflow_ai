// Package auth implements registration, OTP login, Google OIDC, and session
// lifecycle for the Go API.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters fixed for the MVP.
const (
	argonMemory      = 64 * 1024 // 64 MiB
	argonIterations  = 3
	argonParallelism = 2
	argonSaltLen     = 16
	argonKeyLen      = 32
)

// Password policy per FR-AUTH-02.
const (
	MinPasswordLen = 10
	MaxPasswordLen = 128
)

// ErrWeakPassword is returned when a password fails policy checks.
var ErrWeakPassword = errors.New("auth: password does not meet policy")

// A small local deny-list of very common passwords (FR-AUTH-02).
var commonPasswords = map[string]struct{}{
	"password": {}, "password1": {}, "password123": {}, "1234567890": {},
	"qwertyuiop": {}, "letmein123": {}, "iloveyou123": {}, "admin12345": {},
	"welcome123": {}, "changeme123": {}, "passw0rd12": {}, "insightflow": {},
	"abcd123456": {}, "1111111111": {}, "aaaaaaaaaa": {}, "qwerty12345": {},
}

// ValidatePassword enforces length and the local deny-list.
func ValidatePassword(password string) error {
	if len(password) < MinPasswordLen || len(password) > MaxPasswordLen {
		return fmt.Errorf("%w: length must be %d-%d", ErrWeakPassword, MinPasswordLen, MaxPasswordLen)
	}
	if _, bad := commonPasswords[strings.ToLower(password)]; bad {
		return fmt.Errorf("%w: too common", ErrWeakPassword)
	}
	return nil
}

// Hasher hashes and verifies passwords with Argon2id.
type Hasher struct{}

// NewHasher returns the default Argon2id hasher.
func NewHasher() *Hasher { return &Hasher{} }

// Hash returns a PHC-format Argon2id string: $argon2id$v=19$m,t,p$salt$hash.
func (h *Hasher) Hash(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// Verify reports whether password matches the encoded hash. A malformed hash
// returns false, never an error, so callers can treat it as a failed login.
func (h *Hasher) Verify(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	// Reject parameters far beyond what we issue: they would let a malformed
	// hash force unbounded CPU/memory use on verify.
	if memory > argonMemory*4 || iterations > argonIterations*4 || parallelism == 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// EqualizeTiming runs an Argon2id computation for an unknown account so login
// latency does not reveal whether an email exists.
func (h *Hasher) EqualizeTiming(password string) {
	argon2.IDKey([]byte(password), make([]byte, argonSaltLen), argonIterations, argonMemory, argonParallelism, argonKeyLen)
}
