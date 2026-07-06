-- ==========================================
-- CREATE
-- ==========================================

-- name: CreateNotificationTarget :one
INSERT INTO notification_targets (
    notification_id,
    user_id
)
VALUES (
    sqlc.arg('notification_id'),
    sqlc.arg('user_id')
)
RETURNING
    id,
    notification_id,
    user_id,
    created_at;

-- name: CreateNotificationTargetFull :one
INSERT INTO notification_targets (
    notification_id,
    target_type,
    segment_id,
    user_id,
    upload_batch_id
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING
    id,
    notification_id,
    target_type,
    segment_id,
    user_id,
    upload_batch_id,
    created_at;

-- ==========================================
-- LIST
-- ==========================================

-- name: ListNotificationTargets :many
SELECT
    nt.id,
    nt.notification_id,
    nt.target_type,
    nt.segment_id,
    u.id AS user_id,
    u.external_id,
    u.name,
    u.email,
    nt.created_at
FROM notification_targets nt
LEFT JOIN users u ON u.id = nt.user_id
WHERE nt.notification_id = sqlc.arg('notification_id')
ORDER BY u.name NULLS FIRST
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- COUNT
-- ==========================================

-- name: CountNotificationTargets :one
SELECT COUNT(*)
FROM notification_targets
WHERE notification_id = sqlc.arg('notification_id');

-- ==========================================
-- DELETE
-- ==========================================

-- name: DeleteNotificationTarget :exec
DELETE
FROM notification_targets
WHERE id = sqlc.arg('id');

-- name: DeleteNotificationTargetsByNotification :exec
DELETE FROM notification_targets WHERE notification_id = $1;
