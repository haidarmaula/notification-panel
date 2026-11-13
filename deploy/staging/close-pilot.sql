-- Restore the original staging holds after the controlled pilot, including failure.
-- Stop/join processing containers as part of the same operator procedure.
\set ON_ERROR_STOP on
BEGIN;
SET LOCAL lock_timeout='2s';
DO $hold$
BEGIN
    IF current_database()<>'hello_staging' OR session_user<>'hello_staging'
       OR NOT EXISTS(SELECT FROM public.notification_runtime WHERE singleton
            AND origin_epoch='7fb10b55-50b3-4b19-8569-a5c7f3bb49ba'
            AND source_epoch='cdb5a8ea-b654-4a5e-aa8d-de31eceb4a9e')
    THEN RAISE EXCEPTION 'target lineage changed; stop processing and reconcile'; END IF;
END $hold$;
UPDATE public.application_dispatch_policy SET hold_new=true
 WHERE application_id=(SELECT id FROM public.applications WHERE public_id='63677e77-2661-43b8-8253-35c1134bcc60')
   AND (NOT hold_new OR coverage_approved OR dispatch_mode<>'HELD');
UPDATE public.notification_runtime SET sealed=true,migration_hold=true WHERE singleton;
COMMIT;
