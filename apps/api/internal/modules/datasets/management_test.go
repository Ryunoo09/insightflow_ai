package datasets

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/pgconv"
)

func mustCreate(t *testing.T, svc *Service, wsID, userID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	source, err := svc.Resolver().SourcePath(wsID.String(), id.String(), 1, FormatCSV)
	if err != nil {
		t.Fatalf("source path: %v", err)
	}
	if _, err := svc.Create(context.Background(), CreateInput{
		DatasetID:      id,
		WorkspaceID:    wsID,
		UserID:         userID,
		Name:           name,
		Format:         FormatCSV,
		OriginalName:   "data.csv",
		SizeBytes:      10,
		ChecksumSHA256: "deadbeef",
		SourcePath:     source,
	}); err != nil {
		t.Fatalf("create dataset %q: %v", name, err)
	}
	return id
}

func TestRenameEnforcesUniqueNamePerWorkspace(t *testing.T) {
	svc, q, wsID, userID := testDeps(t)
	ctx := context.Background()

	a := mustCreate(t, svc, wsID, userID, "Penjualan")
	b := mustCreate(t, svc, wsID, userID, "Pembelian")

	if _, err := svc.Rename(ctx, a, userID, "Omzet"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := svc.Rename(ctx, b, userID, "Omzet"); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate rename err = %v, want ErrNameTaken", err)
	}
	if _, err := svc.Rename(ctx, b, userID, "   "); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("blank rename err = %v, want ErrInvalidName", err)
	}

	// Same name in a different workspace is allowed.
	u2, err := q.CreateUser(ctx, dbgen.CreateUserParams{Email: uuid.NewString() + "@example.com", FullName: "Other", Status: "active"})
	if err != nil {
		t.Fatalf("create user2: %v", err)
	}
	w2, err := q.CreateWorkspace(ctx, dbgen.CreateWorkspaceParams{Name: "WS2", Slug: "ws-" + uuid.NewString()[:8], CreatedBy: u2.ID})
	if err != nil {
		t.Fatalf("create ws2: %v", err)
	}
	if _, err := q.AddWorkspaceMember(ctx, dbgen.AddWorkspaceMemberParams{WorkspaceID: w2.ID, UserID: u2.ID, Role: "owner"}); err != nil {
		t.Fatalf("add member2: %v", err)
	}
	c := mustCreate(t, svc, uuid.UUID(w2.ID.Bytes), uuid.UUID(u2.ID.Bytes), "Lain")
	if _, err := svc.Rename(ctx, c, uuid.UUID(u2.ID.Bytes), "Omzet"); err != nil {
		t.Fatalf("same name in another workspace must succeed: %v", err)
	}
}

