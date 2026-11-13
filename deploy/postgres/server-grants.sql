-- Requires migrations through 000041. Apply as the trusted object owner after
-- the DBA provisions hello_server as a dedicated NOLOGIN role. Create a separate non-superuser LOGIN and grant it
-- membership in hello_server outside this template; credentials stay with the DBA.
\set ON_ERROR_STOP on
BEGIN;
SET LOCAL search_path=pg_catalog,pg_temp;
CREATE TEMP TABLE server_grant_spec (
    relation_name name NOT NULL,
    permission text NOT NULL,
    columns text
) ON COMMIT DROP;
INSERT INTO server_grant_spec VALUES
('staff_users','SELECT',NULL),
('staff_users','INSERT','role_id,name,email,password_hash'),
('staff_users','UPDATE','role_id,name,email,password_hash,is_active,updated_at'),
('staff_users','DELETE',NULL),
('roles','SELECT',NULL),
('roles','INSERT','name,description'),
('roles','UPDATE','name,description,updated_at'),
('roles','DELETE',NULL),
('staff_sessions','SELECT','session_epoch,id,staff_user_id,auth_epoch,refresh_digest,generation,expires_at,rotated_at,revoked_at,revocation_reason'),
('staff_sessions','INSERT','id,staff_user_id,auth_epoch,refresh_digest,expires_at,session_epoch'),
('staff_sessions','UPDATE','refresh_digest,generation,rotated_at,revoked_at,revocation_reason'),
('staff_session_control','SELECT','singleton,session_epoch'),
('staff_session_control','UPDATE','singleton'),
('staff_administration_guard','SELECT','singleton'),
('staff_administration_guard','UPDATE','singleton'),
('audit_logs','SELECT','application_id,id,actor_user_id,action,entity_type,entity_id,created_at'),
('audit_logs','INSERT','actor_user_id,application_id,action,entity_type,entity_name,entity_id,before_json,after_json,ip_address,user_agent'),
('applications','SELECT',NULL),
('applications','INSERT','key,name'),
('applications','UPDATE','name,revision,updated_at'),
('application_staff_grants','SELECT',NULL),
('application_staff_grants','INSERT','application_id,staff_id'),
('application_staff_grants','UPDATE','is_active,revision,updated_at'),
('users','SELECT',NULL),
('users','UPDATE','id'),
('device_tokens','SELECT','application_id,id,user_id,platform,is_active,last_seen_at,created_at,updated_at'),
('segments','SELECT',NULL),
('segments','INSERT','application_id,name,description,created_by'),
('segments','UPDATE','name,description,updated_at'),
('segments','DELETE',NULL),
('segment_members','SELECT',NULL),
('segment_members','INSERT','application_id,segment_id,user_id'),
('segment_members','DELETE',NULL),
('templates','SELECT',NULL),
('templates','INSERT','application_id,name,title_template,body_template,is_active,created_by'),
('templates','UPDATE','name,title_template,body_template,is_active,updated_at'),
('templates','DELETE',NULL),
('notifications','SELECT',NULL),
('notifications','INSERT','application_id,template_id,template_name,title,body,status,created_by'),
('notifications','UPDATE','title,body,template_id,template_name,updated_at,status,queued_at,deadline_at,origin_epoch,source_epoch,scheduled_at'),
('notifications','DELETE',NULL),
('notification_targets','SELECT',NULL),
('notification_targets','INSERT','application_id,notification_id,target_type,segment_id,user_id'),
('notification_targets','DELETE',NULL),
('notification_deliveries','SELECT','notification_id'),
('notification_reads','SELECT','id,application_id,notification_id,user_id,read_at'),
('notification_reads','INSERT','application_id,notification_id,user_id,read_at'),
('notification_delivery_intents','SELECT',NULL),
('notification_delivery_intents','UPDATE','observed_opened_at,observed_delivered_at,status,delivery_version'),
('notification_preparations','SELECT','application_id,notification_id,origin_epoch,source_epoch,resolved_user_count,eligible_user_count,eligible_device_count,prepared_pilot_cohort'),
('upload_batches','SELECT','id,application_id,total_rows,valid_rows,invalid_rows,created_at'),
('upload_batch_rows','SELECT','id,application_id,batch_id,is_valid,created_at'),
('notification_runtime','SELECT','singleton,origin_epoch,source_epoch,sealed,migration_hold'),
('notification_runtime','UPDATE','singleton'),
('application_dispatch_policy','SELECT','application_id,hold_new,dispatch_revision,dispatch_mode,dispatch_cohort,dispatch_origin_epoch,dispatch_source_epoch,coverage_approved,push_ttl_seconds,key_ttl_seconds'),
('application_dispatch_policy','UPDATE','application_id'),
('notification_send_keys','SELECT',NULL),
('notification_send_keys','INSERT','application_id,staff_user_id,key_source,key,notification_id,queued_at,deadline_at,expires_at'),
('notification_send_keys','UPDATE','key'),
('notification_send_keys','DELETE',NULL),
('retention_policies','SELECT','application_id,record_class'),
('notification_send_outbox','INSERT','application_id,notification_id,requested_at,scheduled_for,deadline_at,origin_epoch,source_epoch'),
('mobile_identities','SELECT',NULL),
('mobile_identities','UPDATE','user_id'),
('mobile_origin_uses','SELECT','application_public_id,issuer,nonce_digest'),
('mobile_origin_uses','INSERT','application_public_id,issuer,nonce_digest,expires_at'),
('mobile_installations','SELECT',NULL),
('mobile_installations','INSERT','application_id,owner_user_id,proof_verifier'),
('mobile_installations','UPDATE','owner_user_id,owner_epoch,version,last_seen_at'),
('installation_bindings','SELECT',NULL),
('installation_bindings','INSERT','id,application_id,installation_id,owner_user_id,owner_epoch,platform,provider_namespace,provider_route_key,address_type,address_fingerprint,address_ciphertext,encryption_key_version'),
('installation_bindings','UPDATE','retired_at'),
('notification_receipt_requests','SELECT','application_id,user_id,client_event_id,fingerprint'),
('notification_receipt_requests','INSERT','application_id,user_id,client_event_id,delivery_id,fingerprint,expires_unix'),
('notification_observations','SELECT','delivery_id,event'),
('notification_observations','INSERT','application_id,delivery_id,event,received_at,observed_at'),
('notification_receipt_metadata','SELECT',NULL),
('notification_receipt_metadata','INSERT','application_id,delivery_id,version,key_id,environment,expires_unix'),
('notification_receipt_metadata','UPDATE','delivery_id'),
('current_binding_routes','SELECT','application_id,binding_id,revision,destination,origin_epoch,source_epoch,resume_from,cohort_id'),
('notification_update_outbox','INSERT','event_id,application_id,notification_id,delivery_id,delivery_version,origin_epoch,source_epoch,payload,occurred_at');
CREATE TEMP TABLE server_sequence_spec(sequence_name name PRIMARY KEY) ON COMMIT DROP;
INSERT INTO server_sequence_spec VALUES
('staff_users_id_seq'),
('roles_id_seq'),
('audit_logs_id_seq'),
('applications_id_seq'),
('segments_id_seq'),
('segment_members_id_seq'),
('templates_id_seq'),
('notifications_id_seq'),
('notification_targets_id_seq'),
('notification_reads_id_seq'),
('notification_send_outbox_id_seq'),
('mobile_installations_id_seq'),
('installation_bindings_id_seq');
DO $$
DECLARE
    server_role name := 'hello_server';
    r pg_roles%ROWTYPE;
    g record;
    item record;
    member_role record;
    col_list text;
