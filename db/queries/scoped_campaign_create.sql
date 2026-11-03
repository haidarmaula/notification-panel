-- name: LockScopedCampaignTemplate :one
SELECT name FROM templates
WHERE application_id = sqlc.arg(application_id)::bigint
  AND id = sqlc.arg(template_id)::bigint AND is_active
FOR SHARE;

-- name: CreateScopedCampaignDraft :one
INSERT INTO notifications (application_id, template_id, template_name, title, body, status, created_by)
VALUES (sqlc.arg(application_id)::bigint, sqlc.narg(template_id), sqlc.narg(template_name),
        sqlc.arg(title), sqlc.arg(body), 'DRAFT', sqlc.arg(created_by))
RETURNING id;

-- name: CreateScopedCampaignTarget :one
INSERT INTO notification_targets (application_id, notification_id, target_type, segment_id, user_id)
SELECT sqlc.arg(application_id)::bigint, sqlc.arg(notification_id)::bigint,
       sqlc.arg(target_type)::varchar, sqlc.narg(segment_id)::bigint, sqlc.narg(user_id)::bigint
WHERE (sqlc.narg(segment_id)::bigint IS NULL OR EXISTS (
    SELECT 1 FROM segments WHERE application_id = sqlc.arg(application_id)::bigint
      AND id = sqlc.narg(segment_id)::bigint FOR KEY SHARE
))
AND (sqlc.narg(user_id)::bigint IS NULL OR EXISTS (
    SELECT 1 FROM users WHERE application_id = sqlc.arg(application_id)::bigint
      AND id = sqlc.narg(user_id)::bigint FOR KEY SHARE
))
RETURNING id;
