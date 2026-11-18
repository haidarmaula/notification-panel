-- Preparation authority reserves resources only. It cannot activate enrollment,
-- install a configuration pointer, acknowledge readiness or release dispatch.
-- The trusted owner installs one immutable host capacity profile and per-app
-- LOGIN scopes. No runtime receives table DML or scope-registration authority.
CREATE TABLE application_runtime_capacity_profile (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),
    environment TEXT NOT NULL CHECK(environment IN ('staging','production')),
    revision BIGINT NOT NULL CHECK(revision>0),
    max_applications INTEGER NOT NULL CHECK(max_applications BETWEEN 1 AND 100),
    max_overlap INTEGER NOT NULL CHECK(max_overlap BETWEEN 1 AND max_applications),
    max_database_connections INTEGER NOT NULL CHECK(max_database_connections BETWEEN 1 AND 10000),
    delivery_database_connections INTEGER NOT NULL CHECK(delivery_database_connections BETWEEN 2 AND 10),
    policy_sha256 BYTEA NOT NULL CHECK(octet_length(policy_sha256)=32),
    UNIQUE(environment,revision),
    CHECK((max_applications::bigint+max_overlap)*delivery_database_connections<=max_database_connections)
);

CREATE TABLE application_runtime_executor_scopes (
    role_id OID PRIMARY KEY,
    application_id BIGINT NOT NULL UNIQUE REFERENCES application_onboarding(application_id) ON DELETE RESTRICT,
    environment TEXT NOT NULL,
    profile_revision BIGINT NOT NULL,
    FOREIGN KEY(environment,profile_revision) REFERENCES application_runtime_capacity_profile(environment,revision)
);

CREATE TABLE application_runtime_reservations (
    id UUID PRIMARY KEY CHECK(id<>'00000000-0000-0000-0000-000000000000'),
    application_id BIGINT NOT NULL,
    configuration_id UUID NOT NULL,
    environment TEXT NOT NULL,
    generation BIGINT NOT NULL CHECK(generation>0),
    profile_revision BIGINT NOT NULL,
    setup_revision BIGINT NOT NULL CHECK(setup_revision>0),
    origin_epoch UUID NOT NULL,
    source_epoch UUID NOT NULL,
    executor_role OID NOT NULL,
    database_connections INTEGER NOT NULL CHECK(database_connections BETWEEN 2 AND 10),
    reserved_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    prepare_before TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()+interval '60 seconds',
    released_at TIMESTAMPTZ,
    UNIQUE(application_id,generation),
    FOREIGN KEY(application_id,environment,configuration_id)
        REFERENCES application_provider_configurations(application_id,environment,id),
    FOREIGN KEY(environment,profile_revision) REFERENCES application_runtime_capacity_profile(environment,revision),
    CHECK(prepare_before>reserved_at),
    CHECK(released_at IS NULL OR released_at>=reserved_at)
);
CREATE INDEX application_runtime_reservation_capacity ON application_runtime_reservations(application_id) WHERE released_at IS NULL;
REVOKE ALL ON application_runtime_capacity_profile,application_runtime_executor_scopes,application_runtime_reservations FROM PUBLIC;

CREATE FUNCTION protect_application_runtime_preparation() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF current_user::regrole::oid<>(SELECT relowner FROM pg_class WHERE oid=TG_RELID) THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='runtime preparation owner required';
    END IF;
    IF TG_OP='INSERT' THEN RETURN NEW; END IF;
    IF TG_TABLE_NAME='application_runtime_reservations' AND TG_OP='UPDATE'
       AND OLD.released_at IS NULL AND NEW.released_at IS NOT NULL
       AND (to_jsonb(OLD)-'released_at')=(to_jsonb(NEW)-'released_at')
    THEN RETURN NEW; END IF;
    RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='immutable runtime preparation evidence';
END;
$$;
REVOKE ALL ON FUNCTION protect_application_runtime_preparation() FROM PUBLIC;
CREATE TRIGGER protect_runtime_capacity_profile BEFORE INSERT OR UPDATE OR DELETE ON application_runtime_capacity_profile
    FOR EACH ROW EXECUTE FUNCTION protect_application_runtime_preparation();
