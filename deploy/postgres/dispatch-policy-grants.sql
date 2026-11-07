-- Apply as the trusted object owner after independently provisioning the two
-- NOLOGIN groups. Role membership and deployment credentials stay with the DBA.
\set ON_ERROR_STOP on
BEGIN;
SET LOCAL search_path=pg_catalog,pg_temp;
DO $profile$
DECLARE
    group_name name;
    r pg_roles%ROWTYPE;
    allowed_columns text[]:=ARRAY['id','project_id','operator_role','origin_epoch','source_epoch','application_revision','expected_revision',
        'previous_mode','destination','cohort_id','reason','legacy_evidence','consent_evidence','coverage_evidence','quota_evidence','expires_at'];
    allowed_function oid;
BEGIN
    FOREACH group_name IN ARRAY ARRAY['hello_dispatch_issuer','hello_dispatch_operator'] LOOP
        SELECT * INTO r FROM pg_roles WHERE rolname=group_name;
        IF NOT FOUND OR r.rolcanlogin OR r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls
           OR EXISTS(SELECT FROM pg_auth_members WHERE member=r.oid)
           OR EXISTS(SELECT FROM pg_shdepend WHERE refclassid='pg_authid'::regclass AND refobjid=r.oid AND deptype='o')
           OR has_schema_privilege(r.oid,'public','CREATE') OR has_database_privilege(r.oid,current_database(),'CREATE')
        THEN RAISE EXCEPTION 'expected a dedicated non-owning NOLOGIN group: %',group_name; END IF;
        allowed_function:=CASE group_name WHEN 'hello_dispatch_issuer' THEN
            'public.revoke_application_dispatch(uuid,uuid)'::regprocedure ELSE 'public.apply_application_dispatch(uuid,uuid)'::regprocedure END;
        IF EXISTS(SELECT FROM pg_class c WHERE c.relnamespace='public'::regnamespace AND c.relkind IN ('r','p','v','m')
              AND has_table_privilege(r.oid,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN'))
           OR EXISTS(SELECT FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
              CROSS JOIN unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES']) p(privilege)
              WHERE c.relnamespace='public'::regnamespace AND c.relkind IN ('r','p','v','m')
                AND has_column_privilege(r.oid,c.oid,a.attnum,p.privilege)
                AND NOT (group_name='hello_dispatch_issuer' AND c.oid='public.application_dispatch_grants'::regclass
                    AND p.privilege='INSERT' AND a.attname=ANY(allowed_columns)))
           OR EXISTS(SELECT FROM pg_proc f WHERE f.pronamespace='public'::regnamespace AND f.prosecdef AND f.oid<>allowed_function
                AND has_function_privilege(r.oid,f.oid,'EXECUTE'))
        THEN RAISE EXCEPTION 'unexpected dispatch profile privileges for %',group_name; END IF;
        EXECUTE format('GRANT CONNECT ON DATABASE %I TO %I',current_database(),group_name);
        EXECUTE format('GRANT USAGE ON SCHEMA public TO %I',group_name);
        EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO %I',allowed_function::regprocedure,group_name);
    END LOOP;
END;
$profile$;
GRANT INSERT (id,project_id,operator_role,origin_epoch,source_epoch,application_revision,expected_revision,previous_mode,destination,cohort_id,reason,
    legacy_evidence,consent_evidence,coverage_evidence,quota_evidence,expires_at)
    ON public.application_dispatch_grants TO hello_dispatch_issuer;
COMMIT;
