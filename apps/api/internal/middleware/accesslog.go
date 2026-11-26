package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"insightflow/api/internal/platform/httpx"
)

// AccessLog emits one structured JSON line per request. It logs only
// correlation and routing metadata (request_id, method, route template, status,
// duration, client ip, error code). Headers, cookies, query strings, and bodies
// are never logged, per Security.md 5.3, so credentials and dataset content
// cannot leak. The route template (not the raw path) avoids logging resource ids.
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := c.Writer.Status()
		attrs := []any{
			"request_id", httpx.RequestID(c),
			"method", c.Request.Method,
			"route", route,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", c.ClientIP(),
		}
		if code := httpx.ErrorCode(c); code != "" {
			attrs = append(attrs, "error_code", code)
		}

		switch {
		case status >= 500:
			slog.Error("http request", attrs...)
		case status >= 400:
			slog.Warn("http request", attrs...)
		default:
			slog.Info("http request", attrs...)
		}
	}
}
