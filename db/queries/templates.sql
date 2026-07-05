-- ==========================================
-- GET
-- ==========================================

-- name: GetTemplateByID :one
SELECT
    id,
    name,
    title_template,
    body_template,
    created_by,
    is_active,
    created_at,
    updated_at
FROM templates
WHERE id = sqlc.arg('id')
LIMIT 1;

-- name: GetTemplateByName :one
SELECT
    id,
    name,
    title_template,
    body_template,
    created_by,
    is_active,
    created_at,
    updated_at
FROM templates
WHERE name = sqlc.arg('name')
LIMIT 1;

-- ==========================================
-- EXISTS
-- ==========================================

-- name: ExistsTemplateByName :one
SELECT EXISTS (
    SELECT 1
    FROM templates
    WHERE name = sqlc.arg('name')
);

-- ==========================================
-- CREATE
-- ==========================================

-- name: CreateTemplate :one
INSERT INTO templates (
    name,
    title_template,
    body_template,
    created_by
)
VALUES (
    sqlc.arg('name'),
    sqlc.arg('title_template'),
    sqlc.arg('body_template'),
    sqlc.arg('created_by')
)
RETURNING
    id,
    name,
    title_template,
    body_template,
    created_by,
    is_active,
    created_at,
    updated_at;

-- ==========================================
-- UPDATE
-- ==========================================

-- name: UpdateTemplate :exec
UPDATE templates
SET
    name = sqlc.arg('name'),
    title_template = sqlc.arg('title_template'),
    body_template = sqlc.arg('body_template'),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- name: UpdateTemplateStatus :exec
UPDATE templates
SET
    is_active = sqlc.arg('is_active'),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- ==========================================
-- DELETE
-- ==========================================

-- name: DeleteTemplate :exec
DELETE
FROM templates
WHERE id = sqlc.arg('id');

-- ==========================================
-- LIST
-- ==========================================

-- name: ListTemplates :many
SELECT
    t.id,
    t.name,
    t.title_template,
    t.body_template,
    t.is_active,
    su.name AS created_by_name,
    t.created_at,
    t.updated_at
FROM templates t
JOIN staff_users su
    ON su.id = t.created_by
ORDER BY t.name
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- SEARCH
-- ==========================================

-- name: SearchTemplates :many
SELECT
    t.id,
    t.name,
    t.title_template,
    t.body_template,
    t.is_active,
    su.name AS created_by_name,
    t.created_at,
    t.updated_at
FROM templates t
JOIN staff_users su
    ON su.id = t.created_by
WHERE
    t.name ILIKE '%' || sqlc.arg('keyword') || '%'
ORDER BY t.name
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- COUNT
-- ==========================================

-- name: CountTemplates :one
SELECT COUNT(*)
FROM templates;
