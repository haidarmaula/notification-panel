-- ==========================================
-- GET
-- ==========================================

-- name: GetSegmentMemberByID :one
SELECT
    id,
    segment_id,
    user_id,
    created_at
FROM segment_members
WHERE id = sqlc.arg('id')
LIMIT 1;

-- ==========================================
-- CREATE
-- ==========================================

-- name: CreateSegmentMember :one
INSERT INTO segment_members (
    segment_id,
    user_id
)
VALUES (
    sqlc.arg('segment_id'),
    sqlc.arg('user_id')
)
RETURNING
    id,
    segment_id,
    user_id,
    created_at;

-- ==========================================
-- DELETE
-- ==========================================

-- name: DeleteSegmentMember :exec
DELETE
FROM segment_members
WHERE id = sqlc.arg('id');

-- ==========================================
-- LIST
-- ==========================================

-- name: ListSegmentMembers :many
SELECT
    sm.id,
    sm.segment_id,
    u.id AS user_id,
    u.name,
    u.email,
    sm.created_at
FROM segment_members sm
JOIN users u
    ON u.id = sm.user_id
WHERE sm.segment_id = sqlc.arg('segment_id')
ORDER BY u.name
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- COUNT
-- ==========================================

-- name: CountSegmentMembers :one
SELECT COUNT(*)
FROM segment_members
WHERE segment_id = sqlc.arg('segment_id');
