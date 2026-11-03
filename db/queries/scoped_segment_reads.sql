-- name: GetScopedSegment :one
SELECT id, name, description, created_at, updated_at
FROM segments
WHERE application_id = sqlc.arg(application_id)::bigint
  AND id = sqlc.arg(segment_id)::bigint;

-- name: ListScopedSegments :many
SELECT id, name, description, created_at, updated_at
FROM segments
WHERE application_id = sqlc.arg(application_id)::bigint
  AND id > sqlc.arg(after_id)::bigint
ORDER BY id
LIMIT greatest(0, least(sqlc.arg(page_size)::integer, 101));

-- name: ListScopedSegmentMembers :many
SELECT m.id, m.user_id, u.external_id, u.name, m.created_at
FROM segment_members m
JOIN segments s ON s.application_id = m.application_id AND s.id = m.segment_id
JOIN users u ON u.application_id = m.application_id AND u.id = m.user_id
WHERE m.application_id = sqlc.arg(application_id)::bigint
  AND m.segment_id = sqlc.arg(segment_id)::bigint
  AND m.id > sqlc.arg(after_id)::bigint
ORDER BY m.id
LIMIT greatest(0, least(sqlc.arg(page_size)::integer, 101));
