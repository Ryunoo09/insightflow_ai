package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"insightflow/api/internal/platform/httpx"
)

// RequestID assigns a fresh identifier to every request and echoes it back.
// A client-supplied id is ignored so it cannot be used to forge correlation or
// inject log content.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := uuid.NewString()
		c.Set(httpx.RequestIDKey, id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}
