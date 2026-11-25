package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"insightflow/api/internal/modules/users"
	"insightflow/api/internal/platform/token"
)

type fakeSessions struct {
	principal users.Principal
	err       error
}

func (f *fakeSessions) ValidateSession(context.Context, uuid.UUID, time.Time) (users.Principal, error) {
	return f.principal, f.err
}

func testIssuer(t *testing.T) *token.Issuer {
	t.Helper()
	iss, err := token.NewIssuer("test-secret-that-is-long-enough-32b", "insightflow", "insightflow-web")
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	return iss
}

func testRouter(iss *token.Issuer, v SessionValidator) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/me", Auth(iss, v), func(c *gin.Context) {
		p, _ := PrincipalFrom(c)
		c.JSON(http.StatusOK, gin.H{"email": p.Email})
	})
	return r
}

func TestAuthMiddleware(t *testing.T) {
	iss := testIssuer(t)
	id := uuid.New()
	validator := &fakeSessions{principal: users.Principal{ID: id, Email: "a@example.com"}}
	router := testRouter(iss, validator)

	valid, _ := iss.Issue(id.String())

	cases := []struct {
		name   string
		header string
		code   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"malformed", "Bearer not-a-jwt", http.StatusUnauthorized},
		{"wrong scheme", "Token " + valid, http.StatusUnauthorized},
		{"valid", "Bearer " + valid, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/me", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.code {
				t.Fatalf("status = %d, want %d", rec.Code, tc.code)
			}
			if tc.code == http.StatusOK {
				var body map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("body: %v", err)
				}
				if body["email"] != "a@example.com" {
					t.Fatalf("principal = %v", body)
				}
			}
		})
	}
}

func TestAuthMiddlewareRejectsRevokedSession(t *testing.T) {
	iss := testIssuer(t)
	id := uuid.New()
	router := testRouter(iss, &fakeSessions{err: users.ErrUnauthorized})
	valid, _ := iss.Issue(id.String())

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+valid)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestCSRFMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/refresh", CSRF([]string{"http://localhost:5173"}), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	cases := []struct {
		name   string
		header bool
		origin string
		code   int
	}{
		{"no header", false, "", http.StatusForbidden},
		{"header only", true, "", http.StatusNoContent},
		{"allowed origin", true, "http://localhost:5173", http.StatusNoContent},
		{"blocked origin", true, "http://evil.example", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
			if tc.header {
				req.Header.Set("X-Requested-With", "InsightFlow")
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tc.code {
				t.Fatalf("status = %d, want %d", rec.Code, tc.code)
			}
		})
	}
}
