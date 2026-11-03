-- name: ListScopedTemplates :many
SELECT id, name, title_template, body_template, is_active
FROM templates
WHERE application_id = sqlc.arg(application_id)::bigint AND id > sqlc.arg(after_id)::bigint
ORDER BY id
LIMIT greatest(0, least(sqlc.arg(page_size)::integer, 101));

-- name: GetScopedTemplate :one
SELECT id, name, title_template, body_template, is_active
FROM templates
WHERE application_id = sqlc.arg(application_id)::bigint AND id = sqlc.arg(template_id)::bigint;

-- name: CreateScopedTemplate :one
INSERT INTO templates(application_id, name, title_template, body_template, is_active, created_by)
VALUES(sqlc.arg(application_id)::bigint, sqlc.arg(name), sqlc.arg(title_template),
       sqlc.arg(body_template), sqlc.arg(is_active), sqlc.arg(created_by))
RETURNING id, name, title_template, body_template, is_active;

-- name: ReplaceScopedTemplate :one
UPDATE templates
SET name = sqlc.arg(name), title_template = sqlc.arg(title_template),
    body_template = sqlc.arg(body_template), is_active = sqlc.arg(is_active), updated_at = now()
WHERE application_id = sqlc.arg(application_id)::bigint AND id = sqlc.arg(template_id)::bigint
RETURNING id, name, title_template, body_template, is_active;
