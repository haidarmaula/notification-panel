-- Run with psql as the trusted object owner. All three dedicated NOLOGIN
-- groups must already exist. Login membership and credentials are provisioned
-- separately by the DBA. No privilege is granted to PUBLIC or unrelated roles.
\set ON_ERROR_STOP on
BEGIN;
SET LOCAL search_path=pg_catalog,pg_temp;
CREATE TEMP TABLE daemon_grant_spec (
    profiles text NOT NULL, relation_name name NOT NULL, permission text NOT NULL,
    columns text
) ON COMMIT DROP;
INSERT INTO daemon_grant_spec VALUES
-- s = scheduler, a = admission, d = delivery. NULL grants the table privilege.
('sad','notification_runtime','SELECT',NULL),
('sad','notification_runtime','UPDATE','singleton'),
('sad','notification_log_sources','SELECT',NULL),
('sad','notification_log_sources','UPDATE','id'),
('sad','applications','UPDATE','id'),
('sa','applications','SELECT',NULL),
('d','applications','SELECT','id,public_id,status'),
('sad','application_dispatch_policy','SELECT',NULL),
('sad','application_dispatch_policy','UPDATE','application_id'),
('sa','notifications','SELECT','id,application_id,status,queued_at,scheduled_at,deadline_at,origin_epoch,source_epoch,preparation_next_attempt_at'),
('d','notifications','SELECT','id,application_id,status,queued_at,scheduled_at,deadline_at,origin_epoch,source_epoch,lease_owner,lease_generation,lease_until,title,body,payload'),
('sa','notifications','UPDATE','status,completed_at,lifecycle_reason,preparation_hold_reason,preparation_next_attempt_at'),
('s','notifications','UPDATE','queued_at'),
('d','notifications','UPDATE','status,completed_at,lifecycle_reason,lease_owner,lease_generation,lease_until'),
('sad','notification_preparations','SELECT',NULL),
('sa','notification_preparations','INSERT','application_id,notification_id,origin_epoch,source_epoch,resolved_user_count,eligible_user_count,eligible_device_count'),
('sa','notification_targets','SELECT',NULL),
('sa','segment_members','SELECT','application_id,segment_id,user_id'),
('sad','users','SELECT','application_id,id,status'),
('d','users','UPDATE','id'),
('sa','device_tokens','SELECT','user_id,is_active'),
('sa','installation_bindings','SELECT','application_id,id,installation_id,owner_user_id,owner_epoch,retired_at'),
('sa','mobile_installations','SELECT','application_id,id,owner_user_id,owner_epoch'),
('sa','notification_delivery_intents','INSERT','application_id,notification_id,installation_id,binding_id,owner_user_id,owner_epoch'),
('sa','notification_delivery_intents','SELECT','id,notification_id,status'),
('d','notification_delivery_intents','SELECT',NULL),
('d','notification_delivery_intents','UPDATE','status,attempt_id,attempt_count,claim_generation,last_attempt_at,attempt_resolved_at,next_attempt_at,provider_message_id,provider_accepted_at,failed_at,terminal_reason,last_result_disposition,last_result_reason,last_result_fingerprint,delivery_version'),
('d','mobile_installations','SELECT','id,public_id,application_id,owner_user_id,owner_epoch,version'),
('d','mobile_installations','UPDATE','version,last_seen_at'),
('d','installation_bindings','SELECT',NULL),
('d','installation_bindings','UPDATE','retired_at'),
('d','notification_receipt_metadata','SELECT',NULL),
('d','notification_receipt_metadata','INSERT','application_id,delivery_id,version,key_id,environment,expires_unix'),
('d','notification_receipt_metadata','UPDATE','version'),
('s','notification_send_outbox','SELECT',NULL),
('s','notification_send_outbox','INSERT','application_id,notification_id,requested_at,scheduled_for,deadline_at,origin_epoch,source_epoch'),
('s','notification_send_outbox','UPDATE','publish_attempts,next_publish_at,last_error'),
('s','notification_send_outbox','DELETE',NULL),
('s','notification_send_outbox_id_seq','USAGE',NULL),
('ad','notification_send_outbox','SELECT','requested_at,next_publish_at'),
('d','notification_update_outbox','SELECT',NULL),
('d','notification_update_outbox','INSERT','event_id,application_id,notification_id,delivery_id,delivery_version,origin_epoch,source_epoch,payload,occurred_at'),
('d','notification_update_outbox','UPDATE','attempts,next_attempt_at'),
('d','notification_update_outbox','DELETE',NULL),
('sa','notification_update_outbox','SELECT','occurred_at,next_attempt_at'),
('a','notification_send_dead_letters','SELECT',NULL),
('a','notification_send_dead_letters','INSERT','log_source_id,original_partition,original_offset,original_key,original_payload,reason,attempts'),
('sd','notification_send_dead_letters','SELECT','log_source_id,original_partition,original_offset,created_at'),
('s','notification_send_dead_letters','UPDATE','log_source_id'),
('s','notification_replay_controls','SELECT',NULL),
('s','notification_replay_controls','UPDATE','log_source_id'),
('ad','notification_replay_controls','SELECT','log_source_id,original_partition,original_offset,resolved_outcome'),
('s','notification_replay_requests','SELECT',NULL),
('s','notification_replay_requests','UPDATE','id'),
('s','notification_replay_grants','SELECT','id,application_id,staff_user_id'),
('s','notification_replay_grants','UPDATE','id'),
('s','notification_replay_attempts','SELECT',NULL),
('s','staff_users','SELECT','id'),
('s','staff_users','UPDATE','id'),
('s','audit_logs','INSERT','actor_user_id,application_id,action,entity_type,entity_name,entity_id,before_json,after_json,ip_address,user_agent'),
('s','audit_logs_id_seq','USAGE',NULL);

