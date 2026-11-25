-- name: CreateWorkspace :one
INSERT INTO workspaces (name, slug, created_by)
VALUES ($1, $2, $3)
RETURNING *;

-- name: AddWorkspaceMember :one
INSERT INTO workspace_members (workspace_id, user_id, role)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetWorkspaceWithMember :one
SELECT w.*, m.role AS member_role
FROM workspaces w
JOIN workspace_members m ON m.workspace_id = w.id
WHERE w.id = $1 AND m.user_id = $2;

-- name: ListUserWorkspaces :many
SELECT w.*, m.role AS member_role
FROM workspaces w
JOIN workspace_members m ON m.workspace_id = w.id
WHERE m.user_id = $1
ORDER BY w.created_at DESC;
