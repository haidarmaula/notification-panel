-- Registry metadata only. No application activation/deletion/identity reassignment.

-- name: CreateApplication :one
INSERT INTO applications (key, name)
VALUES (sqlc.arg('key'), sqlc.arg('name'))
RETURNING *;

-- name: GetApplicationByPublicID :one
SELECT * FROM applications WHERE public_id = sqlc.arg('public_id');

-- name: LockApplicationByPublicID :one
SELECT * FROM applications WHERE public_id = sqlc.arg('public_id') FOR UPDATE;

-- Caller holds application lock and handles matching no-op before this CAS.
-- name: RenameApplication :one
UPDATE applications SET name = sqlc.arg('name'), revision = revision + 1,
    updated_at = clock_timestamp()
WHERE id = sqlc.arg('id') AND revision = sqlc.arg('expected_revision')
    AND name IS DISTINCT FROM sqlc.arg('name')
RETURNING *;

-- One snapshot for both total and page, including an empty/out-of-range page.
-- name: ListManagedApplications :one
WITH eligible AS MATERIALIZED (
    SELECT id, public_id, key, name, status, revision, created_at, updated_at
    FROM applications
    WHERE sqlc.arg('filter_key')::text = '' OR key = sqlc.arg('filter_key')::text
), page AS (
    SELECT * FROM eligible ORDER BY created_at, id
    LIMIT sqlc.arg('limit_count') OFFSET sqlc.arg('offset_count')
)
SELECT (SELECT count(*) FROM eligible)::bigint AS total,
    COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.created_at, p.id) FROM page p), '[]'::jsonb)::jsonb AS items;

-- Does not authenticate the caller; service must check current authority first.
-- name: ListGrantedApplications :one
WITH eligible AS MATERIALIZED (
    SELECT a.id, a.public_id, a.key, a.name, a.status, a.revision, a.created_at, a.updated_at
    FROM applications a
    JOIN application_staff_grants g ON g.application_id = a.id
    WHERE g.staff_id = sqlc.arg('staff_id') AND g.is_active
), page AS (
    SELECT * FROM eligible ORDER BY created_at, id
    LIMIT sqlc.arg('limit_count') OFFSET sqlc.arg('offset_count')
)
SELECT (SELECT count(*) FROM eligible)::bigint AS total,
    COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.created_at, p.id) FROM page p), '[]'::jsonb)::jsonb AS items;

-- Keep staff row when app/grant is absent, for correct 401/404 distinctions.
-- No authorization/permission/status decision is implied by this data lookup.
-- name: GetApplicationAuthority :one
SELECT su.id AS staff_id, su.is_active AS staff_is_active, r.name AS role_name,
    a.id AS application_id, a.public_id AS application_public_id,
    a.status AS application_status, g.is_active AS grant_is_active
FROM staff_users su
LEFT JOIN roles r ON r.id = su.role_id
LEFT JOIN applications a ON a.public_id = sqlc.arg('public_id')
LEFT JOIN application_staff_grants g ON g.application_id = a.id AND g.staff_id = su.id
WHERE su.id = sqlc.arg('staff_id');

-- name: CreateApplicationStaffGrant :one
INSERT INTO application_staff_grants (application_id, staff_id)
VALUES (sqlc.arg('application_id'), sqlc.arg('staff_id'))
RETURNING *;

-- name: GetApplicationStaffGrant :one
SELECT * FROM application_staff_grants
WHERE application_id = sqlc.arg('application_id') AND staff_id = sqlc.arg('staff_id');

-- Caller holds app lock; no-op and conditional semantics belong to service.
-- name: SetApplicationStaffGrantActive :one
UPDATE application_staff_grants SET is_active = sqlc.arg('is_active'),
    revision = revision + 1, updated_at = clock_timestamp()
WHERE application_id = sqlc.arg('application_id') AND staff_id = sqlc.arg('staff_id')
    AND revision = sqlc.arg('expected_revision')
    AND is_active IS DISTINCT FROM sqlc.arg('is_active')
RETURNING *;

-- Includes revoked grants; application identity validated by owning service.
-- name: ListApplicationStaffGrants :one
WITH eligible AS MATERIALIZED (
    SELECT application_id, staff_id, is_active, revision, created_at, updated_at
    FROM application_staff_grants WHERE application_id = sqlc.arg('application_id')
), page AS (
    SELECT * FROM eligible ORDER BY created_at, staff_id
    LIMIT sqlc.arg('limit_count') OFFSET sqlc.arg('offset_count')
)
SELECT (SELECT count(*) FROM eligible)::bigint AS total,
    COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.created_at, p.staff_id) FROM page p), '[]'::jsonb)::jsonb AS items;
