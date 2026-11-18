CREATE TABLE application_onboarding_lifecycle_delegations (
    grant_id uuid PRIMARY KEY REFERENCES application_lifecycle_grants(id),
    operation_id uuid NOT NULL UNIQUE REFERENCES application_onboarding_operations(id),
    enrollment_profile_id uuid NOT NULL,
    generation bigint NOT NULL CHECK (generation>0)
);
REVOKE ALL ON application_onboarding_lifecycle_delegations FROM PUBLIC;
CREATE TRIGGER protect_onboarding_lifecycle_delegation BEFORE INSERT OR UPDATE OR DELETE ON application_onboarding_lifecycle_delegations
    FOR EACH ROW EXECUTE FUNCTION protect_application_worker_preflight_scope();
CREATE TRIGGER protect_onboarding_lifecycle_delegation_history BEFORE TRUNCATE ON application_onboarding_lifecycle_delegations
    FOR EACH STATEMENT EXECUTE FUNCTION protect_application_worker_preflight_scope();

CREATE FUNCTION require_onboarding_lifecycle_completion() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF NEW.applied_at IS NOT NULL AND EXISTS (
        SELECT FROM public.application_onboarding_lifecycle_delegations d
        JOIN public.application_onboarding_operations o ON o.id=d.operation_id
        WHERE d.grant_id=NEW.id AND (o.state<>'SUCCEEDED' OR o.kind<>'ACTIVATE'
            OR o.completed_by IS NULL OR o.resulting_revision IS NULL))
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='delegated activation required'; END IF;
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION require_onboarding_lifecycle_completion() FROM PUBLIC;
CREATE TRIGGER require_onboarding_lifecycle_completion BEFORE UPDATE OF applied_at ON application_lifecycle_grants
    FOR EACH ROW EXECUTE FUNCTION require_onboarding_lifecycle_completion();

CREATE FUNCTION apply_onboarding_first_activation(p_operation uuid,p_grant uuid,p_instance uuid) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE
    delegation public.application_onboarding_lifecycle_delegations%ROWTYPE;
    grant_row public.application_lifecycle_grants%ROWTYPE;
    op public.application_onboarding_operations%ROWTYPE;
    app public.applications%ROWTYPE;
    setup public.application_onboarding%ROWTYPE;
    runtime public.notification_runtime%ROWTYPE;
    policy public.application_dispatch_policy%ROWTYPE;
    session_lineage uuid;
