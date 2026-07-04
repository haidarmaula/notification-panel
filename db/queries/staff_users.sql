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
WHERE id = $1
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
WHERE email = $1
LIMIT 1;

-- name: CreateStaffUser :one
INSERT INTO staff_users (
    role_id,
    name,
    email,
    password_hash
)
VALUES (
    $1,
    $2,
    $3,
    $4
)
RETURNING *;

-- name: UpdateStaffPassword :exec
UPDATE staff_users
SET
    password_hash = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateStaffStatus :exec
UPDATE staff_users
SET
    is_active = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: ListStaffUsers :many
SELECT
    id,
    role_id,
    name,
    email,
    is_active,
    created_at,
    updated_at
FROM staff_users
ORDER BY id;
