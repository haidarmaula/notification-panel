-- ==========================================
-- CREATE
-- ==========================================

-- name: CreateNotificationRead :one
INSERT INTO notification_reads (
    notification_id,
    user_id,
    read_at
)
VALUES (
    sqlc.arg('notification_id'),
    sqlc.arg('user_id'),
    NOW()
)
RETURNING
    id,
    notification_id,
    user_id,
    read_at;

-- ==========================================
-- EXISTS
-- ==========================================

-- name: ExistsNotificationRead :one
SELECT EXISTS (
    SELECT 1
    FROM notification_reads
    WHERE
        notification_id = sqlc.arg('notification_id')
        AND user_id = sqlc.arg('user_id')
);

-- ==========================================
-- LIST
-- ==========================================

-- name: ListNotificationReads :many
SELECT
    nr.id,
    u.name,
    u.email,
    nr.read_at
FROM notification_reads nr
JOIN users u
    ON u.id = nr.user_id
WHERE nr.notification_id = sqlc.arg('notification_id')
ORDER BY nr.read_at DESC
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- COUNT
-- ==========================================

-- name: CountNotificationReads :one
SELECT COUNT(*)
FROM notification_reads
WHERE notification_id = sqlc.arg('notification_id');
