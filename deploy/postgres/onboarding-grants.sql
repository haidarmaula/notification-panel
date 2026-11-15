-- Apply as the trusted schema owner after migrations through 000044.
-- Dedicated runtime groups and their restricted LOGIN members are DBA-owned.
\set ON_ERROR_STOP on
BEGIN;
SET LOCAL search_path=pg_catalog,pg_temp;
CREATE TEMP TABLE onboarding_grant_spec(role_name name,relation_name name,columns text) ON COMMIT DROP;
INSERT INTO onboarding_grant_spec VALUES
('hello_onboarding_provisioner','applications','id,public_id,status'),
('hello_onboarding_provisioner','application_onboarding','application_id,revision'),
('hello_onboarding_provisioner','application_onboarding_ingestions',NULL),
('hello_onboarding_provisioner','staff_users','id,is_active,auth_epoch,role_id'),
('hello_onboarding_provisioner','roles','id,name'),
('hello_onboarding_provisioner','staff_sessions','id,staff_user_id,auth_epoch,session_epoch,revoked_at,expires_at'),
('hello_onboarding_provisioner','staff_session_control','singleton,session_epoch'),
('hello_onboarding_verifier','application_onboarding',NULL),
('hello_onboarding_verifier','application_provider_configurations',NULL),
('hello_onboarding_verifier','application_onboarding_operations',NULL),
('hello_onboarding_verifier','applications','id,public_id,status,revision'),
('hello_onboarding_verifier','application_dispatch_policy','application_id,dispatch_mode,dispatch_revision');
DO $$
DECLARE n name; r pg_roles%ROWTYPE; principal record; entry record; cols text;
BEGIN
    FOREACH n IN ARRAY ARRAY['hello_onboarding_provisioner','hello_onboarding_verifier'] LOOP
        SELECT * INTO r FROM pg_roles WHERE rolname=n;
        IF NOT FOUND OR r.rolcanlogin OR EXISTS(SELECT FROM pg_auth_members WHERE member=r.oid)
        THEN RAISE EXCEPTION 'dedicated non-owning onboarding runtime group required: %',n; END IF;
        FOR principal IN
            WITH RECURSIVE members(id) AS (
                SELECT r.oid UNION SELECT m.member FROM pg_auth_members m JOIN members parent ON m.roleid=parent.id
            ) SELECT p.* FROM pg_roles p JOIN members ON members.id=p.oid
        LOOP
            IF principal.rolsuper OR principal.rolcreatedb OR principal.rolcreaterole OR principal.rolreplication OR principal.rolbypassrls
               OR EXISTS(SELECT FROM pg_shdepend WHERE refclassid='pg_authid'::regclass AND refobjid=principal.oid AND deptype='o')
               OR EXISTS(SELECT FROM pg_auth_members WHERE member=principal.oid AND roleid<>r.oid)
               OR has_schema_privilege(principal.oid,'public','CREATE')
               OR has_database_privilege(principal.oid,current_database(),'CREATE,CONNECT WITH GRANT OPTION')
               OR has_schema_privilege(principal.oid,'public','USAGE WITH GRANT OPTION')
            THEN RAISE EXCEPTION 'restricted onboarding principal required: %',principal.rolname; END IF;
            IF EXISTS(SELECT FROM pg_class c WHERE c.relnamespace='public'::regnamespace AND c.relkind IN ('r','p','v','m')
                 AND (has_table_privilege(principal.oid,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN')
                   OR has_table_privilege(principal.oid,c.oid,'SELECT WITH GRANT OPTION')
                   OR (has_table_privilege(principal.oid,c.oid,'SELECT') AND NOT EXISTS(
                       SELECT FROM onboarding_grant_spec g WHERE g.role_name=n AND g.relation_name=c.relname AND g.columns IS NULL))))
               OR EXISTS(SELECT FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
                 WHERE c.relnamespace='public'::regnamespace AND c.relkind IN ('r','p','v','m') AND
                   (has_column_privilege(principal.oid,c.oid,a.attnum,'INSERT,UPDATE,REFERENCES,SELECT WITH GRANT OPTION')
                    OR (has_column_privilege(principal.oid,c.oid,a.attnum,'SELECT') AND NOT EXISTS(
                        SELECT FROM onboarding_grant_spec g WHERE g.role_name=n AND g.relation_name=c.relname
                          AND (g.columns IS NULL OR a.attname=ANY(string_to_array(g.columns,',')))))))
               OR EXISTS(SELECT FROM pg_class c WHERE c.relnamespace='public'::regnamespace
                    AND CASE WHEN c.relkind='S' THEN has_sequence_privilege(principal.oid,c.oid,'SELECT,UPDATE,USAGE') ELSE false END)
               OR EXISTS(SELECT FROM pg_proc f WHERE f.pronamespace='public'::regnamespace AND f.prosecdef
                    AND (has_function_privilege(principal.oid,f.oid,'EXECUTE WITH GRANT OPTION') OR (has_function_privilege(principal.oid,f.oid,'EXECUTE')
                    AND NOT (n='hello_onboarding_verifier' AND f.oid IN
                      ('public.claim_application_onboarding_verification(uuid,uuid)'::regprocedure,
                       'public.finish_application_onboarding_verification(uuid,uuid,bigint,text,text)'::regprocedure)))))
            THEN RAISE EXCEPTION 'unexpected onboarding privileges for %',principal.rolname; END IF;
        END LOOP;
        EXECUTE format('GRANT CONNECT ON DATABASE %I TO %I',current_database(),n);
        EXECUTE format('GRANT USAGE ON SCHEMA public TO %I',n);
        FOR entry IN SELECT * FROM onboarding_grant_spec WHERE role_name=n LOOP
            IF entry.columns IS NULL THEN
                EXECUTE format('GRANT SELECT ON public.%I TO %I',entry.relation_name,n);
            ELSE
                SELECT string_agg(format('%I',v),',') INTO cols FROM unnest(string_to_array(entry.columns,',')) v;
                EXECUTE format('GRANT SELECT (%s) ON public.%I TO %I',cols,entry.relation_name,n);
            END IF;
        END LOOP;
    END LOOP;
END $$;
GRANT EXECUTE ON FUNCTION public.claim_application_onboarding_verification(uuid,uuid),
    public.finish_application_onboarding_verification(uuid,uuid,bigint,text,text) TO hello_onboarding_verifier;
-- No mutation rights on evidence, current pointers, scope registration or any
-- lifecycle/dispatch table. Verification functions enforce exact app/role scope.
COMMIT;
