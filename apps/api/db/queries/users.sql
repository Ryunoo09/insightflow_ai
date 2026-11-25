-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users
WHERE lower(email) = lower($1);

-- name: CreateUser :one
INSERT INTO users (email, password_hash, full_name, status)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ActivateUser :one
UPDATE users
SET status = 'active', updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateUserProfile :one
UPDATE users
SET full_name = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RevokeUserTokens :exec
UPDATE users
SET tokens_valid_after = now(), updated_at = now()
WHERE id = $1;
