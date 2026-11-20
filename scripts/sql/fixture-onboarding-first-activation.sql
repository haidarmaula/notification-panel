-- Synthetic test data only; the operator transition runs under its own LOGIN.
BEGIN;
\i /fixture-sql/finalize-application-ownership.sql
\i /fixture-sql/install-application-lifecycle.sql
CREATE ROLE hello_application_lifecycle_issuer NOLOGIN;
CREATE ROLE hello_application_lifecycle_operator NOLOGIN;
CREATE ROLE hello_onboarding_activation_issuer NOLOGIN;
CREATE ROLE fixture_activation_issuer LOGIN IN ROLE hello_onboarding_activation_issuer;
CREATE ROLE fixture_activation_operator LOGIN IN ROLE hello_application_lifecycle_operator;
GRANT EXECUTE ON FUNCTION revoke_application_lifecycle(uuid,uuid),
    issue_onboarding_first_activation(uuid,uuid,uuid,text,bigint,bytea,bytea,bytea,name[]) TO fixture_activation_issuer;
GRANT EXECUTE ON FUNCTION apply_application_lifecycle(uuid,uuid),apply_onboarding_first_activation(uuid,uuid,uuid) TO fixture_activation_operator;
INSERT INTO application_onboarding_activation_issuer_scopes(application_id,issuer_role,executor_role)
VALUES(:app_id,'fixture_activation_issuer'::regrole::oid,'fixture_activation_operator'::regrole::oid);
\if :{?activation_operation}
SELECT :'activation_operation'::uuid AS operation \gset
\else
INSERT INTO staff_users(role_id,name,email,password_hash)
SELECT id,'Activation Fixture','activation@fixture.invalid','fixture' FROM roles WHERE name='SUPER_ADMIN' RETURNING id AS actor \gset
INSERT INTO staff_sessions(id,staff_user_id,auth_epoch,session_epoch,refresh_digest,expires_at)
SELECT gen_random_uuid(),s.id,s.auth_epoch,c.session_epoch,sha256('fixture'),clock_timestamp()+interval '10 minutes'
FROM staff_users s CROSS JOIN staff_session_control c WHERE s.id=:actor AND c.singleton
RETURNING id AS session,auth_epoch,session_epoch \gset
UPDATE application_onboarding SET enrollment_profile_id='50000000-0000-4000-8000-000000000001',
    enrollment_platforms='[{"platform":"ANDROID"}]' WHERE application_id=:app_id;
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
\endif
SELECT gen_random_uuid() AS revoked_grant,gen_random_uuid() AS lifecycle_grant,gen_random_uuid() AS instance \gset
SET SESSION AUTHORIZATION fixture_activation_issuer;
SELECT issue_onboarding_first_activation(:'app',:'operation',:'revoked_grant','fixture_activation_operator',1,
    sha256('owner'),sha256('writers'),sha256('cohort'),'{}');
SELECT issue_onboarding_first_activation(:'app',:'operation',:'revoked_grant','fixture_activation_operator',1,
    sha256('owner'),sha256('writers'),sha256('cohort'),'{}')=:'revoked_grant' AS issuance_replayed \gset
SELECT revoke_application_lifecycle(:'revoked_grant',:'app');
SELECT issue_onboarding_first_activation(:'app',:'operation',:'lifecycle_grant','fixture_activation_operator',1,
    sha256('owner'),sha256('writers'),sha256('cohort'),'{}');
RESET SESSION AUTHORIZATION;
\if :issuance_replayed
\else
SELECT 1/0;
\endif
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
\if :{?test_cohort}
CREATE ROLE fixture_cohort_coordinator LOGIN;
CREATE ROLE fixture_cohort_verifier LOGIN;
CREATE ROLE fixture_cohort_unscoped LOGIN;
GRANT EXECUTE ON FUNCTION initialize_onboarding_cohort(uuid,text,uuid,uuid),
    finalize_onboarding_cohort(uuid,text,uuid,uuid,bigint),
    find_onboarding_cohort_operation(uuid,text) TO fixture_cohort_coordinator,fixture_cohort_unscoped;
GRANT EXECUTE ON FUNCTION claim_onboarding_cohort_target(uuid,text,uuid),
    finish_onboarding_cohort_target(uuid,text,uuid,uuid,uuid,uuid,uuid,bigint,uuid,bigint,text,text) TO fixture_cohort_verifier;
