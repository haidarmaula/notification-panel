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
WHERE id = $1
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
WHERE push_token = $1
LIMIT 1;

-- name: GetDeviceTokensByUserID :many
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
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: CreateDeviceToken :one
INSERT INTO device_tokens (
    user_id,
    provider,
    platform,
    installation_id,
    push_token,
    app_version,
    os_version,
    device_model,
    is_active,
    last_seen_at
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
    $10
)
RETURNING *;

-- name: UpdateDeviceToken :exec
UPDATE device_tokens
SET
    provider = $2,
    platform = $3,
    installation_id = $4,
    push_token = $5,
    app_version = $6,
    os_version = $7,
    device_model = $8,
    is_active = $9,
    last_seen_at = $10,
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateDeviceTokenActive :exec
UPDATE device_tokens
SET
    is_active = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateDeviceTokenLastSeen :exec
UPDATE device_tokens
SET
    last_seen_at = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: DeleteDeviceToken :exec
DELETE FROM device_tokens
WHERE id = $1;

-- name: DeleteDeviceTokensByUser :exec
DELETE FROM device_tokens
WHERE user_id = $1;

-- name: ListDeviceTokens :many
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
WHERE
    ($1::bigint IS NULL OR user_id = $1) AND
    ($2::boolean IS NULL OR is_active = $2)
ORDER BY id;
