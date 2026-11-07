-- Owner-only snapshot. psql supplies a quoted public application UUID.
-- Each active user is counted once. A legacy registration cannot establish
-- physical device identity, consent or a match to a modern installation.
WITH project AS MATERIALIZED (
    SELECT id,public_id,status FROM applications WHERE public_id=:'project'::uuid
), active_users AS MATERIALIZED (
    SELECT u.id FROM users u JOIN project p ON p.id=u.application_id WHERE u.status='ACTIVE'
), modern AS MATERIALIZED (
    SELECT i.id,i.owner_user_id AS recipient,b.platform,r.cohort_id,
        CASE WHEN b.id IS NULL THEN 'NO_CURRENT_BINDING'
             WHEN r.revision IS NULL THEN 'FCM_ENROLLED'
             WHEN (r.origin_epoch,r.source_epoch) IS DISTINCT FROM (n.origin_epoch,n.source_epoch) THEN 'PRIOR_EPOCH'
             ELSE r.destination END AS state
    FROM mobile_installations i JOIN project p ON p.id=i.application_id
    JOIN active_users u ON u.id=i.owner_user_id
    CROSS JOIN notification_runtime n
    LEFT JOIN installation_bindings b ON b.application_id=i.application_id AND b.installation_id=i.id
        AND b.owner_user_id=i.owner_user_id AND b.owner_epoch=i.owner_epoch AND b.retired_at IS NULL
    LEFT JOIN LATERAL (SELECT revision,cohort_id,destination,origin_epoch,source_epoch FROM current_binding_routes cr
        WHERE cr.application_id=i.application_id AND cr.binding_id=b.id LIMIT 1) r ON true
    WHERE n.singleton
), legacy AS MATERIALIZED (
    SELECT t.user_id,t.platform,
        -- Limit untrusted legacy version strings to a numeric release shape.
        CASE WHEN t.app_version COLLATE "C" ~ '^[0-9]{1,3}[.][0-9]{1,3}([.][0-9]{1,3})?$'
             THEN t.app_version END AS version, t.application_id IS NULL AS unassigned
    FROM device_tokens t JOIN active_users u ON u.id=t.user_id CROSS JOIN project p
    WHERE t.is_active AND (t.application_id=p.id OR t.application_id IS NULL)
), coverage AS (
    SELECT u.id, coalesce(m.bindings,0) AS bindings, coalesce(m.approved,0) AS approved,
        coalesce(m.installations,0) AS installations, l.user_id IS NOT NULL AS has_legacy
    FROM active_users u LEFT JOIN (
        SELECT recipient,count(*) AS installations,count(platform) AS bindings,
               count(*) FILTER (WHERE state='FCM_ACTIVE') AS approved FROM modern GROUP BY recipient
    ) m ON m.recipient=u.id LEFT JOIN (SELECT DISTINCT user_id FROM legacy) l ON l.user_id=u.id
), groups AS (
    SELECT 'installation' AS unit,platform,NULL::text AS app_version,cohort_id,state,count(*) AS quantity
        FROM modern GROUP BY platform,cohort_id,state
    UNION ALL
    SELECT 'legacy_registration',platform,version,NULL::uuid,
        CASE WHEN unassigned THEN 'UNATTRIBUTED' ELSE 'UNRECONCILED' END,count(*)
        FROM legacy GROUP BY platform,version,unassigned
), limited AS MATERIALIZED (
    SELECT * FROM groups ORDER BY unit,platform NULLS FIRST,app_version NULLS FIRST,cohort_id NULLS FIRST,state LIMIT 201
)
SELECT jsonb_build_object(
    'application',p.public_id,'application_status',p.status,'as_of',statement_timestamp(),
    'population','active_users_in_application','active_users',(SELECT count(*) FROM coverage),
    'users_with_approved_modern_only',(SELECT count(*) FROM coverage WHERE approved>0 AND approved=installations AND NOT has_legacy),
    'users_with_legacy_only',(SELECT count(*) FROM coverage WHERE bindings=0 AND has_legacy),
    'users_with_both_registries',(SELECT count(*) FROM coverage WHERE bindings>0 AND has_legacy),
    'users_without_current_address',(SELECT count(*) FROM coverage WHERE bindings=0 AND NOT has_legacy),
    'owned_installations',(SELECT count(*) FROM modern),
    'ownerless_installations',(SELECT count(*) FROM mobile_installations i WHERE i.application_id=p.id AND i.owner_user_id IS NULL),
    'current_modern_bindings',(SELECT count(platform) FROM modern),
    'approved_bindings',(SELECT count(*) FROM modern WHERE state='FCM_ACTIVE'),
    'unreconciled_legacy_registrations',(SELECT count(*) FROM legacy),
    'unassigned_legacy_registrations_for_scoped_users',(SELECT count(*) FROM legacy WHERE unassigned),
    'consent_excluded_devices',NULL,'eligible_physical_devices',NULL,
    'groups_truncated',(SELECT count(*)>200 FROM limited),
    'groups',coalesce((SELECT jsonb_agg(to_jsonb(g) ORDER BY unit,platform NULLS FIRST,app_version NULLS FIRST,cohort_id NULLS FIRST,state)
        FROM (SELECT * FROM limited ORDER BY unit,platform NULLS FIRST,app_version NULLS FIRST,cohort_id NULLS FIRST,state LIMIT 200) g),'[]'::jsonb)
) AS coverage FROM project p;
