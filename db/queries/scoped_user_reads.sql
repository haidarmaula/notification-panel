-- name: GetScopedUser :one
SELECT id, external_id, name, email, status, created_at, updated_at
FROM users
WHERE application_id = sqlc.arg(application_id)::bigint
  AND sqlc.arg(application_id)::bigint > 0
  AND id = sqlc.arg(user_id)::bigint
  AND sqlc.arg(user_id)::bigint > 0;

-- name: ListScopedUsers :many
SELECT id, external_id, name, email, status, created_at, updated_at
FROM users
WHERE application_id = sqlc.arg(application_id)::bigint
  AND sqlc.arg(application_id)::bigint > 0
  AND id > sqlc.arg(after_id)::bigint
  AND sqlc.arg(after_id)::bigint >= 0
  AND sqlc.arg(page_size)::integer BETWEEN 1 AND 101
ORDER BY id
LIMIT greatest(0, least(sqlc.arg(page_size)::integer, 101));

-- name: ListScopedUserDevices :many
SELECT d.id, d.platform, d.is_active, d.last_seen_at, d.created_at, d.updated_at
FROM device_tokens d
JOIN users u ON u.application_id = d.application_id AND u.id = d.user_id
WHERE d.application_id = sqlc.arg(application_id)::bigint
  AND sqlc.arg(application_id)::bigint > 0
  AND u.id = sqlc.arg(user_id)::bigint
  AND sqlc.arg(user_id)::bigint > 0
  AND d.id > sqlc.arg(after_id)::bigint
  AND sqlc.arg(after_id)::bigint >= 0
  AND sqlc.arg(page_size)::integer BETWEEN 1 AND 101
ORDER BY d.id
LIMIT greatest(0, least(sqlc.arg(page_size)::integer, 101));
