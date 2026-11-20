CREATE TABLE application_onboarding_dispatch_delegations (
    grant_id uuid PRIMARY KEY REFERENCES application_dispatch_grants(id),
    operation_id uuid NOT NULL UNIQUE REFERENCES application_onboarding_operations(id),
    generation bigint NOT NULL CHECK (generation>0)
);
REVOKE ALL ON application_onboarding_dispatch_delegations FROM PUBLIC;
CREATE TRIGGER protect_onboarding_dispatch_delegation BEFORE INSERT OR UPDATE OR DELETE ON application_onboarding_dispatch_delegations
    FOR EACH ROW EXECUTE FUNCTION protect_application_worker_preflight_scope();
CREATE TRIGGER protect_onboarding_dispatch_delegation_history BEFORE TRUNCATE ON application_onboarding_dispatch_delegations
    FOR EACH STATEMENT EXECUTE FUNCTION protect_application_worker_preflight_scope();

-- The HTTP server may retire a dead delegation, but cannot read or edit the
-- delegation or grant. The caller queues its replacement in the same transaction.
CREATE FUNCTION supersede_unusable_onboarding_release(p_application bigint,p_kind text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE
    op public.application_onboarding_operations%ROWTYPE;
    g public.application_dispatch_grants%ROWTYPE;
    project_name text;
BEGIN
    IF p_kind IS NULL OR p_kind NOT IN ('RELEASE_PILOT','RELEASE_FULL') THEN RETURN false; END IF;
    SELECT name INTO project_name FROM public.applications WHERE id=p_application FOR NO KEY UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    SELECT * INTO op FROM public.application_onboarding_operations
    WHERE application_id=p_application AND kind=p_kind AND state='PENDING' AND lease_owner IS NULL;
    IF NOT FOUND THEN RETURN false; END IF;
    SELECT grant_row.* INTO g FROM public.application_onboarding_dispatch_delegations d
    JOIN public.application_dispatch_grants grant_row ON grant_row.id=d.grant_id
    WHERE d.operation_id=op.id FOR UPDATE OF grant_row;
    IF NOT FOUND OR g.applied_at IS NOT NULL OR (g.revoked_at IS NULL AND g.expires_at>clock_timestamp())
    THEN RETURN false; END IF;

    UPDATE public.application_onboarding_operations SET state='SUPERSEDED',completed_at=clock_timestamp(),
        failure_code='RELEASE_GRANT_UNAVAILABLE' WHERE id=op.id AND state='PENDING' AND lease_owner IS NULL;
    IF NOT FOUND THEN RETURN false; END IF;
    INSERT INTO public.audit_logs(application_id,actor_user_id,action,entity_type,entity_id,entity_name,after_json)
    VALUES(p_application,op.actor_id,'APPLICATION_ONBOARDING_OPERATION_COMPLETE','application_onboarding',p_application,project_name,
        jsonb_build_object('operation_id',op.id,'kind',op.kind,'outcome','SUPERSEDED','failure_code','RELEASE_GRANT_UNAVAILABLE'));
    RETURN true;
END;
$$;
REVOKE ALL ON FUNCTION supersede_unusable_onboarding_release(bigint,text) FROM PUBLIC;

-- Both protected steps recheck the same exact approval, independently of the
-- HTTP precheck and the dispatch issuer's evidence on the grant.
CREATE FUNCTION lock_onboarding_release_approval(p_operation uuid,p_destination text,p_environment text) RETURNS uuid
LANGUAGE sql SECURITY INVOKER SET search_path=pg_catalog,pg_temp AS $$
    SELECT p.id FROM public.application_onboarding_operations o
    JOIN public.application_onboarding_approvals p ON (p.application_id,p.id)=(o.application_id,o.approval_id)
    JOIN public.application_runtime_capacity_profile f ON f.environment=p_environment AND f.revision=p.profile_revision
    WHERE o.id=p_operation AND p.kind=p_destination AND p.configuration_id=o.configuration_id
      AND p.cohort_id=o.cohort_id AND p.revoked_at IS NULL AND p.consumed_at IS NULL
      AND p.issued_at<=clock_timestamp() AND p.expires_at>clock_timestamp()
      AND (p.application_revision,p.dispatch_revision,p.origin_epoch,p.source_epoch)
          =(o.expected_application_revision,o.expected_dispatch_revision,o.origin_epoch,o.source_epoch)
    FOR UPDATE OF p FOR SHARE OF f
$$;
REVOKE ALL ON FUNCTION lock_onboarding_release_approval(uuid,text,text) FROM PUBLIC;

-- EXECUTE is separately granted to reviewed dispatch issuer logins, not to the issuer group.
CREATE FUNCTION bind_onboarding_release(p_operation uuid,p_grant uuid,p_generation bigint) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE
    actor oid := session_user::regrole::oid;
    g public.application_dispatch_grants%ROWTYPE;
    op public.application_onboarding_operations%ROWTYPE;
    app public.applications%ROWTYPE;
    policy public.application_dispatch_policy%ROWTYPE;
    setup public.application_onboarding%ROWTYPE;
    runtime public.notification_runtime%ROWTYPE;
    binding public.application_onboarding_dispatch_delegations%ROWTYPE;
BEGIN
    IF NOT public.application_dispatch_actor_valid(actor,true) OR p_generation IS NULL OR p_generation<=0
       OR NOT EXISTS (SELECT FROM public.application_dispatch_grants WHERE id=p_grant AND issuer_role=actor)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='release binding unavailable'; END IF;

    SELECT * INTO op FROM public.application_onboarding_operations WHERE id=p_operation;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='release binding unavailable'; END IF;
    SELECT * INTO STRICT runtime FROM public.notification_runtime WHERE singleton FOR SHARE;
    SELECT * INTO STRICT app FROM public.applications WHERE id=op.application_id FOR NO KEY UPDATE;
    SELECT * INTO STRICT policy FROM public.application_dispatch_policy WHERE application_id=app.id FOR UPDATE;
    SELECT * INTO STRICT g FROM public.application_dispatch_grants WHERE id=p_grant FOR UPDATE;
    SELECT * INTO STRICT setup FROM public.application_onboarding WHERE application_id=app.id FOR UPDATE;
    SELECT * INTO STRICT op FROM public.application_onboarding_operations WHERE id=p_operation FOR UPDATE;

    IF g.issuer_role<>actor OR g.project_id<>app.public_id OR op.application_id<>app.id
       OR g.destination NOT IN ('PILOT','FULL') OR op.kind<>'RELEASE_'||g.destination
       OR g.previous_mode<>(CASE WHEN g.destination='PILOT' THEN 'HELD' ELSE 'PILOT' END)
       OR op.cohort_id IS DISTINCT FROM g.cohort_id
       OR (op.expected_application_revision,op.expected_dispatch_revision,op.origin_epoch,op.source_epoch)
          IS DISTINCT FROM (g.application_revision,g.expected_revision,g.origin_epoch,g.source_epoch)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='release binding unavailable'; END IF;

    SELECT * INTO binding FROM public.application_onboarding_dispatch_delegations WHERE grant_id=p_grant;
    IF FOUND AND binding.operation_id=p_operation AND binding.generation=p_generation THEN
        RETURN p_operation;
    END IF;
    IF FOUND OR EXISTS (SELECT FROM public.application_onboarding_dispatch_delegations WHERE operation_id=p_operation)
    THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='release binding conflicts with existing delegation'; END IF;

    IF NOT public.application_dispatch_actor_valid(g.operator_role,false)
       OR g.applied_at IS NOT NULL OR g.revoked_at IS NOT NULL
       OR g.issued_at>clock_timestamp() OR g.expires_at<=clock_timestamp()
       OR op.state<>'PENDING' OR op.lease_owner IS NOT NULL
       OR app.status<>'ACTIVE' OR app.revision<>g.application_revision
       OR policy.dispatch_revision<>g.expected_revision OR policy.dispatch_mode<>g.previous_mode
       OR setup.revision<>op.expected_setup_revision OR setup.runtime_generation<>p_generation
       OR setup.current_configuration_id IS DISTINCT FROM op.configuration_id OR setup.cohort_id IS DISTINCT FROM op.cohort_id
       OR (runtime.origin_epoch,runtime.source_epoch) IS DISTINCT FROM (g.origin_epoch,g.source_epoch)
       OR runtime.sealed OR runtime.migration_hold
    THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='release binding prerequisites unavailable'; END IF;

    IF public.lock_onboarding_release_approval(op.id,g.destination,setup.environment) IS NULL
    THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='release approval unavailable'; END IF;

    INSERT INTO public.application_onboarding_dispatch_delegations(grant_id,operation_id,generation)
    VALUES(p_grant,p_operation,p_generation);
    RETURN p_operation;
END;
$$;
REVOKE ALL ON FUNCTION bind_onboarding_release(uuid,uuid,bigint) FROM PUBLIC;

CREATE FUNCTION require_onboarding_dispatch_completion() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF NEW.applied_at IS NULL OR NEW.destination='HELD' THEN RETURN NEW; END IF;
    IF EXISTS (SELECT FROM public.application_onboarding_dispatch_delegations WHERE grant_id=NEW.id)
       OR EXISTS (SELECT FROM public.application_onboarding s JOIN public.applications a ON a.id=s.application_id
           WHERE a.public_id=NEW.project_id AND (s.runtime_generation>0 OR s.current_configuration_id IS NOT NULL OR s.cohort_id IS NOT NULL))
    THEN
        IF NOT EXISTS (
            SELECT FROM public.application_onboarding_dispatch_delegations d
            JOIN public.application_onboarding_operations o ON o.id=d.operation_id
            JOIN public.applications a ON a.id=o.application_id
            WHERE d.grant_id=NEW.id AND a.public_id=NEW.project_id AND o.kind='RELEASE_'||NEW.destination
              AND o.state='SUCCEEDED' AND o.completed_by IS NOT NULL AND o.resulting_revision=NEW.expected_revision+1
              AND o.cohort_id=NEW.cohort_id
              AND (o.expected_application_revision,o.expected_dispatch_revision,o.origin_epoch,o.source_epoch)
                 =(NEW.application_revision,NEW.expected_revision,NEW.origin_epoch,NEW.source_epoch)
        ) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='delegated onboarding release required'; END IF;
    END IF;
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION require_onboarding_dispatch_completion() FROM PUBLIC;
CREATE TRIGGER require_onboarding_dispatch_completion BEFORE UPDATE OF applied_at ON application_dispatch_grants
    FOR EACH ROW EXECUTE FUNCTION require_onboarding_dispatch_completion();

CREATE FUNCTION apply_onboarding_release(p_operation uuid,p_grant uuid,p_instance uuid) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE
    generation bigint;
    session_lineage uuid;
    g public.application_dispatch_grants%ROWTYPE;
    app public.applications%ROWTYPE;
    op public.application_onboarding_operations%ROWTYPE;
    setup public.application_onboarding%ROWTYPE;
    policy public.application_dispatch_policy%ROWTYPE;
    runtime public.notification_runtime%ROWTYPE;
    released_revision bigint;
    approval_id uuid;
BEGIN
    SELECT d.generation INTO generation FROM public.application_onboarding_dispatch_delegations d
    JOIN public.application_dispatch_grants grant_row ON grant_row.id=d.grant_id
    WHERE d.operation_id=p_operation AND d.grant_id=p_grant AND grant_row.operator_role=session_user::regrole::oid;
    IF NOT FOUND OR p_instance IS NULL OR p_instance='00000000-0000-0000-0000-000000000000'
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='release delegation unavailable'; END IF;
    SELECT * INTO STRICT op FROM public.application_onboarding_operations WHERE id=p_operation;
    SELECT session_epoch INTO STRICT session_lineage FROM public.staff_session_control WHERE singleton FOR SHARE;
    SELECT * INTO STRICT runtime FROM public.notification_runtime WHERE singleton FOR SHARE;
    SELECT * INTO STRICT app FROM public.applications WHERE id=op.application_id FOR NO KEY UPDATE;
    SELECT * INTO STRICT policy FROM public.application_dispatch_policy WHERE application_id=app.id FOR UPDATE;
    SELECT * INTO STRICT g FROM public.application_dispatch_grants WHERE id=p_grant FOR UPDATE;
    SELECT * INTO STRICT setup FROM public.application_onboarding WHERE application_id=app.id FOR UPDATE;
    SELECT * INTO STRICT op FROM public.application_onboarding_operations WHERE id=p_operation FOR UPDATE;
    IF g.project_id<>app.public_id OR g.operator_role<>session_user::regrole::oid
       OR g.destination NOT IN ('PILOT','FULL') OR op.kind<>'RELEASE_'||g.destination
       OR op.cohort_id IS DISTINCT FROM g.cohort_id
       OR (op.expected_application_revision,op.expected_dispatch_revision,op.origin_epoch,op.source_epoch)
          IS DISTINCT FROM (g.application_revision,g.expected_revision,g.origin_epoch,g.source_epoch)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='release delegation unavailable'; END IF;

    IF g.applied_at IS NOT NULL THEN
        IF op.state<>'SUCCEEDED' OR op.completed_by IS DISTINCT FROM p_instance
           OR op.resulting_revision IS DISTINCT FROM g.expected_revision+1
        THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='release replay unavailable'; END IF;
        -- Replay is a historical result, not authority for any new transition.
        RETURN public.apply_application_dispatch(p_grant,app.public_id);
    END IF;

    PERFORM 1 FROM public.staff_users s JOIN public.roles r ON r.id=s.role_id
    WHERE s.id=op.actor_id AND s.auth_epoch=op.auth_epoch AND s.is_active AND r.name='SUPER_ADMIN' FOR SHARE OF s,r;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='release actor unavailable'; END IF;
    PERFORM 1 FROM public.staff_sessions f WHERE f.id=op.session_id AND f.staff_user_id=op.actor_id
        AND f.auth_epoch=op.auth_epoch AND f.session_epoch=session_lineage AND op.session_epoch=session_lineage
        AND f.revoked_at IS NULL AND f.expires_at>clock_timestamp() FOR SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='release session unavailable'; END IF;

    IF op.state<>'PENDING' OR op.not_before>clock_timestamp()
       OR app.status<>'ACTIVE' OR app.revision<>op.expected_application_revision
       OR policy.dispatch_revision<>op.expected_dispatch_revision
       OR policy.dispatch_mode<>(CASE WHEN g.destination='PILOT' THEN 'HELD' ELSE 'PILOT' END)
       OR setup.revision<>op.expected_setup_revision OR setup.runtime_generation<>generation
       OR setup.current_configuration_id IS DISTINCT FROM op.configuration_id OR setup.cohort_id IS DISTINCT FROM op.cohort_id
       OR (op.origin_epoch,op.source_epoch) IS DISTINCT FROM (runtime.origin_epoch,runtime.source_epoch)
       OR runtime.sealed OR runtime.migration_hold
    THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='release prerequisites unavailable'; END IF;

    approval_id:=public.lock_onboarding_release_approval(op.id,g.destination,setup.environment);
    IF approval_id IS NULL THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='release approval unavailable'; END IF;
    UPDATE public.application_onboarding_approvals SET consumed_at=clock_timestamp()
    WHERE id=approval_id AND expires_at>clock_timestamp();
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='release approval unavailable'; END IF;

    UPDATE public.application_onboarding_operations SET state='SUCCEEDED',completed_at=clock_timestamp(),
        completed_by=p_instance,resulting_revision=g.expected_revision+1 WHERE id=op.id;
    released_revision:=public.apply_application_dispatch(p_grant,app.public_id);
    INSERT INTO public.audit_logs(application_id,actor_user_id,action,entity_type,entity_id,entity_name,after_json)
    VALUES(app.id,op.actor_id,'APPLICATION_ONBOARDING_OPERATION_COMPLETE','application_onboarding',app.id,app.name,
        jsonb_build_object('operation_id',op.id,'kind',op.kind,'outcome','SUCCEEDED','dispatch_revision',released_revision::text));
    RETURN released_revision;
END;
$$;
REVOKE ALL ON FUNCTION apply_onboarding_release(uuid,uuid,uuid) FROM PUBLIC;
