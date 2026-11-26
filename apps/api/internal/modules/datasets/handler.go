package datasets

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"insightflow/api/internal/middleware"
	"insightflow/api/internal/platform/httpx"
)

// Handler exposes the dataset HTTP endpoints.
type Handler struct {
	svc     *Service
	maxCSV  int64
	maxXLSX int64
}

// NewHandler builds the dataset handler. maxCSV and maxXLSX are hard upload caps.
func NewHandler(svc *Service, maxCSV, maxXLSX int64) *Handler {
	return &Handler{svc: svc, maxCSV: maxCSV, maxXLSX: maxXLSX}
}

// List handles GET /workspaces/:workspace_id/datasets with pagination, search,
// status filter, and sorting.
func (h *Handler) List(c *gin.Context) {
	principal, ok := middleware.PrincipalFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, nil)
		return
	}
	workspaceID, ok := middleware.WorkspaceID(c)
	if !ok {
		writeNotFound(c)
		return
	}

	status := c.Query("status")
	switch status {
	case "", "queued", "processing", "ready", "failed":
	default:
		httpx.ValidationFailed(c, httpx.ValidationDetails{Field: "status", Issue: "invalid"})
		return
	}

	perPage := queryInt(c, "per_page", 20)
	if perPage < 1 {
		perPage = 20
	}
	if perPage > 100 {
		perPage = 100
	}
	items, res, err := h.svc.List(c.Request.Context(), workspaceID, principal.ID, ListParams{
		Page:    queryInt(c, "page", 1),
		PerPage: perPage,
		Query:   c.Query("q"),
		Status:  status,
		Sort:    c.Query("sort"),
		Order:   c.Query("order"),
	})
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	data := make([]gin.H, 0, len(items))
	for _, d := range items {
		data = append(data, datasetBody(d))
	}
	c.JSON(http.StatusOK, gin.H{
		"data": data,
		"meta": gin.H{"page": res.Page, "per_page": res.PerPage, "total": res.Total},
	})
}

// Get handles GET /datasets/:dataset_id (detail + current version).
func (h *Handler) Get(c *gin.Context) {
	principal, ok := middleware.PrincipalFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, nil)
		return
	}
	datasetID, ok := parseDatasetID(c)
	if !ok {
		return
	}
	d, err := h.svc.Get(c.Request.Context(), datasetID, principal.ID)
	if err != nil {
		writeDatasetError(c, err)
		return
	}
	c.JSON(http.StatusOK, datasetBody(d))
}

// Status handles GET /datasets/:dataset_id/status for polling.
func (h *Handler) Status(c *gin.Context) {
	principal, ok := middleware.PrincipalFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, nil)
		return
	}
	datasetID, ok := parseDatasetID(c)
	if !ok {
		return
	}
	d, err := h.svc.Get(c.Request.Context(), datasetID, principal.ID)
	if err != nil {
		writeDatasetError(c, err)
		return
	}
	body := gin.H{
		"id":         d.ID.String(),
		"status":     d.Status,
		"version_no": d.CurrentVersionNo,
	}
	if d.ErrorCode != "" || d.ErrorMessage != "" {
		body["error"] = gin.H{"code": d.ErrorCode, "message": d.ErrorMessage}
	}
	c.JSON(http.StatusOK, body)
}

// Rename handles PATCH /datasets/:dataset_id.
func (h *Handler) Rename(c *gin.Context) {
	userID, dataset, ok := h.authorizeEditor(c)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteBindError(c, err)
		return
	}
	updated, err := h.svc.Rename(c.Request.Context(), dataset.ID, userID, req.Name)
	if err != nil {
		writeDatasetError(c, err)
		return
	}
	c.JSON(http.StatusOK, datasetBody(updated))
}

