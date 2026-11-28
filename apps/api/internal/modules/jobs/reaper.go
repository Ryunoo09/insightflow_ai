package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// VersionFailer marks a dataset version permanently failed. datasets.Service
// satisfies it; the interface keeps the reaper testable.
type VersionFailer interface {
	Fail(ctx context.Context, versionID uuid.UUID, code, message string) error
}

// Reaper recovers jobs stranded in 'processing' after a crash or a hung engine
// call: it requeues them while attempts remain, otherwise fails them and the
// version. A healthy job is never touched because staleAfter is far longer than
// the engine's own timeout.
type Reaper struct {
	repo       *Repository
	failer     VersionFailer
	staleAfter time.Duration
	interval   time.Duration
}

// NewReaper builds the stale-job reaper.
func NewReaper(repo *Repository, failer VersionFailer, staleAfter, interval time.Duration) *Reaper {
	if interval <= 0 {
		interval = time.Minute
	}
	return &Reaper{repo: repo, failer: failer, staleAfter: staleAfter, interval: interval}
}

// Run blocks, reaping on each tick, until ctx is cancelled.
func (r *Reaper) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	r.ReapOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.ReapOnce(ctx)
		}
	}
}

// ReapOnce runs one pass: exhausted jobs are failed, the rest requeued.
func (r *Reaper) ReapOnce(ctx context.Context) {
	failed, err := r.repo.FailStale(ctx, r.staleAfter)
	if err != nil {
		slog.Error("reaper: fail stale jobs", "error", err)
	}
	for _, job := range failed {
		versionID := uuid.UUID(job.VersionID.Bytes)
		if err := r.failer.Fail(ctx, versionID, "DATASET_RESOURCE_EXHAUSTED", "pemrosesan melewati batas waktu"); err != nil {
			slog.Error("reaper: mark version failed", "version_id", versionID, "error", err)
		}
	}

	requeued, err := r.repo.RequeueStale(ctx, r.staleAfter)
	if err != nil {
		slog.Error("reaper: requeue stale jobs", "error", err)
	}

	if len(failed) > 0 || len(requeued) > 0 {
		slog.Info("reaper: recovered stale jobs", "failed", len(failed), "requeued", len(requeued))
	}
}