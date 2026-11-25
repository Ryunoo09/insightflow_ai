// Package middleware provides cross-cutting HTTP guards.
package middleware

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"insightflow/api/internal/modules/users"
	"insightflow/api/internal/platform/httpx"
	"insightflow/api/internal/platform/token"
)

// ErrUnauthenticated is returned when a request has no valid session.
var ErrUnauthenticated = errors.New("middleware: unauthenticated")

const principalKey = "auth_principal"

// SessionValidator checks a JWT subject against live account state.
type SessionValidator interface {
	ValidateSession(ctx context.Context, id uuid.UUID, issuedAt time.Time) (users.Principal, error)
}

// Auth rejects requests without a valid bearer token or with a token issued
// before the account's last revocation.
func Auth(issuer *token.Issuer, sessions SessionValidator) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		const prefix = "Bearer "
		if len(header) <= len(prefix) || header[:len(prefix)] != prefix {
			abortUnauthenticated(c)
			return
		}
		claims, err := issuer.Verify(header[len(prefix):])
		if err != nil {
			abortUnauthenticated(c)
			return
		}
		id, err := uuid.Parse(claims.Subject)
		if err != nil {
			abortUnauthenticated(c)
			return
		}
		principal, err := sessions.ValidateSession(c.Request.Context(), id, claims.IssuedAt)
		if err != nil {
			abortUnauthenticated(c)
			return
		}
		c.Set(principalKey, principal)
		c.Next()
	}
}

// PrincipalFrom returns the authenticated caller.
func PrincipalFrom(c *gin.Context) (users.Principal, bool) {
	v, ok := c.Get(principalKey)
	if !ok {
		return users.Principal{}, false
	}
	p, ok := v.(users.Principal)
	return p, ok
}

func abortUnauthenticated(c *gin.Context) {
	c.Abort()
	httpx.WriteError(c, httpx.Err(http.StatusUnauthorized, "AUTH_REQUIRED", "Autentikasi diperlukan."))
}
