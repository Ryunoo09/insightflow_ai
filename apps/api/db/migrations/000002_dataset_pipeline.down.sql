ALTER TABLE dataset_versions
    DROP COLUMN processing_report,
    DROP COLUMN parquet_sha256,
    DROP COLUMN error_code;

DROP INDEX processing_jobs_claim_idx;
CREATE INDEX processing_jobs_claim_idx
    ON processing_jobs (created_at)
    WHERE status = 'queued';

ALTER TABLE processing_jobs
    DROP COLUMN run_after;
