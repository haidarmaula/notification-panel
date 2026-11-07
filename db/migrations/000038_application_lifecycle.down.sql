DO $$ BEGIN
    IF EXISTS (SELECT FROM public.application_lifecycle_grants) OR EXISTS (SELECT FROM public.applications WHERE status<>'PROVISIONING') THEN
        RAISE EXCEPTION 'cannot discard application lifecycle evidence';
    END IF;
END $$;
DROP TRIGGER IF EXISTS guard_application_lifecycle ON applications;
ALTER TABLE applications DROP CONSTRAINT IF EXISTS application_lifecycle_status;
DO $$ BEGIN
    IF NOT EXISTS (SELECT FROM pg_constraint WHERE conrelid='public.applications'::regclass AND conname='applications_status_check') THEN
        ALTER TABLE public.applications ADD CONSTRAINT applications_status_check CHECK(status='PROVISIONING');
    END IF;
END $$;
DROP FUNCTION apply_application_lifecycle(uuid,uuid);
DROP FUNCTION revoke_application_lifecycle(uuid,uuid);
DROP FUNCTION guard_application_lifecycle();
DROP FUNCTION application_ownership_finalized();
DROP TABLE application_lifecycle_grants;
DROP FUNCTION protect_application_lifecycle_grant();
DROP FUNCTION validate_application_lifecycle_issuer();