SELECT to_regclass('public.current_binding_routes') IS NOT NULL AS has_binding_routes \gset
\if :has_binding_routes
\ir daemon-grants-m37.sql
\endif
SELECT to_regprocedure('public.application_lifecycle_managed()') IS NOT NULL AS has_dispatch_release \gset
\if :has_dispatch_release
\ir daemon-grants-m39.sql
\endif

CREATE TEMP TABLE daemon_profiles(profile text PRIMARY KEY, role_name name UNIQUE NOT NULL) ON COMMIT DROP;
INSERT INTO daemon_profiles VALUES ('s','hello_scheduler'),('a','hello_admission'),('d','hello_delivery');
CREATE TEMP TABLE daemon_routines(profile text, signature regprocedure) ON COMMIT DROP;
INSERT INTO daemon_routines VALUES
('d','public.hold_delivery_dispatch(bigint)'),
('s','public.abandon_expired_replay_attempt(bigint,uuid,uuid,integer,bigint,uuid)'),
('s','public.clear_finished_replay_claim(bigint,uuid,uuid,integer,bigint,uuid)');

DO $$
DECLARE r record; g record; item record; col_list text;
BEGIN
    FOR r IN SELECT p.*,a.oid AS role_id,a.rolsuper,a.rolcreaterole,a.rolcreatedb,a.rolcanlogin,a.rolreplication,a.rolbypassrls
             FROM daemon_profiles p LEFT JOIN pg_roles a ON a.rolname=p.role_name LOOP
        IF r.role_id IS NULL OR r.rolsuper OR r.rolcreaterole OR r.rolcreatedb OR r.rolcanlogin OR r.rolreplication OR r.rolbypassrls
           OR EXISTS(SELECT FROM pg_auth_members WHERE member=r.role_id)
           OR EXISTS(SELECT FROM pg_shdepend WHERE refclassid='pg_authid'::regclass AND refobjid=r.role_id AND deptype='o')
           OR has_schema_privilege(r.role_id,'public','CREATE')
           OR has_schema_privilege(r.role_id,'public','USAGE WITH GRANT OPTION')
           OR has_database_privilege(r.role_id,current_database(),'CONNECT WITH GRANT OPTION')
           OR has_database_privilege(r.role_id,current_database(),'CREATE') THEN
            RAISE EXCEPTION 'expected a provisioned non-owning NOLOGIN role without inherited authority: %',r.role_name;
        END IF;
        -- Refuse unexpected relation or column grants rather than taking away
        -- permissions on an operator's existing role. PUBLIC grants also apply.
        FOR item IN
            SELECT c.relname,x.privilege_type,x.is_grantable,NULL::name AS col FROM pg_class c
            CROSS JOIN LATERAL aclexplode(c.relacl) x WHERE c.relnamespace='public'::regnamespace AND x.grantee IN (0,r.role_id)
            UNION ALL
            SELECT c.relname,x.privilege_type,x.is_grantable,a.attname FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid
            CROSS JOIN LATERAL aclexplode(a.attacl) x WHERE c.relnamespace='public'::regnamespace AND x.grantee IN (0,r.role_id)
        LOOP
            IF item.is_grantable OR NOT EXISTS (
                SELECT FROM daemon_grant_spec spec WHERE strpos(spec.profiles,r.profile)>0 AND spec.relation_name=item.relname
                AND spec.permission=item.privilege_type AND (spec.columns IS NULL OR item.col=ANY(string_to_array(spec.columns,',')))
            ) THEN
                RAISE EXCEPTION 'unexpected effective % on % for %',item.privilege_type,item.relname,r.role_name;
            END IF;
        END LOOP;
        IF EXISTS (SELECT FROM pg_proc f WHERE f.pronamespace='public'::regnamespace AND f.prosecdef
                   AND has_function_privilege(r.role_id,f.oid,'EXECUTE') AND (has_function_privilege(r.role_id,f.oid,'EXECUTE WITH GRANT OPTION') OR NOT EXISTS (
                     SELECT FROM daemon_routines x WHERE x.profile=r.profile AND x.signature=f.oid))) THEN
            RAISE EXCEPTION 'unexpected security-definer routine grant for %',r.role_name;
        END IF;
        EXECUTE format('GRANT CONNECT ON DATABASE %I TO %I',current_database(),r.role_name);
        EXECUTE format('GRANT USAGE ON SCHEMA public TO %I',r.role_name);
        FOR g IN SELECT * FROM daemon_grant_spec WHERE strpos(profiles,r.profile)>0 LOOP
            SELECT string_agg(quote_ident(c),',') INTO col_list FROM unnest(string_to_array(g.columns,',')) AS c;
            EXECUTE format('GRANT %s%s ON %s public.%I TO %I',g.permission,
                coalesce(' ('||col_list||')',''),CASE WHEN g.permission='USAGE' THEN 'SEQUENCE' ELSE 'TABLE' END,g.relation_name,r.role_name);
        END LOOP;
        FOR g IN SELECT signature FROM daemon_routines WHERE profile=r.profile LOOP
            EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO %I',g.signature,r.role_name);
        END LOOP;
    END LOOP;
END;
$$;
COMMIT;
