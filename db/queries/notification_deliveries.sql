-- ==========================================
-- GET
-- ==========================================

-- name: GetNotificationDeliveryByID :one
SELECT
    id,
    notification_id,
    user_id,
    provider,
    provider_message_id,
    status,
    sent_at,
    delivered_at,
    opened_at,
    failed_reason,
    created_at,
    updated_at
FROM notification_deliveries
WHERE id = sqlc.arg('id')
LIMIT 1;

-- ==========================================
-- CREATE
-- ==========================================

-- name: CreateNotificationDelivery :one
INSERT INTO notification_deliveries (
    notification_id,
    user_id,
    provider,
    provider_message_id,
    status
)
VALUES (
    sqlc.arg('notification_id'),
    sqlc.arg('user_id'),
    sqlc.arg('provider'),
    sqlc.arg('provider_message_id'),
    sqlc.arg('status')
)
RETURNING
    id,
    notification_id,
    user_id,
    provider,
    provider_message_id,
    status,
    sent_at,
    delivered_at,
    opened_at,
    failed_reason,
    created_at,
    updated_at;

-- ==========================================
-- UPDATE STATUS
-- ==========================================

-- name: MarkNotificationDelivered :exec
UPDATE notification_deliveries
SET
    status = 'DELIVERED',
    delivered_at = NOW(),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- name: MarkNotificationOpened :exec
UPDATE notification_deliveries
SET
    status = 'OPENED',
    opened_at = NOW(),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- name: MarkNotificationFailed :exec
UPDATE notification_deliveries
SET
    status = 'FAILED',
    failed_reason = sqlc.arg('failed_reason'),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- ==========================================
-- LIST
-- ==========================================

-- name: ListNotificationDeliveries :many
SELECT
    nd.id,
    nd.provider,
    nd.status,
    u.name,
    u.email,
    nd.sent_at,
    nd.delivered_at,
    nd.opened_at
FROM notification_deliveries nd
JOIN users u
    ON u.id = nd.user_id
WHERE nd.notification_id = sqlc.arg('notification_id')
ORDER BY nd.created_at
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- name: ListUserNotifications :many
SELECT
    n.id AS notification_id,
    n.title,
    nd.status,
    nd.opened_at
FROM notification_deliveries nd
JOIN notifications n ON n.id = nd.notification_id
WHERE nd.user_id = sqlc.arg('user_id')
ORDER BY nd.created_at DESC
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- COUNT
-- ==========================================

-- name: CountNotificationDeliveries :one
SELECT COUNT(*)
FROM notification_deliveries
WHERE notification_id = sqlc.arg('notification_id');

-- name: CountUserNotifications :one
SELECT COUNT(*)
FROM notification_deliveries
WHERE user_id = sqlc.arg('user_id');
