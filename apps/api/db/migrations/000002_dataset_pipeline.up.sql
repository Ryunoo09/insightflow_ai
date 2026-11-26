-- Add job scheduling and dataset processing metadata needed by the ingestion
-- pipeline (Plan 04).

-- Transient engine failures are retried with exponential backoff: a requeued
-- job is hidden from the claim query until run_after.
ALTER TABLE processing_jobs
    ADD COLUMN run_after timestamptz NOT NULL DEFAULT now();

DROP INDEX processing_jobs_claim_idx;
CREATE INDEX processing_jobs_claim_idx
    ON processing_jobs (run_after, created_at)
    WHERE status = 'queued';

-- Surface a stable error code and the engine's processing report next to the
-- human message; parquet_sha256 lets the parquet artifact be verified.
ALTER TABLE dataset_versions
    ADD COLUMN error_code text,
    ADD COLUMN parquet_sha256 text,
    ADD COLUMN processing_report jsonb;
