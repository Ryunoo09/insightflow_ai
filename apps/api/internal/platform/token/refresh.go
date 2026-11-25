package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
)

// RefreshTokenTTL is the lifetime of a refresh token.
const RefreshTokenTTL = 14 * 24 * time.Hour

// RefreshToken is an opaque token paired with the hash stored in PostgreSQL.
type RefreshToken struct {
	Raw  string
	Hash string
}

// NewRefreshToken generates a 256-bit opaque refresh token.
func NewRefreshToken() (RefreshToken, error) {
	raw, err := RandomToken()
	if err != nil {
		return RefreshToken{}, err
	}
	return RefreshToken{Raw: raw, Hash: HashRefreshToken(raw)}, nil
}

// RandomToken returns a 256-bit base64url string without padding.
func RandomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// NewFamilyID returns a fresh token-family identifier.
func NewFamilyID() string { return uuid.NewString() }

// HashRefreshToken returns the SHA-256 hex digest stored in PostgreSQL.
func HashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
