-- ==========================================
-- GET
-- ==========================================

-- name: GetNotificationByID :one
SELECT
    id,
    title,
    body,
    template_id,
    status,
    created_by,
    scheduled_at,
    created_at,
    updated_at
FROM notifications
WHERE id = sqlc.arg('id')
LIMIT 1;

-- ==========================================
-- CREATE
-- ==========================================

-- name: CreateNotification :one
INSERT INTO notifications (
    title,
    body,
    template_id,
    status,
    created_by,
    scheduled_at
)
VALUES (
    sqlc.arg('title'),
    sqlc.arg('body'),
    sqlc.arg('template_id'),
    sqlc.arg('status'),
    sqlc.arg('created_by'),
    sqlc.arg('scheduled_at')
)
RETURNING
    id,
    title,
    body,
    template_id,
    status,
    created_by,
    scheduled_at,
    created_at,
    updated_at;

-- ==========================================
-- UPDATE
-- ==========================================

-- name: UpdateNotification :exec
UPDATE notifications
SET
    title = sqlc.arg('title'),
    body = sqlc.arg('body'),
    template_id = sqlc.arg('template_id'),
    scheduled_at = sqlc.arg('scheduled_at'),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- name: UpdateNotificationStatus :exec
UPDATE notifications
SET
    status = sqlc.arg('status'),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- name: MarkNotificationSent :exec
UPDATE notifications
SET
    status = 'SENT',
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- ==========================================
-- DELETE
-- ==========================================

-- name: DeleteNotification :exec
DELETE
FROM notifications
WHERE id = sqlc.arg('id');

-- ==========================================
-- LIST
-- ==========================================

-- name: ListNotifications :many
SELECT
    n.id,
    n.title,
    n.status,
    su.name AS created_by_name,
    n.scheduled_at,
    n.created_at
FROM notifications n
JOIN staff_users su
    ON su.id = n.created_by
ORDER BY n.created_at DESC
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- SEARCH
-- ==========================================

-- name: SearchNotifications :many
SELECT
    n.id,
    n.title,
    n.status,
    su.name AS created_by_name,
    n.scheduled_at,
    n.created_at
FROM notifications n
JOIN staff_users su
    ON su.id = n.created_by
WHERE
    n.title ILIKE '%' || sqlc.arg('keyword') || '%'
ORDER BY n.created_at DESC
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- COUNT
-- ==========================================

-- name: CountNotifications :one
SELECT COUNT(*)
FROM notifications;
