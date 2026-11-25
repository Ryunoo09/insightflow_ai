// Package token issues and verifies application session credentials.
package token

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// AccessTokenTTL is the lifetime of a signed access token.
const AccessTokenTTL = 15 * time.Minute

// MinSecretLen is the minimum HS256 signing key length.
const MinSecretLen = 32

// clockLeeway tolerates small clock skew between issuer and verifier.
const clockLeeway = 30 * time.Second

// ErrInvalidToken is returned for any token that fails verification.
var ErrInvalidToken = errors.New("token: invalid access token")

// Issuer creates and verifies HS256 access tokens.
type Issuer struct {
	secret   []byte
	issuer   string
	audience string
	now      func() time.Time
}

// NewIssuer validates the secret and returns an issuer. issuer/audience are
// embedded as claims and enforced on verification.
func NewIssuer(secret, issuer, audience string) (*Issuer, error) {
	if len(secret) < MinSecretLen {
		return nil, fmt.Errorf("token: secret must be at least %d bytes", MinSecretLen)
	}
	return &Issuer{secret: []byte(secret), issuer: issuer, audience: audience, now: time.Now}, nil
}

// Issue signs an access token for userID.
func (i *Issuer) Issue(userID string) (string, error) {
	now := i.now()
	claims := jwt.RegisteredClaims{
		Subject:   userID,
		Issuer:    i.issuer,
		Audience:  jwt.ClaimStrings{i.audience},
		ID:        uuid.NewString(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenTTL)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.secret)
}

// Claims is the verified payload of an access token.
type Claims struct {
	Subject   string
	Issuer    string
	Audience  []string
	ID        string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Verify parses and validates raw. Only HS256 is accepted, which blocks
// algorithm-confusion attacks.
func (i *Issuer) Verify(raw string) (Claims, error) {
	parsed := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(raw, parsed,
		func(*jwt.Token) (any, error) { return i.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(i.issuer),
		jwt.WithAudience(i.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(clockLeeway),
		jwt.WithTimeFunc(func() time.Time { return i.now() }),
	)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if parsed.Subject == "" || parsed.IssuedAt == nil {
		return Claims{}, ErrInvalidToken
	}
	return Claims{
		Subject:   parsed.Subject,
		Issuer:    parsed.Issuer,
		Audience:  parsed.Audience,
		ID:        parsed.ID,
		IssuedAt:  parsed.IssuedAt.Time,
		ExpiresAt: parsed.ExpiresAt.Time,
	}, nil
}
