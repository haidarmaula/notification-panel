-- Preserve the applied verification migration; tighten claim locking and
-- advance setup revisions when abandoned verification attempts are exhausted.
CREATE OR REPLACE FUNCTION claim_application_onboarding_verification(p_project uuid,p_owner uuid) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE app bigint; selected uuid; exhausted public.application_onboarding_operations%ROWTYPE;
BEGIN
    IF p_owner='00000000-0000-0000-0000-000000000000' OR p_owner IS NULL
       OR pg_has_role(session_user,to_regrole('hello_onboarding_verifier'),'USAGE') IS DISTINCT FROM true
       OR pg_has_role(session_user,current_user,'MEMBER')
       OR NOT EXISTS(SELECT FROM public.application_onboarding_verifier_scopes s
           JOIN public.applications a ON a.id=s.application_id WHERE s.role_id=session_user::regrole::oid AND a.public_id=p_project)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='verification scope unavailable'; END IF;
    PERFORM session_epoch FROM public.staff_session_control WHERE singleton FOR SHARE;
    PERFORM origin_epoch FROM public.notification_runtime WHERE singleton FOR SHARE;
    SELECT id INTO STRICT app FROM public.applications WHERE public_id=p_project FOR NO KEY UPDATE;
    PERFORM application_id FROM public.application_onboarding WHERE application_id=app FOR UPDATE;
    -- Obsolete work must not remain RUNNING forever after a setup or authority change.
    UPDATE public.application_onboarding_operations op SET state='SUPERSEDED',completed_at=clock_timestamp(),failure_code='VERIFICATION_SUPERSEDED',lease_owner=NULL,lease_expires_at=NULL
    WHERE op.application_id=app AND op.kind='VERIFY' AND op.state IN ('PENDING','RUNNING') AND NOT EXISTS(
        SELECT FROM public.application_onboarding o
        JOIN public.notification_runtime r ON r.singleton
        JOIN public.applications current_app ON current_app.id=o.application_id AND current_app.status IN ('PROVISIONING','ACTIVE')
        JOIN public.staff_users s ON s.id=op.actor_id AND s.is_active AND s.auth_epoch=op.auth_epoch
        JOIN public.roles role ON role.id=s.role_id AND role.name='SUPER_ADMIN'
        JOIN public.staff_sessions f ON f.id=op.session_id AND f.staff_user_id=s.id AND f.auth_epoch=s.auth_epoch
        JOIN public.staff_session_control lineage ON lineage.singleton AND lineage.session_epoch=op.session_epoch
        WHERE o.application_id=op.application_id AND o.revision=op.expected_setup_revision
          AND op.configuration_id IN (o.candidate_configuration_id,o.current_configuration_id)
          AND current_app.revision=op.expected_application_revision
          AND (op.origin_epoch,op.source_epoch)=(r.origin_epoch,r.source_epoch)
          AND f.session_epoch=lineage.session_epoch AND f.revoked_at IS NULL AND f.expires_at>clock_timestamp()
    );
    FOR exhausted IN SELECT * FROM public.application_onboarding_operations
        WHERE application_id=app AND kind='VERIFY' AND state='RUNNING' AND attempts>=3
          AND lease_expires_at<=clock_timestamp() ORDER BY id FOR UPDATE
    LOOP
        INSERT INTO public.application_provider_checks(id,application_id,configuration_id,operation_id,kind,outcome,origin_epoch,source_epoch,expires_at,failure_code)
        VALUES(gen_random_uuid(),app,exhausted.configuration_id,exhausted.id,'OAUTH','INCONCLUSIVE',exhausted.origin_epoch,exhausted.source_epoch,
            clock_timestamp()+interval '15 minutes','FCM_CHECK_INCONCLUSIVE');
        UPDATE public.application_onboarding SET revision=revision+1,updated_at=clock_timestamp() WHERE application_id=app;
        UPDATE public.application_provider_configurations SET state='STORED' WHERE id=exhausted.configuration_id;
        UPDATE public.application_onboarding_operations SET state='FAILED',completed_at=clock_timestamp(),failure_code='FCM_CHECK_INCONCLUSIVE',
            resulting_revision=(SELECT revision FROM public.application_onboarding WHERE application_id=app),
            lease_owner=NULL,lease_expires_at=NULL WHERE id=exhausted.id;
        INSERT INTO public.audit_logs(application_id,actor_user_id,action,entity_type,entity_id,after_json)
        VALUES(app,exhausted.actor_id,'APPLICATION_ONBOARDING_OPERATION_COMPLETE','application_onboarding',app,
            jsonb_build_object('operation_id',exhausted.id,'kind','VERIFY','outcome','INCONCLUSIVE','reason','lease_retry_exhausted'));
    END LOOP;
    SELECT id INTO selected FROM public.application_onboarding_operations
    WHERE application_id=app AND kind='VERIFY' AND not_before<=clock_timestamp()
      AND (state='PENDING' OR (state='RUNNING' AND lease_expires_at<=clock_timestamp()))
    ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1;
    IF selected IS NULL THEN RETURN NULL; END IF;
    UPDATE public.application_onboarding_operations SET state='RUNNING',lease_owner=p_owner,lease_token=lease_token+1,
        lease_expires_at=clock_timestamp()+interval '60 seconds',attempts=attempts+1 WHERE id=selected;
    RETURN selected;
END;
$$;
REVOKE ALL ON FUNCTION claim_application_onboarding_verification(uuid,uuid) FROM PUBLIC;

