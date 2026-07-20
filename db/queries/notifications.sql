-- ==========================================
-- GET
-- ==========================================

-- name: GetNotificationByID :one
SELECT
    id,
    title,
    body,
    template_id,
    template_name,
    payload,
    priority,
    status,
    created_by,
    scheduled_at,
    published_at,
    completed_at,
    created_at,
    updated_at
FROM notifications
WHERE id = sqlc.arg('id')
LIMIT 1;

-- name: GetNotificationStatistics :one
SELECT
    COUNT(DISTINCT nt.user_id) AS targeted,
    COUNT(CASE WHEN nd.status = 'DELIVERED' OR nd.status = 'OPENED' THEN 1 END) AS delivered,
    COUNT(CASE WHEN nd.status = 'OPENED' THEN 1 END) AS opened
FROM notification_targets nt
LEFT JOIN notification_deliveries nd ON nd.notification_id = nt.notification_id AND nd.user_id = nt.user_id
WHERE nt.notification_id = $1;

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

-- name: UpdateNotificationStatusIfScheduled :execrows
UPDATE notifications
SET
    status = sqlc.arg('status'),
    updated_at = NOW()
WHERE id = sqlc.arg('id')
  AND status = 'SCHEDULED';

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

-- name: ListNotificationsWithFilters :many
SELECT
    n.id,
    n.title,
    n.status,
    su.name AS created_by_name,
    n.scheduled_at,
    n.created_at,
    COALESCE(
        (SELECT DISTINCT nt.target_type 
         FROM notification_targets nt 
         WHERE nt.notification_id = n.id 
         LIMIT 1), 
        'BROADCAST'
    )::text AS target_type
FROM notifications n
JOIN staff_users su ON su.id = n.created_by
WHERE 
    (sqlc.arg('status')::text = '' OR n.status = sqlc.arg('status'))
    AND (sqlc.arg('target_type')::text = '' OR COALESCE(
        (SELECT DISTINCT nt.target_type 
         FROM notification_targets nt 
         WHERE nt.notification_id = n.id 
         LIMIT 1), 
        'BROADCAST'
    ) = sqlc.arg('target_type'))
    AND (sqlc.arg('keyword')::text = '' OR n.title ILIKE '%' || sqlc.arg('keyword') || '%')
ORDER BY n.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: ListScheduledNotificationsDue :many
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
WHERE status = 'SCHEDULED'
  AND scheduled_at <= NOW()
ORDER BY scheduled_at ASC
LIMIT sqlc.arg('limit');

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

-- name: CountNotificationsWithFilters :one
SELECT COUNT(DISTINCT n.id)
FROM notifications n
LEFT JOIN notification_targets nt ON nt.notification_id = n.id
WHERE 
    (sqlc.arg('status')::text = '' OR n.status = sqlc.arg('status'))
    AND (sqlc.arg('target_type')::text = '' OR nt.target_type = sqlc.arg('target_type'))
    AND (sqlc.arg('keyword')::text = '' OR n.title ILIKE '%' || sqlc.arg('keyword') || '%');
