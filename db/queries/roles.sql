-- ==========================================
-- GET
-- ==========================================

-- name: GetRoleByID :one
SELECT
    id,
    name,
    description,
    created_at,
    updated_at
FROM roles
WHERE id = sqlc.arg('id')
LIMIT 1;

-- name: GetRoleByName :one
SELECT
    id,
    name,
    description,
    created_at,
    updated_at
FROM roles
WHERE name = sqlc.arg('name')
LIMIT 1;

-- ==========================================
-- EXISTS
-- ==========================================

-- name: ExistsRoleByName :one
SELECT EXISTS (
    SELECT 1
    FROM roles
    WHERE name = sqlc.arg('name')
);

-- ==========================================
-- CREATE
-- ==========================================

-- name: CreateRole :one
INSERT INTO roles (
    name,
    description
)
VALUES (
    sqlc.arg('name'),
    sqlc.arg('description')
)
RETURNING
    id,
    name,
    description,
    created_at,
    updated_at;

-- ==========================================
-- UPDATE
-- ==========================================

-- name: UpdateRole :exec
UPDATE roles
SET
    name = sqlc.arg('name'),
    description = sqlc.arg('description'),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- ==========================================
-- DELETE
-- ==========================================

-- name: DeleteRole :exec
DELETE
FROM roles
WHERE id = sqlc.arg('id');

-- ==========================================
-- LIST
-- ==========================================

-- name: ListRoles :many
SELECT
    id,
    name,
    description,
    created_at,
    updated_at
FROM roles
ORDER BY name
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- COUNT
-- ==========================================

-- name: CountRoles :one
SELECT COUNT(*)
FROM roles;
