-- $1 is one bound selection object. No user, installation, message, token or
-- subject identifier is projected. Each source stops at its 501st match.
WITH input AS MATERIALIZED (
    SELECT CASE WHEN octet_length($1::text)<=4096 AND $1::text IS JSON OBJECT WITH UNIQUE KEYS AND pg_input_is_valid($1::text,'jsonb')
                THEN $1::text::jsonb END AS v
), subject AS MATERIALIZED (
    SELECT a.id AS app,u.id AS recipient,a.status AS app_status,u.status AS user_status
    FROM input JOIN public.applications a ON a.public_id=CASE
        WHEN v->>'application_id' COLLATE "C" ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        THEN (v->>'application_id')::uuid END
    JOIN public.mobile_identities m ON m.application_id=a.id AND m.identity_namespace=v->>'identity_namespace' AND m.subject=v->>'subject'
    JOIN public.users u ON (u.application_id,u.id)=(m.application_id,m.user_id)
    WHERE v - 'application_id' - 'identity_namespace' - 'subject'='{}'::jsonb
        AND jsonb_typeof(v->'application_id')='string' AND jsonb_typeof(v->'identity_namespace')='string' AND jsonb_typeof(v->'subject')='string'
        AND octet_length(v->>'identity_namespace') BETWEEN 1 AND 255 AND octet_length(v->>'subject') BETWEEN 1 AND 255
), aliases AS MATERIALIZED (
    SELECT 1 FROM public.mobile_identities m JOIN subject s ON (m.application_id,m.user_id)=(s.app,s.recipient) LIMIT 501
), installations AS MATERIALIZED (
    SELECT 1 FROM public.mobile_installations i JOIN subject s ON (i.application_id,i.owner_user_id)=(s.app,s.recipient) LIMIT 501
), bindings AS MATERIALIZED (
    SELECT b.application_id,b.id,b.retired_at FROM public.installation_bindings b
    JOIN subject s ON (b.application_id,b.owner_user_id)=(s.app,s.recipient) LIMIT 501
), routes AS MATERIALIZED (
    SELECT b.retired_at,r.destination FROM bindings b LEFT JOIN LATERAL (
        SELECT destination FROM public.current_binding_routes r WHERE (r.application_id,r.binding_id)=(b.application_id,b.id) LIMIT 1
    ) r ON true
), devices AS MATERIALIZED (
    SELECT t.is_active,t.application_id IS NULL AS unassigned FROM public.device_tokens t
    JOIN subject s ON t.user_id=s.recipient AND (t.application_id=s.app OR t.application_id IS NULL) LIMIT 501
), members AS MATERIALIZED (
    SELECT 1 FROM public.segment_members m JOIN subject s ON (m.application_id,m.user_id)=(s.app,s.recipient) LIMIT 501
), direct_targets AS MATERIALIZED (
    SELECT t.notification_id FROM public.notification_targets t JOIN subject s ON (t.application_id,t.user_id)=(s.app,s.recipient) LIMIT 501
), legacy AS MATERIALIZED (
    SELECT d.notification_id FROM public.notification_deliveries d JOIN subject s ON (d.application_id,d.user_id)=(s.app,s.recipient) LIMIT 501
), reads AS MATERIALIZED (
    SELECT r.notification_id FROM public.notification_reads r JOIN subject s ON (r.application_id,r.user_id)=(s.app,s.recipient) LIMIT 501
), intents AS MATERIALIZED (
    SELECT d.application_id,d.notification_id,d.id,d.status,d.attempt_count,d.last_attempt_at,d.attempt_resolved_at,
        d.last_result_disposition,d.provider_accepted_at
    FROM public.notification_delivery_intents d JOIN subject s ON (d.application_id,d.owner_user_id)=(s.app,s.recipient) LIMIT 501
), requests AS MATERIALIZED (
    SELECT 1 FROM public.notification_receipt_requests r JOIN subject s ON (r.application_id,r.user_id)=(s.app,s.recipient) LIMIT 501
), capabilities AS MATERIALIZED (
    SELECT m.expires_unix FROM intents d JOIN public.notification_receipt_metadata m
        ON (m.application_id,m.delivery_id)=(d.application_id,d.id)
), observations AS MATERIALIZED (
    SELECT o.event FROM intents d JOIN public.notification_observations o ON (o.application_id,o.delivery_id)=(d.application_id,d.id) LIMIT 501
), updates AS MATERIALIZED (
    SELECT 1 FROM intents d JOIN public.notification_update_outbox o ON (o.application_id,o.delivery_id)=(d.application_id,d.id) LIMIT 501
), campaigns AS MATERIALIZED (
    SELECT n.id,n.application_id,n.origin_epoch,n.source_epoch,n.lease_until,n.preparation_hold_reason FROM (
        SELECT notification_id FROM direct_targets UNION SELECT notification_id FROM reads
        UNION SELECT notification_id FROM legacy UNION SELECT notification_id FROM intents
    ) c CROSS JOIN subject s JOIN public.notifications n ON (n.application_id,n.id)=(s.app,c.notification_id) LIMIT 501
), sends AS MATERIALIZED (
    SELECT 1 FROM campaigns n JOIN public.notification_send_outbox o ON (o.application_id,o.notification_id)=(n.application_id,n.id)
), replay AS MATERIALIZED (
    SELECT a.outcome,resolved.notification_id AS resolved_campaign FROM campaigns n JOIN public.notification_replay_attempts a
        ON (a.application_id,a.notification_id)=(n.application_id,n.id)
    LEFT JOIN public.notification_replay_controls c
        ON (c.log_source_id,c.original_partition,c.original_offset)=(a.log_source_id,a.original_partition,a.original_offset)
    LEFT JOIN public.notification_replay_attempts resolved ON c.resolved_application_id=n.application_id
        AND (resolved.application_id,resolved.request_id,resolved.log_source_id,resolved.original_partition,resolved.original_offset)=
            (n.application_id,c.resolved_request_id,a.log_source_id,a.original_partition,a.original_offset)
        AND (resolved.notification_id,resolved.origin_epoch,resolved.source_epoch,resolved.outcome)=(n.id,n.origin_epoch,n.source_epoch,c.resolved_outcome)
    LIMIT 501
), coverage AS (
    SELECT (SELECT count(*)>=501 FROM intents) AS incomplete_intents,
        (SELECT count(*)>=501 FROM intents) OR (SELECT count(*)>=501 FROM direct_targets)
        OR (SELECT count(*)>=501 FROM reads) OR (SELECT count(*)>=501 FROM legacy) OR (SELECT count(*)>=501 FROM campaigns) AS incomplete_campaigns
), groups AS (
    SELECT 'identity_aliases' AS name,count(*)>=501 AS incomplete,jsonb_build_object('mappings',count(*)) AS counts FROM aliases
    UNION ALL SELECT 'current_installations',count(*)>=501,jsonb_build_object('owned',count(*)) FROM installations
    UNION ALL SELECT 'binding_generations',count(*)>=501,jsonb_build_object('current',count(*) FILTER (WHERE retired_at IS NULL),
        'retired',count(*) FILTER (WHERE retired_at IS NOT NULL),'current_with_held_route',count(*) FILTER (WHERE retired_at IS NULL AND destination='HELD')) FROM routes
    UNION ALL SELECT 'legacy_registrations',count(*)>=501,jsonb_build_object(
        'scoped',count(*) FILTER (WHERE NOT unassigned),'active_scoped',count(*) FILTER (WHERE NOT unassigned AND is_active),
        'unassigned_for_mapped_user',count(*) FILTER (WHERE unassigned)) FROM devices
    UNION ALL SELECT 'segment_memberships',count(*)>=501,jsonb_build_object('rows',count(*)) FROM members
    UNION ALL SELECT 'individual_targets',count(*)>=501,jsonb_build_object('rows',count(*)) FROM direct_targets
    UNION ALL SELECT 'legacy_deliveries',count(*)>=501,jsonb_build_object('rows',count(*)) FROM legacy
    UNION ALL SELECT 'campaign_reads',count(*)>=501,jsonb_build_object('rows',count(*)) FROM reads
    UNION ALL SELECT 'delivery_intents',count(*)>=501,jsonb_build_object('total',count(*),'pending',count(*) FILTER (WHERE status='PENDING'),
        'ever_charged',count(*) FILTER (WHERE attempt_count>0),'unresolved_charge',count(*) FILTER (WHERE last_attempt_at IS NOT NULL AND attempt_resolved_at IS NULL),
        'last_result_unconfirmed',count(*) FILTER (WHERE last_result_disposition='unconfirmed'),'provider_acknowledged',count(provider_accepted_at)) FROM intents
    UNION ALL SELECT 'receipt_requests',count(*)>=501,jsonb_build_object('dedup_rows',count(*)) FROM requests
    UNION ALL SELECT 'frozen_capabilities',(SELECT incomplete_intents FROM coverage),jsonb_build_object('total',count(*),
        'unexpired',count(*) FILTER (WHERE expires_unix>extract(epoch FROM statement_timestamp()))) FROM capabilities
    UNION ALL SELECT 'receipt_observations',(SELECT incomplete_intents FROM coverage) OR count(*)>=501,jsonb_build_object(
        'opened',count(*) FILTER (WHERE event='OPENED'),'delivered',count(*) FILTER (WHERE event='DELIVERED')) FROM observations
    UNION ALL SELECT 'unpublished_delivery_updates',(SELECT incomplete_intents FROM coverage) OR count(*)>=501,jsonb_build_object('events',count(*)) FROM updates
    UNION ALL SELECT 'linked_campaigns',(SELECT incomplete_campaigns FROM coverage),jsonb_build_object('total',count(*),
        'live_leases',count(*) FILTER (WHERE lease_until>statement_timestamp()),
        'expired_unreleased_leases',count(*) FILTER (WHERE lease_until<=statement_timestamp()),
        'preparation_holds',count(preparation_hold_reason)) FROM campaigns
    UNION ALL SELECT 'unpublished_campaign_sends',(SELECT incomplete_campaigns FROM coverage),jsonb_build_object('events',count(*)) FROM sends
    UNION ALL SELECT 'attributed_replay_attempts',(SELECT incomplete_campaigns FROM coverage) OR count(*)>=501,jsonb_build_object('total',count(*),
        'started',count(*) FILTER (WHERE outcome='started'),'without_position_resolution',count(*) FILTER (WHERE resolved_campaign IS NULL)) FROM replay
)
SELECT jsonb_build_object('selection','matched','as_of',statement_timestamp(),'application_status',s.app_status,'user_status',s.user_status,
    'runtime_sealed',r.sealed,'runtime_migration_hold',r.migration_hold,'dispatch_hold_new',p.hold_new,
    'held_retention_classes',(SELECT count(*) FROM public.retention_policies h WHERE h.application_id=s.app AND h.held),
    'groups',(SELECT jsonb_object_agg(name,jsonb_build_object('coverage',CASE WHEN incomplete THEN 'lower_bound' ELSE 'exact' END,'counts',counts)) FROM groups),
    'unknown_counts', jsonb_build_object('unprepared_audience_membership',NULL,'unattributed_quarantine',NULL,'unattributed_legacy_rows',NULL,
        'raw_payload_or_audit_mentions',NULL,'issued_identity_tokens',NULL,'subject_legal_holds',NULL,'historical_provider_attempts',NULL,
        'provider_retained_copies',NULL,'published_kafka_records',NULL,'consumer_projections',NULL,'archive_copies',NULL,'recoverable_backup_copies',NULL))
FROM subject s LEFT JOIN public.notification_runtime r ON r.singleton LEFT JOIN public.application_dispatch_policy p ON p.application_id=s.app
UNION ALL SELECT jsonb_build_object('selection','unresolved','as_of',statement_timestamp(),'groups',NULL)
WHERE NOT EXISTS(SELECT 1 FROM subject);
