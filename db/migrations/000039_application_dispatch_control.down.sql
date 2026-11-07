DO $$ BEGIN
    IF EXISTS(SELECT FROM public.notification_preparations WHERE prepared_pilot_cohort IS NOT NULL) OR EXISTS(SELECT FROM public.application_dispatch_grants) OR EXISTS(SELECT FROM public.application_dispatch_policy WHERE dispatch_mode<>'HELD')
       OR (public.application_lifecycle_managed() AND EXISTS(SELECT FROM public.application_dispatch_policy WHERE NOT hold_new OR coverage_approved))
    THEN RAISE EXCEPTION 'cannot discard dispatch approval or active policy'; END IF;
END $$;
DROP FUNCTION apply_application_dispatch(uuid,uuid);
DROP FUNCTION revoke_application_dispatch(uuid,uuid);
DROP TRIGGER guard_application_dispatch_delete ON application_dispatch_policy;
DROP TRIGGER guard_application_dispatch_truncate ON application_dispatch_policy;
DROP TRIGGER guard_application_dispatch_policy ON application_dispatch_policy;
DROP FUNCTION guard_application_dispatch_policy();
DROP FUNCTION application_dispatch_actor_valid(oid,boolean);
DROP TABLE application_dispatch_grants;
DROP FUNCTION protect_application_dispatch_grant();
ALTER TABLE application_dispatch_policy DROP COLUMN dispatch_revision,DROP COLUMN dispatch_mode,DROP COLUMN dispatch_cohort,
    DROP COLUMN dispatch_origin_epoch,DROP COLUMN dispatch_source_epoch;
ALTER TABLE notification_preparations DROP COLUMN prepared_pilot_cohort;
DROP FUNCTION application_lifecycle_managed();
