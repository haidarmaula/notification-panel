-- Independently provisioned logins can issue and apply only the exact routes
-- that belong to an already approved and dry-run-verified onboarding cohort.
CREATE TABLE public.application_onboarding_route_reviewers (
    project_id uuid NOT NULL REFERENCES public.applications(public_id),
    environment text NOT NULL CHECK(environment IN ('staging','production')),
    issuer_role oid NOT NULL,
    executor_role oid NOT NULL,
    PRIMARY KEY(project_id,environment),
    CHECK(issuer_role<>executor_role)
);
REVOKE ALL ON public.application_onboarding_route_reviewers FROM PUBLIC;

CREATE FUNCTION public.onboarding_route_actor_valid(p_actor oid) RETURNS boolean
LANGUAGE sql STABLE SET search_path=pg_catalog,pg_temp AS $$
    SELECT EXISTS (SELECT FROM pg_roles r WHERE r.oid=p_actor AND r.rolcanlogin
      AND NOT (r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls)
      AND NOT pg_has_role(r.oid,(SELECT relowner FROM pg_class WHERE oid='public.binding_route_grants'::regclass),'MEMBER')
      AND NOT has_any_column_privilege(r.oid,'public.binding_route_grants','INSERT,UPDATE,REFERENCES')
      AND NOT has_table_privilege(r.oid,'public.binding_route_grants','DELETE,TRUNCATE,TRIGGER,MAINTAIN')
      AND NOT has_function_privilege(r.oid,'public.apply_binding_route(uuid,uuid)','EXECUTE'))
$$;
REVOKE ALL ON FUNCTION public.onboarding_route_actor_valid(oid) FROM PUBLIC;

-- Serialize with release, hold and enrollment by locking runtime then app.
-- SQL writes in both callers recheck this snapshot within the same transaction.
CREATE FUNCTION public.lock_onboarding_pilot_binding(p_project uuid,p_environment text,p_cohort uuid)
RETURNS TABLE(application_id bigint,binding_id bigint,approval_id uuid,origin_epoch uuid,source_epoch uuid)
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
DECLARE app bigint;
BEGIN
    PERFORM 1 FROM public.notification_runtime WHERE singleton FOR SHARE;
    SELECT a.id INTO STRICT app FROM public.applications a WHERE a.public_id=p_project FOR NO KEY UPDATE;
    RETURN QUERY
    SELECT a.id,b.id,p.id,r.origin_epoch,r.source_epoch
    FROM public.applications a
    JOIN public.application_onboarding s ON s.application_id=a.id
    JOIN public.application_dispatch_policy d ON d.application_id=a.id
    JOIN public.application_onboarding_operations op ON (op.application_id,op.id)=(a.id,s.cohort_id)
    JOIN public.application_onboarding_approvals p ON (p.application_id,p.id)=(a.id,op.approval_id)
    JOIN public.application_onboarding_approved_targets m ON (m.application_id,m.approval_id)=(a.id,p.id)
    JOIN public.application_onboarding_cohort_targets t ON (t.operation_id,t.installation_id,t.binding_id)=(op.id,m.installation_id,m.binding_id)
    JOIN public.mobile_installations i ON (i.application_id,i.id)=(a.id,t.installation_id)
    JOIN public.installation_bindings b ON (b.application_id,b.id,b.installation_id)=(a.id,t.binding_id,i.id)
    JOIN public.users u ON (u.application_id,u.id)=(a.id,i.owner_user_id)
    CROSS JOIN public.notification_runtime r
    WHERE a.id=app AND a.status='ACTIVE' AND s.environment=p_environment AND s.runtime_generation>0
      AND d.dispatch_mode='HELD' AND d.hold_new AND op.id=p_cohort AND op.kind='TEST_COHORT' AND op.state='SUCCEEDED'
      AND s.current_configuration_id=op.configuration_id AND p.configuration_id=op.configuration_id AND p.kind='TEST_COHORT'
      AND (a.revision,d.dispatch_revision)=(op.expected_application_revision,op.expected_dispatch_revision)
      AND r.singleton AND NOT r.sealed AND NOT r.migration_hold AND (op.origin_epoch,op.source_epoch)=(r.origin_epoch,r.source_epoch)
      AND p.consumed_at IS NOT NULL AND p.revoked_at IS NULL AND p.expires_at>clock_timestamp() AND m.revoked_at IS NULL
      AND i.public_id=ANY(op.installation_ids) AND b.retired_at IS NULL AND u.status='ACTIVE'
      AND s.enrollment_platforms @> jsonb_build_array(jsonb_build_object('platform',b.platform))
      AND (b.owner_user_id,b.owner_epoch)=(i.owner_user_id,i.owner_epoch)
      AND t.outcome='PASSED' AND t.checked_at>clock_timestamp()-interval '15 minutes'
    FOR SHARE OF p,m,i,b,u;