// Delete handles DELETE /datasets/:dataset_id (soft delete + scheduled purge).
func (h *Handler) Delete(c *gin.Context) {
	userID, dataset, ok := h.authorizeEditor(c)
	if !ok {
		return
	}
	if err := h.svc.SoftDelete(c.Request.Context(), dataset.ID, userID); err != nil {
		writeDatasetError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"id": dataset.ID.String(), "deleted": true})
}

// Reprocess handles POST /datasets/:dataset_id/reprocess.
func (h *Handler) Reprocess(c *gin.Context) {
	userID, dataset, ok := h.authorizeEditor(c)
	if !ok {
		return
	}
	updated, err := h.svc.Reprocess(c.Request.Context(), dataset.ID, userID)
	if err != nil {
		writeDatasetError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, datasetBody(updated))
}

// authorizeEditor resolves the dataset (404 if not visible) and enforces the
// editor+ role needed to mutate it.
func (h *Handler) authorizeEditor(c *gin.Context) (principalID uuid.UUID, d Dataset, ok bool) {
	principal, found := middleware.PrincipalFrom(c)
	if !found {
		c.JSON(http.StatusUnauthorized, nil)
		return uuid.Nil, Dataset{}, false
	}
	datasetID, valid := parseDatasetID(c)
	if !valid {
		return uuid.Nil, Dataset{}, false
	}
	d, err := h.svc.Get(c.Request.Context(), datasetID, principal.ID)
	if err != nil {
		writeDatasetError(c, err)
		return uuid.Nil, Dataset{}, false
	}
	role, err := h.svc.Role(c.Request.Context(), d.WorkspaceID, principal.ID)
	if err != nil {
		writeDatasetError(c, err)
		return uuid.Nil, Dataset{}, false
	}
	if role != "owner" && role != "editor" {
		httpx.WriteError(c, httpx.Err(http.StatusForbidden, "FORBIDDEN", "Peran kamu tidak dapat mengubah dataset."))
		return uuid.Nil, Dataset{}, false
	}
	return principal.ID, d, true
}

func parseDatasetID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("dataset_id"))
	if err != nil {
		writeNotFound(c)
		return uuid.Nil, false
	}
	return id, true
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

func datasetBody(d Dataset) gin.H {
	version := gin.H{
		"version_no": d.CurrentVersionNo,
		"status":     d.Status,
	}
	if d.RowCount != nil {
		version["row_count"] = *d.RowCount
	}
	if d.ColumnCount != nil {
		version["column_count"] = *d.ColumnCount
	}
	if d.ByteSize != nil {
		version["source_size_bytes"] = *d.ByteSize
	}
	if d.Status == "failed" {
		version["error"] = gin.H{"code": d.ErrorCode, "message": d.ErrorMessage}
	}
	body := gin.H{
		"id":                d.ID.String(),
		"workspace_id":      d.WorkspaceID.String(),
		"name":              d.Name,
		"original_filename": d.OriginalName,
		"file_format":       d.FileFormat,
		"status":            d.Status,
		"current_version":   version,
		"created_at":        d.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":        d.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if d.QualityScore != nil {
		body["quality_score"] = *d.QualityScore
	}
	return body
}

func writeDatasetError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeNotFound(c)
	case errors.Is(err, ErrInvalidName):
		httpx.ValidationFailed(c, httpx.ValidationDetails{Field: "name", Issue: "required"})
	case errors.Is(err, ErrNameTaken):
		httpx.WriteError(c, httpx.Err(http.StatusConflict, "DATASET_NAME_TAKEN", "Nama dataset sudah dipakai di ruang kerja ini."))
	case errors.Is(err, ErrBusy):
		httpx.WriteError(c, httpx.Err(http.StatusConflict, "DATASET_BUSY", "Dataset masih diproses. Coba lagi setelah selesai."))
	default:
		httpx.WriteError(c, err)
	}
}

func writeNotFound(c *gin.Context) {
	httpx.WriteError(c, httpx.Err(http.StatusNotFound, "NOT_FOUND", "Resource tidak ditemukan."))
}