-- name: GetUserByID :one
SELECT
    id,
    external_id,
    name,
    email,
    status,
    created_at,
    updated_at
FROM users
WHERE id = $1
LIMIT 1;

-- name: GetUserByExternalID :one
SELECT
    id,
    external_id,
    name,
    email,
    status,
    created_at,
    updated_at
FROM users
WHERE external_id = $1
LIMIT 1;

-- name: GetUserByEmail :one
SELECT
    id,
    external_id,
    name,
    email,
    status,
    created_at,
    updated_at
FROM users
WHERE email = $1
LIMIT 1;

-- name: CreateUser :one
INSERT INTO users (
    external_id,
    name,
    email,
    status
)
VALUES (
    $1,
    $2,
    $3,
    $4
)
RETURNING *;

-- name: UpdateUser :exec
UPDATE users
SET
    name = $2,
    email = $3,
    status = $4,
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateUserStatus :exec
UPDATE users
SET
    status = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: DeleteUser :exec
DELETE FROM users
WHERE id = $1;

-- name: ListUsers :many
SELECT
    id,
    external_id,
    name,
    email,
    status,
    created_at,
    updated_at
FROM users
WHERE (CARDINALITY($1::text[]) = 0 OR status = ANY($1))
ORDER BY id;
