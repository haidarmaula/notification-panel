-- ==========================================
-- GET
-- ==========================================

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
WHERE id = sqlc.arg('id')
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
WHERE external_id = sqlc.arg('external_id')
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
WHERE email = sqlc.arg('email')
LIMIT 1;

-- ==========================================
-- EXISTS
-- ==========================================

-- name: ExistsUserByExternalID :one
SELECT EXISTS (
    SELECT 1
    FROM users
    WHERE external_id = sqlc.arg('external_id')
);

-- name: ExistsUserByEmail :one
SELECT EXISTS (
    SELECT 1
    FROM users
    WHERE email = sqlc.arg('email')
);

-- ==========================================
-- CREATE
-- ==========================================

-- name: CreateUser :one
INSERT INTO users (
    external_id,
    name,
    email,
    status
)
VALUES (
    sqlc.arg('external_id'),
    sqlc.arg('name'),
    sqlc.arg('email'),
    sqlc.arg('status')
)
RETURNING
    id,
    external_id,
    name,
    email,
    status,
    created_at,
    updated_at;

-- ==========================================
-- UPDATE
-- ==========================================

-- name: UpdateUser :exec
UPDATE users
SET
    name = sqlc.arg('name'),
    email = sqlc.arg('email'),
    status = sqlc.arg('status'),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- name: UpdateUserStatus :exec
UPDATE users
SET
    status = sqlc.arg('status'),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- ==========================================
-- DELETE
-- ==========================================

-- name: DeleteUser :exec
DELETE
FROM users
WHERE id = sqlc.arg('id');

-- ==========================================
-- LIST
-- ==========================================

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
ORDER BY name
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- SEARCH
-- ==========================================

-- name: SearchUsers :many
SELECT
    id,
    external_id,
    name,
    email,
    status,
    created_at,
    updated_at
FROM users
WHERE
    name ILIKE '%' || sqlc.arg('keyword') || '%'
    OR email ILIKE '%' || sqlc.arg('keyword') || '%'
    OR external_id ILIKE '%' || sqlc.arg('keyword') || '%'
ORDER BY name
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- FILTER
-- ==========================================

-- name: ListUsersByStatus :many
SELECT
    id,
    external_id,
    name,
    email,
    status,
    created_at,
    updated_at
FROM users
WHERE status = sqlc.arg('status')
ORDER BY name
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- COUNT
-- ==========================================

-- name: CountUsers :one
SELECT COUNT(*)
FROM users;

-- name: CountUsersByStatus :one
SELECT COUNT(*)
FROM users
WHERE status = sqlc.arg('status');
