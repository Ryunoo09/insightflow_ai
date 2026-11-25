package workspaces

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"insightflow/api/internal/middleware"
	"insightflow/api/internal/platform/httpx"
)

// Handler exposes the workspace HTTP endpoints.
type Handler struct {
	svc *Service
}

// NewHandler builds the workspace handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

type createRequest struct {
	Name string `json:"name" binding:"required,max=100"`
}

// Create handles POST /workspaces.
func (h *Handler) Create(c *gin.Context) {
	principal, ok := middleware.PrincipalFrom(c)
	if !ok {
		httpx.WriteError(c, httpx.Err(http.StatusUnauthorized, "AUTH_REQUIRED", "Autentikasi diperlukan."))
		return
	}
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteBindError(c, err)
		return
	}
	ws, err := h.svc.Create(c.Request.Context(), principal.ID, req.Name)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, workspaceBody(ws))
}

// List handles GET /workspaces.
func (h *Handler) List(c *gin.Context) {
	principal, ok := middleware.PrincipalFrom(c)
	if !ok {
		httpx.WriteError(c, httpx.Err(http.StatusUnauthorized, "AUTH_REQUIRED", "Autentikasi diperlukan."))
		return
	}
	items, err := h.svc.List(c.Request.Context(), principal.ID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	data := make([]gin.H, 0, len(items))
	for _, ws := range items {
		data = append(data, workspaceBody(ws))
	}
	c.JSON(http.StatusOK, gin.H{"data": data, "meta": gin.H{"total": len(data)}})
}

// Get handles GET /workspaces/:workspace_id (member-scoped by middleware).
func (h *Handler) Get(c *gin.Context) {
	principal, ok := middleware.PrincipalFrom(c)
	if !ok {
		httpx.WriteError(c, httpx.Err(http.StatusUnauthorized, "AUTH_REQUIRED", "Autentikasi diperlukan."))
		return
	}
	id, err := uuid.Parse(c.Param("workspace_id"))
	if err != nil {
		writeNotFound(c)
		return
	}
	ws, err := h.svc.Get(c.Request.Context(), principal.ID, id)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, workspaceBody(ws))
}

func (h *Handler) writeError(c *gin.Context, err error) {
	if err == ErrNotFound {
		writeNotFound(c)
		return
	}
	httpx.WriteError(c, httpx.Err(http.StatusUnprocessableEntity, "VALIDATION_FAILED", "Data yang dikirim tidak valid."))
}

func writeNotFound(c *gin.Context) {
	httpx.WriteError(c, httpx.Err(http.StatusNotFound, "NOT_FOUND", "Resource tidak ditemukan."))
}

func workspaceBody(ws Workspace) gin.H {
	return gin.H{"id": ws.ID.String(), "name": ws.Name, "slug": ws.Slug, "role": ws.Role}
}
