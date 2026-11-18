-- Isolated fixture only: use the protected route and dispatch grants with synthetic evidence.
BEGIN;
CREATE ROLE fixture_route_issuer LOGIN;
CREATE ROLE fixture_route_operator LOGIN;
CREATE ROLE hello_dispatch_issuer NOLOGIN;
CREATE ROLE hello_dispatch_operator NOLOGIN;
CREATE ROLE fixture_dispatch_issuer LOGIN IN ROLE hello_dispatch_issuer;
CREATE ROLE fixture_dispatch_operator LOGIN IN ROLE hello_dispatch_operator;
GRANT INSERT ON binding_route_grants TO fixture_route_issuer;
GRANT EXECUTE ON FUNCTION apply_binding_route(uuid,uuid) TO fixture_route_operator;
GRANT INSERT(id,project_id,operator_role,application_revision,expected_revision,previous_mode,destination,cohort_id,
    origin_epoch,source_epoch,reason,legacy_evidence,consent_evidence,coverage_evidence,quota_evidence,expires_at)
    ON application_dispatch_grants TO fixture_dispatch_issuer;
GRANT EXECUTE ON FUNCTION apply_application_dispatch(uuid,uuid) TO fixture_dispatch_operator;
SELECT b.id AS binding FROM installation_bindings b JOIN mobile_installations i ON i.id=b.installation_id AND i.application_id=b.application_id
WHERE i.application_id=:app_id AND i.public_id=:'installation' AND b.retired_at IS NULL \gset
SELECT origin_epoch AS origin,source_epoch AS source FROM notification_runtime WHERE singleton \gset
SELECT a.revision,p.dispatch_revision FROM applications a JOIN application_dispatch_policy p ON p.application_id=a.id
WHERE a.id=:app_id AND a.status='ACTIVE' AND p.dispatch_mode='HELD' AND p.hold_new \gset
SELECT gen_random_uuid() AS cohort,gen_random_uuid() AS route_grant,gen_random_uuid() AS dispatch_grant \gset
SET SESSION AUTHORIZATION fixture_route_issuer;
INSERT INTO binding_route_grants(id,application_id,binding_id,operator_role,origin_epoch,source_epoch,expected_revision,
    destination,cohort_id,evidence_id,reason,expires_at)
VALUES(:'route_grant',:app_id,:binding,'fixture_route_operator'::regrole::oid,:'origin',:'source',0,
    'FCM_ACTIVE',:'cohort',gen_random_uuid(),'pilot',clock_timestamp()+interval '5 minutes');
RESET SESSION AUTHORIZATION;
SET SESSION AUTHORIZATION fixture_route_operator;
SELECT apply_binding_route(:'route_grant',:'app') AS route_revision \gset
RESET SESSION AUTHORIZATION;
SET SESSION AUTHORIZATION fixture_dispatch_issuer;
INSERT INTO application_dispatch_grants(id,project_id,operator_role,application_revision,expected_revision,previous_mode,
    destination,cohort_id,origin_epoch,source_epoch,reason,legacy_evidence,consent_evidence,coverage_evidence,quota_evidence,expires_at)
VALUES(:'dispatch_grant',:'app','fixture_dispatch_operator'::regrole::oid,:revision,:dispatch_revision,'HELD','PILOT',
    :'cohort',:'origin',:'source','pilot',sha256('legacy'),sha256('consent'),sha256('coverage'),sha256('quota'),clock_timestamp()+interval '5 minutes');
RESET SESSION AUTHORIZATION;
SET SESSION AUTHORIZATION fixture_dispatch_operator;
SELECT apply_application_dispatch(:'dispatch_grant',:'app') AS pilot_revision \gset
RESET SESSION AUTHORIZATION;
INSERT INTO notifications(application_id,title,body,payload,created_by,status,scheduled_at,deadline_at,origin_epoch,source_epoch)
SELECT :app_id,'Fixture pilot','Synthetic enrollment test','{"fixture":"integration"}'::jsonb,id,'SCHEDULED',
    clock_timestamp(),clock_timestamp()+interval '2 minutes',:'origin',:'source'
FROM staff_users WHERE email='activation@fixture.invalid' RETURNING id AS campaign \gset
INSERT INTO notification_targets(application_id,notification_id,target_type,user_id)
SELECT :app_id,:campaign,'INDIVIDUAL',owner_user_id FROM installation_bindings WHERE id=:binding AND application_id=:app_id;
COMMIT;
SELECT :campaign;
