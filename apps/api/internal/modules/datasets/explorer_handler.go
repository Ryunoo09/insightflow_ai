package datasets

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"insightflow/api/internal/middleware"
	"insightflow/api/internal/platform/engineclient"
	"insightflow/api/internal/platform/httpx"
)

const (
	defaultExplorerLimit = 100
	maxExplorerLimit     = 200
)

// Explorer handles POST /datasets/:dataset_id/rows/query: it authorizes the
// caller, resolves the target version, and proxies the page query to the engine.
func (h *Handler) Explorer(c *gin.Context) {
	principal, ok := middleware.PrincipalFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, nil)
		return
	}
	datasetID, ok := parseDatasetID(c)
	if !ok {
		return
	}

	var req struct {
		Offset    int                           `json:"offset"`
		Limit     int                           `json:"limit"`
		VersionNo int32                         `json:"version_no"`
		Sort      []engineclient.ExplorerSort   `json:"sort"`
		Filters   []engineclient.ExplorerFilter `json:"filters"`
		Search    string                        `json:"search"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		httpx.WriteBindError(c, err)
		return
	}
	if req.Offset < 0 {
		req.Offset = 0
	}
	if req.Limit <= 0 {
		req.Limit = defaultExplorerLimit
	}
	if req.Limit > maxExplorerLimit {
		req.Limit = maxExplorerLimit
	}

	target, err := h.svc.ExplorerTarget(c.Request.Context(), datasetID, principal.ID, req.VersionNo)
	if err != nil {
		writeDatasetError(c, err)
		return
	}
	// Identifiers must exist in the version's persisted column metadata before
	// any query reaches the engine (Plan 05 SEC-PARAM-SQL).
	if !validExplorerColumns(target.Columns, req.Sort, req.Filters) {
		httpx.WriteError(c, httpx.Err(http.StatusUnprocessableEntity, "EXPLORER_QUERY_INVALID", "Filter atau urutan kolom tidak valid.").
			WithDetails(httpx.ValidationDetails{Field: "column", Issue: "unknown"}))
		return
	}
	if h.engine == nil {
		httpx.WriteError(c, httpx.Err(http.StatusServiceUnavailable, "ENGINE_UNAVAILABLE", "Explorer sementara tidak tersedia."))
		return
	}

	out, err := h.engine.QueryRows(c.Request.Context(), httpx.RequestID(c), engineclient.QueryRowsRequest{
		DatasetRef: engineclient.DatasetRef{
			WorkspaceID: target.WorkspaceID.String(),
			DatasetID:   target.DatasetID.String(),
			VersionNo:   target.VersionNo,
		},
		Offset:  req.Offset,
		Limit:   req.Limit,
		Sort:    req.Sort,
		Filters: req.Filters,
		Search:  req.Search,
	})
	if err != nil {
		writeExplorerError(c, err)
		return
	}
	// Restore the uploaded header for display; the normalized name stays the
	// field identifier used by rows/sort/filter.
	for i := range out.Columns {
		out.Columns[i].OriginalName = target.OriginalNames[out.Columns[i].Name]
	}
	c.JSON(http.StatusOK, out)
}

// Versions handles GET /datasets/:dataset_id/versions for the version switcher.
func (h *Handler) Versions(c *gin.Context) {
	principal, ok := middleware.PrincipalFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, nil)
		return
	}
	datasetID, ok := parseDatasetID(c)
	if !ok {
		return
	}
	versions, err := h.svc.Versions(c.Request.Context(), datasetID, principal.ID)
	if err != nil {
		writeDatasetError(c, err)
		return
	}
	data := make([]gin.H, 0, len(versions))
	for _, v := range versions {
		item := gin.H{
			"version_no": v.VersionNo,
			"status":     v.Status,
			"created_at": v.CreatedAt.Time.UTC().Format(time.RFC3339),
		}
		if v.RowCount.Valid {
			item["row_count"] = v.RowCount.Int64
		}
		if v.ColumnCount.Valid {
			item["column_count"] = v.ColumnCount.Int32
		}
		data = append(data, item)
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

// writeExplorerError maps engine failures onto safe client errors.
func writeExplorerError(c *gin.Context, err error) {
	var engineErr *engineclient.Error
	if errors.As(err, &engineErr) {
		switch {
		case engineErr.StatusCode == http.StatusGatewayTimeout:
			httpx.WriteError(c, httpx.Err(http.StatusGatewayTimeout, "EXPLORER_TIMEOUT", "Query melebihi batas waktu."))
			return
		case engineErr.StatusCode == http.StatusConflict:
			httpx.WriteError(c, httpx.Err(http.StatusConflict, "DATASET_NOT_READY", "Dataset belum siap dieksplorasi."))
			return
		case engineErr.StatusCode >= 400 && engineErr.StatusCode < 500:
			httpx.WriteError(c, httpx.Err(http.StatusUnprocessableEntity, "EXPLORER_QUERY_INVALID", "Filter atau urutan kolom tidak valid."))
			return
		}
	}
	httpx.WriteError(c, httpx.Err(http.StatusServiceUnavailable, "ENGINE_UNAVAILABLE", "Explorer sementara tidak tersedia."))
}

// validExplorerColumns checks every sort/filter identifier against the
// version's authoritative column metadata.
func validExplorerColumns(known map[string]struct{}, sorts []engineclient.ExplorerSort, filters []engineclient.ExplorerFilter) bool {
	for _, sort := range sorts {
		if _, ok := known[sort.Column]; !ok {
			return false
		}
	}
	for _, filter := range filters {
		if _, ok := known[filter.Column]; !ok {
			return false
		}
	}
	return true
}
