-- Read-only, count-only snapshot after migration 000017. No PII/token output.
WITH ownership AS (
SELECT 'users'::text AS relation, count(*) FILTER (WHERE application_id IS NULL) AS unassigned, count(*) FILTER (WHERE application_id IS NOT NULL) AS assigned FROM public.users
UNION ALL
SELECT 'device_tokens'::text AS relation, count(*) FILTER (WHERE application_id IS NULL) AS unassigned, count(*) FILTER (WHERE application_id IS NOT NULL) AS assigned FROM public.device_tokens
UNION ALL
SELECT 'templates'::text AS relation, count(*) FILTER (WHERE application_id IS NULL) AS unassigned, count(*) FILTER (WHERE application_id IS NOT NULL) AS assigned FROM public.templates
UNION ALL
SELECT 'segments'::text AS relation, count(*) FILTER (WHERE application_id IS NULL) AS unassigned, count(*) FILTER (WHERE application_id IS NOT NULL) AS assigned FROM public.segments
UNION ALL
SELECT 'segment_members'::text AS relation, count(*) FILTER (WHERE application_id IS NULL) AS unassigned, count(*) FILTER (WHERE application_id IS NOT NULL) AS assigned FROM public.segment_members
UNION ALL
SELECT 'notifications'::text AS relation, count(*) FILTER (WHERE application_id IS NULL) AS unassigned, count(*) FILTER (WHERE application_id IS NOT NULL) AS assigned FROM public.notifications
UNION ALL
SELECT 'upload_batches'::text AS relation, count(*) FILTER (WHERE application_id IS NULL) AS unassigned, count(*) FILTER (WHERE application_id IS NOT NULL) AS assigned FROM public.upload_batches
UNION ALL
SELECT 'upload_batch_rows'::text AS relation, count(*) FILTER (WHERE application_id IS NULL) AS unassigned, count(*) FILTER (WHERE application_id IS NOT NULL) AS assigned FROM public.upload_batch_rows
UNION ALL
SELECT 'notification_targets'::text AS relation, count(*) FILTER (WHERE application_id IS NULL) AS unassigned, count(*) FILTER (WHERE application_id IS NOT NULL) AS assigned FROM public.notification_targets
UNION ALL
SELECT 'notification_deliveries'::text AS relation, count(*) FILTER (WHERE application_id IS NULL) AS unassigned, count(*) FILTER (WHERE application_id IS NOT NULL) AS assigned FROM public.notification_deliveries
UNION ALL
SELECT 'notification_reads'::text AS relation, count(*) FILTER (WHERE application_id IS NULL) AS unassigned, count(*) FILTER (WHERE application_id IS NOT NULL) AS assigned FROM public.notification_reads
)
SELECT jsonb_build_object(
    'ownership', (SELECT jsonb_object_agg(relation, jsonb_build_object('unassigned', unassigned, 'assigned', assigned)) FROM ownership),
    'device_user_conflicts', (SELECT count(*) FROM public.notification_deliveries d JOIN public.device_tokens t ON t.id=d.device_token_id WHERE d.user_id<>t.user_id),
    'unresolved_business_audits', (SELECT count(*) FROM public.audit_logs a
        LEFT JOIN public.segments s ON a.entity_type='segment' AND s.id=a.entity_id
        LEFT JOIN public.notifications n ON a.entity_type='notification' AND n.id=a.entity_id
        WHERE a.application_id IS NULL AND a.entity_type IN ('segment','notification') AND s.id IS NULL AND n.id IS NULL),
    'unclassified_global_audits', (SELECT count(*) FROM public.audit_logs WHERE application_id IS NULL AND entity_type NOT IN ('staff_user','segment','notification','application','application_grant')),
    'unassigned_resolvable_business_audits', (SELECT count(*) FROM public.audit_logs a
        WHERE a.application_id IS NULL AND ((a.entity_type='segment' AND EXISTS(SELECT 1 FROM public.segments s WHERE s.id=a.entity_id))
           OR (a.entity_type='notification' AND EXISTS(SELECT 1 FROM public.notifications n WHERE n.id=a.entity_id))))
);
