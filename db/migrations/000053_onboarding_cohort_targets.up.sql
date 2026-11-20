-- Only the protected approval importer may attest reviewed consent and test
-- designation by inserting a member. Enrollment alone does not create a member.
CREATE TABLE application_onboarding_approved_targets (
    approval_id uuid NOT NULL,
    application_id bigint NOT NULL,
    installation_id bigint NOT NULL,
    binding_id bigint NOT NULL,
    revoked_at timestamptz,
    PRIMARY KEY (approval_id, installation_id),
    FOREIGN KEY (application_id, approval_id) REFERENCES application_onboarding_approvals(application_id,id),
    FOREIGN KEY (application_id, installation_id) REFERENCES mobile_installations(application_id,id),
    FOREIGN KEY (application_id, binding_id) REFERENCES installation_bindings(application_id,id)
);

CREATE TABLE application_onboarding_cohort_targets (
    operation_id uuid NOT NULL REFERENCES application_onboarding_operations(id),
    installation_id bigint NOT NULL REFERENCES mobile_installations(id),
    binding_id bigint NOT NULL REFERENCES installation_bindings(id),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 3),
    operation_lease_token bigint,
    operation_lease_owner uuid,
    lease_owner uuid,
    lease_expires_at timestamptz,
    not_before timestamptz NOT NULL DEFAULT clock_timestamp(),
    outcome text,
    failure_code text,
    checked_at timestamptz,
    PRIMARY KEY (operation_id, installation_id),
    CHECK ((operation_lease_token IS NULL) = (lease_expires_at IS NULL)),
    CHECK ((operation_lease_owner IS NULL) = (lease_expires_at IS NULL)),
    CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL)),
    CHECK ((outcome IS NULL) = (checked_at IS NULL)),
    CHECK (outcome IS NULL OR lease_expires_at IS NULL),
    CHECK (CASE outcome
        WHEN 'PASSED' THEN failure_code IS NULL
        WHEN 'FAILED' THEN failure_code IN ('FCM_CREDENTIAL_REJECTED','FCM_TARGET_UNREGISTERED',
            'FCM_TARGET_SENDER_MISMATCH','FCM_TARGET_CHECK_REJECTED') IS TRUE
        WHEN 'INCONCLUSIVE' THEN failure_code IN ('FCM_CHECK_INCONCLUSIVE','FCM_CONFIGURATION_UNAVAILABLE') IS TRUE
        ELSE outcome IS NULL AND failure_code IS NULL END)
);

-- The DBA registers distinct, non-owning logins and grants only their entry points.
CREATE TABLE application_onboarding_cohort_scopes (
    role_id oid PRIMARY KEY,
    application_id bigint NOT NULL REFERENCES application_onboarding(application_id),
    environment text NOT NULL CHECK (environment IN ('staging','production')),
    capability text NOT NULL CHECK (capability IN ('COORDINATOR','VERIFIER','APPROVAL_IMPORTER'))
);