CREATE OR REPLACE FUNCTION finish_application_onboarding_verification(p_operation uuid,p_owner uuid,p_token bigint,p_outcome text,p_failure text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE op public.application_onboarding_operations%ROWTYPE; config public.application_provider_configurations%ROWTYPE;
    project public.applications%ROWTYPE; setup public.application_onboarding%ROWTYPE; origin uuid; source uuid; lineage uuid; valid_actor bigint;
BEGIN
    IF pg_has_role(session_user,to_regrole('hello_onboarding_verifier'),'USAGE') IS DISTINCT FROM true
       OR pg_has_role(session_user,current_user,'MEMBER')
       OR p_owner IS NULL OR p_owner='00000000-0000-0000-0000-000000000000' OR p_token IS NULL OR p_token<=0
       OR p_outcome NOT IN ('PASSED','FAILED','INCONCLUSIVE') OR p_outcome IS NULL
       OR (p_outcome='PASSED' AND p_failure IS NOT NULL)
       OR (p_outcome<>'PASSED' AND (p_failure IS NULL OR p_failure NOT IN ('FCM_CREDENTIAL_REJECTED','FCM_CHECK_INCONCLUSIVE','FCM_CONFIGURATION_UNAVAILABLE')))
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='verification result unavailable'; END IF;
    SELECT * INTO op FROM public.application_onboarding_operations WHERE id=p_operation;
    IF NOT FOUND OR NOT EXISTS(SELECT FROM public.application_onboarding_verifier_scopes WHERE role_id=session_user::regrole::oid AND application_id=op.application_id)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='verification scope unavailable'; END IF;
    IF op.state IN ('SUCCEEDED','FAILED') AND op.completed_by=p_owner AND op.lease_token=p_token AND EXISTS(
        SELECT FROM public.application_provider_checks c WHERE c.operation_id=op.id AND c.kind='OAUTH'
          AND c.outcome=p_outcome AND c.failure_code IS NOT DISTINCT FROM p_failure)
    THEN RETURN true; END IF;
    SELECT session_epoch INTO STRICT lineage FROM public.staff_session_control WHERE singleton FOR SHARE;
    SELECT origin_epoch,source_epoch INTO STRICT origin,source FROM public.notification_runtime WHERE singleton FOR SHARE;
    SELECT * INTO STRICT project FROM public.applications WHERE id=op.application_id FOR NO KEY UPDATE;
    SELECT s.id INTO valid_actor FROM public.staff_users s JOIN public.roles r ON r.id=s.role_id
       WHERE s.id=op.actor_id AND s.is_active AND s.auth_epoch=op.auth_epoch AND r.name='SUPER_ADMIN' FOR SHARE OF s;
    PERFORM f.id FROM public.staff_sessions f WHERE f.id=op.session_id AND f.staff_user_id=valid_actor
       AND f.auth_epoch=op.auth_epoch AND f.session_epoch=lineage AND f.revoked_at IS NULL AND f.expires_at>clock_timestamp() FOR SHARE;
    IF NOT FOUND THEN RETURN false; END IF;
    SELECT * INTO STRICT setup FROM public.application_onboarding WHERE application_id=op.application_id FOR UPDATE;
    SELECT * INTO STRICT op FROM public.application_onboarding_operations WHERE id=p_operation FOR UPDATE;
    IF op.state<>'RUNNING' OR op.kind<>'VERIFY' OR op.lease_owner IS DISTINCT FROM p_owner OR op.lease_token<>p_token
       OR op.lease_expires_at<=clock_timestamp() OR (op.origin_epoch,op.source_epoch) IS DISTINCT FROM (origin,source)
       OR setup.revision<>op.expected_setup_revision OR (op.configuration_id IS DISTINCT FROM setup.candidate_configuration_id AND op.configuration_id IS DISTINCT FROM setup.current_configuration_id)
       OR project.status NOT IN ('PROVISIONING','ACTIVE') OR project.revision<>op.expected_application_revision
    THEN RETURN false; END IF;
    SELECT * INTO STRICT config FROM public.application_provider_configurations WHERE application_id=op.application_id AND id=op.configuration_id;
    INSERT INTO public.application_provider_checks(id,application_id,configuration_id,operation_id,kind,outcome,origin_epoch,source_epoch,expires_at,failure_code)
    VALUES(gen_random_uuid(),op.application_id,op.configuration_id,op.id,'OAUTH',p_outcome,origin,source,clock_timestamp()+interval '15 minutes',p_failure);
    UPDATE public.application_provider_configurations SET state=CASE WHEN p_outcome='PASSED' THEN 'VERIFIED' WHEN p_outcome='FAILED' THEN 'REJECTED' ELSE 'STORED' END
        WHERE id=op.configuration_id;
    UPDATE public.application_onboarding SET revision=revision+1,updated_at=clock_timestamp() WHERE application_id=op.application_id;
    UPDATE public.application_onboarding_operations SET state=CASE WHEN p_outcome='PASSED' THEN 'SUCCEEDED' ELSE 'FAILED' END,
        completed_at=clock_timestamp(),completed_by=p_owner,resulting_revision=setup.revision+1,failure_code=p_failure,lease_owner=NULL,lease_expires_at=NULL WHERE id=op.id;
    INSERT INTO public.audit_logs(application_id,actor_user_id,action,entity_type,entity_id,entity_name,after_json)
    VALUES(op.application_id,op.actor_id,'APPLICATION_ONBOARDING_OPERATION_COMPLETE','application_onboarding',op.application_id,project.name,
        jsonb_build_object('operation_id',op.id,'kind','VERIFY','outcome',p_outcome,'revision',(setup.revision+1)::text));
    RETURN true;
END;
$$;
REVOKE ALL ON FUNCTION finish_application_onboarding_verification(uuid,uuid,bigint,text,text) FROM PUBLIC;