INSERT INTO application_onboarding_cohort_scopes VALUES
    ('fixture_cohort_coordinator'::regrole::oid,:app_id,'staging','COORDINATOR'),
    ('fixture_cohort_verifier'::regrole::oid,:app_id,'staging','VERIFIER');
INSERT INTO users(application_id,external_id) VALUES(:app_id,'cohort-owner') RETURNING id AS member_owner \gset
INSERT INTO mobile_installations(application_id,owner_user_id,proof_verifier)
    SELECT :app_id,:member_owner,sha256(convert_to(n::text,'UTF8')) FROM generate_series(1,2) n;
INSERT INTO installation_bindings(application_id,installation_id,owner_user_id,owner_epoch,platform,
    provider_namespace,provider_route_key,address_type,address_fingerprint,address_ciphertext,encryption_key_version)
SELECT :app_id,id,owner_user_id,owner_epoch,'ANDROID','fixture-namespace','fixture-key','FCM_FID',sha256(convert_to(id::text,'UTF8')),sha256('synthetic'),1
FROM mobile_installations WHERE application_id=:app_id;
CREATE ROLE fixture_cohort_importer LOGIN;
GRANT EXECUTE ON FUNCTION approve_onboarding_cohort(uuid,text,uuid,uuid,uuid[],text,bytea,timestamptz)
    TO fixture_cohort_importer,fixture_cohort_unscoped;
INSERT INTO application_onboarding_cohort_scopes VALUES
    ('fixture_cohort_importer'::regrole::oid,:app_id,'staging','APPROVAL_IMPORTER');
SELECT array_agg(public_id ORDER BY public_id) AS bindings FROM mobile_installations WHERE application_id=:app_id \gset
SELECT set_config('fixture.approval_config',:'config',true),set_config('fixture.approval_bindings',:'bindings',true),
    set_config('fixture.rejected_approval',gen_random_uuid()::text,true);
SET SESSION AUTHORIZATION fixture_cohort_unscoped;
DO $$ BEGIN
    PERFORM approve_onboarding_cohort(current_setting('fixture.app')::uuid,'staging',gen_random_uuid(),
        current_setting('fixture.approval_config')::uuid,current_setting('fixture.approval_bindings')::uuid[],
        'fixture',sha256('membership'),clock_timestamp()+interval '5 minutes');
    RAISE EXCEPTION 'unscoped login provisioned an approval';
EXCEPTION WHEN insufficient_privilege THEN NULL;
END $$;
RESET SESSION AUTHORIZATION;
SET SESSION AUTHORIZATION fixture_cohort_importer;
DO $$ BEGIN
    PERFORM approve_onboarding_cohort(current_setting('fixture.app')::uuid,'staging',
        current_setting('fixture.rejected_approval')::uuid,current_setting('fixture.approval_config')::uuid,
        current_setting('fixture.approval_bindings')::uuid[] || ARRAY[gen_random_uuid()],
        'fixture',sha256('membership'),clock_timestamp()+interval '5 minutes');
    RAISE EXCEPTION 'invalid roster was accepted';
EXCEPTION WHEN invalid_parameter_value THEN NULL;
END $$;
SELECT gen_random_uuid() AS approval,clock_timestamp()+interval '5 minutes' AS approval_expiry \gset
SELECT 1/(approve_onboarding_cohort(:'app','staging',:'approval',:'config',:'bindings',
    'fixture',sha256('membership'),:'approval_expiry')=:'approval')::int AS approval_created \gset
SELECT 1/(approve_onboarding_cohort(:'app','staging',:'approval',:'config',:'bindings',
    'fixture',sha256('membership'),:'approval_expiry')=:'approval')::int AS approval_replayed \gset
RESET SESSION AUTHORIZATION;
SELECT 1/(NOT EXISTS(SELECT FROM application_onboarding_approvals
    WHERE id=current_setting('fixture.rejected_approval')::uuid))::int AS rejected_approval_atomic \gset
SAVEPOINT cohort_platform_scope;
INSERT INTO mobile_installations(application_id,owner_user_id,proof_verifier)
VALUES(:app_id,:member_owner,sha256('ios-proof')) RETURNING id AS ios_installation,public_id AS ios_public,owner_epoch AS ios_epoch \gset
INSERT INTO installation_bindings(application_id,installation_id,owner_user_id,owner_epoch,platform,
    provider_namespace,provider_route_key,address_type,address_fingerprint,address_ciphertext,encryption_key_version)
