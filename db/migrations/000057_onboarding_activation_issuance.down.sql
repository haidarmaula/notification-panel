DO $migration$
BEGIN
    LOCK TABLE public.application_onboarding_lifecycle_delegations,
        public.application_onboarding_activation_issuer_scopes IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT FROM public.application_onboarding_activation_issuer_scopes)
    THEN RAISE EXCEPTION 'activation issuer scope history requires M57'; END IF;
    IF EXISTS (SELECT FROM public.application_onboarding_lifecycle_delegations GROUP BY operation_id HAVING count(*)>1)
    THEN RAISE EXCEPTION 'activation approval history requires M57'; END IF;
    DROP FUNCTION public.issue_onboarding_first_activation(uuid,uuid,uuid,text,bigint,bytea,bytea,bytea,name[]);
    DROP INDEX public.onboarding_activation_delegation_operation;
    ALTER TABLE public.application_onboarding_lifecycle_delegations ADD UNIQUE(operation_id);

    CREATE OR REPLACE FUNCTION public.validate_application_lifecycle_issuer() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE issuer_group oid := to_regrole('hello_application_lifecycle_issuer');
        operator_group oid := to_regrole('hello_application_lifecycle_operator');
BEGIN
    IF NEW.applied_at IS NOT NULL OR NEW.revoked_at IS NOT NULL OR issuer_group IS NULL OR operator_group IS NULL OR NEW.issuer_role<>session_user::regrole::oid
       OR NOT pg_has_role(NEW.issuer_role,issuer_group,'USAGE') OR pg_has_role(NEW.issuer_role,operator_group,'MEMBER')
       OR NOT EXISTS (SELECT FROM pg_roles actor WHERE actor.oid=NEW.operator_role AND actor.rolcanlogin AND NOT actor.rolsuper
             AND NOT actor.rolcreaterole AND NOT actor.rolreplication AND NOT actor.rolbypassrls
             AND pg_has_role(actor.oid,operator_group,'USAGE') AND NOT pg_has_role(actor.oid,issuer_group,'MEMBER'))
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='distinct lifecycle issuer and executor required'; END IF;
    RETURN NEW;
END;
$$;
    DROP TABLE public.application_onboarding_activation_issuer_scopes;
END;
$migration$;