BEGIN
    SELECT * INTO r FROM pg_roles WHERE rolname=server_role;
    IF NOT FOUND OR r.rolcanlogin OR r.rolsuper OR r.rolcreatedb OR r.rolcreaterole
       OR r.rolreplication OR r.rolbypassrls
       OR EXISTS(SELECT FROM pg_auth_members WHERE member=r.oid)
       OR EXISTS(SELECT FROM pg_shdepend WHERE refclassid='pg_authid'::regclass AND refobjid=r.oid AND deptype='o')
       OR has_schema_privilege(r.oid,'public','CREATE')
       OR has_schema_privilege(r.oid,'public','USAGE WITH GRANT OPTION')
       OR has_database_privilege(r.oid,current_database(),'CONNECT WITH GRANT OPTION')
       OR has_database_privilege(r.oid,current_database(),'CREATE') THEN
        RAISE EXCEPTION 'expected a dedicated non-owning NOLOGIN role without inherited authority: %',server_role;
    END IF;
    FOR member_role IN
        SELECT u.oid AS role_oid,u.rolname,u.rolcanlogin,u.rolsuper,u.rolcreatedb,u.rolcreaterole,
            u.rolreplication,u.rolbypassrls,m.admin_option,m.inherit_option
        FROM pg_auth_members m JOIN pg_roles u ON u.oid=m.member WHERE m.roleid=r.oid
    LOOP
        IF NOT member_role.rolcanlogin OR member_role.rolsuper OR member_role.rolcreatedb OR member_role.rolcreaterole
           OR member_role.rolreplication OR member_role.rolbypassrls OR member_role.admin_option OR NOT member_role.inherit_option
           OR EXISTS(SELECT FROM pg_auth_members x WHERE x.member=member_role.role_oid AND x.roleid<>r.oid)
           OR EXISTS(SELECT FROM pg_shdepend WHERE refclassid='pg_authid'::regclass AND refobjid=member_role.role_oid AND deptype='o')
           OR has_schema_privilege(member_role.role_oid,'public','CREATE')
           OR has_schema_privilege(member_role.role_oid,'public','USAGE WITH GRANT OPTION')
           OR has_database_privilege(member_role.role_oid,current_database(),'CREATE')
           OR has_database_privilege(member_role.role_oid,current_database(),'CONNECT WITH GRANT OPTION') THEN
            RAISE EXCEPTION 'expected a dedicated non-superuser LOGIN with only inherited server authority: %',member_role.rolname;
        END IF;
    END LOOP;
    FOR item IN
        SELECT c.relname,x.privilege_type,x.is_grantable,NULL::name AS col
        FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) x
        WHERE c.relnamespace='public'::regnamespace AND c.relkind IN ('r','p','v','m')
          AND (x.grantee IN (0,r.oid) OR x.grantee IN (SELECT member FROM pg_auth_members WHERE roleid=r.oid))
        UNION ALL
        SELECT c.relname,x.privilege_type,x.is_grantable,a.attname
        FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid
        CROSS JOIN LATERAL aclexplode(a.attacl) x
        WHERE c.relnamespace='public'::regnamespace AND c.relkind IN ('r','p','v','m')
          AND a.attnum>0 AND NOT a.attisdropped
          AND (x.grantee IN (0,r.oid) OR x.grantee IN (SELECT member FROM pg_auth_members WHERE roleid=r.oid))
    LOOP
        IF item.is_grantable OR NOT EXISTS (
            SELECT FROM server_grant_spec s
            WHERE s.relation_name=item.relname AND s.permission=item.privilege_type
              AND (s.columns IS NULL OR item.col=ANY(string_to_array(s.columns,',')))
        ) THEN
            RAISE EXCEPTION 'unexpected effective % on % for hello_server',item.privilege_type,item.relname;
        END IF;
    END LOOP;
    FOR item IN
        SELECT c.relname,x.privilege_type,x.is_grantable
        FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) x
        WHERE c.relnamespace='public'::regnamespace AND c.relkind='S'
          AND (x.grantee IN (0,r.oid) OR x.grantee IN (SELECT member FROM pg_auth_members WHERE roleid=r.oid))
    LOOP
        IF item.is_grantable OR item.privilege_type<>'USAGE'
           OR NOT EXISTS(SELECT FROM server_sequence_spec s WHERE s.sequence_name=item.relname) THEN
            RAISE EXCEPTION 'unexpected effective % on sequence % for hello_server',item.privilege_type,item.relname;
        END IF;
    END LOOP;
    IF EXISTS(
        SELECT FROM pg_proc f WHERE f.pronamespace='public'::regnamespace AND f.prosecdef
          AND (has_function_privilege(r.oid,f.oid,'EXECUTE') OR EXISTS(
              SELECT FROM pg_auth_members m WHERE m.roleid=r.oid AND has_function_privilege(m.member,f.oid,'EXECUTE')))
    ) THEN
        RAISE EXCEPTION 'unexpected executable security-definer routine for server role';
    END IF;
    FOR g IN SELECT * FROM server_grant_spec LOOP
        SELECT string_agg(quote_ident(c),',') INTO col_list
        FROM unnest(string_to_array(g.columns,',')) AS c;
        EXECUTE format('GRANT %s%s ON TABLE public.%I TO %I',g.permission,
            CASE WHEN g.columns IS NULL THEN '' ELSE ' ('||col_list||')' END,g.relation_name,server_role);
    END LOOP;
    FOR g IN SELECT sequence_name FROM server_sequence_spec LOOP
        EXECUTE format('GRANT USAGE ON SEQUENCE public.%I TO %I',g.sequence_name,server_role);
    END LOOP;
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO %I',current_database(),server_role);
    EXECUTE format('GRANT USAGE ON SCHEMA public TO %I',server_role);
END;
$$;
COMMIT;
