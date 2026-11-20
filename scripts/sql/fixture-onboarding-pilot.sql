-- Isolated fixture only: create protected reviewer/executor roles.
BEGIN;
CREATE ROLE fixture_route_issuer LOGIN;
CREATE ROLE fixture_route_operator LOGIN;
CREATE ROLE hello_dispatch_issuer NOLOGIN;
CREATE ROLE hello_dispatch_operator NOLOGIN;
CREATE ROLE fixture_dispatch_issuer LOGIN IN ROLE hello_dispatch_issuer;
CREATE ROLE fixture_dispatch_operator LOGIN IN ROLE hello_dispatch_operator;
GRANT INSERT(id,project_id,operator_role,application_revision,expected_revision,previous_mode,destination,cohort_id,
    origin_epoch,source_epoch,reason,legacy_evidence,consent_evidence,coverage_evidence,quota_evidence,expires_at)
    ON application_dispatch_grants TO fixture_dispatch_issuer;
GRANT EXECUTE ON FUNCTION apply_application_dispatch(uuid,uuid) TO fixture_dispatch_operator;
GRANT EXECUTE ON FUNCTION bind_onboarding_release(uuid,uuid,bigint),revoke_application_dispatch(uuid,uuid) TO fixture_dispatch_issuer;
GRANT EXECUTE ON FUNCTION apply_onboarding_release(uuid,uuid,uuid) TO fixture_dispatch_operator;
COMMIT;
