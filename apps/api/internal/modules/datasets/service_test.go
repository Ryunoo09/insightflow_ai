package datasets

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"insightflow/api/internal/platform/database"
	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/pgconv"
	"insightflow/api/internal/platform/storage"
)

func testDeps(t *testing.T) (*Service, *dbgen.Queries, uuid.UUID, uuid.UUID) {
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
	svc := NewService(NewRepository(pool), database.NewTxManager(pool), resolver, Limits{
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
		Name:      "Test WS",
		Slug:      "ws-" + uuid.NewString()[:8],
		CreatedBy: user.ID,
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := q.AddWorkspaceMember(ctx, dbgen.AddWorkspaceMemberParams{
		WorkspaceID: ws.ID,
		UserID:      user.ID,
		Role:        "owner",
	}); err != nil {
		t.Fatalf("add member: %v", err)
	}
	return svc, q, uuid.UUID(ws.ID.Bytes), uuid.UUID(user.ID.Bytes)
}

func newInput(datasetID, wsID, userID uuid.UUID) CreateInput {
	return CreateInput{
		DatasetID:      datasetID,
		WorkspaceID:    wsID,
		UserID:         userID,
		Name:           "Penjualan",
		Format:         FormatCSV,
		OriginalName:   "penjualan.csv",
		SizeBytes:      123,
		ChecksumSHA256: "deadbeef",
		SourcePath:     filepath.Join("sources", datasetID.String(), "source.csv"),
	}
}

func TestCreatePersistsDatasetVersionAndJob(t *testing.T) {
	svc, q, wsID, userID := testDeps(t)
	ctx := context.Background()
	datasetID := uuid.New()

	d, err := svc.Create(ctx, newInput(datasetID, wsID, userID))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if d.Status != "queued" || d.CurrentVersionNo != 1 {
		t.Fatalf("unexpected dataset projection: %+v", d)
	}

	version, err := q.GetDatasetVersionByNo(ctx, dbgen.GetDatasetVersionByNoParams{
		DatasetID: pgconv.UUID(datasetID),
		VersionNo: 1,
	})
	if err != nil {
		t.Fatalf("version not created: %v", err)
	}
	job, err := q.GetLatestJobForVersion(ctx, version.ID)
	if err != nil {
		t.Fatalf("job not enqueued: %v", err)
	}
	if job.JobType != "dataset_ingest" || job.Status != "queued" {
		t.Fatalf("unexpected job: %+v", job)
	}
}

func TestCreateRollsBackOnFailure(t *testing.T) {
	svc, q, wsID, userID := testDeps(t)
	ctx := context.Background()
	datasetID := uuid.New()

	if _, err := svc.Create(ctx, newInput(datasetID, wsID, userID)); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	// Reusing the primary key fails at the dataset insert; the whole
	// transaction (version + job) must roll back.
	if _, err := svc.Create(ctx, newInput(datasetID, wsID, userID)); err == nil {
		t.Fatal("expected duplicate dataset id to fail")
	}

	count, err := q.CountWorkspaceDatasets(ctx, pgconv.UUID(wsID))
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("rollback left extra datasets: count=%d", count)
	}
}

func TestCreateSignalsWorkerOnce(t *testing.T) {
	svc, _, wsID, userID := testDeps(t)
	notifier := &fakeNotifier{}
	svc.notifier = notifier

	if _, err := svc.Create(context.Background(), newInput(uuid.New(), wsID, userID)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("signal calls = %d; want 1", notifier.calls)
	}
}

func TestCreateRollbackDoesNotSignal(t *testing.T) {
	svc, _, wsID, userID := testDeps(t)
	ctx := context.Background()
	datasetID := uuid.New()

	if _, err := svc.Create(ctx, newInput(datasetID, wsID, userID)); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	notifier := &fakeNotifier{}
	svc.notifier = notifier
	if _, err := svc.Create(ctx, newInput(datasetID, wsID, userID)); err == nil {
		t.Fatal("expected duplicate dataset id to fail")
	}
	if notifier.calls != 0 {
		t.Fatalf("signal calls = %d; want 0 after rollback", notifier.calls)
	}
}

func TestCreateToleratesSignalFailure(t *testing.T) {
	svc, q, wsID, userID := testDeps(t)
	ctx := context.Background()
	notifier := &fakeNotifier{err: errors.New("redis down")}
	svc.notifier = notifier

	d, err := svc.Create(ctx, newInput(uuid.New(), wsID, userID))
	if err != nil {
		t.Fatalf("Create must succeed when the signal fails: %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("signal calls = %d; want 1", notifier.calls)
	}

	version, err := q.GetDatasetVersionByNo(ctx, dbgen.GetDatasetVersionByNoParams{
		DatasetID: pgconv.UUID(d.ID),
		VersionNo: 1,
	})
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	job, err := q.GetLatestJobForVersion(ctx, version.ID)
	if err != nil || job.Status != "queued" {
		t.Fatalf("job must stay queued for polling: %+v, %v", job, err)
	}
}

type fakeNotifier struct {
	calls int
	err   error
}

func (f *fakeNotifier) Signal(context.Context) error {
	f.calls++
	return f.err
}

func TestCompletePersistsColumnsAndProfile(t *testing.T) {
	svc, q, wsID, userID := testDeps(t)
	ctx := context.Background()
	datasetID := uuid.New()

	if _, err := svc.Create(ctx, newInput(datasetID, wsID, userID)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	version, err := q.GetDatasetVersionByNo(ctx, dbgen.GetDatasetVersionByNoParams{
		DatasetID: pgconv.UUID(datasetID),
		VersionNo: 1,
	})
	if err != nil {
		t.Fatalf("version: %v", err)
	}

	res := ProcessResult{
		RowCount:    3,
		ColumnCount: 2,
		Columns: []ColumnResult{
			{Ordinal: 1, Name: "tanggal", OriginalName: "Tanggal", LogicalType: "date", Role: "time"},
			{Ordinal: 2, Name: "revenue", OriginalName: "Revenue", LogicalType: "decimal", Role: "measure"},
		},
		ParquetSHA256:    "abc123",
		Profile:          json.RawMessage(`{"quality_score": 88.5}`),
		ProcessingReport: json.RawMessage(`{"report_version":1}`),
	}
	if err := svc.Complete(ctx, uuid.UUID(version.ID.Bytes), "/storage/parquet/x/data.parquet", res); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got, err := svc.Get(ctx, datasetID, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "ready" || got.QualityScore == nil || *got.QualityScore != 88.5 {
		t.Fatalf("unexpected summary: %+v", got)
	}
	if got.RowCount == nil || *got.RowCount != 3 {
		t.Fatalf("row count not persisted: %+v", got.RowCount)
	}

	cols, err := q.ListDatasetColumns(ctx, version.ID)
	if err != nil {
		t.Fatalf("list columns: %v", err)
	}
	if len(cols) != 2 || cols[0].ColumnName != "tanggal" {
		t.Fatalf("unexpected columns: %+v", cols)
	}
	if _, err := q.GetDatasetProfile(ctx, version.ID); err != nil {
		t.Fatalf("profile missing: %v", err)
	}
}
