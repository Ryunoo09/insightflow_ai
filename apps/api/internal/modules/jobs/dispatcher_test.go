package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"insightflow/api/internal/modules/datasets"
	"insightflow/api/internal/platform/database"
	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/engineclient"
	"insightflow/api/internal/platform/pgconv"
	"insightflow/api/internal/platform/storage"
)

type harness struct {
	svc     *datasets.Service
	q       *dbgen.Queries
	pool    *pgxpool.Pool
	repo    *Repository
	wsID    uuid.UUID
	userID  uuid.UUID
	dsID    uuid.UUID
	version dbgen.DatasetVersion
}

// makeDue backdates our job's run_after so this test's job is the oldest due
// job. ClaimNextJob orders by run_after, and earlier datasets/jobs tests may
// have left committed jobs in the shared test database; backdating makes the
// selection deterministic without touching other rows.
func (h *harness) makeDue(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	job, err := h.q.GetLatestJobForVersion(ctx, h.version.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if _, err := h.pool.Exec(ctx, "UPDATE processing_jobs SET run_after = to_timestamp(0) WHERE id = $1", job.ID); err != nil {
		t.Fatalf("backdate job: %v", err)
	}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := database.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("database.NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	q := dbgen.New(pool)

	resolver, err := storage.NewResolver(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewResolver: %v", err)
	}
	svc := datasets.NewService(datasets.NewRepository(pool), database.NewTxManager(pool), resolver, datasets.Limits{
		MaxDatasetsPerWorkspace: 20,
		MaxRowsCSV:              1_000_000,
		MaxRowsXLSX:             200_000,
		MaxColumns:              200,
		PurgeGraceDays:          7,
	}, nil)

	user, err := q.CreateUser(ctx, dbgen.CreateUserParams{
		Email:        uuid.NewString() + "@example.com",
		PasswordHash: pgtype.Text{},
		FullName:     "Tester",
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	ws, err := q.CreateWorkspace(ctx, dbgen.CreateWorkspaceParams{
		Name: "Job WS", Slug: "ws-" + uuid.NewString()[:8], CreatedBy: user.ID,
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := q.AddWorkspaceMember(ctx, dbgen.AddWorkspaceMemberParams{
		WorkspaceID: ws.ID, UserID: user.ID, Role: "owner",
	}); err != nil {
		t.Fatalf("add member: %v", err)
	}

	dsID := uuid.New()
	wsID := uuid.UUID(ws.ID.Bytes)
	if _, err := svc.Create(ctx, datasets.CreateInput{
		DatasetID: dsID, WorkspaceID: wsID, UserID: uuid.UUID(user.ID.Bytes),
		Name: "Penjualan", Format: datasets.FormatCSV, OriginalName: "penjualan.csv",
		SizeBytes: 10, ChecksumSHA256: "deadbeef", SourcePath: "unused.csv",
	}); err != nil {
		t.Fatalf("seed dataset: %v", err)
	}
	version, err := q.GetDatasetVersionByNo(ctx, dbgen.GetDatasetVersionByNoParams{
		DatasetID: pgconv.UUID(dsID), VersionNo: 1,
	})
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	return &harness{svc: svc, q: q, pool: pool, repo: NewRepository(q), wsID: wsID, userID: uuid.UUID(user.ID.Bytes), dsID: dsID, version: version}
}

func processBody() map[string]any {
	return map[string]any{
		"row_count":    2,
		"column_count": 2,
		"columns": []map[string]any{
			{"ordinal": 1, "name": "tanggal", "original_name": "Tanggal", "logical_type": "date", "physical_type": "DATE", "role": "time", "null_count": 0, "distinct_count": 2},
			{"ordinal": 2, "name": "revenue", "original_name": "Revenue", "logical_type": "decimal", "physical_type": "DOUBLE", "role": "measure", "null_count": 0, "distinct_count": 2},
		},
		"parquet":           map[string]any{"key": "parquet/x/data.parquet", "size_bytes": 500, "sha256": "abc123"},
		"profile":           map[string]any{"profile_schema_version": 1, "quality_score": 91.5, "row_count": 2, "column_count": 2, "columns": []any{}},
		"processing_report": map[string]any{"report_version": 1},
	}
}

func TestDispatcherCompletesJob(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/datasets/process" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("X-Internal-Token") != "tok" {
			t.Errorf("missing internal token")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(processBody())
	}))
	defer engine.Close()

	d := NewDispatcher(h.repo, h.svc, engineclient.New(engine.URL, "tok", 5*time.Second))
	h.makeDue(t)
	worked, err := d.ProcessOne(ctx)
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !worked {
		t.Fatal("expected a job to be claimed")
	}

	job, err := h.q.GetLatestJobForVersion(ctx, h.version.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.Status != "completed" {
		t.Fatalf("job status = %q, want completed", job.Status)
	}

	got, err := h.svc.Get(ctx, h.dsID, h.userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "ready" || got.QualityScore == nil || *got.QualityScore != 91.5 {
		t.Fatalf("unexpected dataset after dispatch: %+v", got)
	}
	cols, err := h.q.ListDatasetColumns(ctx, h.version.ID)
	if err != nil || len(cols) != 2 {
		t.Fatalf("columns not persisted: %v (%d)", err, len(cols))
	}
}

func TestDispatcherRequeuesTransientFailure(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "ENGINE_UNAVAILABLE", "message": "down", "retryable": true},
		})
	}))
	defer engine.Close()

	d := NewDispatcher(h.repo, h.svc, engineclient.New(engine.URL, "tok", 5*time.Second))
	h.makeDue(t)
	if _, err := d.ProcessOne(ctx); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}

	job, err := h.q.GetLatestJobForVersion(ctx, h.version.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.Status != "queued" || job.Attempts != 1 {
		t.Fatalf("expected requeue after 1 attempt: status=%q attempts=%d", job.Status, job.Attempts)
	}
	// run_after was pushed out, so the job is not immediately due.
	if !job.RunAfter.Valid || !job.RunAfter.Time.After(job.CreatedAt.Time) {
		t.Fatalf("run_after not deferred: %+v", job.RunAfter)
	}
}

func TestDispatcherFailsPermanentlyOnDataError(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "DATASET_PARSE_ERROR", "message": "rusak", "retryable": false},
		})
	}))
	defer engine.Close()

	d := NewDispatcher(h.repo, h.svc, engineclient.New(engine.URL, "tok", 5*time.Second))
	h.makeDue(t)
	if _, err := d.ProcessOne(ctx); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}

	job, err := h.q.GetLatestJobForVersion(ctx, h.version.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.Status != "failed" {
		t.Fatalf("job status = %q, want failed", job.Status)
	}
	got, err := h.svc.Get(ctx, h.dsID, h.userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "failed" || got.ErrorCode != "DATASET_PARSE_ERROR" {
		t.Fatalf("unexpected dataset: %+v", got)
	}
}
