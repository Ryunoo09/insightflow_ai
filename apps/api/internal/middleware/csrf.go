package middleware

import (
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"insightflow/api/internal/platform/httpx"
)

// CSRF guards cookie-authenticated routes. It requires the custom header that a
// cross-site HTML form cannot set, and an allow-listed Origin when present.
func CSRF(allowedOrigins []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.GetHeader("X-Requested-With") {
		case "InsightFlow", "XMLHttpRequest":
		default:
			abortCSRF(c)
			return
		}
		if origin := c.GetHeader("Origin"); origin != "" && !slices.Contains(allowedOrigins, origin) {
			abortCSRF(c)
			return
		}
		c.Next()
	}
}

func abortCSRF(c *gin.Context) {
	c.Abort()
	httpx.WriteError(c, httpx.Err(http.StatusForbidden, "CSRF_CHECK_FAILED", "Permintaan ditolak."))
}
