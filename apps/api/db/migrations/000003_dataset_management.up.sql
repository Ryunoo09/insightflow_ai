-- Plan 11: dataset lifecycle (soft delete + purge), unique names per workspace,
-- and an index that lets the reaper find jobs stranded in 'processing'.

ALTER TABLE datasets
    ADD COLUMN deleted_at timestamptz,
    ADD COLUMN purge_after timestamptz;

-- Live dataset names are unique per workspace, case-insensitively. Deleted rows
-- are excluded so a name can be reused once the dataset is purged.
CREATE UNIQUE INDEX datasets_workspace_name_key
    ON datasets (workspace_id, lower(name))
    WHERE deleted_at IS NULL;

CREATE INDEX datasets_purge_idx
    ON datasets (purge_after)
    WHERE deleted_at IS NOT NULL;

-- Supports the stale-job reaper: it scans only jobs still in 'processing'.
CREATE INDEX processing_jobs_stale_idx
    ON processing_jobs (locked_at)
    WHERE status = 'processing';