-- The importer attests external consent review; this function does not infer consent from enrollment.
CREATE FUNCTION provision_onboarding_cohort_approval(p_project uuid,p_environment text,p_approval uuid,
    p_configuration uuid,p_bindings bigint[],p_issuer text,p_evidence bytea,p_expires timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE app bigint; issued timestamptz;
BEGIN
    SELECT a.id INTO app FROM public.application_onboarding_cohort_scopes scope
    JOIN public.applications a ON a.id=scope.application_id
    JOIN pg_roles role ON role.oid=scope.role_id AND role.rolcanlogin
    WHERE scope.role_id=session_user::regrole::oid AND scope.environment=p_environment
      AND scope.capability='APPROVAL_IMPORTER' AND a.public_id=p_project
      AND NOT pg_has_role(session_user,current_user,'MEMBER')
      AND NOT (role.rolsuper OR role.rolcreaterole OR role.rolcreatedb OR role.rolreplication OR role.rolbypassrls)
    FOR SHARE OF scope;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='approval scope unavailable'; END IF;
    IF cardinality(p_bindings) BETWEEN 1 AND 10 IS DISTINCT FROM true THEN
        RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid approval roster size';
    END IF;
    PERFORM 1 FROM public.notification_runtime WHERE singleton FOR SHARE;
    PERFORM 1 FROM public.applications WHERE id=app FOR NO KEY UPDATE;
    PERFORM 1 FROM public.application_onboarding WHERE application_id=app FOR SHARE;
    PERFORM 1 FROM public.application_dispatch_policy WHERE application_id=app FOR SHARE;
    PERFORM 1 FROM public.application_provider_configurations WHERE application_id=app AND id=p_configuration FOR SHARE;
    PERFORM 1 FROM public.application_runtime_capacity_profile WHERE environment=p_environment FOR SHARE;
    PERFORM 1 FROM public.users u WHERE u.application_id=app AND u.id IN (
        SELECT i.owner_user_id FROM public.mobile_installations i JOIN public.installation_bindings b
        ON (b.application_id,b.installation_id)=(i.application_id,i.id)
        WHERE i.application_id=app AND b.id=ANY(p_bindings)) ORDER BY u.id FOR SHARE;
    PERFORM 1 FROM public.mobile_installations i WHERE i.application_id=app AND i.id IN (
        SELECT b.installation_id FROM public.installation_bindings b WHERE b.application_id=app AND b.id=ANY(p_bindings))
        ORDER BY i.id FOR SHARE;
    PERFORM 1 FROM public.installation_bindings WHERE application_id=app AND id=ANY(p_bindings) ORDER BY id FOR SHARE;

    issued := clock_timestamp();
    INSERT INTO public.application_onboarding_approvals(id,application_id,kind,configuration_id,profile_revision,
        origin_epoch,source_epoch,application_revision,dispatch_revision,issuer_key_id,evidence_sha256,issued_at,expires_at)
    SELECT p_approval,app,'TEST_COHORT',c.id,f.revision,r.origin_epoch,r.source_epoch,a.revision,d.dispatch_revision,
        p_issuer,p_evidence,issued,p_expires
    FROM public.applications a JOIN public.application_onboarding s ON s.application_id=a.id
    JOIN public.application_dispatch_policy d ON d.application_id=a.id
    JOIN public.application_provider_configurations c ON (c.application_id,c.id)=(a.id,s.current_configuration_id)
    JOIN public.application_runtime_capacity_profile f ON f.environment=s.environment
    CROSS JOIN public.notification_runtime r
    WHERE a.id=app AND a.status='ACTIVE' AND d.dispatch_mode='HELD' AND d.hold_new AND NOT d.coverage_approved
      AND r.singleton AND NOT r.sealed AND NOT r.migration_hold
      AND s.environment=p_environment AND c.environment=s.environment AND c.id=p_configuration
      AND c.state='VERIFIED' AND c.file_reference=c.id AND s.enrollment_profile_id IS NOT NULL
      AND jsonb_array_length(s.enrollment_platforms)>0;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='approval prerequisites unavailable'; END IF;

    INSERT INTO public.application_onboarding_approved_targets(approval_id,application_id,installation_id,binding_id)
    SELECT p_approval,app,i.id,b.id FROM public.installation_bindings b
    JOIN public.mobile_installations i ON (i.application_id,i.id)=(b.application_id,b.installation_id)
    JOIN public.users u ON (u.application_id,u.id)=(i.application_id,i.owner_user_id)
    JOIN public.application_onboarding s ON s.application_id=i.application_id
    WHERE b.application_id=app AND b.id=ANY(p_bindings) AND b.retired_at IS NULL
      AND s.enrollment_platforms @> jsonb_build_array(jsonb_build_object('platform',b.platform))
      AND u.status='ACTIVE'
      AND (b.owner_user_id,b.owner_epoch)=(i.owner_user_id,i.owner_epoch);
    IF (SELECT count(*) FROM public.application_onboarding_approved_targets WHERE approval_id=p_approval)
        <> cardinality(p_bindings) THEN
        RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='approval roster unavailable';
    END IF;
    RETURN p_approval;
END;
$$;
REVOKE ALL ON FUNCTION provision_onboarding_cohort_approval(uuid,text,uuid,uuid,bigint[],text,bytea,timestamptz) FROM PUBLIC;

-- The operation UUID doubles as the cohort UUID when the API queues an unbound
-- request. This avoids rewriting immutable request fields at claim time.
CREATE VIEW application_onboarding_live_targets AS
SELECT a.id AS application_key, a.public_id AS application, s.environment, o.id AS operation_id,
       COALESCE(o.cohort_id,o.id) AS cohort_id,
       p.id AS approval_id, c.id AS configuration_id, i.id AS installation_key,
       i.public_id AS installation_id, b.id AS binding_epoch, o.lease_owner,
       o.lease_token AS operation_token, LEAST(o.lease_expires_at,p.expires_at) AS deadline,
       c.file_reference, c.target_project_id, c.service_account_email,
       b.address_type, b.address_ciphertext, b.encryption_key_version
FROM application_onboarding_operations o
JOIN applications a ON a.id=o.application_id
JOIN application_onboarding s ON s.application_id=a.id
JOIN application_dispatch_policy d ON d.application_id=a.id
JOIN application_provider_configurations c ON c.application_id=a.id AND c.id=o.configuration_id
JOIN application_onboarding_approvals p ON p.application_id=a.id AND p.id=o.approval_id
JOIN application_onboarding_approved_targets m ON m.application_id=a.id AND m.approval_id=p.id
JOIN mobile_installations i ON i.application_id=a.id AND i.id=m.installation_id
JOIN installation_bindings b ON b.application_id=a.id AND b.id=m.binding_id AND b.installation_id=i.id
JOIN users owner ON owner.application_id=a.id AND owner.id=i.owner_user_id AND owner.status='ACTIVE'
JOIN notification_runtime r ON r.singleton
JOIN application_runtime_capacity_profile f ON f.environment=s.environment
WHERE a.status='ACTIVE' AND d.dispatch_mode='HELD' AND d.hold_new AND NOT d.coverage_approved
  AND NOT r.sealed AND NOT r.migration_hold
  AND o.kind='TEST_COHORT' AND o.state IN ('PENDING','RUNNING')
  AND (o.cohort_id IS NULL OR o.cohort_id=o.id)
  AND s.enrollment_profile_id IS NOT NULL
  AND EXISTS (SELECT FROM staff_users actor
      JOIN roles role ON role.id=actor.role_id AND role.name='SUPER_ADMIN'
      JOIN staff_sessions session ON session.staff_user_id=actor.id AND session.auth_epoch=actor.auth_epoch
      JOIN staff_session_control lineage ON lineage.singleton AND lineage.session_epoch=session.session_epoch
      WHERE actor.id=o.actor_id AND actor.is_active AND actor.auth_epoch=o.auth_epoch
        AND session.id=o.session_id AND session.session_epoch=o.session_epoch
        AND session.revoked_at IS NULL AND session.expires_at>clock_timestamp())
  AND s.current_configuration_id=c.id AND s.environment=c.environment
  AND c.state='VERIFIED' AND c.file_reference=c.id
  AND s.revision=o.expected_setup_revision AND a.revision=o.expected_application_revision
  AND d.dispatch_revision=o.expected_dispatch_revision
  AND (o.origin_epoch,o.source_epoch)=(r.origin_epoch,r.source_epoch)
  AND p.kind='TEST_COHORT' AND p.configuration_id=c.id AND p.profile_revision=f.revision
  AND (p.cohort_id IS NULL OR p.cohort_id=COALESCE(o.cohort_id,o.id))
  AND (p.origin_epoch,p.source_epoch)=(r.origin_epoch,r.source_epoch)
  AND p.application_revision=a.revision AND p.dispatch_revision=d.dispatch_revision
  AND p.revoked_at IS NULL AND p.issued_at<=clock_timestamp() AND p.expires_at>clock_timestamp()
  AND m.revoked_at IS NULL AND i.public_id=ANY(o.installation_ids)
  AND cardinality(o.installation_ids) BETWEEN 1 AND 10
  AND ARRAY(SELECT u.id FROM unnest(o.installation_ids) AS u(id) ORDER BY u.id) = ARRAY(
      SELECT member.public_id
      FROM application_onboarding_approved_targets approved
      JOIN mobile_installations member ON member.id=approved.installation_id
      WHERE approved.approval_id=p.id AND approved.application_id=a.id
      ORDER BY member.public_id)
  AND b.retired_at IS NULL AND s.enrollment_platforms @> jsonb_build_array(jsonb_build_object('platform',b.platform))
  AND (b.owner_user_id,b.owner_epoch)=(i.owner_user_id,i.owner_epoch);

REVOKE ALL ON application_onboarding_approved_targets, application_onboarding_cohort_targets,
    application_onboarding_live_targets, application_onboarding_cohort_scopes FROM PUBLIC;

-- Serialize short DB transitions in lifecycle lock order; no provider I/O holds these locks.
CREATE FUNCTION lock_onboarding_cohort(p_project uuid,p_environment text,p_operation uuid,p_capability text)
RETURNS uuid LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
DECLARE app bigint; op public.application_onboarding_operations%ROWTYPE;
BEGIN
    SELECT a.id INTO app FROM public.application_onboarding_cohort_scopes s
    JOIN public.applications a ON a.id=s.application_id
    JOIN pg_roles role ON role.oid=s.role_id AND role.rolcanlogin
    WHERE s.role_id=session_user::regrole::oid AND s.environment=p_environment AND s.capability=p_capability
      AND a.public_id=p_project AND NOT pg_has_role(session_user,current_user,'MEMBER')
      AND NOT (role.rolsuper OR role.rolcreaterole OR role.rolcreatedb OR role.rolreplication OR role.rolbypassrls)
    FOR SHARE OF s;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cohort scope unavailable'; END IF;
    PERFORM 1 FROM public.staff_session_control WHERE singleton FOR SHARE;
    PERFORM 1 FROM public.notification_runtime WHERE singleton FOR SHARE;
    PERFORM 1 FROM public.applications WHERE id=app FOR NO KEY UPDATE;
    SELECT * INTO op FROM public.application_onboarding_operations
    WHERE application_id=app AND kind='TEST_COHORT' AND
        (id=p_operation OR (p_operation IS NULL AND state='RUNNING'));
    IF NOT FOUND THEN RETURN NULL; END IF;
    PERFORM 1 FROM public.staff_users WHERE id=op.actor_id FOR SHARE;
    PERFORM 1 FROM public.staff_sessions WHERE id=op.session_id FOR SHARE;
    PERFORM 1 FROM public.application_onboarding WHERE application_id=app AND environment=p_environment FOR UPDATE;
    IF NOT FOUND THEN RETURN NULL; END IF;
    PERFORM 1 FROM public.application_dispatch_policy WHERE application_id=app FOR SHARE;
    PERFORM 1 FROM public.application_onboarding_operations WHERE id=op.id FOR UPDATE;
    PERFORM 1 FROM public.application_provider_configurations WHERE id=op.configuration_id FOR SHARE;
    PERFORM 1 FROM public.application_runtime_capacity_profile WHERE environment=p_environment FOR SHARE;
    -- FOR UPDATE also excludes new member FK references while the roster is checked.
    PERFORM 1 FROM public.application_onboarding_approvals WHERE id=op.approval_id FOR UPDATE;
    PERFORM 1 FROM public.application_onboarding_approved_targets WHERE approval_id=op.approval_id
        ORDER BY installation_id FOR SHARE;
    PERFORM 1 FROM public.users u WHERE u.id IN (
        SELECT i.owner_user_id FROM public.mobile_installations i
        JOIN public.application_onboarding_approved_targets m ON m.installation_id=i.id
        WHERE m.approval_id=op.approval_id) ORDER BY u.id FOR SHARE;
    PERFORM 1 FROM public.mobile_installations i JOIN public.application_onboarding_approved_targets m ON m.installation_id=i.id
        WHERE m.approval_id=op.approval_id ORDER BY i.id FOR SHARE OF i;
    PERFORM 1 FROM public.installation_bindings b JOIN public.application_onboarding_approved_targets m ON m.binding_id=b.id
        WHERE m.approval_id=op.approval_id ORDER BY b.id FOR SHARE OF b;
    RETURN op.id;
END;
$$;

CREATE FUNCTION onboarding_cohort_eligible(p_operation uuid,p_initialized boolean)
RETURNS boolean LANGUAGE sql SET search_path=pg_catalog,pg_temp AS $$
    SELECT EXISTS (
        SELECT FROM public.application_onboarding_operations o
        JOIN public.application_onboarding_approvals p ON p.id=o.approval_id
        WHERE o.id=p_operation AND (p.consumed_at IS NOT NULL)=p_initialized
          AND cardinality(o.installation_ids) BETWEEN 1 AND 10
          AND cardinality(o.installation_ids)=(SELECT count(*) FROM public.application_onboarding_live_targets l WHERE l.operation_id=o.id)
          AND (NOT p_initialized OR cardinality(o.installation_ids)=(
              SELECT count(*) FROM public.application_onboarding_live_targets l
              JOIN public.application_onboarding_cohort_targets t ON (t.operation_id,t.installation_id,t.binding_id)=
                  (l.operation_id,l.installation_key,l.binding_epoch) WHERE l.operation_id=o.id))
    );
$$;

CREATE FUNCTION initialize_onboarding_cohort(p_project uuid,p_environment text,p_operation uuid,p_owner uuid)
RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE op public.application_onboarding_operations%ROWTYPE; selected uuid; expiry timestamptz;
BEGIN
    IF p_operation IS NULL OR p_owner IS NULL OR p_owner='00000000-0000-0000-0000-000000000000' THEN RETURN NULL; END IF;
    selected:=public.lock_onboarding_cohort(p_project,p_environment,p_operation,'COORDINATOR');
    SELECT * INTO op FROM public.application_onboarding_operations WHERE id=selected;
    IF NOT FOUND OR op.state NOT IN ('PENDING','RUNNING') OR op.not_before>clock_timestamp() THEN RETURN NULL; END IF;
    IF NOT public.onboarding_cohort_eligible(op.id,op.state='RUNNING') THEN
        UPDATE public.application_onboarding_operations SET state='SUPERSEDED',completed_at=clock_timestamp(),
            failure_code='COHORT_PREREQUISITES_CHANGED',lease_owner=NULL,lease_expires_at=NULL WHERE id=op.id;
        INSERT INTO public.audit_logs(application_id,actor_user_id,action,entity_type,entity_id,after_json)
        VALUES(op.application_id,op.actor_id,'APPLICATION_ONBOARDING_OPERATION_COMPLETE','application_onboarding',op.application_id,
            jsonb_build_object('operation_id',op.id,'kind','TEST_COHORT','outcome','SUPERSEDED'));
        RETURN NULL;
    END IF;
    IF op.state='RUNNING' AND op.lease_expires_at>clock_timestamp() THEN
        RETURN CASE WHEN op.lease_owner=p_owner THEN op.lease_token END;
    END IF;
    SELECT LEAST(expires_at,clock_timestamp()+interval '5 minutes') INTO expiry
        FROM public.application_onboarding_approvals WHERE id=op.approval_id;
    IF expiry<=clock_timestamp() THEN RETURN NULL; END IF;
    IF op.state='PENDING' THEN
        INSERT INTO public.application_onboarding_cohort_targets(operation_id,installation_id,binding_id)
        SELECT operation_id,installation_key,binding_epoch FROM public.application_onboarding_live_targets WHERE operation_id=op.id;
        IF NOT public.onboarding_cohort_eligible(op.id,false) OR cardinality(op.installation_ids)<>(
            SELECT count(*) FROM public.application_onboarding_cohort_targets WHERE operation_id=op.id)
        THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='cohort prerequisites changed during initialization'; END IF;
        UPDATE public.application_onboarding_approvals SET consumed_at=clock_timestamp()
        WHERE id=op.approval_id AND consumed_at IS NULL AND expires_at>clock_timestamp();
        IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='cohort approval no longer available'; END IF;
    END IF;
    UPDATE public.application_onboarding_operations SET state='RUNNING',lease_owner=p_owner,lease_token=lease_token+1,
        lease_expires_at=expiry,attempts=attempts+1 WHERE id=op.id RETURNING lease_token INTO op.lease_token;
    RETURN op.lease_token;
END;
$$;

CREATE FUNCTION finalize_onboarding_cohort(p_project uuid,p_environment text,p_operation uuid,p_owner uuid,p_token bigint)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE op public.application_onboarding_operations%ROWTYPE; selected uuid; result text; failure text;
    recorded_at timestamptz; earliest_pass timestamptz; revision bigint;
BEGIN
    IF p_operation IS NULL OR p_owner IS NULL OR p_token IS NULL OR p_token<=0 THEN RETURN false; END IF;
    selected:=public.lock_onboarding_cohort(p_project,p_environment,p_operation,'COORDINATOR');
    SELECT * INTO op FROM public.application_onboarding_operations WHERE id=selected;
    IF NOT FOUND OR op.lease_token<>p_token THEN RETURN false; END IF;
    IF op.state IN ('SUCCEEDED','FAILED') THEN RETURN op.completed_by IS NOT DISTINCT FROM p_owner; END IF;
    IF op.state<>'RUNNING' OR op.lease_owner IS DISTINCT FROM p_owner OR op.lease_expires_at<=clock_timestamp()
       OR NOT public.onboarding_cohort_eligible(op.id,true) THEN RETURN false; END IF;

    UPDATE public.application_onboarding_cohort_targets SET outcome='INCONCLUSIVE',failure_code='FCM_CHECK_INCONCLUSIVE',
        checked_at=clock_timestamp(),operation_lease_token=NULL,operation_lease_owner=NULL,lease_owner=NULL,lease_expires_at=NULL
    WHERE operation_id=op.id AND outcome IS NULL AND attempts=3
      AND (lease_expires_at<=clock_timestamp() OR operation_lease_token<>p_token OR operation_lease_owner<>p_owner);
    IF EXISTS(SELECT FROM public.application_onboarding_cohort_targets WHERE operation_id=op.id AND outcome IS NULL)
    THEN RETURN false; END IF;
    SELECT outcome,failure_code INTO result,failure FROM public.application_onboarding_cohort_targets
        WHERE operation_id=op.id AND outcome<>'PASSED' ORDER BY (outcome='FAILED') DESC,installation_id LIMIT 1;
    recorded_at:=clock_timestamp();
    SELECT min(checked_at) INTO earliest_pass FROM public.application_onboarding_cohort_targets WHERE operation_id=op.id;
    IF result IS NULL THEN
        IF earliest_pass+interval '15 minutes'<=recorded_at THEN
            result:='INCONCLUSIVE'; failure:='FCM_CHECK_INCONCLUSIVE';
        ELSE result:='PASSED'; END IF;
    END IF;
    IF op.lease_expires_at<=recorded_at OR NOT public.onboarding_cohort_eligible(op.id,true) THEN RETURN false; END IF;
    INSERT INTO public.application_provider_checks(id,application_id,configuration_id,operation_id,kind,outcome,
        origin_epoch,source_epoch,checked_at,expires_at,failure_code)
    VALUES(gen_random_uuid(),op.application_id,op.configuration_id,op.id,'TARGET_PERMISSION',result,op.origin_epoch,op.source_epoch,
        recorded_at,(CASE WHEN result='PASSED' THEN earliest_pass ELSE recorded_at END)+interval '15 minutes',failure);
    UPDATE public.application_onboarding SET cohort_id=CASE WHEN result='PASSED' THEN op.id ELSE cohort_id END,
        revision=application_onboarding.revision+1,updated_at=recorded_at WHERE application_id=op.application_id
        RETURNING application_onboarding.revision INTO revision;
    UPDATE public.application_onboarding_operations SET state=CASE WHEN result='PASSED' THEN 'SUCCEEDED' ELSE 'FAILED' END,
        completed_at=recorded_at,completed_by=p_owner,resulting_revision=revision,failure_code=failure,
        lease_owner=NULL,lease_expires_at=NULL WHERE id=op.id;
    INSERT INTO public.audit_logs(application_id,actor_user_id,action,entity_type,entity_id,after_json)
    VALUES(op.application_id,op.actor_id,'APPLICATION_ONBOARDING_OPERATION_COMPLETE','application_onboarding',op.application_id,
        jsonb_build_object('operation_id',op.id,'kind','TEST_COHORT','outcome',result,'revision',revision::text));
    RETURN true;
END;
$$;

CREATE FUNCTION claim_onboarding_cohort_target(p_project uuid,p_environment text,p_owner uuid)
RETURNS TABLE(application_key bigint,application uuid,environment text,operation_id uuid,cohort_id uuid,
    approval_id uuid,configuration_id uuid,installation_key bigint,installation_id uuid,binding_epoch bigint,
    file_reference uuid,target_project_id text,service_account_email text,address_type text,
    address_ciphertext bytea,encryption_key_version integer,lease_token bigint,target_owner uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE selected uuid;
BEGIN
    IF p_owner IS NULL OR p_owner='00000000-0000-0000-0000-000000000000' THEN RETURN; END IF;
    selected:=public.lock_onboarding_cohort(p_project,p_environment,NULL,'VERIFIER');
    IF selected IS NULL OR NOT public.onboarding_cohort_eligible(selected,true) THEN RETURN; END IF;
    RETURN QUERY
    WITH candidate AS MATERIALIZED (
        SELECT l.* FROM public.application_onboarding_live_targets l
        JOIN public.application_onboarding_cohort_targets t ON (t.operation_id,t.installation_id,t.binding_id)=
            (l.operation_id,l.installation_key,l.binding_epoch)
        WHERE l.operation_id=selected AND l.operation_token>0 AND l.deadline>clock_timestamp()
          AND t.outcome IS NULL AND t.attempts<3 AND t.not_before<=clock_timestamp()
          AND (t.lease_expires_at IS NULL OR t.lease_expires_at<=clock_timestamp()
               OR t.operation_lease_token<>l.operation_token OR t.operation_lease_owner<>l.lease_owner)
        ORDER BY t.not_before,t.installation_id LIMIT 1
    ), claimed AS (
        UPDATE public.application_onboarding_cohort_targets t
        SET attempts=t.attempts+1,operation_lease_token=l.operation_token,operation_lease_owner=l.lease_owner,
            lease_owner=p_owner,lease_expires_at=LEAST(l.deadline,clock_timestamp()+interval '60 seconds')
        FROM candidate l WHERE t.operation_id=l.operation_id AND t.installation_id=l.installation_key
          AND l.deadline>clock_timestamp()
        RETURNING t.attempts::bigint,t.lease_owner
    )
    SELECT l.application_key,l.application,l.environment,l.operation_id,l.cohort_id,l.approval_id,l.configuration_id,
        l.installation_key,l.installation_id,l.binding_epoch,l.file_reference,l.target_project_id,l.service_account_email,
        l.address_type,l.address_ciphertext,l.encryption_key_version,c.* FROM candidate l CROSS JOIN claimed c;
END;
$$;

CREATE FUNCTION finish_onboarding_cohort_target(p_project uuid,p_environment text,p_operation uuid,p_cohort uuid,
    p_approval uuid,p_configuration uuid,p_installation uuid,p_binding_epoch bigint,p_owner uuid,p_token bigint,
    p_outcome text,p_failure text)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE selected uuid;
BEGIN
    IF p_operation IS NULL OR ((p_outcome,p_failure) IN (
        ('PASSED',''), ('FAILED','FCM_CREDENTIAL_REJECTED'), ('FAILED','FCM_TARGET_UNREGISTERED'),
        ('FAILED','FCM_TARGET_SENDER_MISMATCH'), ('FAILED','FCM_TARGET_CHECK_REJECTED'),
        ('INCONCLUSIVE','FCM_CHECK_INCONCLUSIVE'), ('INCONCLUSIVE','FCM_CONFIGURATION_UNAVAILABLE'))) IS NOT TRUE
    THEN RETURN false; END IF;
    selected:=public.lock_onboarding_cohort(p_project,p_environment,p_operation,'VERIFIER');
    IF selected IS NULL OR NOT public.onboarding_cohort_eligible(selected,true) THEN RETURN false; END IF;
    UPDATE public.application_onboarding_cohort_targets t
    SET operation_lease_token=NULL,operation_lease_owner=NULL,lease_owner=NULL,lease_expires_at=NULL,
        not_before=clock_timestamp()+interval '30 seconds',
        outcome=CASE WHEN p_outcome<>'INCONCLUSIVE' OR t.attempts=3 THEN p_outcome END,
        failure_code=CASE WHEN p_outcome<>'INCONCLUSIVE' OR t.attempts=3 THEN NULLIF(p_failure,'') END,
        checked_at=CASE WHEN p_outcome<>'INCONCLUSIVE' OR t.attempts=3 THEN clock_timestamp() END
    FROM public.application_onboarding_live_targets l
    WHERE t.operation_id=selected AND (t.operation_id,t.installation_id,t.binding_id)=(l.operation_id,l.installation_key,l.binding_epoch)
      AND (l.cohort_id,l.approval_id,l.configuration_id,l.installation_id,l.binding_epoch)=
          (p_cohort,p_approval,p_configuration,p_installation,p_binding_epoch)
      AND t.lease_owner=p_owner AND t.attempts=p_token AND t.outcome IS NULL
      AND t.operation_lease_owner=l.lease_owner AND t.operation_lease_token=l.operation_token
      AND l.deadline>clock_timestamp() AND t.lease_expires_at>clock_timestamp();
    RETURN FOUND;
END;
$$;

REVOKE ALL ON FUNCTION lock_onboarding_cohort(uuid,text,uuid,text),onboarding_cohort_eligible(uuid,boolean),
    initialize_onboarding_cohort(uuid,text,uuid,uuid),finalize_onboarding_cohort(uuid,text,uuid,uuid,bigint),
    claim_onboarding_cohort_target(uuid,text,uuid),
    finish_onboarding_cohort_target(uuid,text,uuid,uuid,uuid,uuid,uuid,bigint,uuid,bigint,text,text) FROM PUBLIC;
