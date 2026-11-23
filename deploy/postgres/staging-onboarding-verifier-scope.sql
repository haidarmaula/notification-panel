-- Staging-only row isolation for the OAuth verifier role.
\set ON_ERROR_STOP on
BEGIN;
SET LOCAL search_path=pg_catalog,pg_temp;
SET LOCAL lock_timeout='3s';

CREATE SCHEMA IF NOT EXISTS hello_staging_onboarding;
GRANT USAGE ON SCHEMA hello_staging_onboarding TO PUBLIC;

CREATE OR REPLACE FUNCTION hello_staging_onboarding.owns_verification_scope(p_application bigint)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
    SELECT EXISTS(
        SELECT FROM public.application_onboarding_verifier_scopes
        WHERE role_id=session_user::regrole::oid AND application_id=p_application
    );
$$;

DO $$
DECLARE item record;
BEGIN
    IF current_database() <> 'hello_staging' THEN
        RAISE EXCEPTION 'this grant profile is for the staging database';
    END IF;
    FOR item IN SELECT * FROM (VALUES
        ('application_onboarding','application_id'),
        ('applications','id'),
        ('application_dispatch_policy','application_id'),
        ('application_provider_configurations','application_id'),
        ('application_onboarding_operations','application_id')
    ) AS spec(relation_name,application_column)
    LOOP
        IF EXISTS(SELECT FROM pg_policies WHERE schemaname='public' AND tablename=item.relation_name
                  AND policyname<>'hello_staging_onboarding_scope') THEN
            RAISE EXCEPTION 'unexpected row policy for %',item.relation_name;
        END IF;
        EXECUTE format('ALTER TABLE public.%I ENABLE ROW LEVEL SECURITY',item.relation_name);
        EXECUTE format('DROP POLICY IF EXISTS hello_staging_onboarding_scope ON public.%I',item.relation_name);
        EXECUTE format(
            'CREATE POLICY hello_staging_onboarding_scope ON public.%I USING
              (NOT COALESCE(pg_has_role(current_user,to_regrole(''hello_onboarding_verifier''),''USAGE''),false)
               OR hello_staging_onboarding.owns_verification_scope(%I))',
            item.relation_name,item.application_column);
    END LOOP;
END
$$;
COMMIT;
