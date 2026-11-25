// Package users owns user records and session validation.
package users

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/pgconv"
)

// Repository errors.
var (
	ErrNotFound   = errors.New("users: not found")
	ErrEmailTaken = errors.New("users: email already registered")
)

// Repository is the user data-access layer.
type Repository struct {
	q *dbgen.Queries
}

// NewRepository wraps sqlc queries.
func NewRepository(q *dbgen.Queries) *Repository { return &Repository{q: q} }

// GetByID loads a user by primary key.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (dbgen.User, error) {
	u, err := r.q.GetUserByID(ctx, pgconv.UUID(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dbgen.User{}, ErrNotFound
		}
		return dbgen.User{}, err
	}
	return u, nil
}

// GetByEmail loads a user case-insensitively.
func (r *Repository) GetByEmail(ctx context.Context, email string) (dbgen.User, error) {
	u, err := r.q.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dbgen.User{}, ErrNotFound
		}
		return dbgen.User{}, err
	}
	return u, nil
}

// Create inserts a user. passwordHash is empty for Google-only accounts.
func (r *Repository) Create(ctx context.Context, email, passwordHash, fullName, status string) (dbgen.User, error) {
	u, err := r.q.CreateUser(ctx, dbgen.CreateUserParams{
		Email:        email,
		PasswordHash: textOrNull(passwordHash),
		FullName:     fullName,
		Status:       status,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return dbgen.User{}, ErrEmailTaken
		}
		return dbgen.User{}, err
	}
	return u, nil
}

// Activate marks a pending account as active.
func (r *Repository) Activate(ctx context.Context, id uuid.UUID) (dbgen.User, error) {
	u, err := r.q.ActivateUser(ctx, pgconv.UUID(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dbgen.User{}, ErrNotFound
		}
		return dbgen.User{}, err
	}
	return u, nil
}

// RevokeTokens invalidates every access token issued before now.
func (r *Repository) RevokeTokens(ctx context.Context, id uuid.UUID) error {
	return r.q.RevokeUserTokens(ctx, pgconv.UUID(id))
}

func textOrNull(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func isUniqueViolation(err error) bool {
	return pgconv.IsUniqueViolation(err)
}
