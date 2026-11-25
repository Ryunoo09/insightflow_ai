-- name: EnqueueJob :one
INSERT INTO processing_jobs (workspace_id, version_id, job_type, status)
VALUES ($1, $2, $3, 'queued')
RETURNING *;

-- name: ClaimNextJob :one
-- Atomically claim the oldest queued job. FOR UPDATE SKIP LOCKED lets multiple
-- workers poll concurrently without double-claiming.
UPDATE processing_jobs
SET status = 'processing',
    attempts = attempts + 1,
    locked_at = now(),
    updated_at = now()
WHERE id = (
    SELECT id FROM processing_jobs
    WHERE status = 'queued' AND attempts < max_attempts
    ORDER BY created_at
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: UpdateJobStatus :one
UPDATE processing_jobs
SET status = $2,
    error_message = $3,
    finished_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING *;
