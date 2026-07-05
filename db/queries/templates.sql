-- name: GetTemplateByID :one
SELECT
    id,
    name,
    title_template,
    body_template,
    variables,
    created_by,
    is_active,
    created_at,
    updated_at
FROM templates
WHERE id = $1
LIMIT 1;

-- name: GetTemplateByName :one
SELECT
    id,
    name,
    title_template,
    body_template,
    variables,
    created_by,
    is_active,
    created_at,
    updated_at
FROM templates
WHERE name = $1
LIMIT 1;

-- name: CreateTemplate :one
INSERT INTO templates (
    name,
    title_template,
    body_template,
    variables,
    created_by,
    is_active
)
VALUES (
    $1,
    $2,
    $3,
    $4,
    $5,
    $6
)
RETURNING *;

-- name: UpdateTemplate :exec
UPDATE templates
SET
    name = $2,
    title_template = $3,
    body_template = $4,
    variables = $5,
    is_active = $6,
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateTemplateActive :exec
UPDATE templates
SET
    is_active = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: DeleteTemplate :exec
DELETE FROM templates
WHERE id = $1;

-- name: ListTemplates :many
SELECT
    id,
    name,
    title_template,
    body_template,
    variables,
    created_by,
    is_active,
    created_at,
    updated_at
FROM templates
WHERE
    ($1::boolean IS NULL OR is_active = $1) AND
    ($2::bigint IS NULL OR created_by = $2)
ORDER BY id;
