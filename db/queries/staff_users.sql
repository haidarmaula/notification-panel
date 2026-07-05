-- ==========================================
-- GET
-- ==========================================

-- name: GetStaffUserByID :one
SELECT
    id,
    role_id,
    name,
    email,
    password_hash,
    is_active,
    created_at,
    updated_at
FROM staff_users
WHERE id = sqlc.arg('id')
LIMIT 1;

-- name: GetStaffUserByEmail :one
SELECT
    id,
    role_id,
    name,
    email,
    password_hash,
    is_active,
    created_at,
    updated_at
FROM staff_users
WHERE email = sqlc.arg('email')
LIMIT 1;

-- ==========================================
-- EXISTS
-- ==========================================

-- name: ExistsStaffUserByEmail :one
SELECT EXISTS (
    SELECT 1
    FROM staff_users
    WHERE email = sqlc.arg('email')
);

-- ==========================================
-- CREATE
-- ==========================================

-- name: CreateStaffUser :one
INSERT INTO staff_users (
    role_id,
    name,
    email,
    password_hash
)
VALUES (
    sqlc.arg('role_id'),
    sqlc.arg('name'),
    sqlc.arg('email'),
    sqlc.arg('password_hash')
)
RETURNING
    id,
    role_id,
    name,
    email,
    password_hash,
    is_active,
    created_at,
    updated_at;

-- ==========================================
-- UPDATE
-- ==========================================

-- name: UpdateStaffUser :exec
UPDATE staff_users
SET
    role_id = sqlc.arg('role_id'),
    name = sqlc.arg('name'),
    email = sqlc.arg('email'),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- name: UpdateStaffPassword :exec
UPDATE staff_users
SET
    password_hash = sqlc.arg('password_hash'),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- name: UpdateStaffStatus :exec
UPDATE staff_users
SET
    is_active = sqlc.arg('is_active'),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- ==========================================
-- DELETE
-- ==========================================

-- name: DeleteStaffUser :exec
DELETE
FROM staff_users
WHERE id = sqlc.arg('id');

-- ==========================================
-- LIST
-- ==========================================

-- name: ListStaffUsers :many
SELECT
    su.id,
    su.role_id,
    r.name AS role_name,
    su.name,
    su.email,
    su.is_active,
    su.created_at,
    su.updated_at
FROM staff_users su
INNER JOIN roles r
    ON r.id = su.role_id
ORDER BY su.name
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- SEARCH
-- ==========================================

-- name: SearchStaffUsers :many
SELECT
    su.id,
    su.role_id,
    r.name AS role_name,
    su.name,
    su.email,
    su.is_active,
    su.created_at,
    su.updated_at
FROM staff_users su
INNER JOIN roles r
    ON r.id = su.role_id
WHERE
    su.name ILIKE '%' || sqlc.arg('keyword') || '%'
    OR su.email ILIKE '%' || sqlc.arg('keyword') || '%'
ORDER BY su.name
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- COUNT
-- ==========================================

-- name: CountStaffUsers :one
SELECT COUNT(*)
FROM staff_users;
