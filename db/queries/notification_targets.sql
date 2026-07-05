-- name: GetNotificationTargetByID :one
SELECT
    id,
    notification_id,
    target_type,
    segment_id,
    user_id,
    upload_batch_id,
    created_at
FROM notification_targets
WHERE id = $1
LIMIT 1;

-- name: GetNotificationTargetsByNotificationID :many
SELECT
    id,
    notification_id,
    target_type,
    segment_id,
    user_id,
    upload_batch_id,
    created_at
FROM notification_targets
WHERE notification_id = $1
ORDER BY id;

-- name: CreateNotificationTarget :one
INSERT INTO notification_targets (
    notification_id,
    target_type,
    segment_id,
    user_id,
    upload_batch_id
)
VALUES (
    $1,
    $2,
    $3,
    $4,
    $5
)
RETURNING *;

-- name: DeleteNotificationTarget :exec
DELETE FROM notification_targets
WHERE id = $1;

-- name: DeleteNotificationTargetsByNotification :exec
DELETE FROM notification_targets
WHERE notification_id = $1;

-- name: ListNotificationTargets :many
SELECT
    id,
    notification_id,
    target_type,
    segment_id,
    user_id,
    upload_batch_id,
    created_at
FROM notification_targets
WHERE
    ($1::bigint IS NULL OR notification_id = $1) AND
    ($2::text IS NULL OR target_type = $2)
ORDER BY id;
