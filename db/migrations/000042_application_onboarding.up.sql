CREATE TABLE application_onboarding (
    application_id BIGINT PRIMARY KEY REFERENCES applications(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    greenfield BOOLEAN NOT NULL DEFAULT FALSE,
    environment TEXT CHECK (environment IN ('staging','production')),
    enrollment_profile_id UUID,
    enrollment_platforms JSONB NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(enrollment_platforms)='array'),
    candidate_configuration_id UUID,
    current_configuration_id UUID,
    cohort_id UUID,
    runtime_generation BIGINT NOT NULL DEFAULT 0 CHECK (runtime_generation >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO application_onboarding(application_id) SELECT id FROM applications;

CREATE FUNCTION initialize_application_onboarding() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    INSERT INTO public.application_onboarding(application_id,greenfield) VALUES(NEW.id,true);
    RETURN NEW;
END;
$$;
CREATE TRIGGER initialize_application_onboarding AFTER INSERT ON applications
    FOR EACH ROW EXECUTE FUNCTION initialize_application_onboarding();

CREATE TABLE application_provider_configurations (
    id UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
    application_id BIGINT NOT NULL REFERENCES application_onboarding(application_id) ON DELETE RESTRICT,
    version BIGINT NOT NULL CHECK (version > 0),
    environment TEXT NOT NULL CHECK(environment IN ('staging','production')),
    target_project_id TEXT NOT NULL CHECK (octet_length(target_project_id) BETWEEN 1 AND 128),
    service_account_email TEXT NOT NULL CHECK (octet_length(service_account_email) BETWEEN 1 AND 320),
    file_reference UUID NOT NULL UNIQUE,
    state TEXT NOT NULL DEFAULT 'STORED' CHECK (state IN ('STORED','VERIFYING','VERIFIED','REJECTED','SUPERSEDED')),
    created_by BIGINT NOT NULL CHECK(created_by > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(application_id,id), UNIQUE(application_id,version), UNIQUE(application_id,environment,id)
);
ALTER TABLE application_onboarding
    ADD CONSTRAINT onboarding_candidate_scope FOREIGN KEY(application_id,environment,candidate_configuration_id)
        REFERENCES application_provider_configurations(application_id,environment,id),
    ADD CONSTRAINT onboarding_current_scope FOREIGN KEY(application_id,environment,current_configuration_id)
        REFERENCES application_provider_configurations(application_id,environment,id),
    ADD CHECK (environment IS NOT NULL OR (candidate_configuration_id IS NULL AND current_configuration_id IS NULL));

CREATE FUNCTION protect_application_provider_configuration() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF TG_OP <> 'UPDATE' THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='immutable provider configuration';
    END IF;
    IF (NEW.id,NEW.application_id,NEW.version,NEW.environment,NEW.target_project_id,
        NEW.service_account_email,NEW.file_reference,NEW.created_by,NEW.created_at)
       IS DISTINCT FROM
       (OLD.id,OLD.application_id,OLD.version,OLD.environment,OLD.target_project_id,
        OLD.service_account_email,OLD.file_reference,OLD.created_by,OLD.created_at) THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='immutable provider configuration';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER protect_application_provider_configuration BEFORE UPDATE OR DELETE ON application_provider_configurations
    FOR EACH ROW EXECUTE FUNCTION protect_application_provider_configuration();
CREATE TRIGGER protect_application_provider_configuration_history BEFORE TRUNCATE ON application_provider_configurations
    FOR EACH STATEMENT EXECUTE FUNCTION protect_application_provider_configuration();

CREATE TABLE application_onboarding_ingestions (
    id UUID PRIMARY KEY,
    application_id BIGINT NOT NULL REFERENCES application_onboarding(application_id) ON DELETE RESTRICT,
    actor_id BIGINT NOT NULL CHECK(actor_id > 0),
    session_id UUID NOT NULL,
    session_epoch UUID NOT NULL,
    auth_epoch BIGINT NOT NULL CHECK(auth_epoch > 0),
    expected_revision BIGINT NOT NULL CHECK(expected_revision > 0),
    idempotency_key UUID NOT NULL,
    request_fingerprint BYTEA NOT NULL CHECK(octet_length(request_fingerprint)=32),
    fingerprint_key_version INTEGER NOT NULL CHECK(fingerprint_key_version > 0),
    configuration_id UUID NOT NULL,
    environment TEXT NOT NULL CHECK(environment IN ('staging','production')),
    target_project_id TEXT NOT NULL CHECK(octet_length(target_project_id) BETWEEN 1 AND 128),
    service_account_email TEXT NOT NULL CHECK(octet_length(service_account_email) BETWEEN 1 AND 320),
    state TEXT NOT NULL DEFAULT 'RESERVED' CHECK(state IN ('RESERVED','COMMITTED','ABANDONED')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(application_id,id), UNIQUE(application_id,actor_id,idempotency_key),
    CHECK(expires_at > created_at)
);
CREATE INDEX application_onboarding_ingestion_expiry ON application_onboarding_ingestions(expires_at) WHERE state='RESERVED';

CREATE TABLE application_onboarding_approvals (
    id UUID PRIMARY KEY,
    application_id BIGINT NOT NULL REFERENCES application_onboarding(application_id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK(kind IN ('TEST_COHORT','PILOT','FULL')),
    configuration_id UUID NOT NULL,
    profile_revision BIGINT NOT NULL CHECK(profile_revision > 0),
    origin_epoch UUID NOT NULL, source_epoch UUID NOT NULL,
    application_revision BIGINT NOT NULL CHECK(application_revision > 0),
    dispatch_revision BIGINT NOT NULL CHECK(dispatch_revision > 0),
    cohort_id UUID,
    issuer_key_id TEXT NOT NULL CHECK(octet_length(issuer_key_id) BETWEEN 1 AND 128),
    evidence_sha256 BYTEA NOT NULL CHECK(octet_length(evidence_sha256)=32),
    issued_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ, consumed_at TIMESTAMPTZ,
    UNIQUE(application_id,id),
    FOREIGN KEY(application_id,configuration_id) REFERENCES application_provider_configurations(application_id,id),
    CHECK(expires_at > issued_at AND expires_at <= issued_at + interval '24 hours')
);

CREATE TABLE application_onboarding_operations (
    id UUID PRIMARY KEY,
    application_id BIGINT NOT NULL REFERENCES application_onboarding(application_id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK(kind IN ('VERIFY','ACTIVATE','PROMOTE','TEST_COHORT','RELEASE_PILOT','RELEASE_FULL')),
    state TEXT NOT NULL DEFAULT 'PENDING' CHECK(state IN ('PENDING','RUNNING','SUCCEEDED','FAILED','SUPERSEDED')),
    actor_id BIGINT NOT NULL CHECK(actor_id > 0), session_id UUID NOT NULL, session_epoch UUID NOT NULL,
    auth_epoch BIGINT NOT NULL CHECK(auth_epoch > 0),
    idempotency_key UUID NOT NULL,
    request_fingerprint BYTEA NOT NULL CHECK(octet_length(request_fingerprint)=32),
    fingerprint_key_version INTEGER NOT NULL CHECK(fingerprint_key_version > 0),
    configuration_id UUID NOT NULL, approval_id UUID, cohort_id UUID, test_installation_id UUID,
    installation_ids UUID[] NOT NULL DEFAULT '{}', reason TEXT NOT NULL DEFAULT '' CHECK(octet_length(reason)<=256),
    expected_setup_revision BIGINT NOT NULL CHECK(expected_setup_revision > 0),
    expected_application_revision BIGINT NOT NULL CHECK(expected_application_revision > 0),
    expected_dispatch_revision BIGINT NOT NULL CHECK(expected_dispatch_revision>=0 AND (kind='VERIFY' OR expected_dispatch_revision>0)),
    origin_epoch UUID NOT NULL, source_epoch UUID NOT NULL,
    lease_owner UUID, lease_token BIGINT NOT NULL DEFAULT 0 CHECK(lease_token >= 0), lease_expires_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts >= 0),
    not_before TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), completed_at TIMESTAMPTZ, completed_by UUID,
    resulting_revision BIGINT CHECK(resulting_revision > 0), failure_code TEXT,
    UNIQUE(application_id,id), UNIQUE(application_id,actor_id,kind,idempotency_key),
    FOREIGN KEY(application_id,configuration_id) REFERENCES application_provider_configurations(application_id,id),
    FOREIGN KEY(application_id,approval_id) REFERENCES application_onboarding_approvals(application_id,id),
    CHECK((state IN ('SUCCEEDED','FAILED','SUPERSEDED'))=(completed_at IS NOT NULL)),
    CHECK((lease_owner IS NULL)=(lease_expires_at IS NULL)),
    CHECK(state<>'RUNNING' OR lease_owner IS NOT NULL),
    CHECK(failure_code IS NULL OR failure_code ~ '^[A-Z][A-Z0-9_]{0,63}$')
);
CREATE UNIQUE INDEX application_onboarding_one_mutation ON application_onboarding_operations(application_id)
    WHERE state IN ('PENDING','RUNNING') AND kind <> 'VERIFY';
CREATE UNIQUE INDEX application_onboarding_one_verification ON application_onboarding_operations(application_id,configuration_id)
    WHERE state IN ('PENDING','RUNNING') AND kind='VERIFY';
CREATE INDEX application_onboarding_operation_queue ON application_onboarding_operations(state,not_before,id)
    WHERE state IN ('PENDING','RUNNING');
CREATE INDEX application_onboarding_operation_history ON application_onboarding_operations(application_id,created_at DESC,id);

CREATE TABLE application_provider_checks (
    id UUID PRIMARY KEY,
    application_id BIGINT NOT NULL,
    configuration_id UUID NOT NULL,
    operation_id UUID NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('LOCAL_FORMAT','OAUTH','TARGET_PERMISSION','DEVICE_DELIVERY')),
    outcome TEXT NOT NULL CHECK(outcome IN ('NOT_CHECKED','PASSED','FAILED','INCONCLUSIVE')),
    origin_epoch UUID NOT NULL, source_epoch UUID NOT NULL,
    binding_id BIGINT, binding_epoch BIGINT, route_revision BIGINT,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), expires_at TIMESTAMPTZ NOT NULL,
    failure_code TEXT CHECK(failure_code IS NULL OR failure_code ~ '^[A-Z][A-Z0-9_]{0,63}$'),
    UNIQUE(application_id,id), UNIQUE(application_id,operation_id,kind),
    FOREIGN KEY(application_id,configuration_id) REFERENCES application_provider_configurations(application_id,id),
    FOREIGN KEY(application_id,operation_id) REFERENCES application_onboarding_operations(application_id,id),
    FOREIGN KEY(application_id,binding_id) REFERENCES installation_bindings(application_id,id),
    CHECK(expires_at>checked_at),
    CHECK((binding_id IS NULL)=(binding_epoch IS NULL)), CHECK(binding_epoch IS NULL OR binding_epoch > 0)
);
CREATE INDEX application_provider_checks_latest ON application_provider_checks(application_id,configuration_id,kind,checked_at DESC);

CREATE TABLE application_runtime_acknowledgements (
    application_id BIGINT NOT NULL,
    component TEXT NOT NULL CHECK(component IN ('SERVER','ADMISSION','DELIVERY','SCHEDULER')),
    generation BIGINT NOT NULL CHECK(generation > 0),
    configuration_id UUID NOT NULL,
    profile_revision BIGINT NOT NULL CHECK(profile_revision > 0),
    policy_sha256 BYTEA NOT NULL CHECK(octet_length(policy_sha256)=32),
    origin_epoch UUID NOT NULL, source_epoch UUID NOT NULL,
    executor_id UUID NOT NULL, lease_expires_at TIMESTAMPTZ NOT NULL,
    acknowledged_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(application_id,component,generation),
    FOREIGN KEY(application_id,configuration_id) REFERENCES application_provider_configurations(application_id,id),
    CHECK(lease_expires_at>acknowledged_at)
);

ALTER TABLE audit_logs DROP CONSTRAINT audit_application_scope_required;
ALTER TABLE audit_logs ADD CONSTRAINT audit_application_scope_required CHECK(
    entity_type NOT IN ('application','application_grant','application_onboarding') OR application_id IS NOT NULL
);

-- Role capabilities are installed separately; a migration does not grant a runtime
-- secret ingestion, deployment, lifecycle or dispatch authority.
REVOKE ALL ON application_onboarding,application_provider_configurations,application_onboarding_ingestions,
    application_onboarding_operations,application_provider_checks,application_runtime_acknowledgements,
    application_onboarding_approvals FROM PUBLIC;

CREATE FUNCTION protect_application_onboarding_identity() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF (NEW.application_id,NEW.greenfield,NEW.created_at) IS DISTINCT FROM
       (OLD.application_id,OLD.greenfield,OLD.created_at)
       OR (OLD.environment IS NOT NULL AND NEW.environment IS DISTINCT FROM OLD.environment)
    THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='immutable onboarding identity'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER protect_application_onboarding_identity BEFORE UPDATE ON application_onboarding
    FOR EACH ROW EXECUTE FUNCTION protect_application_onboarding_identity();
REVOKE ALL ON FUNCTION initialize_application_onboarding() FROM PUBLIC;

CREATE TABLE application_onboarding_requests (
    application_id BIGINT NOT NULL REFERENCES application_onboarding(application_id) ON DELETE RESTRICT,
    actor_id BIGINT NOT NULL CHECK(actor_id>0),
    kind TEXT NOT NULL CHECK(kind IN ('ENROLLMENT','HOLD')),
    idempotency_key UUID NOT NULL,
    request_fingerprint BYTEA NOT NULL CHECK(octet_length(request_fingerprint)=32),
    fingerprint_key_version INTEGER NOT NULL CHECK(fingerprint_key_version>0),
    response_data JSONB NOT NULL CHECK(jsonb_typeof(response_data)='object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()+interval '24 hours',
    PRIMARY KEY(application_id,actor_id,kind,idempotency_key)
);
REVOKE ALL ON application_onboarding_requests FROM PUBLIC;

CREATE FUNCTION protect_application_onboarding_operation() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF TG_OP<>'UPDATE' OR
       (to_jsonb(OLD)-ARRAY['state','lease_owner','lease_token','lease_expires_at','attempts','not_before','completed_at','completed_by','resulting_revision','failure_code'])
       IS DISTINCT FROM
       (to_jsonb(NEW)-ARRAY['state','lease_owner','lease_token','lease_expires_at','attempts','not_before','completed_at','completed_by','resulting_revision','failure_code'])
    THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='immutable onboarding operation'; END IF;
    IF current_user::regrole::oid<>(SELECT relowner FROM pg_class WHERE oid=TG_RELID) AND
       (OLD.state NOT IN ('PENDING','RUNNING') OR NEW.state<>'SUPERSEDED' OR NEW.failure_code IS DISTINCT FROM 'DISPATCH_HELD'
        OR NEW.lease_owner IS NOT NULL OR NEW.lease_expires_at IS NOT NULL OR NEW.completed_at IS NULL
        OR (NEW.lease_token,NEW.attempts,NEW.not_before,NEW.completed_by,NEW.resulting_revision)
           IS DISTINCT FROM (OLD.lease_token,OLD.attempts,OLD.not_before,OLD.completed_by,OLD.resulting_revision))
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='onboarding completion unavailable'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER protect_application_onboarding_operation BEFORE UPDATE OR DELETE ON application_onboarding_operations
    FOR EACH ROW EXECUTE FUNCTION protect_application_onboarding_operation();
CREATE TRIGGER protect_application_onboarding_operation_history BEFORE TRUNCATE ON application_onboarding_operations
    FOR EACH STATEMENT EXECUTE FUNCTION protect_application_onboarding_operation();
