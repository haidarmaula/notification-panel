CREATE TABLE application_runtime_component_scopes (
    application_id bigint NOT NULL,
    generation bigint NOT NULL,
    component text NOT NULL CHECK (component IN ('SERVER','ADMISSION','DELIVERY','SCHEDULER')),
    login_role oid NOT NULL,
    policy_sha256 bytea NOT NULL CHECK (octet_length(policy_sha256)=32),
    PRIMARY KEY (application_id,generation,component),
    UNIQUE (application_id,generation,login_role),
    FOREIGN KEY (application_id,generation) REFERENCES application_runtime_reservations(application_id,generation)
);
REVOKE ALL ON application_runtime_component_scopes FROM PUBLIC;

CREATE FUNCTION acknowledge_application_runtime(p_app uuid,p_generation bigint,p_component text,p_digest bytea,p_instance uuid)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET lock_timeout='2s' AS $$
DECLARE
    app public.applications%ROWTYPE;
    setup public.application_onboarding%ROWTYPE;
    runtime public.notification_runtime%ROWTYPE;
    reservation public.application_runtime_reservations%ROWTYPE;
    observed_at timestamptz;
BEGIN
    IF p_instance IS NULL OR p_instance='00000000-0000-0000-0000-000000000000' OR
       NOT EXISTS (SELECT FROM pg_roles WHERE oid=session_user::regrole::oid AND rolcanlogin
           AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls)
    THEN RETURN false; END IF;
    IF NOT EXISTS (SELECT FROM public.application_runtime_component_scopes scope
        JOIN public.applications a ON a.id=scope.application_id
        WHERE a.public_id=p_app AND scope.generation=p_generation AND scope.component=p_component
            AND scope.policy_sha256=p_digest AND scope.login_role=session_user::regrole::oid)
    THEN RETURN false; END IF;
    SELECT * INTO runtime FROM public.notification_runtime WHERE singleton FOR SHARE;
    IF NOT FOUND OR runtime.sealed OR runtime.migration_hold THEN RETURN false; END IF;
    SELECT * INTO app FROM public.applications WHERE public_id=p_app FOR SHARE;
    IF NOT FOUND OR app.status NOT IN ('PROVISIONING','ACTIVE') THEN RETURN false; END IF;
    SELECT * INTO setup FROM public.application_onboarding WHERE application_id=app.id FOR SHARE;
    IF NOT FOUND THEN RETURN false; END IF;
    SELECT * INTO reservation FROM public.application_runtime_reservations
    WHERE application_id=app.id AND generation=p_generation AND released_at IS NULL FOR SHARE;
    IF NOT FOUND OR (reservation.origin_epoch,reservation.source_epoch,reservation.environment) IS DISTINCT FROM
        (runtime.origin_epoch,runtime.source_epoch,setup.environment) THEN RETURN false; END IF;
    IF (
        (setup.runtime_generation=p_generation AND app.status='ACTIVE' AND setup.current_configuration_id=reservation.configuration_id)
        OR (setup.runtime_generation<p_generation AND setup.revision=reservation.setup_revision AND
            setup.candidate_configuration_id=reservation.configuration_id AND reservation.prepare_before>clock_timestamp())
    ) IS NOT TRUE OR NOT EXISTS (SELECT FROM public.application_provider_configurations c WHERE c.application_id=app.id
        AND c.id=reservation.configuration_id AND c.state='VERIFIED') THEN RETURN false; END IF;
    PERFORM 1 FROM public.application_runtime_component_scopes scope
    WHERE scope.application_id=app.id AND scope.generation=p_generation AND scope.component=p_component
        AND scope.policy_sha256=p_digest AND scope.login_role=session_user::regrole::oid FOR SHARE;
    IF NOT FOUND THEN RETURN false; END IF;
    IF p_component='DELIVERY' AND NOT EXISTS (SELECT FROM public.application_worker_preflight_scopes scope
        WHERE scope.application_id=app.id AND scope.generation=p_generation AND scope.worker_role=session_user::regrole::oid
            AND scope.delivery_policy_sha256=p_digest AND scope.configuration_id=reservation.configuration_id
            AND scope.profile_revision=reservation.profile_revision AND scope.environment=reservation.environment)
    THEN RETURN false; END IF;

    observed_at := clock_timestamp();
    INSERT INTO public.application_runtime_acknowledgements AS ack
        (application_id,component,generation,configuration_id,profile_revision,policy_sha256,origin_epoch,source_epoch,
         executor_id,lease_expires_at,acknowledged_at)
    VALUES (app.id,p_component,p_generation,reservation.configuration_id,reservation.profile_revision,p_digest,
        runtime.origin_epoch,runtime.source_epoch,p_instance,observed_at+interval '15 seconds',observed_at)
    ON CONFLICT (application_id,component,generation) DO UPDATE SET
        executor_id=EXCLUDED.executor_id,
        acknowledged_at=EXCLUDED.acknowledged_at,lease_expires_at=EXCLUDED.lease_expires_at
    WHERE (ack.executor_id=EXCLUDED.executor_id OR ack.lease_expires_at<=EXCLUDED.acknowledged_at)
        AND ack.acknowledged_at<=EXCLUDED.acknowledged_at
        AND ack.policy_sha256=EXCLUDED.policy_sha256
        AND (ack.configuration_id,ack.profile_revision,ack.origin_epoch,ack.source_epoch) IS NOT DISTINCT FROM
            (EXCLUDED.configuration_id,EXCLUDED.profile_revision,EXCLUDED.origin_epoch,EXCLUDED.source_epoch);
    RETURN FOUND;
END;
$$;
REVOKE ALL ON FUNCTION acknowledge_application_runtime(uuid,bigint,text,bytea,uuid) FROM PUBLIC;