VALUES(:app_id,:ios_installation,:member_owner,:ios_epoch,'IOS','fixture-ios','fixture-ios-route',
    'FCM_FID',sha256('ios-address'),sha256('ios-ciphertext'),1);
SELECT set_config('fixture.ios_installation',:'ios_public',true);
SET SESSION AUTHORIZATION fixture_cohort_importer;
DO $$ BEGIN
    PERFORM approve_onboarding_cohort(current_setting('fixture.app')::uuid,'staging',gen_random_uuid(),
        current_setting('fixture.approval_config')::uuid,ARRAY[current_setting('fixture.ios_installation')::uuid],
        'fixture',sha256('ios-consent'),clock_timestamp()+interval '5 minutes');
    RAISE EXCEPTION 'approval accepted an unconfigured platform';
EXCEPTION WHEN invalid_parameter_value THEN NULL;
END $$;
RESET SESSION AUTHORIZATION;
UPDATE application_onboarding SET enrollment_platforms='[{"platform":"ANDROID"},{"platform":"IOS"}]' WHERE application_id=:app_id;
SET SESSION AUTHORIZATION fixture_cohort_importer;
SELECT approve_onboarding_cohort(:'app','staging',gen_random_uuid(),:'config',:'bindings'::uuid[] || ARRAY[:'ios_public'::uuid],
    'fixture',sha256('mixed-consent'),clock_timestamp()+interval '5 minutes') AS mixed_approval \gset
RESET SESSION AUTHORIZATION;
SELECT 1/(count(*)=3)::int AS configured_platforms_approved
FROM application_onboarding_approved_targets WHERE approval_id=:'mixed_approval' \gset
ROLLBACK TO SAVEPOINT cohort_platform_scope;
INSERT INTO application_onboarding_operations(id,application_id,kind,actor_id,session_id,session_epoch,auth_epoch,
    idempotency_key,request_fingerprint,fingerprint_key_version,configuration_id,approval_id,installation_ids,
    expected_setup_revision,expected_application_revision,expected_dispatch_revision,origin_epoch,source_epoch)
SELECT gen_random_uuid(),:app_id,'TEST_COHORT',:actor,:'session',:'session_epoch',:auth_epoch,gen_random_uuid(),sha256('cohort'),1,
    configuration_id,id,ARRAY(SELECT public_id FROM mobile_installations WHERE application_id=:app_id ORDER BY public_id DESC),
    (SELECT revision FROM application_onboarding WHERE application_id=:app_id),application_revision,dispatch_revision,origin_epoch,source_epoch
FROM application_onboarding_approvals WHERE id=:'approval' RETURNING id AS cohort_operation \gset
SELECT gen_random_uuid() AS coordinator,gen_random_uuid() AS takeover,gen_random_uuid() AS verifier \gset
SELECT set_config('fixture.cohort_op',:'cohort_operation',true);
SET SESSION AUTHORIZATION fixture_cohort_unscoped;
DO $$ BEGIN
    PERFORM initialize_onboarding_cohort(current_setting('fixture.app')::uuid,'staging',current_setting('fixture.cohort_op')::uuid,gen_random_uuid());
    RAISE EXCEPTION 'unscoped login initialized a cohort';
EXCEPTION WHEN insufficient_privilege THEN NULL;
END $$;
RESET SESSION AUTHORIZATION;
SAVEPOINT revoke_member;
UPDATE application_onboarding_approved_targets SET revoked_at=clock_timestamp()
WHERE approval_id=:'approval' AND installation_id=(SELECT min(installation_id) FROM application_onboarding_approved_targets WHERE approval_id=:'approval');
SET SESSION AUTHORIZATION fixture_cohort_coordinator;
SELECT initialize_onboarding_cohort(:'app','staging',:'cohort_operation',:'coordinator') IS NULL AS revoked_rejected \gset
RESET SESSION AUTHORIZATION;
SELECT state='SUPERSEDED' AND NOT EXISTS(SELECT FROM application_onboarding_cohort_targets WHERE operation_id=:'cohort_operation')
    AND (SELECT consumed_at IS NULL FROM application_onboarding_approvals WHERE id=:'approval') AS rejected_without_consuming
