-- ==========================================
-- GET
-- ==========================================

-- name: GetAuditLogByID :one
SELECT
    id,
    actor_user_id,
    action,
    entity_type,
    entity_id,
    before_json,
    after_json,
    ip_address,
    created_at
FROM audit_logs
WHERE id = sqlc.arg('id')
LIMIT 1;

-- ==========================================
-- CREATE
-- ==========================================

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
    sqlc.arg('actor_user_id'),
    sqlc.arg('action'),
    sqlc.arg('entity_type'),
    sqlc.arg('entity_name'),
    sqlc.arg('entity_id'),
    sqlc.arg('before_json'),
    sqlc.arg('after_json'),
    sqlc.arg('ip_address'),
    sqlc.arg('user_agent')
)
RETURNING
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
    created_at;

-- ==========================================
-- LIST
-- ==========================================

-- name: ListAuditLogs :many
SELECT
    al.id,
    su.name AS actor_name,
    al.action,
    al.entity_type,
    al.entity_id,
    al.ip_address,
    al.created_at
FROM audit_logs al
JOIN staff_users su
    ON su.id = al.actor_user_id
ORDER BY al.created_at DESC
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- COUNT
-- ==========================================

-- name: CountAuditLogs :one
SELECT COUNT(*)
FROM audit_logs;
