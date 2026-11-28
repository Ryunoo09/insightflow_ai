package jobs

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"insightflow/api/internal/modules/datasets"
	"insightflow/api/internal/platform/database"
	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/pgconv"
)

func TestConcurrentClaimsDoNotDoubleClaim(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	dbURL := os.Getenv("TEST_DATABASE_URL")

	extraNames := []string{"Beta", "Gamma", "Delta", "Epsilon", "Zeta", "Eta"}
	versions := []uuid.UUID{uuid.UUID(h.version.ID.Bytes)}
	for _, name := range extraNames {
		id := uuid.New()
		if _, err := h.svc.Create(ctx, datasets.CreateInput{
			DatasetID: id, WorkspaceID: h.wsID, UserID: h.userID,
			Name: name, Format: datasets.FormatCSV, OriginalName: "d.csv",
			SizeBytes: 5, ChecksumSHA256: "c", SourcePath: "unused.csv",
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		v, err := h.q.GetDatasetVersionByNo(ctx, dbgen.GetDatasetVersionByNoParams{
			DatasetID: pgconv.UUID(id), VersionNo: 1,
		})
		if err != nil {
			t.Fatalf("version %s: %v", name, err)
		}
		versions = append(versions, uuid.UUID(v.ID.Bytes))
	}

	// Make our jobs the oldest due rows so the claim order is deterministic.
	for _, v := range versions {
		job, err := h.q.GetLatestJobForVersion(ctx, pgconv.UUID(v))
		if err != nil {
			t.Fatalf("job: %v", err)
		}
		if _, err := h.pool.Exec(ctx, "UPDATE processing_jobs SET run_after = to_timestamp(0) WHERE id = $1", job.ID); err != nil {
			t.Fatalf("backdate job: %v", err)
		}
	}

	second, err := database.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("second pool: %v", err)
	}
	t.Cleanup(second.Close)
	pools := []*pgxpool.Pool{h.pool, second}

	var mu sync.Mutex
	var claimed []dbgen.ProcessingJob
	var wg sync.WaitGroup
	for _, p := range pools {
		repo := NewRepository(dbgen.New(p))
		wg.Add(1)
		go func(repo *Repository) {
			defer wg.Done()
			empty := 0
			for empty < 3 {
				job, err := repo.Claim(ctx)
				if errors.Is(err, ErrNoJob) {
					empty++
					time.Sleep(5 * time.Millisecond)
					continue
				}
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				empty = 0
				mu.Lock()
				claimed = append(claimed, job)
				mu.Unlock()
			}
		}(repo)
	}
	wg.Wait()

	seenJob := map[uuid.UUID]int{}
	for _, job := range claimed {
		seenJob[uuid.UUID(job.ID.Bytes)]++
	}
	for id, n := range seenJob {
		if n > 1 {
			t.Fatalf("job %s was claimed %d times", id, n)
		}
	}

	seenVersion := map[uuid.UUID]int{}
	for _, job := range claimed {
		seenVersion[uuid.UUID(job.VersionID.Bytes)]++
	}
	for _, v := range versions {
		if seenVersion[v] != 1 {
			t.Fatalf("version %s claimed %d times, want exactly 1", v, seenVersion[v])
		}
	}
}

func TestReaperRecoversStaleJobs(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	job, err := h.q.GetLatestJobForVersion(ctx, h.version.ID)
	if err != nil {
		t.Fatalf("job: %v", err)
	}
	setProcessing := func(attempts int32, age string) {
		t.Helper()
		if _, err := h.pool.Exec(ctx,
			"UPDATE processing_jobs SET status='processing', locked_at = now() - $2::interval, attempts = $3 WHERE id = $1",
			job.ID, age, attempts); err != nil {
			t.Fatalf("set processing: %v", err)
		}
	}
	getJob := func() dbgen.ProcessingJob {
		t.Helper()
		got, err := h.q.GetLatestJobForVersion(ctx, h.version.ID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		return got
	}

	reaper := NewReaper(h.repo, h.svc, time.Hour, time.Hour)

	// A fresh processing job must be left alone.
	setProcessing(1, "0 seconds")
	reaper.ReapOnce(ctx)
	if got := getJob(); got.Status != "processing" {
		t.Fatalf("fresh job status = %q, want processing", got.Status)
	}

	// Stale with attempts left is requeued; a second pass is a no-op.
	setProcessing(1, "2 hours")
	reaper.ReapOnce(ctx)
	if got := getJob(); got.Status != "queued" || got.LockedAt.Valid {
		t.Fatalf("stale job not requeued: status=%q locked=%v", got.Status, got.LockedAt.Valid)
	}
	reaper.ReapOnce(ctx)
	if got := getJob(); got.Status != "queued" {
		t.Fatalf("reaper is not idempotent: status=%q", got.Status)
	}

	// Stale with attempts exhausted fails the job and the version.
	setProcessing(3, "2 hours")
	reaper.ReapOnce(ctx)
	if got := getJob(); got.Status != "failed" {
		t.Fatalf("exhausted job status = %q, want failed", got.Status)
	}
	d, err := h.svc.Get(ctx, h.dsID, h.userID)
	if err != nil {
		t.Fatalf("get dataset: %v", err)
	}
	if d.Status != "failed" || d.ErrorCode != "DATASET_RESOURCE_EXHAUSTED" {
		t.Fatalf("version not failed correctly: status=%q code=%q", d.Status, d.ErrorCode)
	}
}