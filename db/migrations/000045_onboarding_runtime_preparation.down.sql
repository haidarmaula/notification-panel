DO $$ BEGIN
    IF EXISTS(SELECT FROM application_runtime_reservations)
       OR EXISTS(SELECT FROM application_runtime_executor_scopes)
       OR EXISTS(SELECT FROM application_runtime_capacity_profile)
    THEN RAISE EXCEPTION 'runtime preparation history and approved scopes require explicit recovery before rollback'; END IF;
END $$;
DROP FUNCTION release_application_onboarding_runtime(uuid,uuid,boolean);
DROP FUNCTION check_application_onboarding_runtime(uuid,uuid,bytea);
DROP FUNCTION reserve_application_onboarding_runtime(uuid,uuid,uuid,bigint,bigint,bigint,uuid,uuid);
DROP TABLE application_runtime_reservations;
DROP TABLE application_runtime_executor_scopes;
DROP TABLE application_runtime_capacity_profile;
DROP FUNCTION protect_application_runtime_preparation();
