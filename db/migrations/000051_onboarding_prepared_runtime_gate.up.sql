CREATE TRIGGER protect_runtime_component_scope BEFORE INSERT OR UPDATE OR DELETE ON application_runtime_component_scopes
    FOR EACH ROW EXECUTE FUNCTION protect_application_worker_preflight_scope();
CREATE TRIGGER protect_runtime_component_scope_history BEFORE TRUNCATE ON application_runtime_component_scopes
    FOR EACH STATEMENT EXECUTE FUNCTION protect_application_worker_preflight_scope();

CREATE FUNCTION onboarding_runtime_prepared(p_application bigint,p_generation bigint,p_configuration uuid)
RETURNS boolean LANGUAGE sql VOLATILE SET search_path=pg_catalog,pg_temp AS $$
    SELECT EXISTS (
        SELECT
        FROM public.application_runtime_reservations reservation
        JOIN public.application_onboarding setup ON setup.application_id=reservation.application_id
        JOIN public.notification_runtime runtime ON runtime.singleton
        WHERE reservation.application_id=p_application AND reservation.generation=p_generation
            AND reservation.configuration_id=p_configuration AND reservation.released_at IS NULL
            AND reservation.prepare_before>clock_timestamp() AND NOT runtime.sealed AND NOT runtime.migration_hold
            AND (reservation.environment,reservation.setup_revision,reservation.origin_epoch,reservation.source_epoch)
                = (setup.environment,setup.revision,runtime.origin_epoch,runtime.source_epoch)
            AND setup.candidate_configuration_id=p_configuration AND setup.runtime_generation<p_generation
            AND 4=(
                SELECT count(*) FROM public.application_runtime_component_scopes scope
                JOIN pg_roles login ON login.oid=scope.login_role AND login.rolcanlogin
                    AND NOT login.rolsuper AND NOT login.rolcreaterole AND NOT login.rolcreatedb
                    AND NOT login.rolreplication AND NOT login.rolbypassrls
                JOIN public.application_runtime_acknowledgements ack
                    ON (ack.application_id,ack.generation,ack.component,ack.policy_sha256)
                     = (scope.application_id,scope.generation,scope.component,scope.policy_sha256)
                WHERE (scope.application_id,scope.generation)=(reservation.application_id,reservation.generation)
                    AND (ack.configuration_id,ack.profile_revision,ack.origin_epoch,ack.source_epoch)
                        = (reservation.configuration_id,reservation.profile_revision,reservation.origin_epoch,reservation.source_epoch)
                    AND ack.lease_expires_at>clock_timestamp()
                    AND CASE WHEN scope.component='DELIVERY' THEN
                        has_function_privilege(login.oid,'public.acknowledge_application_runtime(uuid,bigint,text,bytea,uuid)','EXECUTE')
                            AND EXISTS (SELECT FROM public.application_worker_preflight_scopes delivery
                                WHERE (delivery.application_id,delivery.generation,delivery.worker_role,delivery.delivery_policy_sha256)
                                    = (scope.application_id,scope.generation,scope.login_role,scope.policy_sha256)
                                    AND (delivery.configuration_id,delivery.profile_revision,delivery.environment)
                                        = (reservation.configuration_id,reservation.profile_revision,reservation.environment))
                        ELSE has_function_privilege(login.oid,'public.acknowledge_shared_application_runtime(text,bytea,uuid)','EXECUTE') END
            )
    )
$$;
REVOKE ALL ON FUNCTION onboarding_runtime_prepared(bigint,bigint,uuid) FROM PUBLIC;
