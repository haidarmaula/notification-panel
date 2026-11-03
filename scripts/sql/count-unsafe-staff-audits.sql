-- Read-only verification; returns a count, never stored credential material.
SELECT count(*) AS unsafe_rows
FROM audit_logs AS a
WHERE a.entity_type = 'staff_user'
  AND (
      (a.action = 'STAFF_UPDATE_PASSWORD' AND (a.before_json IS NOT NULL OR a.after_json IS NOT NULL))
      OR (a.before_json IS NOT NULL AND (
          jsonb_typeof(a.before_json) <> 'object'
          OR EXISTS (
              SELECT 1
              FROM jsonb_each(CASE WHEN jsonb_typeof(a.before_json) = 'object'
                                   THEN a.before_json ELSE '{}'::jsonb END)
              WHERE key NOT IN ('ID','RoleID','RoleName','Name','Email','IsActive','CreatedAt','UpdatedAt')
                 OR jsonb_typeof(value) NOT IN ('string','number','boolean','null')
          )
      ))
      OR (a.after_json IS NOT NULL AND (
          jsonb_typeof(a.after_json) <> 'object'
          OR EXISTS (
              SELECT 1
              FROM jsonb_each(CASE WHEN jsonb_typeof(a.after_json) = 'object'
                                   THEN a.after_json ELSE '{}'::jsonb END)
              WHERE key NOT IN ('ID','RoleID','RoleName','Name','Email','IsActive','CreatedAt','UpdatedAt')
                 OR jsonb_typeof(value) NOT IN ('string','number','boolean','null')
          )
      ))
  );