FROM application_onboarding_operations WHERE id=:'cohort_operation' \gset
ROLLBACK TO SAVEPOINT revoke_member;
SET SESSION AUTHORIZATION fixture_cohort_coordinator;
SELECT 1/(find_onboarding_cohort_operation(:'app','staging')=:'cohort_operation')::int AS discovered_pending_cohort \gset
SELECT initialize_onboarding_cohort(:'app','staging',:'cohort_operation',:'coordinator')=1
    AND NOT finalize_onboarding_cohort(:'app','staging',:'cohort_operation',:'coordinator',1) AS incomplete \gset
SELECT initialize_onboarding_cohort(:'app','staging',:'cohort_operation',:'coordinator')=1
    AND initialize_onboarding_cohort(:'app','staging',:'cohort_operation',:'takeover') IS NULL AS exclusive \gset
SET SESSION AUTHORIZATION fixture_cohort_verifier;
SELECT installation_id AS first_installation,binding_epoch AS first_binding,lease_token AS first_token
FROM claim_onboarding_cohort_target(:'app','staging',:'verifier') \gset
RESET SESSION AUTHORIZATION;
UPDATE application_onboarding_operations SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=:'cohort_operation';
SET SESSION AUTHORIZATION fixture_cohort_coordinator;
SELECT initialize_onboarding_cohort(:'app','staging',:'cohort_operation',:'takeover')=2 AS took_over \gset
SET SESSION AUTHORIZATION fixture_cohort_verifier;
SELECT NOT finish_onboarding_cohort_target(:'app','staging',:'cohort_operation',:'cohort_operation',:'approval',:'config',
    :'first_installation',:first_binding,:'verifier',:first_token,'PASSED','') AS stale_rejected \gset
SELECT installation_id AS target,binding_epoch AS binding,lease_token AS token
FROM claim_onboarding_cohort_target(:'app','staging',:'verifier') \gset
SELECT finish_onboarding_cohort_target(:'app','staging',:'cohort_operation',:'cohort_operation',:'approval',:'config',
    :'target',:binding,:'verifier',:token,'PASSED','') AS first_passed \gset
SET SESSION AUTHORIZATION fixture_cohort_coordinator;
SELECT NOT finalize_onboarding_cohort(:'app','staging',:'cohort_operation',:'takeover',2) AS partial_rejected \gset
SET SESSION AUTHORIZATION fixture_cohort_verifier;
SELECT installation_id AS target,binding_epoch AS binding,lease_token AS token
FROM claim_onboarding_cohort_target(:'app','staging',:'verifier') \gset
SELECT finish_onboarding_cohort_target(:'app','staging',:'cohort_operation',:'cohort_operation',:'approval',:'config',
    :'target',:binding,:'verifier',:token,'PASSED','') AS second_passed \gset
