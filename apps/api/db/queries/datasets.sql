-- name: CreateDataset :one
INSERT INTO datasets (workspace_id, name, description, created_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: CreateDatasetVersion :one
INSERT INTO dataset_versions (
    dataset_id, version_no, file_format, original_name,
    byte_size, checksum_sha256, source_path, status
)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'queued')
RETURNING *;

-- name: GetDatasetWithAccess :one
SELECT d.*
FROM datasets d
JOIN workspace_members m ON m.workspace_id = d.workspace_id
WHERE d.id = $1 AND m.user_id = $2;

-- name: ListWorkspaceDatasets :many
SELECT d.*
FROM datasets d
JOIN workspace_members m ON m.workspace_id = d.workspace_id
WHERE d.workspace_id = $1 AND m.user_id = $2
ORDER BY d.created_at DESC;

-- name: GetDatasetVersion :one
SELECT * FROM dataset_versions
WHERE id = $1;

-- name: MarkDatasetVersionReady :one
UPDATE dataset_versions
SET status = 'ready',
    row_count = $2,
    column_count = $3,
    parquet_path = $4,
    error_message = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkDatasetVersionFailed :one
UPDATE dataset_versions
SET status = 'failed',
    error_message = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;
