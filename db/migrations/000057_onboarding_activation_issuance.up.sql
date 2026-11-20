-- A scoped activation issuer is not the legacy offline lifecycle issuer.
CREATE TABLE public.application_onboarding_activation_issuer_scopes (
    application_id bigint PRIMARY KEY REFERENCES public.application_onboarding(application_id),
    issuer_role oid NOT NULL UNIQUE,
    executor_role oid NOT NULL,
    CHECK (issuer_role<>executor_role)
);
REVOKE ALL ON public.application_onboarding_activation_issuer_scopes FROM PUBLIC;
CREATE TRIGGER protect_onboarding_activation_issuer_scope
    BEFORE INSERT OR UPDATE OR DELETE ON public.application_onboarding_activation_issuer_scopes
    FOR EACH ROW EXECUTE FUNCTION public.protect_application_worker_preflight_scope();
CREATE TRIGGER protect_onboarding_activation_issuer_scope_history
    BEFORE TRUNCATE ON public.application_onboarding_activation_issuer_scopes
    FOR EACH STATEMENT EXECUTE FUNCTION public.protect_application_worker_preflight_scope();

CREATE OR REPLACE FUNCTION public.validate_application_lifecycle_issuer() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE issuer_group oid := to_regrole('hello_application_lifecycle_issuer');
        operator_group oid := to_regrole('hello_application_lifecycle_operator');
        onboarding_group oid := to_regrole('hello_onboarding_activation_issuer');
        onboarding_issuer boolean := false;
BEGIN
    IF onboarding_group IS NOT NULL
        AND current_user::regrole::oid=(SELECT relowner FROM pg_class WHERE oid=TG_RELID)
        AND pg_has_role(session_user,onboarding_group,'USAGE')
        AND NOT pg_has_role(session_user,issuer_group,'MEMBER')
    THEN
        SELECT EXISTS (
            SELECT FROM public.application_onboarding_activation_issuer_scopes scope
            JOIN public.applications app ON app.id=scope.application_id
            WHERE app.public_id=NEW.project_id AND scope.issuer_role=NEW.issuer_role AND scope.executor_role=NEW.operator_role
        ) INTO onboarding_issuer;
    END IF;
    IF NEW.applied_at IS NOT NULL OR NEW.revoked_at IS NOT NULL OR issuer_group IS NULL OR operator_group IS NULL OR NEW.issuer_role<>session_user::regrole::oid
       OR (NOT pg_has_role(NEW.issuer_role,issuer_group,'USAGE') AND NOT onboarding_issuer) OR pg_has_role(NEW.issuer_role,operator_group,'MEMBER')
       OR NOT EXISTS (SELECT FROM pg_roles actor WHERE actor.oid=NEW.operator_role AND actor.rolcanlogin AND NOT actor.rolsuper
             AND NOT actor.rolcreaterole AND NOT actor.rolreplication AND NOT actor.rolbypassrls
             AND pg_has_role(actor.oid,operator_group,'USAGE') AND NOT pg_has_role(actor.oid,issuer_group,'MEMBER'))
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='distinct lifecycle issuer and executor required'; END IF;
    RETURN NEW;
END;
$$;

-- Keep every delegation for audit and permit an explicit replacement after loss
-- of an unconsumed grant. The app lock serializes issuance and application.
ALTER TABLE public.application_onboarding_lifecycle_delegations
    DROP CONSTRAINT application_onboarding_lifecycle_delegations_operation_id_key;
CREATE INDEX onboarding_activation_delegation_operation
    ON public.application_onboarding_lifecycle_delegations(operation_id);

