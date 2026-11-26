-- name: EnqueueJob :one
INSERT INTO processing_jobs (workspace_id, version_id, job_type, status)
VALUES ($1, $2, $3, 'queued')
RETURNING *;

-- name: ClaimNextJob :one
-- Atomically claim the oldest due job. FOR UPDATE SKIP LOCKED lets multiple
-- workers poll concurrently without double-claiming; run_after defers retries.
-- Jobs for soft-deleted datasets are never claimed.
UPDATE processing_jobs
SET status = 'processing',
    attempts = attempts + 1,
    locked_at = now(),
    updated_at = now()
WHERE id = (
    SELECT j.id FROM processing_jobs j
    JOIN dataset_versions v ON v.id = j.version_id
    JOIN datasets d ON d.id = v.dataset_id
    WHERE j.status = 'queued' AND j.attempts < j.max_attempts AND j.run_after <= now()
      AND d.deleted_at IS NULL
    ORDER BY j.run_after, j.created_at
    LIMIT 1
    FOR UPDATE OF j SKIP LOCKED
)
RETURNING *;

-- name: RequeueStaleJobs :many
-- Recover jobs stranded in 'processing' after a crash: rows still under their
-- attempt budget go back to the queue. FailStaleJobs handles the exhausted ones.
UPDATE processing_jobs
SET status = 'queued',
    locked_at = NULL,
    run_after = now(),
    error_message = 'stale job requeued',
    updated_at = now()
WHERE status = 'processing'
  AND locked_at IS NOT NULL
  AND locked_at < now() - make_interval(secs => $1)
  AND attempts < max_attempts
RETURNING *;

-- name: FailStaleJobs :many
UPDATE processing_jobs
SET status = 'failed',
    finished_at = now(),
    error_message = 'stale job exceeded max attempts',
    updated_at = now()
WHERE status = 'processing'
  AND locked_at IS NOT NULL
  AND locked_at < now() - make_interval(secs => $1)
  AND attempts >= max_attempts
RETURNING *;

-- name: FailJobsForDataset :exec
-- A soft-deleted dataset must not keep work queued; fail every open job.
UPDATE processing_jobs j
SET status = 'failed',
    finished_at = now(),
    error_message = 'dataset dihapus',
    updated_at = now()
FROM dataset_versions v
WHERE j.version_id = v.id
  AND v.dataset_id = $1
  AND j.status IN ('queued', 'processing');

-- name: RequeueJob :one
-- Release a claimed job back to the queue for a later attempt. run_after is
-- pushed out so retries back off instead of spinning.
UPDATE processing_jobs
SET status = 'queued',
    error_message = $2,
    run_after = now() + ($3::int * interval '1 second'),
    locked_at = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateJobStatus :one
UPDATE processing_jobs
SET status = $2,
    error_message = $3,
    finished_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetLatestJobForVersion :one
SELECT * FROM processing_jobs
WHERE version_id = $1
ORDER BY created_at DESC
LIMIT 1;
