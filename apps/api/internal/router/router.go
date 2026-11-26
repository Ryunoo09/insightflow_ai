package router

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"insightflow/api/internal/config"
	"insightflow/api/internal/middleware"
	"insightflow/api/internal/modules/auth"
	"insightflow/api/internal/modules/datasets"
	"insightflow/api/internal/modules/users"
	"insightflow/api/internal/modules/workspaces"
	"insightflow/api/internal/platform/token"
)

// Deps are the wired HTTP collaborators.
type Deps struct {
	Auth       *auth.Handler
	Workspaces *workspaces.Handler
	Datasets   *datasets.Handler
	Issuer     *token.Issuer
	Users      *users.Service
	Members    *workspaces.Service
	Origins    []string
}

// New builds the HTTP router with baseline middleware and all v1 routes.
func New(cfg *config.Config, deps Deps) http.Handler {
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	// Trust no proxy headers by default: X-Forwarded-For is only honored once a
	// trusted reverse proxy is configured explicitly.
	_ = r.SetTrustedProxies(nil)
	r.Use(gin.Recovery(), middleware.RequestID(), middleware.AccessLog())

	r.GET("/healthz", healthz)

	api := r.Group("/api/v1")
	api.POST("/auth/register", deps.Auth.Register)
	api.POST("/auth/login", deps.Auth.Login)
	api.POST("/auth/otp/verify", deps.Auth.VerifyOTP)
	api.POST("/auth/otp/resend", deps.Auth.ResendOTP)
	api.GET("/auth/google/start", deps.Auth.GoogleStart)
	api.GET("/auth/google/callback", deps.Auth.GoogleCallback)
	api.GET("/config/limits", configLimits(cfg))

	csrf := middleware.CSRF(deps.Origins)
	api.POST("/auth/refresh", csrf, deps.Auth.Refresh)
	api.POST("/auth/logout", csrf, deps.Auth.Logout)

	protected := api.Group("")
	protected.Use(middleware.Auth(deps.Issuer, deps.Users))
	protected.GET("/users/me", deps.Auth.Me)
	protected.POST("/workspaces", deps.Workspaces.Create)
	protected.GET("/workspaces", deps.Workspaces.List)
	protected.GET("/workspaces/:workspace_id", middleware.WorkspaceMember(deps.Members), deps.Workspaces.Get)

	// Datasets require workspace membership; upload requires an editor+ role
	// (checked inside the handler). Detail/status resolve membership in-query.
	protected.POST("/workspaces/:workspace_id/datasets", middleware.WorkspaceMember(deps.Members), deps.Datasets.Upload)
	protected.GET("/workspaces/:workspace_id/datasets", middleware.WorkspaceMember(deps.Members), deps.Datasets.List)
	protected.GET("/datasets/:dataset_id", deps.Datasets.Get)
	protected.GET("/datasets/:dataset_id/status", deps.Datasets.Status)
	protected.PATCH("/datasets/:dataset_id", deps.Datasets.Rename)
	protected.DELETE("/datasets/:dataset_id", deps.Datasets.Delete)
	protected.POST("/datasets/:dataset_id/reprocess", deps.Datasets.Reprocess)

	return r
}

// configLimits exposes only public bounds: no URLs, tokens, or credentials.
func configLimits(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"upload": gin.H{
				"csv_max_bytes":  cfg.MaxUploadCSVBytes,
				"xlsx_max_bytes": cfg.MaxUploadXLSXBytes,
			},
			"dataset": gin.H{
				"max_per_workspace": cfg.MaxDatasetsPerWorkspace,
				"max_rows_csv":      cfg.MaxDatasetRowsCSV,
				"max_rows_xlsx":     cfg.MaxDatasetRowsXLSX,
				"max_columns":       cfg.MaxDatasetColumns,
			},
			"profile": gin.H{
				"timeout_seconds": cfg.ProfileTimeoutSeconds,
			},
			"auth": gin.H{
				"google_enabled": cfg.GoogleEnabled(),
			},
		})
	}
}

func healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