CREATE FUNCTION public.issue_onboarding_first_activation(
    p_project uuid, p_operation uuid, p_grant uuid, p_executor text, p_generation bigint,
    p_ownership bytea, p_writer_fence bytea, p_cohort_fence bytea, p_retired_roles name[]
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE
    executor oid;
    op public.application_onboarding_operations%ROWTYPE;
    app public.applications%ROWTYPE;
    setup public.application_onboarding%ROWTYPE;
    runtime public.notification_runtime%ROWTYPE;
    policy public.application_dispatch_policy%ROWTYPE;
    lineage uuid;
BEGIN
    IF current_setting('transaction_isolation')<>'read committed'
        OR pg_has_role(session_user,current_user,'MEMBER')
        OR pg_has_role(session_user,to_regrole('hello_onboarding_activation_issuer'),'USAGE') IS DISTINCT FROM true
        OR pg_has_role(session_user,to_regrole('hello_application_lifecycle_issuer'),'MEMBER') IS DISTINCT FROM false
        OR pg_has_role(session_user,to_regrole('hello_application_lifecycle_operator'),'MEMBER') IS DISTINCT FROM false
        OR NOT EXISTS (SELECT FROM pg_roles WHERE oid=session_user::regrole::oid AND rolcanlogin AND NOT rolsuper
            AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT rolbypassrls)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='activation issuer unavailable'; END IF;

    SELECT oid INTO executor FROM pg_roles WHERE rolname::text COLLATE "C"=p_executor COLLATE "C"
        AND rolcanlogin AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT rolbypassrls
        AND pg_has_role(oid,to_regrole('hello_application_lifecycle_operator'),'USAGE')
        AND NOT pg_has_role(oid,to_regrole('hello_application_lifecycle_issuer'),'MEMBER');
    IF executor IS NULL OR p_generation IS NULL OR p_generation<=0 OR p_grant IS NULL
        OR p_ownership IS NULL OR octet_length(p_ownership)<>32
        OR p_writer_fence IS NULL OR octet_length(p_writer_fence)<>32
        OR p_cohort_fence IS NULL OR octet_length(p_cohort_fence)<>32
        OR p_retired_roles IS NULL OR cardinality(p_retired_roles)>64 OR array_position(p_retired_roles,NULL) IS NOT NULL
    THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid activation approval'; END IF;

    SELECT session_epoch INTO STRICT lineage FROM public.staff_session_control WHERE singleton FOR SHARE;
    SELECT * INTO STRICT runtime FROM public.notification_runtime WHERE singleton FOR SHARE;
    SELECT * INTO app FROM public.applications WHERE public_id=p_project FOR NO KEY UPDATE;
    IF NOT EXISTS (SELECT FROM public.application_onboarding_activation_issuer_scopes scope
        WHERE scope.application_id=app.id AND scope.issuer_role=session_user::regrole::oid AND scope.executor_role=executor)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='activation issuer scope unavailable'; END IF;
    SELECT * INTO op FROM public.application_onboarding_operations WHERE id=p_operation AND application_id=app.id;
    IF NOT FOUND OR op.kind<>'ACTIVATE'
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='activation operation unavailable'; END IF;

    -- A lost acknowledgement can be retried with the same immutable approval.
    IF EXISTS (
        SELECT FROM public.application_onboarding_lifecycle_delegations d
        JOIN public.application_lifecycle_grants g ON g.id=d.grant_id
        WHERE d.operation_id=op.id AND d.grant_id=p_grant AND d.generation=p_generation
            AND g.issuer_role=session_user::regrole::oid AND g.operator_role=executor
            AND g.ownership_evidence=p_ownership AND g.writer_fence_evidence=p_writer_fence
            AND g.cohort_fence_evidence=p_cohort_fence AND g.retired_writer_roles=p_retired_roles
    ) THEN RETURN p_grant; END IF;

    PERFORM 1 FROM public.staff_users s JOIN public.roles r ON r.id=s.role_id
        WHERE s.id=op.actor_id AND s.auth_epoch=op.auth_epoch AND s.is_active AND r.name='SUPER_ADMIN' FOR SHARE OF s;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='activation actor unavailable'; END IF;
    PERFORM 1 FROM public.staff_sessions f WHERE f.id=op.session_id AND f.staff_user_id=op.actor_id
        AND f.auth_epoch=op.auth_epoch AND f.session_epoch=lineage AND op.session_epoch=lineage
        AND f.revoked_at IS NULL AND f.expires_at>clock_timestamp() FOR SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='activation session unavailable'; END IF;

    SELECT * INTO STRICT setup FROM public.application_onboarding WHERE application_id=app.id FOR UPDATE;
    SELECT * INTO STRICT policy FROM public.application_dispatch_policy WHERE application_id=app.id FOR UPDATE;
    IF op.state<>'PENDING' OR op.not_before>clock_timestamp()
        OR app.status<>'PROVISIONING' OR app.revision<>op.expected_application_revision
        OR NOT setup.greenfield OR setup.current_configuration_id IS NOT NULL OR setup.runtime_generation<>0
        OR setup.revision<>op.expected_setup_revision OR setup.candidate_configuration_id IS DISTINCT FROM op.configuration_id
        OR setup.enrollment_profile_id IS NULL OR setup.enrollment_platforms='[]'
        OR policy.dispatch_mode<>'HELD' OR NOT policy.hold_new OR policy.coverage_approved OR policy.dispatch_revision<>op.expected_dispatch_revision
        OR (op.origin_epoch,op.source_epoch) IS DISTINCT FROM (runtime.origin_epoch,runtime.source_epoch)
        OR runtime.sealed OR runtime.migration_hold
        OR NOT public.onboarding_runtime_prepared(app.id,p_generation,op.configuration_id)
        OR NOT EXISTS (SELECT FROM public.application_provider_checks c
            WHERE c.application_id=app.id AND c.configuration_id=op.configuration_id AND c.kind='OAUTH' AND c.outcome='PASSED'
                AND (c.origin_epoch,c.source_epoch)=(runtime.origin_epoch,runtime.source_epoch)
                AND c.checked_at BETWEEN clock_timestamp()-interval '15 minutes' AND clock_timestamp()
                AND c.expires_at>clock_timestamp())
        OR EXISTS (SELECT FROM public.application_onboarding_lifecycle_delegations d
            JOIN public.application_lifecycle_grants g ON g.id=d.grant_id
            WHERE d.operation_id=op.id AND (g.applied_at IS NOT NULL
                OR (g.revoked_at IS NULL AND g.expires_at>clock_timestamp())))
    THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='activation approval prerequisites unavailable'; END IF;

    INSERT INTO public.application_lifecycle_grants(id,project_id,operator_role,origin_epoch,source_epoch,expected_revision,
        previous_status,destination,ownership_evidence,writer_fence_evidence,cohort_fence_evidence,retired_writer_roles,expires_at)
    VALUES(p_grant,p_project,executor,runtime.origin_epoch,runtime.source_epoch,app.revision,
        'PROVISIONING','ACTIVE',p_ownership,p_writer_fence,p_cohort_fence,p_retired_roles,clock_timestamp()+interval '10 minutes');
    INSERT INTO public.application_onboarding_lifecycle_delegations(grant_id,operation_id,enrollment_profile_id,generation)
    VALUES(p_grant,op.id,setup.enrollment_profile_id,p_generation);
    RETURN p_grant;
END;
$$;
REVOKE ALL ON FUNCTION public.issue_onboarding_first_activation(uuid,uuid,uuid,text,bigint,bytea,bytea,bytea,name[]) FROM PUBLIC;
