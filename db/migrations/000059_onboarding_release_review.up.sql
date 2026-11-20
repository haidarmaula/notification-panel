-- The DBA provisions reviewer/executor pairs independently of HTTP staff roles.
CREATE TABLE public.application_onboarding_release_reviewers (
    project_id uuid NOT NULL REFERENCES public.applications(public_id),
    environment text NOT NULL CHECK(environment IN ('staging','production')),
    issuer_role oid NOT NULL,
    executor_role oid NOT NULL,
    PRIMARY KEY(project_id,environment,issuer_role),
    CHECK(issuer_role<>executor_role)
);
REVOKE ALL ON public.application_onboarding_release_reviewers FROM PUBLIC;

-- Extract a current, reviewed application snapshot without granting table reads.
CREATE FUNCTION public.approve_onboarding_release(p_project uuid,p_environment text,p_approval uuid,
    p_kind text,p_configuration uuid,p_cohort uuid,p_evidence bytea,p_expires timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE
    actor oid := session_user::regrole::oid;
    previous public.application_onboarding_approvals%ROWTYPE;
    app public.applications%ROWTYPE;
    issued timestamptz;
BEGIN
    PERFORM 1 FROM public.application_onboarding_release_reviewers
    WHERE project_id=p_project AND environment=p_environment AND issuer_role=actor FOR SHARE;
    IF NOT FOUND OR NOT public.application_dispatch_actor_valid(actor,true) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='release reviewer scope unavailable'; END IF;
    IF p_kind IS NULL OR p_kind NOT IN ('PILOT','FULL') OR p_approval IS NULL
       OR p_approval='00000000-0000-0000-0000-000000000000' OR octet_length(p_evidence) IS DISTINCT FROM 32
    THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid release approval'; END IF;
    PERFORM 1 FROM public.notification_runtime WHERE singleton FOR SHARE;
    SELECT * INTO STRICT app FROM public.applications WHERE public_id=p_project FOR NO KEY UPDATE;
    SELECT * INTO previous FROM public.application_onboarding_approvals WHERE id=p_approval;
    IF FOUND THEN
        IF (previous.application_id,previous.kind,previous.configuration_id,previous.cohort_id,previous.issuer_key_id,
            previous.evidence_sha256,previous.expires_at) IS NOT DISTINCT FROM
           (app.id,p_kind,p_configuration,p_cohort,session_user::text,p_evidence,p_expires)
        THEN RETURN p_approval; END IF;
        RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='release approval replay conflict';
    END IF;

    issued := clock_timestamp();
    INSERT INTO public.application_onboarding_approvals(id,application_id,kind,configuration_id,profile_revision,
        origin_epoch,source_epoch,application_revision,dispatch_revision,cohort_id,issuer_key_id,evidence_sha256,issued_at,expires_at)
    SELECT p_approval,app.id,p_kind,c.id,f.revision,n.origin_epoch,n.source_epoch,app.revision,d.dispatch_revision,
        p_cohort,session_user::text,p_evidence,issued,p_expires
    FROM public.application_onboarding s
    JOIN public.application_dispatch_policy d ON d.application_id=s.application_id
    JOIN public.application_provider_configurations c ON (c.application_id,c.id)=(s.application_id,s.current_configuration_id)
    JOIN public.application_runtime_capacity_profile f ON f.environment=s.environment
    CROSS JOIN public.notification_runtime n
    WHERE s.application_id=app.id AND app.status='ACTIVE' AND s.environment=p_environment AND c.environment=s.environment
      AND c.id=p_configuration AND c.file_reference=c.id
      AND s.cohort_id=p_cohort AND s.runtime_generation>0 AND c.state='VERIFIED'
      AND n.singleton AND NOT n.sealed AND NOT n.migration_hold
      AND d.dispatch_mode=CASE p_kind WHEN 'PILOT' THEN 'HELD' ELSE 'PILOT' END
      AND p_expires>issued AND p_expires<=issued+interval '24 hours';
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='release approval prerequisites unavailable'; END IF;
    RETURN p_approval;
END;
$$;
REVOKE ALL ON FUNCTION public.approve_onboarding_release(uuid,text,uuid,text,uuid,uuid,bytea,timestamptz) FROM PUBLIC;

CREATE FUNCTION public.delegate_onboarding_release(p_project uuid,p_environment text,p_operation uuid,p_grant uuid,
    p_generation bigint,p_legacy bytea,p_consent bytea,p_coverage bytea,p_quota bytea,p_expires timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE
    actor oid := session_user::regrole::oid;
    executor oid;
    app bigint;
    op public.application_onboarding_operations%ROWTYPE;
    g public.application_dispatch_grants%ROWTYPE;
    destination text;
BEGIN
    IF NOT public.application_dispatch_actor_valid(actor,true)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='release reviewer scope unavailable'; END IF;
    SELECT executor_role INTO executor FROM public.application_onboarding_release_reviewers
    WHERE project_id=p_project AND environment=p_environment AND issuer_role=actor FOR SHARE;
    IF NOT FOUND OR NOT public.application_dispatch_actor_valid(executor,false)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='release reviewer scope unavailable'; END IF;
    PERFORM 1 FROM public.notification_runtime WHERE singleton FOR SHARE;
    SELECT id INTO STRICT app FROM public.applications WHERE public_id=p_project FOR NO KEY UPDATE;
    SELECT o.* INTO op FROM public.application_onboarding_operations o
    JOIN public.application_onboarding s ON s.application_id=o.application_id
    WHERE o.id=p_operation AND o.application_id=app AND s.environment=p_environment;
    IF NOT FOUND OR op.kind NOT IN ('RELEASE_PILOT','RELEASE_FULL')
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='release operation unavailable'; END IF;
    destination := substring(op.kind from 9);
    SELECT * INTO g FROM public.application_dispatch_grants WHERE id=p_grant;
    IF FOUND THEN
        IF (g.project_id,g.issuer_role,g.operator_role,g.legacy_evidence,g.consent_evidence,g.coverage_evidence,g.quota_evidence,g.expires_at)
            IS DISTINCT FROM (p_project,actor,executor,p_legacy,p_consent,p_coverage,p_quota,p_expires)
        THEN RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='release grant replay conflict'; END IF;
    ELSE
        INSERT INTO public.application_dispatch_grants(id,project_id,operator_role,application_revision,expected_revision,
            previous_mode,destination,cohort_id,origin_epoch,source_epoch,reason,legacy_evidence,consent_evidence,
            coverage_evidence,quota_evidence,expires_at)
        VALUES(p_grant,p_project,executor,op.expected_application_revision,op.expected_dispatch_revision,
            CASE destination WHEN 'PILOT' THEN 'HELD' ELSE 'PILOT' END,destination,op.cohort_id,op.origin_epoch,op.source_epoch,
            CASE destination WHEN 'PILOT' THEN 'pilot' ELSE 'full_coverage' END,p_legacy,p_consent,p_coverage,p_quota,p_expires);
    END IF;

    -- The binding rejects stale or cross-application state, rolling back new grants.
    PERFORM public.bind_onboarding_release(p_operation,p_grant,p_generation);
    RETURN p_grant;
END;
$$;
REVOKE ALL ON FUNCTION public.delegate_onboarding_release(uuid,text,uuid,uuid,bigint,bytea,bytea,bytea,bytea,timestamptz) FROM PUBLIC;

CREATE OR REPLACE FUNCTION supersede_unusable_onboarding_release(p_application bigint,p_kind text) RETURNS boolean
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
    IF FOUND AND g.applied_at IS NOT NULL THEN RETURN false; END IF;
    -- An unbound request can still be delegated while its approval is usable.
    -- Once the approval or snapshot goes stale, an exact old-key replay remains
    -- available while a new request can claim the slot.
    IF (g.id IS NULL OR (g.revoked_at IS NULL AND g.expires_at>clock_timestamp()
           AND public.application_dispatch_actor_valid(g.issuer_role,true)
           AND public.application_dispatch_actor_valid(g.operator_role,false)))
       AND EXISTS (
           SELECT FROM public.application_onboarding_approvals a
           JOIN public.applications app ON app.id=a.application_id
           JOIN public.application_onboarding setup ON setup.application_id=app.id
           JOIN public.application_dispatch_policy policy ON policy.application_id=app.id
           CROSS JOIN public.notification_runtime runtime
           WHERE (a.application_id,a.id)=(op.application_id,op.approval_id)
             AND a.revoked_at IS NULL AND a.consumed_at IS NULL AND a.expires_at>clock_timestamp()
             AND setup.revision=op.expected_setup_revision AND app.revision=op.expected_application_revision
             AND policy.dispatch_revision=op.expected_dispatch_revision
             AND runtime.singleton AND (runtime.origin_epoch,runtime.source_epoch)=(op.origin_epoch,op.source_epoch)
       )
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