END;
$$;
REVOKE ALL ON FUNCTION public.lock_onboarding_pilot_binding(uuid,text,uuid) FROM PUBLIC;

CREATE FUNCTION public.approve_onboarding_pilot_route(p_project uuid,p_environment text,p_cohort uuid,
    p_installation uuid,p_grant uuid,p_expires timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE
    scope public.application_onboarding_route_reviewers%ROWTYPE;
    existing public.binding_route_grants%ROWTYPE;
    target record;
    current_revision bigint;
BEGIN
    SELECT * INTO scope FROM public.application_onboarding_route_reviewers
        WHERE project_id=p_project AND environment=p_environment AND issuer_role=session_user::regrole::oid FOR SHARE;
    IF NOT FOUND OR NOT public.onboarding_route_actor_valid(scope.issuer_role)
       OR NOT public.onboarding_route_actor_valid(scope.executor_role)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='pilot route reviewer unavailable'; END IF;

    SELECT eligible.* INTO target FROM public.lock_onboarding_pilot_binding(p_project,p_environment,p_cohort) eligible
    JOIN public.installation_bindings b ON (b.application_id,b.id)=(eligible.application_id,eligible.binding_id)
    JOIN public.mobile_installations i ON (i.application_id,i.id)=(b.application_id,b.installation_id)
    WHERE i.public_id=p_installation;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='pilot route review prerequisites unavailable'; END IF;

    SELECT * INTO existing FROM public.binding_route_grants WHERE id=p_grant;
    IF FOUND THEN
        IF (existing.application_id,existing.binding_id,existing.issuer_role,existing.operator_role,existing.cohort_id,
            existing.evidence_id,existing.destination,existing.origin_epoch,existing.source_epoch,existing.expires_at)
            IS DISTINCT FROM (target.application_id,target.binding_id,scope.issuer_role,scope.executor_role,p_cohort,
                target.approval_id,'FCM_ACTIVE'::text,target.origin_epoch,target.source_epoch,p_expires)
        THEN RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='pilot route review replay conflict'; END IF;
        RETURN p_grant;
    END IF;
    IF NOT coalesce(p_expires>clock_timestamp() AND p_expires<=clock_timestamp()+interval '15 minutes',false)
    THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid pilot route grant expiry'; END IF;

    SELECT coalesce(max(revision),0) INTO current_revision FROM public.current_binding_routes
    WHERE (application_id,binding_id)=(target.application_id,target.binding_id);
    INSERT INTO public.binding_route_grants(id,application_id,binding_id,operator_role,origin_epoch,source_epoch,
        expected_revision,destination,cohort_id,evidence_id,reason,expires_at)
    VALUES(p_grant,target.application_id,target.binding_id,scope.executor_role,target.origin_epoch,target.source_epoch,
        current_revision,'FCM_ACTIVE',p_cohort,target.approval_id,'pilot',p_expires);
    RETURN p_grant;
END;
$$;
REVOKE ALL ON FUNCTION public.approve_onboarding_pilot_route(uuid,text,uuid,uuid,uuid,timestamptz) FROM PUBLIC;

CREATE FUNCTION public.apply_onboarding_pilot_route(p_project uuid,p_environment text,p_grant uuid)
RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE
    scope public.application_onboarding_route_reviewers%ROWTYPE;
    grant_row public.binding_route_grants%ROWTYPE;
BEGIN
    SELECT * INTO scope FROM public.application_onboarding_route_reviewers
        WHERE project_id=p_project AND environment=p_environment AND executor_role=session_user::regrole::oid FOR SHARE;
    IF NOT FOUND OR NOT public.onboarding_route_actor_valid(scope.issuer_role)
       OR NOT public.onboarding_route_actor_valid(scope.executor_role)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='pilot route executor unavailable'; END IF;

    SELECT g.* INTO grant_row FROM public.binding_route_grants g JOIN public.applications a ON a.id=g.application_id
    WHERE g.id=p_grant AND a.public_id=p_project AND (g.issuer_role,g.operator_role)=(scope.issuer_role,scope.executor_role)
        AND g.destination='FCM_ACTIVE' AND g.reason='pilot';
    IF NOT FOUND OR NOT EXISTS (
        SELECT FROM public.lock_onboarding_pilot_binding(p_project,p_environment,grant_row.cohort_id) t
        WHERE (t.application_id,t.binding_id,t.approval_id,t.origin_epoch,t.source_epoch)
            =(grant_row.application_id,grant_row.binding_id,grant_row.evidence_id,grant_row.origin_epoch,grant_row.source_epoch)
    ) THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='pilot route application prerequisites unavailable'; END IF;
    RETURN public.apply_binding_route(p_grant,p_project);
END;
$$;
REVOKE ALL ON FUNCTION public.apply_onboarding_pilot_route(uuid,text,uuid) FROM PUBLIC;
