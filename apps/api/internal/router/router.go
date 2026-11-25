package router

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"insightflow/api/internal/config"
)

// New builds the HTTP router with baseline middleware and health routes.
func New(cfg *config.Config) http.Handler {
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", healthz)
	return r
}

func healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