RESET SESSION AUTHORIZATION;
SAVEPOINT rotate_binding;
UPDATE installation_bindings SET retired_at=clock_timestamp() WHERE id=:binding;
SET SESSION AUTHORIZATION fixture_cohort_coordinator;
SELECT NOT finalize_onboarding_cohort(:'app','staging',:'cohort_operation',:'takeover',2) AS retired_rejected \gset
RESET SESSION AUTHORIZATION;
ROLLBACK TO SAVEPOINT rotate_binding;
SET SESSION AUTHORIZATION fixture_cohort_coordinator;
SELECT NOT finalize_onboarding_cohort(:'app','staging',:'cohort_operation',:'coordinator',1) AS stale_finalize \gset
SELECT finalize_onboarding_cohort(:'app','staging',:'cohort_operation',:'takeover',2) AS finalized \gset
SELECT finalize_onboarding_cohort(:'app','staging',:'cohort_operation',:'takeover',2) AS replayed \gset
RESET SESSION AUTHORIZATION;
SELECT NOT has_table_privilege('fixture_cohort_coordinator','application_onboarding_approved_targets','INSERT')
   AND NOT has_table_privilege('fixture_cohort_verifier','application_onboarding_cohort_targets','UPDATE')
   AND NOT has_table_privilege('fixture_cohort_verifier','application_onboarding_live_targets','SELECT')
   AND NOT has_function_privilege('fixture_cohort_coordinator','apply_application_dispatch(uuid,uuid)','EXECUTE')
   AND NOT has_function_privilege('fixture_cohort_coordinator','apply_binding_route(uuid,uuid)','EXECUTE')
   AND o.state='SUCCEEDED' AND o.resulting_revision=s.revision AND s.cohort_id=o.id
   AND p.dispatch_mode='HELD' AND p.hold_new AND NOT p.coverage_approved AND p.dispatch_revision=o.expected_dispatch_revision
   AND (SELECT consumed_at IS NOT NULL FROM application_onboarding_approvals WHERE id=:'approval')
   AND (SELECT count(*) FROM application_provider_checks WHERE operation_id=o.id AND kind='TARGET_PERMISSION' AND outcome='PASSED')=1
   AND (SELECT count(*) FROM audit_logs WHERE application_id=s.application_id AND after_json->>'operation_id'=o.id::text)=1
   AND NOT EXISTS(SELECT FROM current_binding_routes WHERE application_id=s.application_id)
   AND :'incomplete'::boolean AND :'exclusive'::boolean AND :'took_over'::boolean AND :'stale_rejected'::boolean
   AND :'first_passed'::boolean AND :'partial_rejected'::boolean AND :'second_passed'::boolean
   AND :'revoked_rejected'::boolean AND :'rejected_without_consuming'::boolean AND :'retired_rejected'::boolean
   AND :'stale_finalize'::boolean AND :'finalized'::boolean AND :'replayed'::boolean AS cohort_test_passed
FROM application_onboarding s JOIN application_dispatch_policy p USING(application_id)
JOIN application_onboarding_operations o USING(application_id) WHERE o.id=:'cohort_operation' \gset
\if :cohort_test_passed
\else
SELECT 1/0;
\endif

SELECT set_config('fixture.pilot_app',:'app_id',true);
DO $$
DECLARE app_id bigint := current_setting('fixture.pilot_app')::bigint;
    cohort uuid := current_setting('fixture.cohort_op')::uuid;
    member bigint; extra_installation bigint; extra_binding bigint;
