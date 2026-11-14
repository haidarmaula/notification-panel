-- Only for the private hello-local PostgreSQL instance. Safe to resume after failure.
BEGIN;
DO $$
DECLARE role_name text;
BEGIN
  FOREACH role_name IN ARRAY ARRAY['hello_application_lifecycle_issuer',
    'hello_application_lifecycle_operator','hello_dispatch_issuer','hello_dispatch_operator',
    'load_retired_writer'] LOOP
    IF NOT EXISTS(SELECT FROM pg_roles WHERE rolname=role_name) THEN
      EXECUTE format('CREATE ROLE %I NOLOGIN',role_name);
    END IF;
  END LOOP;
  FOREACH role_name IN ARRAY ARRAY['load_reviewer','load_executor','load_dispatch_reviewer',
    'load_dispatch_executor','load_binding_reviewer','load_binding_executor'] LOOP
    IF NOT EXISTS(SELECT FROM pg_roles WHERE rolname=role_name) THEN
      EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L',role_name,'fixture-only');
    END IF;
  END LOOP;
END $$;
GRANT hello_application_lifecycle_issuer TO load_reviewer;
GRANT hello_application_lifecycle_operator TO load_executor;
GRANT hello_dispatch_issuer TO load_dispatch_reviewer;
GRANT hello_dispatch_operator TO load_dispatch_executor;
GRANT INSERT (id,application_id,binding_id,operator_role,origin_epoch,source_epoch,expected_revision,
    destination,cohort_id,evidence_id,reason,expires_at) ON binding_route_grants TO load_binding_reviewer;
GRANT EXECUTE ON FUNCTION apply_binding_route(uuid,uuid) TO load_binding_executor;
INSERT INTO applications(public_id,key,name)
VALUES('10000000-0000-4000-8000-000000000001','load-fixture','Hello Local')
ON CONFLICT(public_id) DO NOTHING;
INSERT INTO application_dispatch_policy(application_id,push_ttl_seconds,key_ttl_seconds)
SELECT id,3600,86400 FROM applications WHERE key='load-fixture'
ON CONFLICT(application_id) DO NOTHING;
COMMIT;