CREATE TRIGGER protect_runtime_capacity_history BEFORE TRUNCATE ON application_runtime_capacity_profile
    FOR EACH STATEMENT EXECUTE FUNCTION protect_application_runtime_preparation();
CREATE TRIGGER protect_runtime_executor_scope BEFORE INSERT OR UPDATE OR DELETE ON application_runtime_executor_scopes
    FOR EACH ROW EXECUTE FUNCTION protect_application_runtime_preparation();
CREATE TRIGGER protect_runtime_executor_history BEFORE TRUNCATE ON application_runtime_executor_scopes
    FOR EACH STATEMENT EXECUTE FUNCTION protect_application_runtime_preparation();
CREATE TRIGGER protect_runtime_reservation BEFORE INSERT OR UPDATE OR DELETE ON application_runtime_reservations
    FOR EACH ROW EXECUTE FUNCTION protect_application_runtime_preparation();
CREATE TRIGGER protect_runtime_reservation_history BEFORE TRUNCATE ON application_runtime_reservations
    FOR EACH STATEMENT EXECUTE FUNCTION protect_application_runtime_preparation();

-- Roles are installed by the operator, as for the verifier profile. In particular
-- a migration does not create cluster-wide roles or grant membership to a LOGIN.
CREATE FUNCTION reserve_application_onboarding_runtime(
    p_project uuid,p_reservation uuid,p_configuration uuid,p_generation bigint,
    p_profile_revision bigint,p_setup_revision bigint,p_origin uuid,p_source uuid
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE app public.applications%ROWTYPE; setup public.application_onboarding%ROWTYPE;
    profile public.application_runtime_capacity_profile%ROWTYPE;
    existing public.application_runtime_reservations%ROWTYPE;
    origin uuid; source uuid; runtime_sealed boolean; runtime_held boolean;
    allocated bigint; applications bigint; overlap_count bigint; connections bigint; app_allocations bigint;
BEGIN
    IF p_reservation IS NULL OR p_reservation='00000000-0000-0000-0000-000000000000'
       OR p_generation IS NULL OR p_generation<=0 OR p_setup_revision IS NULL OR p_setup_revision<=0
       OR p_profile_revision IS NULL OR p_profile_revision<=0
       OR pg_has_role(session_user,to_regrole('hello_onboarding_runtime_executor'),'USAGE') IS DISTINCT FROM true
       OR pg_has_role(session_user,current_user,'MEMBER')
       OR NOT EXISTS(SELECT FROM pg_roles WHERE rolname='hello_onboarding_runtime_executor' AND NOT rolcanlogin
           AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls)
       OR NOT EXISTS(SELECT FROM pg_roles WHERE oid=session_user::regrole::oid AND rolcanlogin
           AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls)
       OR NOT EXISTS(SELECT FROM public.application_runtime_executor_scopes s JOIN public.applications a ON a.id=s.application_id
           WHERE s.role_id=session_user::regrole::oid AND a.public_id=p_project AND s.profile_revision=p_profile_revision)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='runtime preparation scope unavailable'; END IF;
    -- Match lifecycle lock order; the single profile row serializes the host-wide
    -- capacity check across different application LOGINs.
    SELECT origin_epoch,source_epoch,sealed,migration_hold INTO STRICT origin,source,runtime_sealed,runtime_held
        FROM public.notification_runtime WHERE singleton FOR SHARE;
    SELECT * INTO STRICT app FROM public.applications WHERE public_id=p_project FOR NO KEY UPDATE;
    SELECT * INTO STRICT setup FROM public.application_onboarding WHERE application_id=app.id FOR UPDATE;
    SELECT * INTO STRICT profile FROM public.application_runtime_capacity_profile WHERE singleton FOR UPDATE;
    SELECT * INTO existing FROM public.application_runtime_reservations WHERE id=p_reservation;
    IF FOUND THEN
        IF (existing.application_id,existing.configuration_id,existing.generation,existing.profile_revision,existing.setup_revision,
            existing.origin_epoch,existing.source_epoch,existing.executor_role)
           IS DISTINCT FROM (app.id,p_configuration,p_generation,p_profile_revision,p_setup_revision,p_origin,p_source,session_user::regrole::oid)
        THEN RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='runtime reservation replay conflict'; END IF;
        -- Replays are history lookups, never renewed launch permission. The caller
        -- must inspect the original deadline/release state before any host effect.
        RETURN existing.id;
    END IF;
    IF runtime_sealed OR runtime_held OR (p_origin,p_source) IS DISTINCT FROM (origin,source) OR setup.revision<>p_setup_revision
       OR profile.revision<>p_profile_revision OR profile.environment IS DISTINCT FROM setup.environment
       OR NOT EXISTS(SELECT FROM public.application_runtime_executor_scopes s WHERE s.role_id=session_user::regrole::oid
           AND s.application_id=app.id AND s.environment=profile.environment AND s.profile_revision=profile.revision)
       OR app.status NOT IN ('PROVISIONING','ACTIVE') OR p_generation<=setup.runtime_generation
       OR (p_configuration IS DISTINCT FROM setup.candidate_configuration_id AND p_configuration IS DISTINCT FROM setup.current_configuration_id)
       OR NOT EXISTS(SELECT FROM public.application_provider_configurations c WHERE c.application_id=app.id AND c.id=p_configuration
           AND c.environment=profile.environment AND c.state='VERIFIED')
    THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='runtime preparation snapshot unavailable'; END IF;
    SELECT count(*),count(DISTINCT application_id),coalesce(sum(database_connections),0)
        INTO allocated,applications,connections FROM public.application_runtime_reservations WHERE released_at IS NULL;
    SELECT count(*) INTO app_allocations FROM public.application_runtime_reservations WHERE application_id=app.id AND released_at IS NULL;
    overlap_count:=allocated-applications;
    IF app_allocations>=2 OR (app_allocations=0 AND applications>=profile.max_applications)
       OR (app_allocations>0 AND overlap_count>=profile.max_overlap)
       OR connections+profile.delivery_database_connections>profile.max_database_connections
    THEN RAISE EXCEPTION USING ERRCODE='53300',MESSAGE='runtime preparation capacity exhausted'; END IF;
    INSERT INTO public.application_runtime_reservations(id,application_id,configuration_id,environment,generation,profile_revision,
        setup_revision,origin_epoch,source_epoch,executor_role,database_connections)
    VALUES(p_reservation,app.id,p_configuration,profile.environment,p_generation,p_profile_revision,p_setup_revision,
        p_origin,p_source,session_user::regrole::oid,profile.delivery_database_connections);
    RETURN p_reservation;
END;
$$;
REVOKE ALL ON FUNCTION reserve_application_onboarding_runtime(uuid,uuid,uuid,bigint,bigint,bigint,uuid,uuid) FROM PUBLIC;

-- This check permits only stopped-container preparation. A database snapshot
-- cannot authorize a later start or establish that an external effect occurred.
CREATE FUNCTION check_application_onboarding_runtime(p_project uuid,p_reservation uuid,p_policy_sha256 bytea) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
BEGIN
    IF pg_has_role(session_user,to_regrole('hello_onboarding_runtime_executor'),'USAGE') IS DISTINCT FROM true
       OR pg_has_role(session_user,current_user,'MEMBER')
       OR NOT EXISTS(SELECT FROM pg_roles WHERE rolname='hello_onboarding_runtime_executor' AND NOT rolcanlogin
           AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls)
       OR NOT EXISTS(SELECT FROM pg_roles WHERE oid=session_user::regrole::oid AND rolcanlogin
           AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='runtime preparation scope unavailable'; END IF;
    RETURN EXISTS(
        SELECT FROM public.application_runtime_reservations reservation
        JOIN public.application_runtime_executor_scopes scope ON scope.role_id=session_user::regrole::oid
            AND scope.role_id=reservation.executor_role AND scope.application_id=reservation.application_id
            AND scope.environment=reservation.environment AND scope.profile_revision=reservation.profile_revision
        JOIN public.application_runtime_capacity_profile profile ON profile.environment=reservation.environment
            AND profile.revision=reservation.profile_revision AND profile.policy_sha256=p_policy_sha256
        JOIN public.applications app ON app.id=reservation.application_id AND app.public_id=p_project
            AND app.status IN ('PROVISIONING','ACTIVE')
        JOIN public.application_onboarding setup ON setup.application_id=reservation.application_id
            AND setup.revision=reservation.setup_revision AND setup.environment=reservation.environment
            AND reservation.generation>setup.runtime_generation
            AND reservation.configuration_id IN (setup.candidate_configuration_id,setup.current_configuration_id)
        JOIN public.application_provider_configurations config ON config.application_id=reservation.application_id
            AND config.id=reservation.configuration_id AND config.environment=reservation.environment AND config.state='VERIFIED'
        JOIN public.notification_runtime runtime ON runtime.singleton AND NOT runtime.sealed AND NOT runtime.migration_hold
            AND (runtime.origin_epoch,runtime.source_epoch)=(reservation.origin_epoch,reservation.source_epoch)
        WHERE reservation.id=p_reservation AND reservation.released_at IS NULL AND reservation.prepare_before>clock_timestamp()
    );
END;
$$;
REVOKE ALL ON FUNCTION check_application_onboarding_runtime(uuid,uuid,bytea) FROM PUBLIC;

-- Expiry never returns capacity automatically: a timed-out host call can leave
-- an uncertain container. Release additionally waits for the original effect
-- window plus 30 seconds, longer than the preparer's five-second HTTP timeout.
-- This timer does not prove Docker finished an uncertain request: the trusted
-- executor must quiesce live preparers, reconcile daemon effects and observe
-- absence after the grace period before attesting p_runtime_absent=true.
CREATE FUNCTION release_application_onboarding_runtime(p_project uuid,p_reservation uuid,p_runtime_absent boolean) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE app bigint; reservation public.application_runtime_reservations%ROWTYPE;
BEGIN
    IF p_runtime_absent IS DISTINCT FROM true
       OR pg_has_role(session_user,to_regrole('hello_onboarding_runtime_executor'),'USAGE') IS DISTINCT FROM true
       OR pg_has_role(session_user,current_user,'MEMBER')
       OR NOT EXISTS(SELECT FROM pg_roles WHERE rolname='hello_onboarding_runtime_executor' AND NOT rolcanlogin
           AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls)
       OR NOT EXISTS(SELECT FROM pg_roles WHERE oid=session_user::regrole::oid AND rolcanlogin
           AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls)
       OR NOT EXISTS(SELECT FROM public.application_runtime_executor_scopes s JOIN public.applications a ON a.id=s.application_id
           WHERE s.role_id=session_user::regrole::oid AND a.public_id=p_project)
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='runtime preparation scope unavailable'; END IF;
    SELECT id INTO STRICT app FROM public.applications WHERE public_id=p_project FOR NO KEY UPDATE;
    PERFORM singleton FROM public.application_runtime_capacity_profile WHERE singleton FOR UPDATE;
    SELECT * INTO reservation FROM public.application_runtime_reservations WHERE id=p_reservation FOR UPDATE;
    IF NOT FOUND OR reservation.application_id<>app OR reservation.executor_role<>session_user::regrole::oid
    THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='runtime preparation scope unavailable'; END IF;
    IF reservation.released_at IS NOT NULL THEN RETURN true; END IF;
    IF clock_timestamp()<reservation.prepare_before+interval '30 seconds' THEN
        RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='runtime preparation release grace incomplete';
    END IF;
    UPDATE public.application_runtime_reservations SET released_at=clock_timestamp() WHERE id=p_reservation;
    RETURN true;
END;
$$;
REVOKE ALL ON FUNCTION release_application_onboarding_runtime(uuid,uuid,boolean) FROM PUBLIC;
