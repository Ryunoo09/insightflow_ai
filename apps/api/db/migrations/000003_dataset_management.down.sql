DROP INDEX IF EXISTS processing_jobs_stale_idx;
DROP INDEX IF EXISTS datasets_purge_idx;
DROP INDEX IF EXISTS datasets_workspace_name_key;

ALTER TABLE datasets
    DROP COLUMN IF EXISTS purge_after,
    DROP COLUMN IF EXISTS deleted_at;
