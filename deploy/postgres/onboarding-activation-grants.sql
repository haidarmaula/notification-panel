-- The deployment owner creates the dedicated NOLOGIN group, assigns restricted
-- LOGINs and installs exact app/issuer/executor scope rows before enabling it.
\set ON_ERROR_STOP on
BEGIN;
SET LOCAL search_path=pg_catalog,pg_temp;
DO $$
DECLARE issuer record;
BEGIN
    FOR issuer IN
        WITH RECURSIVE members(id) AS (
            SELECT to_regrole('hello_onboarding_activation_issuer')::oid
            UNION
            SELECT m.member FROM pg_auth_members m JOIN members parent ON m.roleid=parent.id
        )
        SELECT role.* FROM pg_roles role JOIN members ON role.oid=members.id
    LOOP
        IF issuer.rolsuper OR issuer.rolcreatedb OR issuer.rolcreaterole OR issuer.rolreplication OR issuer.rolbypassrls
            OR (issuer.rolname='hello_onboarding_activation_issuer' AND issuer.rolcanlogin)
            OR EXISTS (SELECT FROM pg_auth_members WHERE member=issuer.oid
                AND roleid<>to_regrole('hello_onboarding_activation_issuer'))
            OR EXISTS (SELECT FROM pg_shdepend WHERE refclassid='pg_authid'::regclass AND refobjid=issuer.oid AND deptype='o')
            OR has_any_column_privilege(issuer.oid,'public.application_lifecycle_grants','INSERT')
            OR has_schema_privilege(issuer.oid,'public','CREATE')
        THEN RAISE EXCEPTION 'restricted onboarding activation issuer required'; END IF;
    END LOOP;
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO hello_onboarding_activation_issuer',current_database());
END;
$$;
GRANT USAGE ON SCHEMA public TO hello_onboarding_activation_issuer;
GRANT EXECUTE ON FUNCTION public.issue_onboarding_first_activation(uuid,uuid,uuid,text,bigint,bytea,bytea,bytea,name[]),
    public.revoke_application_lifecycle(uuid,uuid) TO hello_onboarding_activation_issuer;
COMMIT;
