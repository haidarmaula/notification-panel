-- Target-owner operation for the explicitly approved greenfield staging pilot.
-- Never run this file for a restored database or a production deployment.
-- It opens the runtime only; binding and PILOT grants remain separate gates.
\set ON_ERROR_STOP on
BEGIN;
SET LOCAL lock_timeout='2s';
SET LOCAL statement_timeout='15s';
DO $pilot$
DECLARE app bigint; runtime public.notification_runtime%ROWTYPE;
BEGIN
    IF current_database()<>'hello_staging' OR session_user<>'hello_staging'
       OR NOT EXISTS(SELECT FROM pg_roles WHERE rolname=session_user AND rolsuper)
       OR NOT EXISTS(SELECT FROM public.schema_migrations WHERE version=41 AND NOT dirty)
    THEN RAISE EXCEPTION 'unexpected staging owner/database/schema'; END IF;
    SELECT * INTO STRICT runtime FROM public.notification_runtime WHERE singleton FOR UPDATE;
    IF NOT runtime.sealed OR NOT runtime.migration_hold
       OR runtime.origin_epoch<>'7fb10b55-50b3-4b19-8569-a5c7f3bb49ba'::uuid
       OR runtime.source_epoch<>'cdb5a8ea-b654-4a5e-aa8d-de31eceb4a9e'::uuid
    THEN RAISE EXCEPTION 'staging runtime differs from reviewed lineage/holds'; END IF;
    SELECT id INTO STRICT app FROM public.applications
      WHERE public_id='63677e77-2661-43b8-8253-35c1134bcc60' AND status='ACTIVE' AND revision=2 FOR UPDATE;
    LOCK TABLE public.notifications,public.notification_targets,public.users,
      public.mobile_installations,public.installation_bindings,public.notification_delivery_intents,
      public.notification_send_outbox,public.notification_update_outbox IN SHARE ROW EXCLUSIVE MODE;
    IF (SELECT count(*) FROM public.applications)<>1 OR NOT public.application_ownership_finalized()
       OR (SELECT count(*) FROM public.users)<>1
       OR (SELECT count(*) FROM public.mobile_installations)<>1
       OR (SELECT count(*) FROM public.installation_bindings)<>1
       OR (SELECT count(*) FROM public.notifications)<>1
       OR (SELECT count(*) FROM public.notification_targets)<>1
       OR EXISTS(SELECT FROM public.notification_delivery_intents)
       OR EXISTS(SELECT FROM public.notification_send_outbox)
       OR EXISTS(SELECT FROM public.notification_update_outbox)
       OR EXISTS(SELECT FROM public.binding_route_grants)
       OR EXISTS(SELECT FROM public.application_dispatch_grants)
       OR NOT EXISTS(SELECT FROM public.application_dispatch_policy WHERE application_id=app
            AND hold_new AND NOT coverage_approved AND dispatch_mode='HELD' AND dispatch_revision=1)
       OR NOT EXISTS(SELECT FROM public.notifications n JOIN public.notification_targets t
            ON t.application_id=n.application_id AND t.notification_id=n.id
            JOIN public.users u ON u.application_id=t.application_id AND u.id=t.user_id
            WHERE n.application_id=app AND n.id=1 AND n.status='DRAFT' AND n.title='Hello staging'
              AND n.body='Staging push test to Haidar''s Android phone.'
              AND t.target_type='INDIVIDUAL' AND u.external_id='staging-android-smoke-haidar' AND u.status='ACTIVE')
       OR NOT EXISTS(SELECT FROM public.installation_bindings b JOIN public.mobile_installations i
            ON i.application_id=b.application_id AND i.id=b.installation_id
            JOIN public.users u ON u.application_id=b.application_id AND u.id=b.owner_user_id
            WHERE b.application_id=app AND b.retired_at IS NULL AND u.status='ACTIVE'
              AND i.owner_user_id=b.owner_user_id AND i.owner_epoch=b.owner_epoch
              AND b.platform='ANDROID' AND b.address_type='FCM_FID'
              AND b.provider_namespace='push-notifications-stagi-a6a41' AND b.provider_route_key='android-fid')
       OR (SELECT count(*) FROM public.notification_log_sources)<>2
       OR (SELECT count(*) FROM public.notification_log_sources WHERE active AND cluster_identity='eGTxoZosTWKe7DWll1Teug'
            AND ((id='eb345ad7-507e-486d-aea3-0966dc1732c3' AND topic='notification.send.requested' AND topic_incarnation='7DWU8nW_Sku7FufiVO568Q')
              OR (id='323f3a1e-e608-4466-a7ed-1f25ad268128' AND topic='notification.delivery.updated' AND topic_incarnation='Kll9XLH2R2eZUHpILxg2lg')))<>2
    THEN RAISE EXCEPTION 'staging pilot inventory differs from approved one-device proposal'; END IF;
    UPDATE public.notification_runtime SET sealed=false,migration_hold=false WHERE singleton;
END $pilot$;
COMMIT;
