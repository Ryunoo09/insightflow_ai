package users

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"insightflow/api/internal/platform/pgconv"
)

// ErrUnauthorized is returned when a session no longer maps to an active user.
var ErrUnauthorized = errors.New("users: session not authorized")

// Principal is the authenticated caller placed on the request context.
type Principal struct {
	ID       uuid.UUID
	Email    string
	FullName string
}

// Service exposes session validation and profile reads.
type Service struct {
	repo *Repository
}

// NewService builds the user service.
func NewService(repo *Repository) *Service { return &Service{repo: repo} }

// ValidateSession confirms the user is active and the token predates the last
// revocation. tokens_valid_after is truncated to the second because the JWT
// `iat` claim has second precision: a token that shares the revocation second
// is still rejected at the next second.
func (s *Service) ValidateSession(ctx context.Context, id uuid.UUID, issuedAt time.Time) (Principal, error) {
	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Principal{}, ErrUnauthorized
		}
		return Principal{}, err
	}
	if u.Status != "active" {
		return Principal{}, ErrUnauthorized
	}
	if issuedAt.Before(pgconv.Time(u.TokensValidAfter).Truncate(time.Second)) {
		return Principal{}, ErrUnauthorized
	}
	return Principal{ID: id, Email: u.Email, FullName: u.FullName}, nil
}

// Profile returns the caller's public profile.
func (s *Service) Profile(ctx context.Context, id uuid.UUID) (Principal, error) {
	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return Principal{}, err
	}
	return Principal{ID: id, Email: u.Email, FullName: u.FullName}, nil
}
