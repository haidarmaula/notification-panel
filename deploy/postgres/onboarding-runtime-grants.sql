-- Apply as the trusted schema owner after migration 000045 or later.
-- The DBA creates the dedicated NOLOGIN group and restricted per-app LOGINs;
-- this installer never creates roles, membership, profiles or app scopes.
-- Install application_runtime_capacity_profile and executor scopes once as owner
-- only after approving the protected policy bytes and their SHA256 digest.
-- Reapply after migration 000046 to grant the read-only worker-preflight check.
\set ON_ERROR_STOP on
BEGIN;
SET LOCAL search_path=pg_catalog,pg_temp;
DO $$
DECLARE runtime_group pg_roles%ROWTYPE; principal record;
    dispatch_bootstrap oid := to_regprocedure('public.bootstrap_onboarding_held_dispatch(uuid,text,bigint,integer,integer)');
    worker_preflight oid := to_regprocedure('public.check_application_onboarding_worker_preflight(uuid,uuid,bytea,text,text,bytea,text)');
BEGIN
    IF EXISTS(SELECT FROM pg_class WHERE oid IN (
        'public.application_runtime_capacity_profile'::regclass,
        'public.application_runtime_executor_scopes'::regclass,
        'public.application_runtime_reservations'::regclass
    ) AND relowner<>current_user::regrole::oid)
    OR EXISTS(SELECT FROM pg_proc WHERE oid IN (worker_preflight,dispatch_bootstrap) AND proowner<>current_user::regrole::oid)
    THEN RAISE EXCEPTION 'trusted runtime preparation schema owner required'; END IF;
    SELECT * INTO runtime_group FROM pg_roles WHERE rolname='hello_onboarding_runtime_executor';
    IF NOT FOUND OR runtime_group.rolcanlogin OR EXISTS(SELECT FROM pg_auth_members WHERE member=runtime_group.oid)
    THEN RAISE EXCEPTION 'dedicated non-owning runtime preparation NOLOGIN group required'; END IF;
    FOR principal IN
        WITH RECURSIVE members(id) AS (
            SELECT runtime_group.oid UNION SELECT m.member FROM pg_auth_members m JOIN members parent ON m.roleid=parent.id
        ) SELECT p.* FROM pg_roles p JOIN members ON members.id=p.oid
    LOOP
        IF principal.rolsuper OR principal.rolcreatedb OR principal.rolcreaterole OR principal.rolreplication OR principal.rolbypassrls
           OR EXISTS(SELECT FROM pg_shdepend WHERE refclassid='pg_authid'::regclass AND refobjid=principal.oid AND deptype='o')
           OR EXISTS(SELECT FROM pg_auth_members WHERE member=principal.oid AND roleid<>runtime_group.oid)
           OR has_schema_privilege(principal.oid,'public','CREATE,USAGE WITH GRANT OPTION')
           OR has_database_privilege(principal.oid,current_database(),'CREATE,CONNECT WITH GRANT OPTION')
        THEN RAISE EXCEPTION 'restricted runtime preparation principal required: %',principal.rolname; END IF;
        IF EXISTS(SELECT FROM pg_class c WHERE c.relnamespace='public'::regnamespace AND c.relkind IN ('r','p','v','m')
                AND has_table_privilege(principal.oid,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN'))
           OR EXISTS(SELECT FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
                WHERE c.relnamespace='public'::regnamespace AND c.relkind IN ('r','p','v','m')
                AND has_column_privilege(principal.oid,c.oid,a.attnum,'SELECT,INSERT,UPDATE,REFERENCES'))
           OR EXISTS(SELECT FROM pg_class c WHERE c.relnamespace='public'::regnamespace
                AND CASE WHEN c.relkind='S' THEN has_sequence_privilege(principal.oid,c.oid,'SELECT,UPDATE,USAGE') ELSE false END)
           OR EXISTS(SELECT FROM pg_proc f WHERE f.pronamespace='public'::regnamespace AND f.prosecdef
                AND (has_function_privilege(principal.oid,f.oid,'EXECUTE WITH GRANT OPTION')
                    OR (has_function_privilege(principal.oid,f.oid,'EXECUTE') AND f.oid IS DISTINCT FROM worker_preflight
                      AND f.oid IS DISTINCT FROM dispatch_bootstrap AND f.oid NOT IN (
                        'public.reserve_application_onboarding_runtime(uuid,uuid,uuid,bigint,bigint,bigint,uuid,uuid)'::regprocedure,
                        'public.check_application_onboarding_runtime(uuid,uuid,bytea)'::regprocedure,
                        'public.release_application_onboarding_runtime(uuid,uuid,boolean)'::regprocedure))))
        THEN RAISE EXCEPTION 'unexpected runtime preparation privileges for %',principal.rolname; END IF;
    END LOOP;
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO hello_onboarding_runtime_executor',current_database());
    IF dispatch_bootstrap IS NOT NULL THEN
        GRANT EXECUTE ON FUNCTION public.bootstrap_onboarding_held_dispatch(uuid,text,bigint,integer,integer)
            TO hello_onboarding_runtime_executor;
    END IF;
    IF worker_preflight IS NOT NULL THEN
        GRANT EXECUTE ON FUNCTION public.check_application_onboarding_worker_preflight(uuid,uuid,bytea,text,text,bytea,text)
            TO hello_onboarding_runtime_executor;
    END IF;
END;
$$;
GRANT USAGE ON SCHEMA public TO hello_onboarding_runtime_executor;
GRANT EXECUTE ON FUNCTION public.reserve_application_onboarding_runtime(uuid,uuid,uuid,bigint,bigint,bigint,uuid,uuid),
    public.check_application_onboarding_runtime(uuid,uuid,bytea),
    public.release_application_onboarding_runtime(uuid,uuid,boolean) TO hello_onboarding_runtime_executor;
-- No table/sequence access, owner privileges, readiness acknowledgement,
-- configuration switch, enrollment activation or dispatch release authority.
COMMIT;
