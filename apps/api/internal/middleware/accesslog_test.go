package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestAccessLogRedactsSensitiveInput guards Security.md 5.3: the access log must
// carry only route/status/correlation metadata, never headers, cookies, or query.
func TestAccessLogRedactsSensitiveInput(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID(), AccessLog())
	r.POST("/api/v1/workspaces/:workspace_id", func(c *gin.Context) {
		c.Status(http.StatusForbidden)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/abc123?token=secret-query", nil)
	req.Header.Set("Authorization", "Bearer super-secret-token")
	req.Header.Set("Cookie", "if_refresh=refresh-secret")
	r.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	for _, leak := range []string{"super-secret-token", "refresh-secret", "secret-query", "abc123"} {
		if strings.Contains(out, leak) {
			t.Fatalf("access log leaked %q: %s", leak, out)
		}
	}
	for _, want := range []string{
		`"route":"/api/v1/workspaces/:workspace_id"`,
		`"status":403`,
		`"method":"POST"`,
		`"request_id"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("access log missing %q: %s", want, out)
		}
	}
}
