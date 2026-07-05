-- name: GetSegmentByID :one
SELECT
    id,
    name,
    description,
    created_by,
    created_at,
    updated_at
FROM segments
WHERE id = $1
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
WHERE name = $1
LIMIT 1;

-- name: CreateSegment :one
INSERT INTO segments (
    name,
    description,
    created_by
)
VALUES (
    $1,
    $2,
    $3
)
RETURNING *;

-- name: UpdateSegment :exec
UPDATE segments
SET
    name = $2,
    description = $3,
    updated_at = NOW()
WHERE id = $1;

-- name: DeleteSegment :exec
DELETE FROM segments
WHERE id = $1;

-- name: ListSegments :many
SELECT
    id,
    name,
    description,
    created_by,
    created_at,
    updated_at
FROM segments
ORDER BY id;
