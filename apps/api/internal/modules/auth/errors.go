package auth

import (
	"errors"
	"fmt"

	"insightflow/api/internal/modules/users"
)

// Service-level sentinel errors. The HTTP layer maps these to stable codes.
var (
	ErrInvalidCredentials   = errors.New("auth: invalid email or password")
	ErrEmailTaken           = users.ErrEmailTaken
	ErrRefreshInvalid       = errors.New("auth: refresh token invalid")
	ErrRefreshReused        = errors.New("auth: refresh token reused")
	ErrOAuthEmailUnverified = errors.New("auth: google email not verified")
	ErrTooManyAttempts      = errors.New("auth: too many attempts")
)

// FieldError is a validation failure tied to a request field.
type FieldError struct {
	Field string
	Issue string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("auth: invalid %s: %s", e.Field, e.Issue)
}

func fieldErr(field, issue string) *FieldError { return &FieldError{Field: field, Issue: issue} }
