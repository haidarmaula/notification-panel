# Called by the isolated load runner after provisioning its empty database.
# The separate Unix-socket logins exercise the real issuer and executor path.
load_sql() {
  local login=$1
  shift
  "${compose[@]}" exec -T postgres psql -XqAt -v ON_ERROR_STOP=1 -U "$login" -d notification_integration "$@"
}

load_sql integration <<'SQL'
CREATE ROLE hello_application_lifecycle_issuer;
CREATE ROLE hello_application_lifecycle_operator;
CREATE ROLE load_reviewer LOGIN IN ROLE hello_application_lifecycle_issuer;
CREATE ROLE load_executor LOGIN IN ROLE hello_application_lifecycle_operator;
CREATE ROLE load_retired_writer NOLOGIN;
CREATE ROLE hello_dispatch_issuer;
CREATE ROLE hello_dispatch_operator;
CREATE ROLE load_dispatch_reviewer LOGIN PASSWORD 'fixture-only' IN ROLE hello_dispatch_issuer;
CREATE ROLE load_dispatch_executor LOGIN PASSWORD 'fixture-only' IN ROLE hello_dispatch_operator;
CREATE ROLE load_binding_reviewer LOGIN PASSWORD 'fixture-only';
CREATE ROLE load_binding_executor LOGIN PASSWORD 'fixture-only';
GRANT INSERT (id,application_id,binding_id,operator_role,origin_epoch,source_epoch,expected_revision,
    destination,cohort_id,evidence_id,reason,expires_at) ON binding_route_grants TO load_binding_reviewer;
GRANT EXECUTE ON FUNCTION apply_binding_route(uuid,uuid) TO load_binding_executor;
INSERT INTO applications(public_id,key,name) VALUES('10000000-0000-4000-8000-000000000001','load-fixture','Load fixture');
INSERT INTO application_dispatch_policy(application_id,push_ttl_seconds,key_ttl_seconds) SELECT id,3600,86400 FROM applications;
SQL
load_sql integration < scripts/sql/finalize-application-ownership.sql
load_sql integration < scripts/sql/install-application-lifecycle.sql
load_sql integration < deploy/postgres/application-lifecycle-grants.sql
load_sql integration < deploy/postgres/dispatch-policy-grants.sql
read -r load_origin load_source load_expiry load_ownership load_writers load_cohorts <<< "$(load_sql integration -F ' ' -c "
  SELECT origin_epoch,source_epoch,to_char(clock_timestamp()+interval '1 hour','YYYY-MM-DD\"T\"HH24:MI:SSOF'),
      encode(sha256('synthetic ownership'),'hex'),encode(sha256('synthetic retired writers'),'hex'),encode(sha256('synthetic cohort'),'hex')
  FROM notification_runtime")"
load_project=10000000-0000-4000-8000-000000000001
load_grant=30000000-0000-4000-8000-000000000001
load_sql load_reviewer -v project="$load_project" -v grant_id="$load_grant" -v executor=load_executor \
  -v origin="$load_origin" -v source="$load_source" -v revision=1 -v from_status=PROVISIONING -v destination=ACTIVE \
  -v ownership_sha256="$load_ownership" -v writers_sha256="$load_writers" -v cohorts_sha256="$load_cohorts" \
  -v retired_roles='{load_retired_writer}' -v expires_utc="$load_expiry" < scripts/sql/issue-application-lifecycle.sql
load_sql load_executor -v project="$load_project" -v grant_id="$load_grant" < scripts/sql/apply-application-lifecycle.sql
