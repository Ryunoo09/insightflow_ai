package datasets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"insightflow/api/internal/modules/users"
	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/pgconv"
)

func TestExplorerRejectsNonMember(t *testing.T) {
	svc, q, wsID, ownerID := testDeps(t)
	dsID := mustCreate(t, svc, wsID, ownerID, "Explorer akses")

	other, err := q.CreateUser(context.Background(), dbgen.CreateUserParams{
		Email:    uuid.NewString() + "@example.com",
		FullName: "Outsider",
		Status:   "active",
	})
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}

	rec := explorerRequest(t, NewHandler(svc, nil, 1<<20, 1<<20), dsID, uuid.UUID(other.ID.Bytes), `{"offset":0,"limit":10}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestExplorerRequiresReadyVersion(t *testing.T) {
	svc, _, wsID, userID := testDeps(t)
	dsID := mustCreate(t, svc, wsID, userID, "Explorer belum siap")

	rec := explorerRequest(t, NewHandler(svc, nil, 1<<20, 1<<20), dsID, userID, `{}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body.Error.Code != "DATASET_NOT_READY" {
		t.Fatalf("code = %q, want DATASET_NOT_READY", body.Error.Code)
	}
}

func markReady(t *testing.T, svc *Service, q *dbgen.Queries, datasetID uuid.UUID, columnName string) {
	t.Helper()
	ctx := context.Background()
	version, err := q.GetDatasetVersionByNo(ctx, dbgen.GetDatasetVersionByNoParams{
		DatasetID: pgconv.UUID(datasetID),
		VersionNo: 1,
	})
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if err := svc.Complete(ctx, uuid.UUID(version.ID.Bytes), "data.parquet", ProcessResult{
		RowCount:    2,
		ColumnCount: 1,
		Columns: []ColumnResult{{
			Ordinal:      1,
			Name:         columnName,
			OriginalName: columnName,
			LogicalType:  "number",
			Role:         "measure",
		}},
		Profile: []byte(`{"quality_score": 90}`),
	}); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

func TestExplorerRejectsUnknownColumn(t *testing.T) {
	svc, q, wsID, userID := testDeps(t)
	dsID := mustCreate(t, svc, wsID, userID, "Explorer kolom")
	markReady(t, svc, q, dsID, "revenue")

	rec := explorerRequest(t, NewHandler(svc, nil, 1<<20, 1<<20), dsID, userID, `{"sort":[{"column":"secret","direction":"asc"}]}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "EXPLORER_QUERY_INVALID") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestExplorerAllowsKnownColumnThenNeedsEngine(t *testing.T) {
	svc, q, wsID, userID := testDeps(t)
	dsID := mustCreate(t, svc, wsID, userID, "Explorer kolom valid")
	markReady(t, svc, q, dsID, "revenue")

	rec := explorerRequest(t, NewHandler(svc, nil, 1<<20, 1<<20), dsID, userID, `{"sort":[{"column":"revenue","direction":"desc"}]}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 once the allowlist passes (body %s)", rec.Code, rec.Body.String())
	}
}

func explorerRequest(t *testing.T, h *Handler, datasetID, userID uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/datasets/:dataset_id/rows/query", func(c *gin.Context) {
		c.Set("auth_principal", users.Principal{ID: userID})
		h.Explorer(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/datasets/"+datasetID.String()+"/rows/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}
