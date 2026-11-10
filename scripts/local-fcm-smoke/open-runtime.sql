-- Local-only owner operation for the synthetic FCM smoke database.
-- This is not a restore procedure or a production hold-release mechanism.
\set ON_ERROR_STOP on
BEGIN;
SET LOCAL search_path = pg_catalog, public, pg_temp;

DO $guard$
DECLARE
    active_sources integer;
    expected_sources integer;
BEGIN
    IF current_database() <> 'hello_fcm_smoke'
       OR NOT EXISTS (SELECT FROM pg_roles WHERE rolname = session_user AND rolsuper)
       OR (SELECT count(*) FROM public.applications) <> 1
       OR (SELECT count(*) FROM public.applications WHERE status = 'ACTIVE') <> 1
       OR NOT public.application_ownership_finalized()
       OR (SELECT count(*) FROM public.users) <> 1
       OR (SELECT count(*) FROM public.mobile_installations) <> 1
       OR (SELECT count(*) FROM public.installation_bindings) <> 1
       OR (SELECT count(*) FROM public.notifications) <> 0
       OR (SELECT count(*) FROM public.notification_delivery_intents) <> 0
       OR (SELECT count(*) FROM public.notification_send_outbox) <> 0
       OR (SELECT count(*) FROM public.notification_update_outbox) <> 0
       OR (SELECT count(*) FROM public.application_dispatch_grants) <> 0
       OR (SELECT count(*) FROM public.binding_route_grants) <> 0
       OR EXISTS (SELECT FROM public.current_binding_routes)
       OR NOT EXISTS (
           SELECT FROM public.application_dispatch_policy p
           JOIN public.applications a ON a.id = p.application_id
           WHERE p.hold_new AND NOT p.coverage_approved AND p.dispatch_mode = 'HELD'
       )
    THEN
        RAISE EXCEPTION 'local FCM smoke preflight failed';
    END IF;

    SELECT count(*) FILTER (WHERE active),
           count(*) FILTER (WHERE active AND topic IN ('hello.fcm.smoke.send', 'hello.fcm.smoke.updated'))
      INTO active_sources, expected_sources
      FROM public.notification_log_sources;
    IF active_sources <> 2 OR expected_sources <> 2 THEN
        RAISE EXCEPTION 'local FCM smoke source preflight failed';
    END IF;

    UPDATE public.notification_runtime
       SET sealed = false, migration_hold = false
     WHERE singleton AND sealed AND migration_hold;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'local FCM smoke runtime state changed';
    END IF;
END;
$guard$;

COMMIT;

SELECT current_database(), sealed, migration_hold
  FROM public.notification_runtime WHERE singleton;
