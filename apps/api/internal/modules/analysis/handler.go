package analysis

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"insightflow/api/internal/middleware"
	"insightflow/api/internal/platform/cache"
	"insightflow/api/internal/platform/httpx"
)

// Handler exposes the analysis session endpoints.
type Handler struct {
	svc      *Service
	analyzer Analyzer
	cache    *cache.Client
	quota    *DailyQuota
	env      string
}

// NewHandler builds the analysis handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc, quota: NewDailyQuota(nil, "", 0)} }

// NewHandlerWithEngine wires the SSE gateway. cache and analyzer may be nil when
// the engine or Redis is not configured; SendMessage then fails closed.
func NewHandlerWithEngine(svc *Service, analyzer Analyzer, cacheClient *cache.Client, env string, quota *DailyQuota) *Handler {
	if quota == nil {
		quota = NewDailyQuota(nil, env, 0)
	}
	return &Handler{svc: svc, analyzer: analyzer, cache: cacheClient, quota: quota, env: env}
}

type createSessionRequest struct {
	DatasetID string `json:"dataset_id" binding:"required,uuid"`
	Title     string `json:"title" binding:"max=200"`
}

// Create handles POST /workspaces/:workspace_id/analysis/sessions.
func (h *Handler) Create(c *gin.Context) {
	principal, ok := middleware.PrincipalFrom(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	workspaceID, ok := middleware.WorkspaceID(c)
	if !ok {
		writeNotFound(c)
		return
	}
	var req createSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteBindError(c, err)
		return
	}
	datasetID, err := uuid.Parse(req.DatasetID)
	if err != nil {
		httpx.ValidationFailed(c, httpx.ValidationDetails{Field: "dataset_id", Issue: "uuid"})
		return
	}
	session, err := h.svc.CreateSession(c.Request.Context(), workspaceID, datasetID, principal.ID, req.Title)
	if err != nil {
		writeAnalysisError(c, err)
		return
	}
	c.JSON(http.StatusCreated, sessionBody(session))
}

// List handles GET /workspaces/:workspace_id/analysis/sessions.
func (h *Handler) List(c *gin.Context) {
	principal, ok := middleware.PrincipalFrom(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	workspaceID, ok := middleware.WorkspaceID(c)
	if !ok {
		writeNotFound(c)
		return
	}
	page := queryInt(c, "page", 1)
	perPage := queryInt(c, "per_page", 20)
	items, total, err := h.svc.ListSessions(c.Request.Context(), workspaceID, principal.ID, page, perPage)
	if err != nil {
		writeAnalysisError(c, err)
		return
	}
	data := make([]gin.H, 0, len(items))
	for _, item := range items {
		data = append(data, sessionBody(item))
	}
	c.JSON(http.StatusOK, gin.H{
		"data": data,
		"meta": gin.H{"page": page, "per_page": perPage, "total": total},
	})
}

// Get handles GET /analysis/sessions/:session_id.
func (h *Handler) Get(c *gin.Context) {
	principal, ok := middleware.PrincipalFrom(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	sessionID, err := uuid.Parse(c.Param("session_id"))
	if err != nil {
		writeNotFound(c)
		return
	}
	detail, err := h.svc.GetSession(c.Request.Context(), sessionID, principal.ID)
	if err != nil {
		writeAnalysisError(c, err)
		return
	}
	messages := make([]gin.H, 0, len(detail.Messages))
	for _, m := range detail.Messages {
		messages = append(messages, gin.H{
			"id":         m.ID.String(),
			"role":       m.Role,
			"content":    m.Content,
			"created_at": m.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	body := sessionBody(detail.Session)
	body["messages"] = messages
	body["context"] = detail.Context
	c.JSON(http.StatusOK, body)
}

func sessionBody(s Session) gin.H {
	return gin.H{
		"id":           s.ID.String(),
		"workspace_id": s.WorkspaceID.String(),
		"dataset_id":   s.DatasetID.String(),
		"title":        s.Title,
		"created_at":   s.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":   s.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func writeAnalysisError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeNotFound(c)
	case errors.Is(err, ErrNotReady):
		httpx.WriteError(c, httpx.Err(http.StatusConflict, "ANALYSIS_DATASET_NOT_READY", "Dataset belum siap dianalisis."))
	case errors.Is(err, ErrInvalidTitle):
		httpx.ValidationFailed(c, httpx.ValidationDetails{Field: "title", Issue: "max"})
	case errors.Is(err, ErrInvalidMessage):
		httpx.ValidationFailed(c, httpx.ValidationDetails{Field: "content", Issue: "required"})
	case errors.Is(err, ErrQuotaExceeded):
		httpx.WriteError(c, httpx.Err(http.StatusTooManyRequests, "ANALYSIS_QUOTA_EXCEEDED", "Kuota analisis harian ruang kerja tercapai."))
	default:
		httpx.WriteError(c, err)
	}
}

func writeNotFound(c *gin.Context) {
	httpx.WriteError(c, httpx.Err(http.StatusNotFound, "NOT_FOUND", "Resource tidak ditemukan."))
}

func writeUnauthorized(c *gin.Context) {
	httpx.WriteError(c, httpx.Err(http.StatusUnauthorized, "AUTH_REQUIRED", "Autentikasi diperlukan."))
}

func queryInt(c *gin.Context, key string, fallback int) int {
	v := c.Query(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
