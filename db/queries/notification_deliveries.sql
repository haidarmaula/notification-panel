-- name: GetDeliveryByID :one
SELECT
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
    updated_at
FROM notification_deliveries
WHERE id = $1
LIMIT 1;

-- name: GetDeliveriesByNotificationID :many
SELECT
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
    updated_at
FROM notification_deliveries
WHERE notification_id = $1
ORDER BY id;

-- name: GetDeliveriesByUserID :many
SELECT
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
    updated_at
FROM notification_deliveries
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: GetDeliveriesByStatus :many
SELECT
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
    updated_at
FROM notification_deliveries
WHERE status = $1
ORDER BY created_at;

-- name: CreateDelivery :one
INSERT INTO notification_deliveries (
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
    opened_at
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

-- name: UpdateDelivery :exec
UPDATE notification_deliveries
SET
    provider_message_id = $2,
    status = $3,
    retry_count = $4,
    failed_reason = $5,
    sent_at = $6,
    delivered_at = $7,
    opened_at = $8,
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateDeliveryStatus :exec
UPDATE notification_deliveries
SET
    status = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: DeleteDelivery :exec
DELETE FROM notification_deliveries
WHERE id = $1;

-- name: ListDeliveries :many
SELECT
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
    updated_at
FROM notification_deliveries
WHERE
    ($1::bigint IS NULL OR notification_id = $1) AND
    ($2::bigint IS NULL OR user_id = $2) AND
    ($3::text[] IS NULL OR status = ANY($3))
ORDER BY created_at DESC;
