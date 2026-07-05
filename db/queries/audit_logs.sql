-- name: GetAuditLogByID :one
SELECT
    id,
    actor_user_id,
    action,
    entity_type,
    entity_name,
    entity_id,
    before_json,
    after_json,
    ip_address,
    user_agent,
    created_at
FROM audit_logs
WHERE id = $1
LIMIT 1;

-- name: GetAuditLogsByActor :many
SELECT
    id,
    actor_user_id,
    action,
    entity_type,
    entity_name,
    entity_id,
    before_json,
    after_json,
    ip_address,
    user_agent,
    created_at
FROM audit_logs
WHERE actor_user_id = $1
ORDER BY created_at DESC;

-- name: GetAuditLogsByEntity :many
SELECT
    id,
    actor_user_id,
    action,
    entity_type,
    entity_name,
    entity_id,
    before_json,
    after_json,
    ip_address,
    user_agent,
    created_at
FROM audit_logs
WHERE entity_type = $1 AND entity_id = $2
ORDER BY created_at DESC;

-- name: CreateAuditLog :one
INSERT INTO audit_logs (
    actor_user_id,
    action,
    entity_type,
    entity_name,
    entity_id,
    before_json,
    after_json,
    ip_address,
    user_agent
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
    $9
)
RETURNING *;

-- name: ListAuditLogs :many
SELECT
    id,
    actor_user_id,
    action,
    entity_type,
    entity_name,
    entity_id,
    before_json,
    after_json,
    ip_address,
    user_agent,
    created_at
FROM audit_logs
WHERE
    ($1::bigint IS NULL OR actor_user_id = $1) AND
    ($2::text IS NULL OR entity_type = $2) AND
    ($3::timestamptz IS NULL OR created_at >= $3) AND
    ($4::timestamptz IS NULL OR created_at <= $4)
ORDER BY created_at DESC;