func TestSoftDeleteHidesDatasetAndFailsJobs(t *testing.T) {
	svc, q, wsID, userID := testDeps(t)
	ctx := context.Background()

	id := mustCreate(t, svc, wsID, userID, "Hapus")
	if err := svc.SoftDelete(ctx, id, userID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if _, err := svc.Get(ctx, id, userID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete err = %v, want ErrNotFound", err)
	}
	items, _, err := svc.List(ctx, wsID, userID, ListParams{Page: 1, PerPage: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, d := range items {
		if d.ID == id {
			t.Fatal("soft-deleted dataset still listed")
		}
	}

	raw, err := q.GetDatasetByID(ctx, pgconv.UUID(id))
	if err != nil {
		t.Fatalf("raw dataset: %v", err)
	}
	if !raw.DeletedAt.Valid || !raw.PurgeAfter.Valid {
		t.Fatalf("delete markers not set: %+v", raw)
	}

	ver, err := q.GetDatasetVersionByNo(ctx, dbgen.GetDatasetVersionByNoParams{DatasetID: pgconv.UUID(id), VersionNo: 1})
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	job, err := q.GetLatestJobForVersion(ctx, ver.ID)
	if err != nil {
		t.Fatalf("job: %v", err)
	}
	if job.Status != "failed" {
		t.Fatalf("job status = %q, want failed after delete", job.Status)
	}
}

func TestJanitorPurgesFilesAndRow(t *testing.T) {
	svc, q, wsID, userID := testDeps(t)
	ctx := context.Background()

	id := mustCreate(t, svc, wsID, userID, "Purge")
	resolver := svc.Resolver()
	source, err := resolver.SourcePath(wsID.String(), id.String(), 1, FormatCSV)
	if err != nil {
		t.Fatalf("source path: %v", err)
	}
	parquet, err := resolver.ParquetPath(wsID.String(), id.String(), 1)
	if err != nil {
		t.Fatalf("parquet path: %v", err)
	}
	writeFile(t, source)
	writeFile(t, parquet)

	ver, err := q.GetDatasetVersionByNo(ctx, dbgen.GetDatasetVersionByNoParams{DatasetID: pgconv.UUID(id), VersionNo: 1})
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if _, err := q.MarkDatasetVersionReady(ctx, dbgen.MarkDatasetVersionReadyParams{
		ID:               ver.ID,
		ParquetPath:      pgtype.Text{String: parquet, Valid: true},
		ProcessingReport: []byte("{}"),
	}); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	if err := svc.SoftDelete(ctx, id, userID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	// Backdate the deadline so the janitor picks it up on this tick.
	if _, err := svc.repo.db.Exec(ctx, "UPDATE datasets SET purge_after = now() - interval '1 day' WHERE id = $1", pgconv.UUID(id)); err != nil {
		t.Fatalf("backdate purge: %v", err)
	}

	janitor := NewJanitor(svc.repo, time.Hour)
	janitor.PurgeOnce(ctx)
	janitor.PurgeOnce(ctx) // idempotent

	if _, err := os.Stat(source); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("source file still present: %v", err)
	}
	if _, err := os.Stat(parquet); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("parquet file still present: %v", err)
	}
	if _, err := q.GetDatasetByID(ctx, pgconv.UUID(id)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("dataset row still present: %v", err)
	}
}

func TestReprocessVersionsAndRetention(t *testing.T) {
	svc, q, wsID, userID := testDeps(t)
	ctx := context.Background()

	id := mustCreate(t, svc, wsID, userID, "Repro")
	resolver := svc.Resolver()
	complete := func(versionNo int32) {
		t.Helper()
		ver, err := q.GetDatasetVersionByNo(ctx, dbgen.GetDatasetVersionByNoParams{DatasetID: pgconv.UUID(id), VersionNo: versionNo})
		if err != nil {
			t.Fatalf("version %d: %v", versionNo, err)
		}
		parquet, err := resolver.ParquetPath(wsID.String(), id.String(), int(versionNo))
		if err != nil {
			t.Fatalf("parquet path: %v", err)
		}
		writeFile(t, parquet)
		if err := svc.Complete(ctx, uuid.UUID(ver.ID.Bytes), parquet, ProcessResult{
			RowCount: 1, ColumnCount: 1,
			Profile: []byte(`{}`), ProcessingReport: []byte(`{}`),
		}); err != nil {
			t.Fatalf("complete v%d: %v", versionNo, err)
		}
	}

	complete(1)
	d, err := svc.Reprocess(ctx, id, userID)
	if err != nil {
		t.Fatalf("reprocess: %v", err)
	}
	if d.CurrentVersionNo != 2 {
		t.Fatalf("current version = %d, want 2", d.CurrentVersionNo)
	}
	v1, _ := q.GetDatasetVersionByNo(ctx, dbgen.GetDatasetVersionByNoParams{DatasetID: pgconv.UUID(id), VersionNo: 1})
	if v1.Status != "ready" {
		t.Fatalf("v1 status = %q, want ready (not overwritten)", v1.Status)
	}
	if _, err := svc.Reprocess(ctx, id, userID); !errors.Is(err, ErrBusy) {
		t.Fatalf("reprocess while queued err = %v, want ErrBusy", err)
	}

	complete(2)
	if _, err := svc.Reprocess(ctx, id, userID); err != nil {
		t.Fatalf("reprocess v3: %v", err)
	}
	complete(3)
	if _, err := svc.Reprocess(ctx, id, userID); err != nil {
		t.Fatalf("reprocess v4: %v", err)
	}
	complete(4)

	if _, err := q.GetDatasetVersionByNo(ctx, dbgen.GetDatasetVersionByNoParams{DatasetID: pgconv.UUID(id), VersionNo: 1}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("v1 must be pruned, err = %v", err)
	}
	v1parquet, _ := resolver.ParquetPath(wsID.String(), id.String(), 1)
	if _, err := os.Stat(v1parquet); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("v1 parquet must be removed: %v", err)
	}
	for _, n := range []int32{2, 3, 4} {
		if _, err := q.GetDatasetVersionByNo(ctx, dbgen.GetDatasetVersionByNoParams{DatasetID: pgconv.UUID(id), VersionNo: n}); err != nil {
			t.Fatalf("version %d must remain: %v", n, err)
		}
	}
}

func TestListPaginationSearchFilterSort(t *testing.T) {
	svc, _, wsID, userID := testDeps(t)
	ctx := context.Background()
	for _, name := range []string{"Alpha", "beta", "Gamma", "delta", "Epsilon"} {
		mustCreate(t, svc, wsID, userID, name)
	}

	items, res, err := svc.List(ctx, wsID, userID, ListParams{Page: 1, PerPage: 2})
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if res.Total != 5 || len(items) != 2 {
		t.Fatalf("page1 total=%d len=%d, want 5/2", res.Total, len(items))
	}
	if _, res3, err := svc.List(ctx, wsID, userID, ListParams{Page: 3, PerPage: 2}); err != nil || res3.Total != 5 {
		t.Fatalf("page3: %v total=%d", err, res3.Total)
	}

	if _, sRes, err := svc.List(ctx, wsID, userID, ListParams{Page: 1, PerPage: 100, Query: "a"}); err != nil || sRes.Total != 4 {
		t.Fatalf("search 'a' total=%d err=%v, want 4", sRes.Total, err)
	}
	// A literal % must not act as a wildcard.
	if _, wRes, err := svc.List(ctx, wsID, userID, ListParams{Page: 1, PerPage: 100, Query: "%"}); err != nil || wRes.Total != 0 {
		t.Fatalf("search '%%' total=%d err=%v, want 0", wRes.Total, err)
	}
	if _, fRes, err := svc.List(ctx, wsID, userID, ListParams{Page: 1, PerPage: 100, Status: "ready"}); err != nil || fRes.Total != 0 {
		t.Fatalf("filter ready total=%d err=%v, want 0", fRes.Total, err)
	}
	sorted, _, err := svc.List(ctx, wsID, userID, ListParams{Page: 1, PerPage: 100, Sort: "name", Order: "asc"})
	if err != nil {
		t.Fatalf("sort: %v", err)
	}
	if len(sorted) == 0 || sorted[0].Name != "Alpha" {
		t.Fatalf("sort name asc first = %v", sorted)
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}