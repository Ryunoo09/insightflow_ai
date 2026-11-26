package datasets

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"insightflow/api/internal/platform/dbgen"
)

// Janitor purges soft-deleted datasets whose grace period has elapsed. It
// removes files first (idempotent) and the row afterwards, so a crash between
// the two just retries on the next tick.
type Janitor struct {
	repo     *Repository
	interval time.Duration
	batch    int32
}

// NewJanitor builds the purge janitor.
func NewJanitor(repo *Repository, interval time.Duration) *Janitor {
	if interval <= 0 {
		interval = time.Hour
	}
	return &Janitor{repo: repo, interval: interval, batch: 100}
}

// Run blocks, purging on each tick, until ctx is cancelled.
func (j *Janitor) Run(ctx context.Context) {
	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()
	j.PurgeOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			j.PurgeOnce(ctx)
		}
	}
}

// PurgeOnce runs one purge pass.
func (j *Janitor) PurgeOnce(ctx context.Context) {
	due, err := j.repo.ListPurgeDue(ctx, j.batch)
	if err != nil {
		slog.Error("purge janitor: list due datasets", "error", err)
		return
	}
	for _, d := range due {
		datasetID := uuid.UUID(d.ID.Bytes)
		versions, err := j.repo.ListVersions(ctx, datasetID)
		if err != nil {
			slog.Error("purge janitor: list versions", "dataset_id", datasetID, "error", err)
			continue
		}
		for _, v := range versions {
			removeVersionFiles(v)
		}
		if err := j.repo.Purge(ctx, datasetID); err != nil {
			slog.Error("purge janitor: delete dataset", "dataset_id", datasetID, "error", err)
			continue
		}
		slog.Info("dataset purged", "dataset_id", datasetID, "versions", len(versions))
	}
}

// enforceRetention removes versions older than retentionVersions, files first.
//
// ponytail: files are deleted inside the same transaction as the row deletes;
// a rollback would leave rows pointing at missing files. Acceptable because the
// only caller runs right after a version turned ready, and a retry re-removes
// nothing. Upgrade to a post-commit file queue if this ever bites.
func enforceRetention(ctx context.Context, q *dbgen.Queries, datasetID pgtype.UUID) error {
	old, err := q.ListDatasetVersionsBeyondRetention(ctx, dbgen.ListDatasetVersionsBeyondRetentionParams{
		DatasetID: datasetID,
		Offset:    retentionVersions,
	})
	if err != nil {
		return err
	}
	for _, v := range old {
		removeVersionFiles(v)
		if err := q.DeleteDatasetVersion(ctx, v.ID); err != nil {
			return err
		}
	}
	return nil
}

// removeVersionFiles deletes a version's stored files. Missing files are fine
// (idempotent). Nothing absolute is logged.
func removeVersionFiles(v dbgen.DatasetVersion) {
	if v.ParquetPath.Valid && v.ParquetPath.String != "" {
		if err := os.Remove(v.ParquetPath.String); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("dataset file remove failed", "file", "parquet")
		}
	}
	if v.SourcePath != "" {
		if err := os.Remove(v.SourcePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("dataset file remove failed", "file", "source")
		}
	}
}
