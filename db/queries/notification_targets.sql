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

-- ==========================================
-- LIST
-- ==========================================

-- name: ListNotificationTargets :many
SELECT
    nt.id,
    nt.notification_id,
    u.id AS user_id,
    u.external_id,
    u.name,
    u.email,
    nt.created_at
FROM notification_targets nt
JOIN users u
    ON u.id = nt.user_id
WHERE nt.notification_id = sqlc.arg('notification_id')
ORDER BY u.name
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
