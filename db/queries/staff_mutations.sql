-- name: LockStaffAdministration :one
UPDATE staff_administration_guard SET singleton = true WHERE singleton RETURNING singleton;

-- name: LockStaffMutationLineage :one
SELECT session_epoch FROM staff_session_control WHERE singleton FOR SHARE;

-- name: LockStaffMutationUsers :many
SELECT id FROM staff_users WHERE id IN (sqlc.arg(actor)::bigint, sqlc.arg(target)::bigint)
ORDER BY id FOR UPDATE;

-- name: LockStaffMutationSession :one
SELECT f.id FROM staff_sessions f JOIN staff_users s ON s.id = f.staff_user_id
WHERE f.id = sqlc.arg(session_id)::uuid AND s.id = sqlc.arg(actor)::bigint AND s.is_active
  AND f.auth_epoch = s.auth_epoch AND f.session_epoch = sqlc.arg(session_epoch)::uuid
  AND f.revoked_at IS NULL AND f.expires_at > clock_timestamp()
FOR SHARE OF f;

-- name: HasActiveSuperAdmin :one
SELECT EXISTS (
    SELECT 1 FROM staff_users s JOIN roles r ON r.id = s.role_id
    WHERE s.is_active AND r.name = 'SUPER_ADMIN'
);

-- name: UpdateOwnStaffProfile :one
UPDATE staff_users SET
    name = COALESCE(NULLIF(sqlc.arg(name)::text, ''), name),
    email = COALESCE(NULLIF(sqlc.arg(email)::text, ''), email),
    updated_at = clock_timestamp()
WHERE id = sqlc.arg(id)::bigint AND is_active
RETURNING *;

-- name: CountMatchingStaff :one
SELECT count(*) FROM staff_users WHERE name ILIKE '%' || sqlc.arg(keyword)::text || '%'
    OR email ILIKE '%' || sqlc.arg(keyword)::text || '%';
