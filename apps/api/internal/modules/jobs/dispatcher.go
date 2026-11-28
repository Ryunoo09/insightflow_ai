package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"insightflow/api/internal/modules/datasets"
	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/engineclient"
)

// Dispatcher claims jobs and runs the Go -> Engine ingestion call.
type Dispatcher struct {
	repo     *Repository
	datasets *datasets.Service
	engine   *engineclient.Client
}

// NewDispatcher wires the job dispatcher.
func NewDispatcher(repo *Repository, ds *datasets.Service, engine *engineclient.Client) *Dispatcher {
	return &Dispatcher{repo: repo, datasets: ds, engine: engine}
}

// ProcessOne claims and handles one job. It returns false when the queue is
// currently empty so the worker can idle instead of spinning.
func (d *Dispatcher) ProcessOne(ctx context.Context) (bool, error) {
	job, err := d.repo.Claim(ctx)
	if errors.Is(err, ErrNoJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	d.handle(ctx, job)
	return true, nil
}

func (d *Dispatcher) handle(ctx context.Context, job dbgen.ProcessingJob) {
	versionID := uuid.UUID(job.VersionID.Bytes)

	meta, err := d.datasets.VersionMeta(ctx, versionID)
	if err != nil {
		d.failPermanently(ctx, job, versionID, "DATASET_PROCESS_FAILED", "metadata dataset tidak ditemukan")
		return
	}

	if err := d.datasets.MarkProcessing(ctx, versionID); err != nil {
		slog.Error("mark version processing failed", "version_id", versionID, "error", err)
	}

	req := engineclient.ProcessRequest{
		DatasetRef: engineclient.DatasetRef{
			WorkspaceID: meta.WorkspaceID.String(),
			DatasetID:   meta.DatasetID.String(),
			VersionNo:   meta.VersionNo,
		},
		SourceFormat: meta.FileFormat,
		SourceSHA256: meta.ChecksumSHA256,
		Limits: engineclient.Limits{
			MaxRows:    d.datasets.MaxRowsForFormat(meta.FileFormat),
			MaxColumns: d.datasets.MaxColumns(),
		},
	}

	res, err := d.engine.Process(ctx, uuid.UUID(job.ID.Bytes).String(), req)
	if err != nil {
		d.handleFailure(ctx, job, versionID, err)
		return
	}

	parquetPath, err := d.datasets.Resolver().ParquetPath(meta.WorkspaceID.String(), meta.DatasetID.String(), int(meta.VersionNo))
	if err != nil {
		d.failPermanently(ctx, job, versionID, "DATASET_PROCESS_FAILED", "path penyimpanan tidak valid")
		return
	}

	result := datasets.ProcessResult{
		RowCount:         res.RowCount,
		ColumnCount:      res.ColumnCount,
		Columns:          toColumnResults(res.Columns),
		ParquetSHA256:    res.Parquet.SHA256,
		ParquetSizeBytes: res.Parquet.SizeBytes,
		Profile:          res.Profile,
		ProcessingReport: res.ProcessingReport,
	}

	// Bookkeeping must survive a shutdown that cancelled the polling context.
	txctx, cancel := bookkeepingContext(ctx)
	defer cancel()
	if err := d.datasets.Complete(txctx, versionID, parquetPath, result); err != nil {
		slog.Error("persist processing result failed", "version_id", versionID, "error", err)
		d.failPermanently(txctx, job, versionID, "DATASET_PERSIST_FAILED", "gagal menyimpan hasil pemrosesan")
		return
	}
	if err := d.repo.Finish(txctx, job.ID, "completed", ""); err != nil {
		slog.Error("finish job failed", "job_id", uuid.UUID(job.ID.Bytes), "error", err)
	}
}

func (d *Dispatcher) handleFailure(ctx context.Context, job dbgen.ProcessingJob, versionID uuid.UUID, err error) {
	bctx, cancel := bookkeepingContext(ctx)
	defer cancel()

	if engineclient.Retryable(err) && job.Attempts < job.MaxAttempts {
		// Exponential backoff: 2s, 4s, 8s for attempts 1, 2, 3.
		delay := int32(1) << uint(job.Attempts)
		if rerr := d.repo.Requeue(bctx, job.ID, truncate(err.Error(), 300), delay); rerr != nil {
			slog.Error("requeue job failed", "job_id", uuid.UUID(job.ID.Bytes), "error", rerr)
		}
		return
	}

	code := "DATASET_PROCESS_FAILED"
	var engineErr *engineclient.Error
	if errors.As(err, &engineErr) && engineErr.Code != "" {
		code = engineErr.Code
	}
	d.failPermanently(bctx, job, versionID, code, err.Error())
}

func (d *Dispatcher) failPermanently(ctx context.Context, job dbgen.ProcessingJob, versionID uuid.UUID, code, message string) {
	if err := d.datasets.Fail(ctx, versionID, code, message); err != nil {
		slog.Error("mark version failed", "version_id", versionID, "error", err)
	}
	if err := d.repo.Finish(ctx, job.ID, "failed", truncate(message, 300)); err != nil {
		slog.Error("finish job failed", "job_id", uuid.UUID(job.ID.Bytes), "error", err)
	}
}

func toColumnResults(cols []engineclient.ProcessColumn) []datasets.ColumnResult {
	out := make([]datasets.ColumnResult, 0, len(cols))
	for _, c := range cols {
		out = append(out, datasets.ColumnResult{
			Ordinal:       c.Ordinal,
			Name:          c.Name,
			OriginalName:  c.OriginalName,
			LogicalType:   c.LogicalType,
			Role:          c.Role,
			NullCount:     c.NullCount,
			DistinctCount: c.DistinctCount,
		})
	}
	return out
}

// bookkeepingContext detaches DB writes from a cancellation caused by shutdown
// so a claimed job is never stranded, and bounds them with a short timeout.
func bookkeepingContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
