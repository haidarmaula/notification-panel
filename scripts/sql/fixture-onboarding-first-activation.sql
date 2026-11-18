-- Synthetic test data only; the operator transition runs under its own LOGIN.
BEGIN;
\i /fixture-sql/finalize-application-ownership.sql
\i /fixture-sql/install-application-lifecycle.sql
CREATE ROLE hello_application_lifecycle_issuer NOLOGIN;
CREATE ROLE hello_application_lifecycle_operator NOLOGIN;
CREATE ROLE fixture_activation_issuer LOGIN IN ROLE hello_application_lifecycle_issuer;
CREATE ROLE fixture_activation_operator LOGIN IN ROLE hello_application_lifecycle_operator;
GRANT INSERT ON application_lifecycle_grants TO fixture_activation_issuer;
GRANT EXECUTE ON FUNCTION apply_application_lifecycle(uuid,uuid),apply_onboarding_first_activation(uuid,uuid,uuid) TO fixture_activation_operator;
INSERT INTO staff_users(role_id,name,email,password_hash)
SELECT id,'Activation Fixture','activation@fixture.invalid','fixture' FROM roles WHERE name='SUPER_ADMIN' RETURNING id AS actor \gset
INSERT INTO staff_sessions(id,staff_user_id,auth_epoch,session_epoch,refresh_digest,expires_at)
SELECT gen_random_uuid(),s.id,s.auth_epoch,c.session_epoch,sha256('fixture'),clock_timestamp()+interval '10 minutes'
FROM staff_users s CROSS JOIN staff_session_control c WHERE s.id=:actor AND c.singleton
RETURNING id AS session,auth_epoch,session_epoch \gset
UPDATE application_onboarding SET enrollment_profile_id='50000000-0000-4000-8000-000000000001',enrollment_platforms='[{}]' WHERE application_id=:app_id;
INSERT INTO application_onboarding_operations(id,application_id,kind,state,actor_id,session_id,session_epoch,auth_epoch,
    idempotency_key,request_fingerprint,fingerprint_key_version,configuration_id,expected_setup_revision,
    expected_application_revision,expected_dispatch_revision,origin_epoch,source_epoch,completed_at)
SELECT gen_random_uuid(),:app_id,'VERIFY','SUCCEEDED',:actor,:'session',:'session_epoch',:auth_epoch,gen_random_uuid(),sha256('verify'),1,
    :'config',1,1,0,origin_epoch,source_epoch,clock_timestamp() FROM notification_runtime WHERE singleton RETURNING id AS verify_operation \gset
INSERT INTO application_provider_checks(id,application_id,configuration_id,operation_id,kind,outcome,origin_epoch,source_epoch,expires_at)
SELECT gen_random_uuid(),:app_id,:'config',:'verify_operation','OAUTH','PASSED',origin_epoch,source_epoch,
    clock_timestamp()+interval '15 minutes' FROM notification_runtime WHERE singleton;
INSERT INTO application_onboarding_operations(id,application_id,kind,actor_id,session_id,session_epoch,auth_epoch,
    idempotency_key,request_fingerprint,fingerprint_key_version,configuration_id,expected_setup_revision,
    expected_application_revision,expected_dispatch_revision,origin_epoch,source_epoch)
SELECT gen_random_uuid(),:app_id,'ACTIVATE',:actor,:'session',:'session_epoch',:auth_epoch,gen_random_uuid(),sha256('activate'),1,
    :'config',s.revision,a.revision,p.dispatch_revision,r.origin_epoch,r.source_epoch
FROM applications a JOIN application_onboarding s ON a.id=s.application_id JOIN application_dispatch_policy p ON p.application_id=a.id
CROSS JOIN notification_runtime r WHERE a.id=:app_id AND r.singleton RETURNING id AS operation \gset
SELECT origin_epoch AS origin,source_epoch AS source FROM notification_runtime WHERE singleton \gset
SELECT gen_random_uuid() AS lifecycle_grant,gen_random_uuid() AS instance \gset
SET SESSION AUTHORIZATION fixture_activation_issuer;
INSERT INTO application_lifecycle_grants(id,project_id,operator_role,origin_epoch,source_epoch,expected_revision,previous_status,
    destination,ownership_evidence,writer_fence_evidence,cohort_fence_evidence,retired_writer_roles,expires_at)
VALUES(:'lifecycle_grant',:'app','fixture_activation_operator'::regrole::oid,:'origin',:'source',1,'PROVISIONING','ACTIVE',
    sha256('owner'),sha256('writers'),sha256('cohort'),'{}',clock_timestamp()+interval '10 minutes');
RESET SESSION AUTHORIZATION;
INSERT INTO application_onboarding_lifecycle_delegations VALUES(:'lifecycle_grant',:'operation','50000000-0000-4000-8000-000000000001',1);
SELECT set_config('fixture.lifecycle_grant',:'lifecycle_grant',true),set_config('fixture.app',:'app',true);
SET SESSION AUTHORIZATION fixture_activation_operator;
DO $$
BEGIN
    PERFORM apply_application_lifecycle(current_setting('fixture.lifecycle_grant')::uuid,current_setting('fixture.app')::uuid);
    RAISE EXCEPTION 'raw lifecycle bypassed the delegated activation';
EXCEPTION WHEN insufficient_privilege THEN NULL;
END;
$$;
SELECT apply_onboarding_first_activation(:'operation',:'lifecycle_grant',:'instance') AS first_revision \gset
SELECT apply_onboarding_first_activation(:'operation',:'lifecycle_grant',:'instance')=:first_revision AS activation_replayed \gset
RESET SESSION AUTHORIZATION;
SELECT a.status='ACTIVE' AND p.dispatch_mode='HELD' AND p.hold_new AND s.current_configuration_id=:'config' AND s.runtime_generation=1
    AND (SELECT count(*) FROM audit_logs WHERE application_id=a.id AND after_json->>'operation_id'=:'operation')=1 AS activation_applied
FROM applications a JOIN application_onboarding s ON a.id=s.application_id JOIN application_dispatch_policy p ON p.application_id=a.id WHERE a.id=:app_id \gset
\if :activation_replayed
\else
SELECT 1/0;
\endif
\if :activation_applied
\else
SELECT 1/0;
\endif
\if :commit_activation
COMMIT;
\else
ROLLBACK;
\endif
