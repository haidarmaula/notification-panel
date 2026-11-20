-- The API only needs safe target status, never leases or provider addresses.
CREATE VIEW public.application_onboarding_cohort_target_status AS
SELECT o.application_id, o.id AS operation_id, i.public_id AS installation_id,
       b.platform, t.outcome, t.failure_code, t.checked_at
FROM public.application_onboarding_cohort_targets t
JOIN public.application_onboarding_operations o ON o.id=t.operation_id
JOIN public.mobile_installations i ON (i.application_id,i.id)=(o.application_id,t.installation_id)
JOIN public.installation_bindings b ON (b.application_id,b.installation_id,b.id)=(o.application_id,i.id,t.binding_id)
WHERE o.kind='TEST_COHORT';

REVOKE ALL ON public.application_onboarding_cohort_target_status FROM PUBLIC;

-- An importer takes the same opaque installation IDs used by the API. Resolve
-- the current binding once; M53 revalidates the exact binding under lock.
CREATE FUNCTION public.approve_onboarding_cohort(
    p_project uuid,p_environment text,p_approval uuid,p_configuration uuid,
    p_installations uuid[],p_issuer text,p_evidence bytea,p_expires timestamptz
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE app bigint; bindings bigint[]; existing public.application_onboarding_approvals%ROWTYPE; original_installations uuid[];
BEGIN
    SELECT a.id INTO app FROM public.application_onboarding_cohort_scopes scope
    JOIN public.applications a ON a.id=scope.application_id
    JOIN pg_roles login ON login.oid=scope.role_id AND login.rolcanlogin
    WHERE scope.role_id=session_user::regrole::oid AND a.public_id=p_project
      AND scope.environment=p_environment AND scope.capability='APPROVAL_IMPORTER'
      AND NOT pg_has_role(session_user,current_user,'MEMBER')
      AND NOT (login.rolsuper OR login.rolcreaterole OR login.rolcreatedb OR login.rolreplication OR login.rolbypassrls)
    FOR SHARE OF scope;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='approval scope unavailable'; END IF;

    IF cardinality(p_installations) BETWEEN 1 AND 10 IS DISTINCT FROM true
      OR octet_length(p_evidence) IS DISTINCT FROM 32
      OR p_expires IS NULL
    THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid cohort approval'; END IF;

    -- Match M53's lock order and serialize repeat imports for one application.
    PERFORM 1 FROM public.notification_runtime WHERE singleton FOR SHARE;
    PERFORM 1 FROM public.applications WHERE id=app FOR NO KEY UPDATE;
    SELECT * INTO existing FROM public.application_onboarding_approvals
    WHERE application_id=app AND id=p_approval;
    IF FOUND THEN
        SELECT array_agg(i.public_id ORDER BY i.public_id) INTO original_installations
        FROM public.application_onboarding_approved_targets t
        JOIN public.mobile_installations i ON (i.application_id,i.id)=(t.application_id,t.installation_id)
        WHERE t.approval_id=p_approval AND t.application_id=app;
        IF (existing.kind,existing.configuration_id,existing.issuer_key_id,existing.evidence_sha256,existing.expires_at)
             IS DISTINCT FROM ('TEST_COHORT',p_configuration,p_issuer,p_evidence,p_expires)
          OR original_installations IS DISTINCT FROM
             ARRAY(SELECT id FROM unnest(p_installations) AS selected(id) ORDER BY id)
        THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='conflicting cohort approval'; END IF;
        RETURN existing.id;
    END IF;
    IF p_expires<=clock_timestamp() OR p_expires>clock_timestamp()+interval '24 hours'
    THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid cohort approval expiry'; END IF;

    SELECT array_agg(b.id ORDER BY b.id) INTO bindings
    FROM public.mobile_installations i
    JOIN public.installation_bindings b ON (b.application_id,b.installation_id)=(i.application_id,i.id)
    WHERE i.application_id=app AND i.public_id=ANY(p_installations) AND b.retired_at IS NULL;
    IF cardinality(bindings) IS DISTINCT FROM cardinality(p_installations)
    THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='approval roster unavailable'; END IF;

    RETURN public.provision_onboarding_cohort_approval(
        p_project,p_environment,p_approval,p_configuration,bindings,p_issuer,p_evidence,p_expires);
END;
$$;
REVOKE ALL ON FUNCTION public.approve_onboarding_cohort(uuid,text,uuid,uuid,uuid[],text,bytea,timestamptz) FROM PUBLIC;

-- Select only from the coordinator's app; initialization revalidates and
-- locks the selected operation, so discovery does not grant a work lease.
CREATE FUNCTION public.find_onboarding_cohort_operation(p_project uuid,p_environment text)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE app bigint; selected uuid;
BEGIN
    SELECT a.id INTO app FROM public.application_onboarding_cohort_scopes scope
    JOIN public.applications a ON a.id=scope.application_id
    JOIN pg_roles login ON login.oid=scope.role_id AND login.rolcanlogin
    WHERE scope.role_id=session_user::regrole::oid AND a.public_id=p_project
      AND scope.environment=p_environment AND scope.capability='COORDINATOR'
      AND NOT pg_has_role(session_user,current_user,'MEMBER')
      AND NOT (login.rolsuper OR login.rolcreaterole OR login.rolcreatedb OR login.rolreplication OR login.rolbypassrls);
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cohort scope unavailable'; END IF;
    SELECT id INTO selected FROM public.application_onboarding_operations
    WHERE application_id=app AND kind='TEST_COHORT' AND state IN ('PENDING','RUNNING')
      AND not_before<=clock_timestamp();
    RETURN selected;
END;
$$;
REVOKE ALL ON FUNCTION public.find_onboarding_cohort_operation(uuid,text) FROM PUBLIC;
