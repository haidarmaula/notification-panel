-- ==========================================
-- GET
-- ==========================================

-- name: GetSegmentByID :one
SELECT
    id,
    name,
    description,
    created_by,
    created_at,
    updated_at
FROM segments
WHERE id = sqlc.arg('id')
LIMIT 1;

-- name: GetSegmentByName :one
SELECT
    id,
    name,
    description,
    created_by,
    created_at,
    updated_at
FROM segments
WHERE name = sqlc.arg('name')
LIMIT 1;

-- ==========================================
-- EXISTS
-- ==========================================

-- name: ExistsSegmentByName :one
SELECT EXISTS (
    SELECT 1
    FROM segments
    WHERE name = sqlc.arg('name')
);

-- ==========================================
-- CREATE
-- ==========================================

-- name: CreateSegment :one
INSERT INTO segments (
    name,
    description,
    created_by
)
VALUES (
    sqlc.arg('name'),
    sqlc.arg('description'),
    sqlc.arg('created_by')
)
RETURNING
    id,
    name,
    description,
    created_by,
    created_at,
    updated_at;

-- ==========================================
-- UPDATE
-- ==========================================

-- name: UpdateSegment :exec
UPDATE segments
SET
    name = sqlc.arg('name'),
    description = sqlc.arg('description'),
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- ==========================================
-- DELETE
-- ==========================================

-- name: DeleteSegment :exec
DELETE
FROM segments
WHERE id = sqlc.arg('id');

-- ==========================================
-- LIST
-- ==========================================

-- name: ListSegments :many
SELECT
    s.id,
    s.name,
    s.description,
    s.created_by,
    su.name AS created_by_name,
    s.created_at,
    s.updated_at
FROM segments s
JOIN staff_users su ON su.id = s.created_by
ORDER BY s.name
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- SEARCH
-- ==========================================

-- name: SearchSegments :many
SELECT
    s.id,
    s.name,
    s.description,
    s.created_by,
    su.name AS created_by_name,
    s.created_at,
    s.updated_at
FROM segments s
JOIN staff_users su ON su.id = s.created_by
WHERE s.name ILIKE '%' || sqlc.arg('keyword') || '%'
ORDER BY s.name
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- ==========================================
-- COUNT
-- ==========================================

-- name: CountSegments :one
SELECT COUNT(*)
FROM segments;
