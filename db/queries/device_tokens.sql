-- ==========================================
-- GET
-- ==========================================

-- name: GetDeviceTokenByID :one
SELECT
    id,
    user_id,
    platform,
    installation_id,
    push_token,
    is_active,
    last_seen_at,
    created_at,
    updated_at
FROM device_tokens
WHERE id = sqlc.arg('id')
LIMIT 1;

-- name: GetDeviceTokenByPushToken :one
SELECT
    id,
    user_id,
    platform,
    installation_id,
    push_token,
    is_active,
    last_seen_at,
    created_at,
    updated_at
FROM device_tokens
WHERE push_token = sqlc.arg('push_token')
LIMIT 1;

-- ==========================================
-- EXISTS
-- ==========================================

-- name: ExistsDeviceToken :one
SELECT EXISTS (
    SELECT 1
    FROM device_tokens
    WHERE push_token = sqlc.arg('push_token')
);

-- ==========================================
-- CREATE
-- ==========================================

-- name: CreateDeviceToken :one
INSERT INTO device_tokens (
    user_id,
    platform,
    installation_id,
    push_token
)
VALUES (
    sqlc.arg('user_id'),
    sqlc.arg('platform'),
    sqlc.arg('installation_id'),
    sqlc.arg('push_token')
)
RETURNING
    id,
    user_id,
    platform,
    installation_id,
    push_token,
    is_active,
    last_seen_at,
    created_at,
    updated_at;

-- ==========================================
-- UPDATE
-- ==========================================

-- name: UpdateDeviceToken :exec
UPDATE device_tokens
SET
    push_token = sqlc.arg('push_token'),
    last_seen_at = NOW(),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- name: UpdateDeviceTokenStatus :exec
UPDATE device_tokens
SET
    is_active = sqlc.arg('is_active'),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- ==========================================
-- DELETE
-- ==========================================

-- name: DeleteDeviceToken :exec
DELETE
FROM device_tokens
WHERE id = sqlc.arg('id');

-- ==========================================
-- LIST
-- ==========================================

-- name: ListDeviceTokensByUser :many
SELECT
    id,
    user_id,
    platform,
    installation_id,
    push_token,
    is_active,
    last_seen_at,
    created_at,
    updated_at
FROM device_tokens
WHERE user_id = sqlc.arg('user_id')
ORDER BY created_at DESC
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- COUNT
-- ==========================================

-- name: CountDeviceTokensByUser :one
SELECT COUNT(*)
FROM device_tokens
WHERE user_id = sqlc.arg('user_id');
