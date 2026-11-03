-- Operator-only, atomic batch. See docs/runbooks/staff-audit-redaction.md.
-- No credentials or old payloads are returned. No schema changes are made.
WITH candidates AS MATERIALIZED (
    SELECT a.id, clean.before_json, clean.after_json
    FROM audit_logs AS a
    CROSS JOIN LATERAL (
        SELECT
            CASE WHEN a.action = 'STAFF_UPDATE_PASSWORD' THEN NULL::jsonb
                 WHEN jsonb_typeof(a.before_json) = 'object' THEN (
                     SELECT COALESCE(jsonb_object_agg(key, value), '{}'::jsonb)
                     FROM jsonb_each(CASE WHEN jsonb_typeof(a.before_json) = 'object'
                                          THEN a.before_json ELSE '{}'::jsonb END)
                     WHERE key IN ('ID','RoleID','RoleName','Name','Email','IsActive','CreatedAt','UpdatedAt')
                       AND jsonb_typeof(value) IN ('string','number','boolean','null')
                 ) ELSE NULL::jsonb END AS before_json,
            CASE WHEN a.action = 'STAFF_UPDATE_PASSWORD' THEN NULL::jsonb
                 WHEN jsonb_typeof(a.after_json) = 'object' THEN (
                     SELECT COALESCE(jsonb_object_agg(key, value), '{}'::jsonb)
                     FROM jsonb_each(CASE WHEN jsonb_typeof(a.after_json) = 'object'
                                          THEN a.after_json ELSE '{}'::jsonb END)
                     WHERE key IN ('ID','RoleID','RoleName','Name','Email','IsActive','CreatedAt','UpdatedAt')
                       AND jsonb_typeof(value) IN ('string','number','boolean','null')
                 ) ELSE NULL::jsonb END AS after_json
    ) AS clean
    WHERE a.entity_type = 'staff_user'
      AND (a.before_json IS DISTINCT FROM clean.before_json
        OR a.after_json IS DISTINCT FROM clean.after_json)
    ORDER BY a.id
    LIMIT 500
    FOR UPDATE OF a SKIP LOCKED
), changed AS (
    UPDATE audit_logs AS a
    SET before_json = c.before_json, after_json = c.after_json
    FROM candidates AS c
    WHERE a.id = c.id
    RETURNING a.id
)
SELECT count(*) AS redacted_rows FROM changed;
