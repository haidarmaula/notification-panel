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
    device_token_id,
    provider,
    provider_message_id,
    status
) VALUES (
    sqlc.arg('notification_id'),
    sqlc.arg('user_id'),
    sqlc.arg('device_token_id'),
    sqlc.arg('provider'),
    sqlc.arg('provider_message_id'),
    sqlc.arg('status')
)
RETURNING
    id,
    notification_id,
    user_id,
    device_token_id,
    provider,
    provider_message_id,
    status,
    retry_count,
    failed_reason,
    sent_at,
    delivered_at,
    opened_at,
    created_at,
    updated_at,
    application_id;

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
-- Returns one row per distinct notification for a user, collapsing the
-- one-row-per-device deliveries into a single representative row. The
-- representative delivery is chosen by status priority (OPENED beats
-- DELIVERED beats SENT beats FAILED beats anything else) so that a
-- notification the user has opened on any device appears as OPENED in
-- the mobile inbox. Ordering is by the representative delivery's
-- created_at descending, so the most recently received notification
-- appears first.
SELECT
    notification_id,
    title,
    status,
    opened_at
FROM (
    SELECT DISTINCT ON (n.id)
        n.id AS notification_id,
        n.title,
        nd.status,
        nd.opened_at,
        nd.created_at
    FROM notification_deliveries nd
    JOIN notifications n ON n.id = nd.notification_id
    WHERE nd.user_id = sqlc.arg('user_id')
    ORDER BY
        n.id,
        CASE nd.status
            WHEN 'OPENED' THEN 1
            WHEN 'DELIVERED' THEN 2
            WHEN 'SENT' THEN 3
            WHEN 'FAILED' THEN 4
            ELSE 5
        END ASC,
        nd.created_at DESC
) AS per_notification
ORDER BY created_at DESC
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
-- Counts distinct notifications rather than delivery rows, so the total
-- matches the number of items ListUserNotifications can return for the
-- same user.
SELECT COUNT(DISTINCT notification_id)
FROM notification_deliveries
WHERE user_id = sqlc.arg('user_id');
