-- name: GetNotificationByID :one
SELECT
    id,
    template_id,
    template_name,
    title,
    body,
    payload,
    priority,
    status,
    scheduled_at,
    published_at,
    completed_at,
    created_by,
    created_at,
    updated_at
FROM notifications
WHERE id = $1
LIMIT 1;

-- name: GetNotificationsByStatus :many
SELECT
    id,
    template_id,
    template_name,
    title,
    body,
    payload,
    priority,
    status,
    scheduled_at,
    published_at,
    completed_at,
    created_by,
    created_at,
    updated_at
FROM notifications
WHERE status = $1
ORDER BY created_at DESC;

-- name: GetNotificationsByCreatedBy :many
SELECT
    id,
    template_id,
    template_name,
    title,
    body,
    payload,
    priority,
    status,
    scheduled_at,
    published_at,
    completed_at,
    created_by,
    created_at,
    updated_at
FROM notifications
WHERE created_by = $1
ORDER BY created_at DESC;

-- name: CreateNotification :one
INSERT INTO notifications (
    template_id,
    template_name,
    title,
    body,
    payload,
    priority,
    status,
    scheduled_at,
    published_at,
    completed_at,
    created_by
)
VALUES (
    $1,
    $2,
    $3,
    $4,
    $5,
    $6,
    $7,
    $8,
    $9,
    $10,
    $11
)
RETURNING *;

-- name: UpdateNotification :exec
UPDATE notifications
SET
    template_id = $2,
    template_name = $3,
    title = $4,
    body = $5,
    payload = $6,
    priority = $7,
    status = $8,
    scheduled_at = $9,
    published_at = $10,
    completed_at = $11,
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateNotificationStatus :exec
UPDATE notifications
SET
    status = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateNotificationSchedule :exec
UPDATE notifications
SET
    scheduled_at = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: DeleteNotification :exec
DELETE FROM notifications
WHERE id = $1;

-- name: ListNotifications :many
SELECT
    id,
    template_id,
    template_name,
    title,
    body,
    payload,
    priority,
    status,
    scheduled_at,
    published_at,
    completed_at,
    created_by,
    created_at,
    updated_at
FROM notifications
WHERE
    ($1::text[] IS NULL OR status = ANY($1)) AND
    ($2::bigint IS NULL OR created_by = $2) AND
    ($3::timestamptz IS NULL OR created_at >= $3) AND
    ($4::timestamptz IS NULL OR created_at <= $4)
ORDER BY created_at DESC;
