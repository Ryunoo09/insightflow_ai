-- Refresh token lifecycle (rotating, family-based reuse detection).

-- name: InsertRefreshToken :one
INSERT INTO refresh_tokens (user_id, token_hash, family_id, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetRefreshTokenByHash :one
SELECT * FROM refresh_tokens
WHERE token_hash = $1;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens
SET revoked_at = now()
WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeRefreshTokenFamily :exec
UPDATE refresh_tokens
SET revoked_at = now()
WHERE family_id = $1 AND revoked_at IS NULL;

-- name: RevokeUserRefreshTokens :exec
UPDATE refresh_tokens
SET revoked_at = now()
WHERE user_id = $1 AND revoked_at IS NULL;

-- Google OIDC identities.

-- name: GetUserIdentity :one
SELECT * FROM user_identities
WHERE provider = $1 AND provider_subject = $2;

-- name: CreateUserIdentity :one
INSERT INTO user_identities (user_id, provider, provider_subject)
VALUES ($1, $2, $3)
RETURNING *;

-- name: DeleteUser :exec
DELETE FROM users
WHERE id = $1;

-- name: InsertUserAuditLog :exec
INSERT INTO audit_logs (user_id, action, resource_type, details)
VALUES ($1, $2, $3, $4);

-- name: ListUserIdentityProviders :many
SELECT provider FROM user_identities
WHERE user_id = $1;
