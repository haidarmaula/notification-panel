-- ==========================================
-- GET
-- ==========================================

-- name: GetDeviceTokenByID :one
SELECT
    id,
    user_id,
    provider,
    platform,
    installation_id,
    push_token,
    app_version,
    os_version,
    device_model,
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
    provider,
    platform,
    installation_id,
    push_token,
    app_version,
    os_version,
    device_model,
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
    push_token,
    provider,
    app_version,
    os_version,
    device_model
) VALUES (
    sqlc.arg('user_id'),
    sqlc.arg('platform'),
    sqlc.arg('installation_id'),
    sqlc.arg('push_token'),
    sqlc.arg('provider'),
    sqlc.arg('app_version'),
    sqlc.arg('os_version'),
    sqlc.arg('device_model')
)
RETURNING
    id,
    user_id,
    platform,
    installation_id,
    push_token,
    provider,
    app_version,
    os_version,
    device_model,
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

-- name: UpdateDeviceTokenFull :exec
UPDATE device_tokens
SET
    platform = sqlc.arg('platform'),
    installation_id = sqlc.arg('installation_id'),
    push_token = sqlc.arg('push_token'),
    provider = sqlc.arg('provider'),
    app_version = sqlc.arg('app_version'),
    os_version = sqlc.arg('os_version'),
    device_model = sqlc.arg('device_model'),
    last_seen_at = NOW(),
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
    provider,
    platform,
    installation_id,
    push_token,
    app_version,
    os_version,
    device_model,
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
