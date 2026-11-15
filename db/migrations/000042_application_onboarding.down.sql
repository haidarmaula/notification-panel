DO $$ BEGIN
    IF EXISTS(SELECT FROM application_onboarding_requests) OR EXISTS(SELECT FROM application_provider_configurations) OR EXISTS(SELECT FROM application_onboarding_ingestions)
       OR EXISTS(SELECT FROM application_onboarding_operations) OR EXISTS(SELECT FROM application_onboarding_approvals)
       OR EXISTS(SELECT FROM application_runtime_acknowledgements)
    THEN RAISE EXCEPTION 'onboarding history requires explicit recovery before rollback'; END IF;
END $$;
ALTER TABLE audit_logs DROP CONSTRAINT audit_application_scope_required;
ALTER TABLE audit_logs ADD CONSTRAINT audit_application_scope_required CHECK(
    entity_type NOT IN ('application','application_grant') OR application_id IS NOT NULL
);
DROP TABLE application_onboarding_requests;
DROP TABLE application_runtime_acknowledgements;
DROP TABLE application_provider_checks;
DROP TABLE application_onboarding_operations;
DROP FUNCTION protect_application_onboarding_operation();
DROP TABLE application_onboarding_approvals;
DROP TABLE application_onboarding_ingestions;
ALTER TABLE application_onboarding DROP CONSTRAINT onboarding_candidate_scope,DROP CONSTRAINT onboarding_current_scope;
DROP TABLE application_provider_configurations;
DROP FUNCTION protect_application_provider_configuration();
DROP TRIGGER initialize_application_onboarding ON applications;
DROP FUNCTION initialize_application_onboarding();
DROP TABLE application_onboarding;
DROP FUNCTION protect_application_onboarding_identity();