BEGIN
    IF onboarding_pilot_ready(app_id,cohort) THEN RAISE EXCEPTION 'missing routes passed'; END IF;
    INSERT INTO binding_route_grants(id,application_id,binding_id,operator_role,origin_epoch,source_epoch,
        expected_revision,destination,cohort_id,evidence_id,reason,issued_at,expires_at,applied_at)
    SELECT gen_random_uuid(),app_id,t.binding_id,'fixture_cohort_coordinator'::regrole::oid,r.origin_epoch,r.source_epoch,
        0,'FCM_ACTIVE',cohort,gen_random_uuid(),'pilot',statement_timestamp(),statement_timestamp()+interval '5 minutes',clock_timestamp()
    FROM application_onboarding_cohort_targets t CROSS JOIN notification_runtime r
    WHERE t.operation_id=cohort AND r.singleton ORDER BY t.installation_id LIMIT 1;
    IF onboarding_pilot_ready(app_id,cohort) THEN RAISE EXCEPTION 'partial routes passed'; END IF;
    INSERT INTO binding_route_grants(id,application_id,binding_id,operator_role,origin_epoch,source_epoch,
        expected_revision,destination,cohort_id,evidence_id,reason,issued_at,expires_at,applied_at)
    SELECT gen_random_uuid(),app_id,t.binding_id,'fixture_cohort_coordinator'::regrole::oid,r.origin_epoch,r.source_epoch,
        0,'FCM_ACTIVE',cohort,gen_random_uuid(),'pilot',statement_timestamp(),statement_timestamp()+interval '5 minutes',clock_timestamp()
    FROM application_onboarding_cohort_targets t CROSS JOIN notification_runtime r
    WHERE t.operation_id=cohort AND r.singleton ORDER BY t.installation_id DESC LIMIT 1;
    IF NOT onboarding_pilot_ready(app_id,cohort) OR onboarding_pilot_ready(app_id,gen_random_uuid())
        OR has_function_privilege('fixture_cohort_coordinator','onboarding_pilot_ready(bigint,uuid)','EXECUTE')
    THEN RAISE EXCEPTION 'complete roster or predicate access check failed'; END IF;

    SELECT min(installation_id) INTO member FROM application_onboarding_cohort_targets WHERE operation_id=cohort;
    BEGIN
        UPDATE application_provider_checks SET expires_at=checked_at+interval '1 microsecond'
        WHERE operation_id=cohort AND kind='TARGET_PERMISSION';
        IF onboarding_pilot_ready(app_id,cohort) THEN RAISE EXCEPTION 'expired aggregate evidence passed'; END IF;
        RAISE SQLSTATE 'P5400';
    EXCEPTION WHEN SQLSTATE 'P5400' THEN NULL;
    END;
    BEGIN
        UPDATE application_onboarding_operations SET state='FAILED' WHERE id=cohort;
        IF onboarding_pilot_ready(app_id,cohort) THEN RAISE EXCEPTION 'failed cohort passed'; END IF;
        RAISE SQLSTATE 'P5400';
    EXCEPTION WHEN SQLSTATE 'P5400' THEN NULL;
    END;
    BEGIN
        UPDATE application_onboarding_cohort_targets SET checked_at=clock_timestamp()-interval '16 minutes'
        WHERE operation_id=cohort AND installation_id=member;
        IF onboarding_pilot_ready(app_id,cohort) THEN RAISE EXCEPTION 'stale member passed'; END IF;
        RAISE SQLSTATE 'P5400';
    EXCEPTION WHEN SQLSTATE 'P5400' THEN NULL;
    END;
    BEGIN
        DELETE FROM application_onboarding_cohort_targets WHERE operation_id=cohort AND installation_id=member;
        IF onboarding_pilot_ready(app_id,cohort) THEN RAISE EXCEPTION 'missing member passed'; END IF;
        RAISE SQLSTATE 'P5400';
    EXCEPTION WHEN SQLSTATE 'P5400' THEN NULL;
    END;
    BEGIN
        UPDATE application_onboarding_approved_targets SET revoked_at=clock_timestamp() WHERE installation_id=member;
        IF onboarding_pilot_ready(app_id,cohort) THEN RAISE EXCEPTION 'revoked member passed'; END IF;
        RAISE SQLSTATE 'P5400';
    EXCEPTION WHEN SQLSTATE 'P5400' THEN NULL;
    END;
    BEGIN
        UPDATE installation_bindings SET retired_at=clock_timestamp() WHERE installation_id=member;
        IF onboarding_pilot_ready(app_id,cohort) THEN RAISE EXCEPTION 'retired binding passed'; END IF;
        RAISE SQLSTATE 'P5400';
    EXCEPTION WHEN SQLSTATE 'P5400' THEN NULL;
    END;
    BEGIN
        INSERT INTO mobile_installations(application_id,owner_user_id,proof_verifier)
        SELECT app_id,owner_user_id,sha256('extra') FROM mobile_installations WHERE id=member RETURNING id INTO extra_installation;
        INSERT INTO installation_bindings(application_id,installation_id,owner_user_id,owner_epoch,platform,
            provider_namespace,provider_route_key,address_type,address_fingerprint,address_ciphertext,encryption_key_version)
        SELECT app_id,id,owner_user_id,owner_epoch,'ANDROID','fixture-namespace','fixture-key','FCM_FID',sha256('extra'),sha256('synthetic'),1
        FROM mobile_installations WHERE id=extra_installation RETURNING id INTO extra_binding;
        INSERT INTO binding_route_grants(id,application_id,binding_id,operator_role,origin_epoch,source_epoch,
            expected_revision,destination,cohort_id,evidence_id,reason,issued_at,expires_at,applied_at)
        SELECT gen_random_uuid(),app_id,extra_binding,'fixture_cohort_coordinator'::regrole::oid,r.origin_epoch,r.source_epoch,
            0,'FCM_ACTIVE',cohort,gen_random_uuid(),'pilot',statement_timestamp(),statement_timestamp()+interval '5 minutes',clock_timestamp()
        FROM notification_runtime r WHERE r.singleton;
        IF onboarding_pilot_ready(app_id,cohort) THEN RAISE EXCEPTION 'extra route passed'; END IF;
        RAISE SQLSTATE 'P5400';
    EXCEPTION WHEN SQLSTATE 'P5400' THEN NULL;
    END;
END $$;
\endif
\if :commit_activation
COMMIT;
\else
ROLLBACK;
\endif
