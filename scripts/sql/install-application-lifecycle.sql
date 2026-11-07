-- Run offline as the trusted public-schema owner after ownership finalization.
-- The migration deliberately leaves the original registry gate in place.
DO $install$
BEGIN
    PERFORM set_config('lock_timeout','2s',true);
    LOCK TABLE public.applications IN ACCESS EXCLUSIVE MODE;
    IF NOT public.application_ownership_finalized() THEN RAISE EXCEPTION 'ownership finalization required'; END IF;
    IF EXISTS (SELECT FROM pg_catalog.pg_trigger WHERE tgrelid='public.applications'::regclass
        AND tgname='guard_application_lifecycle' AND tgfoid='public.guard_application_lifecycle()'::regprocedure AND tgenabled IN ('O','A')) THEN
        RETURN;
    END IF;
    IF EXISTS (SELECT FROM public.applications WHERE status<>'PROVISIONING') THEN RAISE EXCEPTION 'existing lifecycle state requires reconciliation'; END IF;
    ALTER TABLE public.applications DROP CONSTRAINT applications_status_check;
    ALTER TABLE public.applications ADD CONSTRAINT application_lifecycle_status CHECK (status IN ('PROVISIONING','ACTIVE','HELD','RETIRED'));
    CREATE TRIGGER guard_application_lifecycle BEFORE INSERT OR UPDATE ON public.applications
        FOR EACH ROW EXECUTE FUNCTION public.guard_application_lifecycle();
END;
$install$;
