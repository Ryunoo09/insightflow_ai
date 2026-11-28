// Package jobs owns the PostgreSQL-backed processing job queue and worker pool.
package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"insightflow/api/internal/platform/dbgen"
)

// ErrNoJob means the queue is empty for now (not a failure).
var ErrNoJob = errors.New("jobs: no queued job")

// Repository is the job data-access layer.
type Repository struct {
	q *dbgen.Queries
}

// NewRepository wraps sqlc queries.
func NewRepository(q *dbgen.Queries) *Repository { return &Repository{q: q} }

// Claim atomically takes the oldest due job. FOR UPDATE SKIP LOCKED lets two
// workers poll without double-claiming; attempts is incremented on claim.
func (r *Repository) Claim(ctx context.Context) (dbgen.ProcessingJob, error) {
	job, err := r.q.ClaimNextJob(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dbgen.ProcessingJob{}, ErrNoJob
		}
		return dbgen.ProcessingJob{}, err
	}
	return job, nil
}

// Requeue releases a transiently failed job for a later attempt.
func (r *Repository) Requeue(ctx context.Context, id pgtype.UUID, message string, delaySeconds int32) error {
	_, err := r.q.RequeueJob(ctx, dbgen.RequeueJobParams{
		ID:           id,
		ErrorMessage: pgtype.Text{String: message, Valid: message != ""},
		Column3:      delaySeconds,
	})
	return err
}

// Finish marks a job terminal (completed or failed).
func (r *Repository) Finish(ctx context.Context, id pgtype.UUID, status, message string) error {
	_, err := r.q.UpdateJobStatus(ctx, dbgen.UpdateJobStatusParams{
		ID:           id,
		Status:       status,
		ErrorMessage: pgtype.Text{String: message, Valid: message != ""},
	})
	return err
}

// RequeueStale returns stale processing jobs that still have attempts left and
// resets them to queued.
func (r *Repository) RequeueStale(ctx context.Context, staleAfter time.Duration) ([]dbgen.ProcessingJob, error) {
	return r.q.RequeueStaleJobs(ctx, staleAfter.Seconds())
}

// FailStale marks stale processing jobs whose attempts are exhausted as failed.
func (r *Repository) FailStale(ctx context.Context, staleAfter time.Duration) ([]dbgen.ProcessingJob, error) {
	return r.q.FailStaleJobs(ctx, staleAfter.Seconds())
}