BEGIN
    SELECT d.* INTO delegation FROM public.application_onboarding_lifecycle_delegations d
    JOIN public.application_lifecycle_grants g ON g.id=d.grant_id
    WHERE d.grant_id=p_grant AND d.operation_id=p_operation AND g.operator_role=session_user::regrole::oid;
    IF NOT FOUND OR p_instance IS NULL OR p_instance='00000000-0000-0000-0000-000000000000'
        OR NOT EXISTS (SELECT FROM pg_roles WHERE oid=session_user::regrole::oid AND rolcanlogin
            AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='activation delegation unavailable'; END IF;
    SELECT * INTO STRICT op FROM public.application_onboarding_operations WHERE id=p_operation;
    SELECT session_epoch INTO STRICT session_lineage FROM public.staff_session_control WHERE singleton FOR SHARE;
    SELECT * INTO STRICT runtime FROM public.notification_runtime WHERE singleton FOR SHARE;
    SELECT * INTO STRICT app FROM public.applications WHERE id=op.application_id FOR NO KEY UPDATE;
    SELECT * INTO STRICT grant_row FROM public.application_lifecycle_grants WHERE id=p_grant FOR UPDATE;
    IF grant_row.operator_role<>session_user::regrole::oid OR grant_row.project_id<>app.public_id
        OR grant_row.previous_status<>'PROVISIONING' OR grant_row.destination<>'ACTIVE'
        OR grant_row.expected_revision<>op.expected_application_revision OR grant_row.revoked_at IS NOT NULL
        OR grant_row.expires_at>grant_row.issued_at+interval '15 minutes'
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='activation delegation unavailable'; END IF;
    SELECT * INTO STRICT op FROM public.application_onboarding_operations WHERE id=p_operation;
    IF op.kind='ACTIVATE' AND op.state='SUCCEEDED' AND grant_row.applied_at IS NOT NULL
    THEN RETURN op.resulting_revision; END IF;

    PERFORM 1 FROM public.staff_users s JOIN public.roles r ON r.id=s.role_id
    WHERE s.id=op.actor_id AND s.auth_epoch=op.auth_epoch AND s.is_active AND r.name='SUPER_ADMIN' FOR SHARE OF s;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='activation actor unavailable'; END IF;
    PERFORM 1 FROM public.staff_sessions f WHERE f.id=op.session_id AND f.staff_user_id=op.actor_id
        AND f.auth_epoch=op.auth_epoch AND f.session_epoch=session_lineage AND op.session_epoch=session_lineage
        AND f.revoked_at IS NULL AND f.expires_at>clock_timestamp() FOR SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='activation session unavailable'; END IF;

    SELECT * INTO STRICT setup FROM public.application_onboarding WHERE application_id=app.id FOR UPDATE;
    SELECT * INTO STRICT policy FROM public.application_dispatch_policy WHERE application_id=app.id FOR UPDATE;
    SELECT * INTO STRICT op FROM public.application_onboarding_operations WHERE id=p_operation FOR UPDATE;
    IF op.kind<>'ACTIVATE' OR op.state<>'PENDING' OR op.not_before>clock_timestamp()
        OR app.status<>'PROVISIONING' OR app.revision<>op.expected_application_revision
        OR NOT setup.greenfield OR setup.current_configuration_id IS NOT NULL OR setup.runtime_generation<>0
        OR setup.revision<>op.expected_setup_revision OR setup.candidate_configuration_id IS DISTINCT FROM op.configuration_id
        OR setup.enrollment_profile_id IS DISTINCT FROM delegation.enrollment_profile_id OR setup.enrollment_platforms='[]'
        OR policy.dispatch_mode<>'HELD' OR NOT policy.hold_new OR policy.coverage_approved OR policy.dispatch_revision<>op.expected_dispatch_revision
        OR (op.origin_epoch,op.source_epoch) IS DISTINCT FROM (runtime.origin_epoch,runtime.source_epoch)
        OR runtime.sealed OR runtime.migration_hold
        OR NOT public.onboarding_runtime_prepared(app.id,delegation.generation,op.configuration_id)
        OR NOT EXISTS (SELECT FROM public.application_provider_configurations c
            WHERE c.application_id=app.id AND c.id=op.configuration_id AND c.state='VERIFIED')
        OR NOT EXISTS (SELECT FROM public.application_provider_checks c
            WHERE c.application_id=app.id AND c.configuration_id=op.configuration_id AND c.kind='OAUTH' AND c.outcome='PASSED'
                AND (c.origin_epoch,c.source_epoch)=(runtime.origin_epoch,runtime.source_epoch)
                AND c.checked_at BETWEEN clock_timestamp()-interval '15 minutes' AND clock_timestamp()
                AND c.expires_at>clock_timestamp())
    THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='activation prerequisites unavailable'; END IF;

    UPDATE public.application_onboarding_operations SET state='SUCCEEDED',completed_at=clock_timestamp(),
        completed_by=p_instance,resulting_revision=setup.revision+1 WHERE id=op.id;
    PERFORM public.apply_application_lifecycle(p_grant,app.public_id);
    UPDATE public.application_onboarding SET current_configuration_id=op.configuration_id,
        candidate_configuration_id=NULL,runtime_generation=delegation.generation,revision=revision+1,updated_at=clock_timestamp()
        WHERE application_id=app.id;
    INSERT INTO public.audit_logs(application_id,actor_user_id,action,entity_type,entity_id,entity_name,after_json)
    VALUES(app.id,op.actor_id,'APPLICATION_ONBOARDING_OPERATION_COMPLETE','application_onboarding',app.id,app.name,
        jsonb_build_object('operation_id',op.id,'kind','ACTIVATE','outcome','SUCCEEDED','revision',(setup.revision+1)::text));
    RETURN setup.revision+1;
END;
$$;
REVOKE ALL ON FUNCTION apply_onboarding_first_activation(uuid,uuid,uuid) FROM PUBLIC;
