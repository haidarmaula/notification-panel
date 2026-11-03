-- name: LockScopedSegment :one
SELECT id, name, description, created_at, updated_at
FROM segments
WHERE application_id = sqlc.arg(application_id)::bigint AND id = sqlc.arg(segment_id)::bigint
FOR UPDATE;

-- name: ScopedSegmentNameTaken :one
SELECT EXISTS (
    SELECT 1 FROM segments
    WHERE application_id = sqlc.arg(application_id)::bigint
      AND name = sqlc.arg(name)
      AND id <> sqlc.arg(except_id)::bigint
);

-- name: CreateScopedSegment :one
INSERT INTO segments (application_id, name, description, created_by)
VALUES (sqlc.arg(application_id)::bigint, sqlc.arg(name), sqlc.narg(description), sqlc.arg(created_by))
RETURNING id, name, description, created_at, updated_at;

-- name: ReplaceScopedSegment :one
UPDATE segments
SET name = sqlc.arg(name), description = sqlc.narg(description), updated_at = now()
WHERE application_id = sqlc.arg(application_id)::bigint AND id = sqlc.arg(segment_id)::bigint
RETURNING id, name, description, created_at, updated_at;

-- name: ScopedSegmentHasReferences :one
SELECT (EXISTS (SELECT 1 FROM segment_members WHERE segment_id = sqlc.arg(segment_id)::bigint)
    OR EXISTS (SELECT 1 FROM notification_targets WHERE segment_id = sqlc.arg(segment_id)::bigint))::boolean;

-- name: DeleteScopedSegment :exec
DELETE FROM segments
WHERE application_id = sqlc.arg(application_id)::bigint AND id = sqlc.arg(segment_id)::bigint;

-- name: CreateScopedSegmentMember :one
INSERT INTO segment_members (application_id, segment_id, user_id)
SELECT u.application_id, sqlc.arg(segment_id)::bigint, u.id
FROM users u
WHERE u.application_id = sqlc.arg(application_id)::bigint AND u.id = sqlc.arg(user_id)::bigint
FOR KEY SHARE OF u
RETURNING id;

-- name: DeleteScopedSegmentMember :one
DELETE FROM segment_members
WHERE application_id = sqlc.arg(application_id)::bigint
  AND segment_id = sqlc.arg(segment_id)::bigint AND user_id = sqlc.arg(user_id)::bigint
RETURNING id;
