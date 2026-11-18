-- Worker preflight approval binds a reservation's scope to a single protected
-- delivery policy and worker LOGIN. The schema owner installs this metadata;
-- neither the HTTP server nor the host preparer can write approvals.
CREATE TABLE application_worker_preflight_scopes (
    application_id BIGINT NOT NULL,
    configuration_id UUID NOT NULL,
    environment TEXT NOT NULL,
    generation BIGINT NOT NULL CHECK(generation>0),
    profile_revision BIGINT NOT NULL,
    delivery_policy_sha256 BYTEA NOT NULL CHECK(octet_length(delivery_policy_sha256)=32),
    worker_role OID NOT NULL UNIQUE,
    PRIMARY KEY(application_id,generation),
    FOREIGN KEY(application_id,environment,configuration_id)
        REFERENCES application_provider_configurations(application_id,environment,id),
    FOREIGN KEY(environment,profile_revision) REFERENCES application_runtime_capacity_profile(environment,revision)
);
REVOKE ALL ON application_worker_preflight_scopes FROM PUBLIC;

CREATE FUNCTION protect_application_worker_preflight_scope() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF current_user::regrole::oid<>(SELECT relowner FROM pg_class WHERE oid=TG_RELID) THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='worker preflight scope owner required';
    END IF;
    IF TG_OP='INSERT' THEN RETURN NEW; END IF;
    RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='immutable worker preflight scope';
END;
$$;
REVOKE ALL ON FUNCTION protect_application_worker_preflight_scope() FROM PUBLIC;
CREATE TRIGGER protect_worker_preflight_scope BEFORE INSERT OR UPDATE OR DELETE ON application_worker_preflight_scopes
    FOR EACH ROW EXECUTE FUNCTION protect_application_worker_preflight_scope();
CREATE TRIGGER protect_worker_preflight_history BEFORE TRUNCATE ON application_worker_preflight_scopes
    FOR EACH STATEMENT EXECUTE FUNCTION protect_application_worker_preflight_scope();

-- This point-in-time authorization covers bounded, one-shot worker preflight.
-- It does not grant start authority for the normal delivery loop or produce a
-- readiness acknowledgement. The worker still validates its own actual LOGIN.
CREATE FUNCTION check_application_onboarding_worker_preflight(
    p_project uuid,p_reservation uuid,p_policy_sha256 bytea,p_target_project text,
    p_service_account_email text,p_delivery_policy_sha256 bytea,p_worker_role text
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
BEGIN
    IF NOT public.check_application_onboarding_runtime(p_project,p_reservation,p_policy_sha256) THEN
        RETURN false;
    END IF;
    RETURN EXISTS(
        SELECT FROM public.application_runtime_reservations reservation
        JOIN public.application_worker_preflight_scopes scope
            ON (scope.application_id,scope.configuration_id,scope.environment,scope.generation,scope.profile_revision)
             = (reservation.application_id,reservation.configuration_id,reservation.environment,reservation.generation,reservation.profile_revision)
            AND scope.delivery_policy_sha256=p_delivery_policy_sha256
        JOIN public.application_provider_configurations configuration ON configuration.application_id=scope.application_id
            AND configuration.id=scope.configuration_id AND configuration.environment=scope.environment
            AND configuration.id=configuration.file_reference
            AND configuration.target_project_id COLLATE "C"=p_target_project COLLATE "C"
            AND configuration.service_account_email COLLATE "C"=p_service_account_email COLLATE "C"
        JOIN pg_roles worker ON worker.oid=scope.worker_role AND worker.rolname::text COLLATE "C"=p_worker_role COLLATE "C"
            AND worker.rolcanlogin AND NOT worker.rolsuper AND NOT worker.rolcreaterole AND NOT worker.rolcreatedb
            AND NOT worker.rolreplication AND NOT worker.rolbypassrls AND worker.oid<>reservation.executor_role
        JOIN pg_roles delivery ON delivery.rolname='hello_delivery' AND NOT delivery.rolcanlogin
            AND NOT delivery.rolsuper AND NOT delivery.rolcreaterole AND NOT delivery.rolcreatedb
            AND NOT delivery.rolreplication AND NOT delivery.rolbypassrls
        WHERE reservation.id=p_reservation
            AND pg_has_role(worker.oid,delivery.oid,'USAGE')
            AND NOT EXISTS(SELECT FROM pg_auth_members WHERE member=worker.oid AND roleid<>delivery.oid)
            AND NOT EXISTS(SELECT FROM pg_auth_members WHERE member=delivery.oid)
            AND NOT EXISTS(SELECT FROM pg_shdepend WHERE refclassid='pg_authid'::regclass
                AND refobjid IN (worker.oid,delivery.oid) AND deptype='o')
    );
END;
$$;
REVOKE ALL ON FUNCTION check_application_onboarding_worker_preflight(uuid,uuid,bytea,text,text,bytea,text) FROM PUBLIC;
