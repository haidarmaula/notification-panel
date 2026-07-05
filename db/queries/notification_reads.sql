-- name: GetReadByID :one
SELECT
    id,
    notification_id,
    user_id,
    read_at
FROM notification_reads
WHERE id = $1
LIMIT 1;

-- name: GetReadByNotificationAndUser :one
SELECT
    id,
    notification_id,
    user_id,
    read_at
FROM notification_reads
WHERE notification_id = $1 AND user_id = $2
LIMIT 1;

-- name: GetReadsByNotificationID :many
SELECT
    id,
    notification_id,
    user_id,
    read_at
FROM notification_reads
WHERE notification_id = $1
ORDER BY id;

-- name: GetReadsByUserID :many
SELECT
    id,
    notification_id,
    user_id,
    read_at
FROM notification_reads
WHERE user_id = $1
ORDER BY read_at DESC;

-- name: CreateRead :one
INSERT INTO notification_reads (
    notification_id,
    user_id,
    read_at
)
VALUES (
    $1,
    $2,
    $3
)
RETURNING *;

-- name: DeleteRead :exec
DELETE FROM notification_reads
WHERE id = $1;

-- name: ListReads :many
SELECT
    id,
    notification_id,
    user_id,
    read_at
FROM notification_reads
WHERE
    ($1::bigint IS NULL OR notification_id = $1) AND
    ($2::bigint IS NULL OR user_id = $2)
ORDER BY read_at DESC;
