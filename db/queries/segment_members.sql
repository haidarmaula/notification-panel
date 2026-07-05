-- name: GetSegmentMemberByID :one
SELECT
    id,
    segment_id,
    user_id,
    created_at
FROM segment_members
WHERE id = $1
LIMIT 1;

-- name: GetSegmentMemberBySegmentAndUser :one
SELECT
    id,
    segment_id,
    user_id,
    created_at
FROM segment_members
WHERE segment_id = $1 AND user_id = $2
LIMIT 1;

-- name: GetSegmentMembersBySegmentID :many
SELECT
    id,
    segment_id,
    user_id,
    created_at
FROM segment_members
WHERE segment_id = $1
ORDER BY id;

-- name: GetSegmentMembersByUserID :many
SELECT
    id,
    segment_id,
    user_id,
    created_at
FROM segment_members
WHERE user_id = $1
ORDER BY id;

-- name: CreateSegmentMember :one
INSERT INTO segment_members (
    segment_id,
    user_id
)
VALUES (
    $1,
    $2
)
RETURNING *;

-- name: DeleteSegmentMember :exec
DELETE FROM segment_members
WHERE id = $1;

-- name: DeleteSegmentMembersBySegment :exec
DELETE FROM segment_members
WHERE segment_id = $1;

-- name: DeleteSegmentMembersByUser :exec
DELETE FROM segment_members
WHERE user_id = $1;

-- name: ListSegmentMembers :many
SELECT
    id,
    segment_id,
    user_id,
    created_at
FROM segment_members
WHERE
    ($1::bigint IS NULL OR segment_id = $1) AND
    ($2::bigint IS NULL OR user_id = $2)
ORDER BY id;
