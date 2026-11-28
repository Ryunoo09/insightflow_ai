package analysis

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"insightflow/api/internal/platform/database"
	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/pgconv"
)

// testDeps returns a service plus a ready dataset owned by userID in wsID.
func testDeps(t *testing.T) (svc *Service, q *dbgen.Queries, wsID, userID, datasetID uuid.UUID) {
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
	q = dbgen.New(pool)

	user, err := q.CreateUser(ctx, dbgen.CreateUserParams{
		Email:        uuid.NewString() + "@example.com",
		PasswordHash: pgtype.Text{},
		FullName:     "Analis",
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	ws, err := q.CreateWorkspace(ctx, dbgen.CreateWorkspaceParams{
		Name:      "Analysis WS",
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

	dsID := uuid.New()
	if _, err := q.CreateDataset(ctx, dbgen.CreateDatasetParams{
		ID:          pgconv.UUID(dsID),
		WorkspaceID: ws.ID,
		Name:        "Penjualan",
		Description: "",
		CreatedBy:   user.ID,
	}); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	version, err := q.CreateDatasetVersion(ctx, dbgen.CreateDatasetVersionParams{
		DatasetID:      pgconv.UUID(dsID),
		VersionNo:      1,
		FileFormat:     "csv",
		OriginalName:   "penjualan.csv",
		ByteSize:       pgtype.Int8{Int64: 10, Valid: true},
		ChecksumSha256: pgtype.Text{String: "deadbeef", Valid: true},
		SourcePath:     "sources/x/y/1/source.csv",
	})
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	if _, err := q.InsertDatasetColumn(ctx, dbgen.InsertDatasetColumnParams{
		VersionID:    version.ID,
		ColumnName:   "amount",
		OriginalName: "Amount",
		InferredType: "number",
		IsNullable:   true,
		ColumnOrder:  1,
	}); err != nil {
		t.Fatalf("insert column: %v", err)
	}
	if _, err := q.UpsertDatasetProfile(ctx, dbgen.UpsertDatasetProfileParams{
		VersionID:    version.ID,
		ProfileJson:  []byte(`{"quality_score": 90, "columns": [{"name": "amount", "null_pct": 0}]}`),
		QualityScore: pgconv.Numeric(90),
	}); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}
	if _, err := q.MarkDatasetVersionReady(ctx, dbgen.MarkDatasetVersionReadyParams{
		ID:          version.ID,
		RowCount:    pgtype.Int8{Int64: 20, Valid: true},
		ColumnCount: pgtype.Int4{Int32: 1, Valid: true},
		ParquetPath: pgtype.Text{String: "parquet/x/y/1/data.parquet", Valid: true},
	}); err != nil {
		t.Fatalf("mark ready: %v", err)
	}

	return NewService(NewRepository(pool)), q, uuid.UUID(ws.ID.Bytes), uuid.UUID(user.ID.Bytes), dsID
}

func TestCreateSessionStoresHistoryAndBuildsContext(t *testing.T) {
	svc, q, wsID, userID, datasetID := testDeps(t)
	ctx := context.Background()

	session, err := svc.CreateSession(ctx, wsID, datasetID, userID, "  Analisis penjualan  ")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if session.Title != "Analisis penjualan" {
		t.Fatalf("title = %q, want trimmed", session.Title)
	}

	if _, err := q.CreateAnalysisMessage(ctx, dbgen.CreateAnalysisMessageParams{
		SessionID: pgconv.UUID(session.ID), Role: "user", Content: "total amount?",
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}

	detail, err := svc.GetSession(ctx, session.ID, userID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(detail.Messages) != 1 || detail.Messages[0].Content != "total amount?" {
		t.Fatalf("messages = %+v", detail.Messages)
	}
	if detail.Context.Dataset.DatasetID != datasetID.String() {
		t.Fatalf("context dataset = %q, want %q", detail.Context.Dataset.DatasetID, datasetID)
	}
	if len(detail.Context.Dataset.Columns) != 1 || detail.Context.Dataset.Columns[0].OriginalName != "Amount" {
		t.Fatalf("context columns = %+v", detail.Context.Dataset.Columns)
	}
	if len(detail.Context.Messages) != 1 || detail.Context.Messages[0].Role != "user" {
		t.Fatalf("context messages = %+v", detail.Context.Messages)
	}
}

func TestCreateSessionDefaultsEmptyTitle(t *testing.T) {
	svc, _, wsID, userID, datasetID := testDeps(t)
	session, err := svc.CreateSession(context.Background(), wsID, datasetID, userID, "   ")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if session.Title != defaultTitle {
		t.Fatalf("title = %q, want %q", session.Title, defaultTitle)
	}
}

func TestSessionIsInvisibleToNonMember(t *testing.T) {
	svc, q, wsID, userID, datasetID := testDeps(t)
	ctx := context.Background()
	session, err := svc.CreateSession(ctx, wsID, datasetID, userID, "private")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	outsider, err := q.CreateUser(ctx, dbgen.CreateUserParams{
		Email: uuid.NewString() + "@example.com", PasswordHash: pgtype.Text{},
		FullName: "Outsider", Status: "active",
	})
	if err != nil {
		t.Fatalf("create outsider: %v", err)
	}
	outsiderID := uuid.UUID(outsider.ID.Bytes)

	if _, err := svc.GetSession(ctx, session.ID, outsiderID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSession as outsider = %v, want ErrNotFound", err)
	}
	if _, err := svc.CreateSession(ctx, wsID, datasetID, outsiderID, "hijack"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateSession as outsider = %v, want ErrNotFound", err)
	}
	if _, _, err := svc.ListSessions(ctx, wsID, outsiderID, 1, 20); err != nil {
		t.Fatalf("ListSessions as outsider: %v", err)
	}
	// An outsider's list must be empty, not the owner's sessions.
	items, total, err := svc.ListSessions(ctx, wsID, outsiderID, 1, 20)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(items) != 0 || total != 0 {
		t.Fatalf("outsider list = %d items, total %d, want 0", len(items), total)
	}
}

func TestGetSessionRequiresReadyVersion(t *testing.T) {
	svc, q, wsID, userID, datasetID := testDeps(t)
	ctx := context.Background()
	session, err := svc.CreateSession(ctx, wsID, datasetID, userID, "queued dataset")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	version, err := q.GetDatasetVersionByNo(ctx, dbgen.GetDatasetVersionByNoParams{
		DatasetID: pgconv.UUID(datasetID), VersionNo: 1,
	})
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	if _, err := q.MarkDatasetVersionProcessing(ctx, version.ID); err != nil {
		t.Fatalf("mark processing: %v", err)
	}

	if _, err := svc.GetSession(ctx, session.ID, userID); !errors.Is(err, ErrNotReady) {
		t.Fatalf("GetSession = %v, want ErrNotReady", err)
	}
}

func TestListSessionsIsPaginated(t *testing.T) {
	svc, _, wsID, userID, datasetID := testDeps(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := svc.CreateSession(ctx, wsID, datasetID, userID, "sesi"); err != nil {
			t.Fatalf("CreateSession %d: %v", i, err)
		}
	}
	items, total, err := svc.ListSessions(ctx, wsID, userID, 1, 2)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if total != 3 || len(items) != 2 {
		t.Fatalf("page = %d items of %d, want 2 of 3", len(items), total)
	}
}
