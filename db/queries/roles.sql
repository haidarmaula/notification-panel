-- name: GetRoleByID :one
SELECT
    id,
    name,
    description,
    created_at,
    updated_at
FROM roles
WHERE id = $1
LIMIT 1;

-- name: GetRoleByName :one
SELECT
    id,
    name,
    description,
    created_at,
    updated_at
FROM roles
WHERE name = $1
LIMIT 1;

-- name: CreateRole :one
INSERT INTO roles (
    name,
    description
)
VALUES (
    $1,
    $2
)
RETURNING *;

-- name: UpdateRole :exec
UPDATE roles
SET
    name = $2,
    description = $3,
    updated_at = NOW()
WHERE id = $1;

-- name: DeleteRole :exec
DELETE FROM roles
WHERE id = $1;

-- name: ListRoles :many
SELECT
    id,
    name,
    description,
    created_at,
    updated_at
FROM roles
ORDER BY id;
