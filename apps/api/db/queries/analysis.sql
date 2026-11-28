-- Analysis sessions and messages (Plan 06, TASK-601).
-- Every query joins workspace_members so a non-member can never read another
-- tenant's conversation, and a miss is indistinguishable from a missing row.

-- name: CreateAnalysisSession :one
INSERT INTO analysis_sessions (workspace_id, dataset_id, title, created_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetAnalysisSession :one
SELECT s.*
FROM analysis_sessions s
JOIN workspace_members m ON m.workspace_id = s.workspace_id
WHERE s.id = $1 AND m.user_id = $2;

-- name: ListAnalysisSessions :many
SELECT s.*
FROM analysis_sessions s
JOIN workspace_members m ON m.workspace_id = s.workspace_id
WHERE s.workspace_id = $1 AND m.user_id = $2
ORDER BY s.updated_at DESC
LIMIT $3 OFFSET $4;

-- name: CountAnalysisSessions :one
SELECT count(*)
FROM analysis_sessions s
JOIN workspace_members m ON m.workspace_id = s.workspace_id
WHERE s.workspace_id = $1 AND m.user_id = $2;

-- name: GetAnalyzableDatasetVersion :one
-- Resolves the dataset's current version for a workspace member. Only a ready
-- version is analyzable, so the caller gets the status and decides.
SELECT d.id AS dataset_id, d.workspace_id, d.current_version_no,
       v.id AS version_id, v.version_no, v.status, v.row_count, v.column_count
FROM datasets d
JOIN workspace_members m ON m.workspace_id = d.workspace_id AND m.user_id = $2
JOIN dataset_versions v ON v.dataset_id = d.id AND v.version_no = d.current_version_no
WHERE d.id = $1 AND d.workspace_id = $3 AND d.deleted_at IS NULL;

-- name: CreateAnalysisMessage :one
INSERT INTO analysis_messages (session_id, role, content, client_message_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListAnalysisMessages :many
SELECT * FROM analysis_messages WHERE session_id = $1 ORDER BY created_at, id;

-- name: ListRecentAnalysisMessages :many
-- Context window: the newest messages, returned oldest-first for the prompt.
SELECT * FROM (
    SELECT * FROM analysis_messages
    WHERE session_id = $1
    ORDER BY created_at DESC, id DESC
    LIMIT $2
) recent
ORDER BY created_at, id;

-- name: TouchAnalysisSession :exec
UPDATE analysis_sessions SET updated_at = now() WHERE id = $1;
