-- name: OpenStaffSession :execrows
WITH lineage AS MATERIALIZED (
    SELECT session_epoch FROM staff_session_control WHERE singleton FOR SHARE
), actor AS MATERIALIZED (
    SELECT s.id,s.auth_epoch,l.session_epoch FROM lineage l CROSS JOIN staff_users s WHERE s.id=sqlc.arg(staff_id)::bigint
        AND s.is_active AND s.auth_epoch=sqlc.arg(auth_epoch)::bigint FOR SHARE OF s
)
INSERT INTO staff_sessions(id,staff_user_id,auth_epoch,refresh_digest,expires_at,session_epoch)
SELECT sqlc.arg(session_id)::uuid,a.id,a.auth_epoch,sqlc.arg(refresh_digest)::bytea,sqlc.arg(expires_at)::timestamptz,a.session_epoch
FROM actor a WHERE clock_timestamp()<sqlc.arg(expires_at)::timestamptz;

-- name: StaffSessionActive :one
SELECT EXISTS(SELECT 1 FROM staff_sessions f JOIN staff_users s ON s.id=f.staff_user_id AND s.auth_epoch=f.auth_epoch
    JOIN staff_session_control l ON l.singleton AND l.session_epoch=f.session_epoch
    WHERE f.id=sqlc.arg(session_id)::uuid AND s.id=sqlc.arg(staff_id)::bigint AND s.is_active
        AND f.revoked_at IS NULL AND f.expires_at>clock_timestamp());

-- name: RotateStaffSession :one
WITH lineage AS MATERIALIZED (
    SELECT session_epoch FROM staff_session_control WHERE singleton FOR SHARE
), actor AS MATERIALIZED (
    SELECT s.id,s.auth_epoch,l.session_epoch FROM lineage l CROSS JOIN staff_users s WHERE s.id=sqlc.arg(staff_id)::bigint
        AND s.is_active AND s.auth_epoch=sqlc.arg(auth_epoch)::bigint FOR SHARE OF s
), head AS MATERIALIZED (
    SELECT f.id,f.refresh_digest=sqlc.arg(presented_digest)::bytea AS matches FROM actor s JOIN staff_sessions f
        ON f.staff_user_id=s.id AND f.auth_epoch=s.auth_epoch AND f.session_epoch=s.session_epoch
    WHERE f.id=sqlc.arg(session_id)::uuid AND f.revoked_at IS NULL AND f.expires_at=sqlc.arg(expires_at)::timestamptz
        AND f.expires_at>clock_timestamp() FOR UPDATE OF f
)
UPDATE staff_sessions f SET
    refresh_digest=CASE WHEN h.matches THEN sqlc.arg(next_digest)::bytea ELSE f.refresh_digest END,
    generation=CASE WHEN h.matches THEN f.generation+1 ELSE f.generation END,
    rotated_at=CASE WHEN h.matches THEN clock_timestamp() ELSE f.rotated_at END,
    revoked_at=CASE WHEN h.matches THEN NULL ELSE clock_timestamp() END,
    revocation_reason=CASE WHEN h.matches THEN NULL ELSE 'refresh_reuse' END
FROM head h WHERE h.id=f.id AND f.revoked_at IS NULL AND f.expires_at>clock_timestamp()
RETURNING (f.revoked_at IS NULL)::boolean AS rotated;

-- name: RevokeStaffSession :execrows
UPDATE staff_sessions SET revoked_at=clock_timestamp(),revocation_reason='logout'
WHERE id=sqlc.arg(session_id)::uuid AND staff_user_id=sqlc.arg(staff_id)::bigint AND revoked_at IS NULL;
