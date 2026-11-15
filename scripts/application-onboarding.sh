#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
[[ $# == 0 ]] || exit 2

image='postgres:17@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d'
timeout_bin=$(command -v timeout || command -v gtimeout || true)
[[ -n "$timeout_bin" ]] || { echo 'timeout or gtimeout is required' >&2; exit 2; }
tmp=$(mktemp -d "${TMPDIR:-/tmp}/hello-onboarding.XXXXXX")
node=$(basename "$tmp" | tr '[:upper:].' '[:lower:]-')
label="hello.onboarding=$node"
log="$tmp/run.log"
exec 3>&1 4>&2
exec >"$log" 2>&1

cleanup() {
    status=$?
    trap - EXIT
    if [[ "$(docker_cmd inspect --format '{{ index .Config.Labels "hello.onboarding" }}' "$node" 2>/dev/null || true)" == "$node" ]]; then
        docker_cmd rm -fv "$node" >/dev/null || status=1
    fi
    exec 1>&3 2>&4
    if (( status != 0 )); then
        cat "$log" >&2
        echo "Full log preserved at $log" >&2
    else
        tail -n 1 "$log" >&3
        rm -rf -- "$tmp" || status=1
        if (( status != 0 )); then
            echo "Cleanup failed; full log preserved at $log" >&2
        fi
    fi
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

docker_cmd() { "$timeout_bin" -k 3s 30s docker "$@"; }
docker_cmd run -d --pull=never --name "$node" --label "$label" --network none \
    -e POSTGRES_HOST_AUTH_METHOD=trust --tmpfs /var/lib/postgresql/data \
    -v "$PWD/db/migrations:/migrations:ro" \
    "$image" postgres -c listen_addresses= >/dev/null
[[ "$(docker_cmd inspect --format '{{.HostConfig.NetworkMode}} {{len .HostConfig.PortBindings}}' "$node")" == 'none 0' ]]

sql() { docker_cmd exec -iu postgres "$node" psql -XqAt -v ON_ERROR_STOP=1 -v VERBOSITY=verbose -d "$1" "${@:2}"; }
assert_query() {
    local expected=$1 description=$2 query=$3 actual
    if ! actual=$(sql postgres -c "$query"); then
        echo "Assertion query failed: $description" >&2
        exit 1
    fi
    if [[ "$actual" != "$expected" ]]; then
        echo "Assertion failed: $description (expected '$expected', got '$actual')" >&2
        exit 1
    fi
}
verifier_sql() {
    local login=$1
    shift
    docker_cmd exec -iu postgres "$node" psql -U "$login" -XqAt -v ON_ERROR_STOP=1 -v VERBOSITY=verbose -d postgres "$@"
}
assert_login_query() {
    local expected=$1 description=$2 login=$3 query=$4 actual
    if ! actual=$(verifier_sql "$login" -c "$query"); then
        echo "Assertion query failed for $login: $description" >&2
        exit 1
    fi
    if [[ "$actual" != "$expected" ]]; then
        echo "Assertion failed for $login: $description (expected '$expected', got '$actual')" >&2
        exit 1
    fi
}
extract_reservation_query() {
    python3 - "$PWD/db/queries/application_onboarding.sql" "$tmp/reservation-query.sql" <<'PY'
import re
import sys
from pathlib import Path

source = Path(sys.argv[1]).read_text()
match = re.search(
    r"(?ms)^-- name: VerifyApplicationFCMReservation :one\s*\n(.*?)(?=^-- name:|\Z)",
    source,
)
if not match:
    raise SystemExit("VerifyApplicationFCMReservation query not found")
query = match.group(1).strip().removesuffix(";").strip()
names = list(dict.fromkeys(re.findall(r"sqlc\.arg\((\w+)\)", query)))
expected = ["public_id", "reservation_id", "configuration_id", "environment", "target_project_id", "service_account_email"]
if names != expected:
    raise SystemExit(f"unexpected reservation query arguments: {names!r}")
positions = {name: index for index, name in enumerate(names, 1)}
query = re.sub(r"sqlc\.arg\((\w+)\)", lambda m: f"${positions[m.group(1)]}", query)
Path(sys.argv[2]).write_text(query + "\n")
PY
}
deny() {
    local code=$1 output
    shift
    if output=$("$@" 2>&1); then
        echo "Expected SQLSTATE $code, but operation succeeded" >&2
        exit 1
    fi
    if [[ "$output" != *"$code:"* ]]; then
        echo "$output" >&2
        echo "Expected SQLSTATE $code" >&2
        exit 1
    fi
}

ready=false
for _ in {1..20}; do
    if [[ "$("$timeout_bin" -k 1s 3s docker exec "$node" cat /proc/1/comm)" == postgres ]] && sql postgres -c 'SELECT 1' >/dev/null 2>&1; then
        ready=true
        break
    fi
    sleep .25
done
[[ "$ready" == true ]] || { echo 'Postgres did not become ready before the fixture deadline' >&2; exit 1; }

# Seed applications before 000042 so the migration's backfill can be distinguished
# from the after-migration onboarding initializer.
for migration in db/migrations/0000{01..41}_*.up.sql; do
    [[ "$migration" == *000042* ]] && continue
    sql postgres < "$migration"
done
sql postgres <<'SQL'
INSERT INTO applications(key,name) VALUES
  ('onboarding-legacy-a','Onboarding legacy A'),('onboarding-legacy-b','Onboarding legacy B');
INSERT INTO application_dispatch_policy(application_id,push_ttl_seconds,key_ttl_seconds)
SELECT id,60,3600 FROM applications WHERE key='onboarding-legacy-a';
SQL
sql postgres < db/migrations/000042_application_onboarding.up.sql
sql postgres < db/migrations/000043_onboarding_verification.up.sql
sql postgres < db/migrations/000044_onboarding_verification_fences.up.sql

assert_query '1|false' 'pre-existing application is backfilled as non-greenfield' "SELECT o.revision||'|'||o.greenfield FROM application_onboarding o JOIN applications a ON a.id=o.application_id WHERE a.key='onboarding-legacy-a'"
assert_query 'true|false' 'existing dispatch policy remains held' "SELECT hold_new||'|'||coverage_approved FROM application_dispatch_policy p JOIN applications a ON a.id=p.application_id WHERE a.key='onboarding-legacy-a'"
sql postgres -c "INSERT INTO applications(key,name) VALUES('onboarding-new-app','Onboarding new app')"
assert_query '1|true' 'new application starts at revision one and greenfield' "SELECT o.revision||'|'||o.greenfield FROM application_onboarding o JOIN applications a ON a.id=o.application_id WHERE a.key='onboarding-new-app'"

sql postgres <<'SQL'
INSERT INTO application_provider_configurations
  (id,application_id,version,environment,target_project_id,service_account_email,file_reference,created_by)
SELECT '10000000-0000-4000-8000-000000000001',id,1,'staging','fixture-project-a','a@example.invalid',
       '20000000-0000-4000-8000-000000000001',1 FROM applications WHERE key='onboarding-legacy-a';
INSERT INTO application_provider_configurations
  (id,application_id,version,environment,target_project_id,service_account_email,file_reference,created_by)
SELECT '10000000-0000-4000-8000-000000000002',id,1,'staging','fixture-project-b','b@example.invalid',
       '20000000-0000-4000-8000-000000000002',1 FROM applications WHERE key='onboarding-legacy-b';
SQL
sql postgres -c "UPDATE application_onboarding SET environment='staging',candidate_configuration_id='10000000-0000-4000-8000-000000000001' WHERE application_id=(SELECT id FROM applications WHERE key='onboarding-legacy-a')"
deny 23503 sql postgres -c "UPDATE application_onboarding SET environment='staging',candidate_configuration_id='10000000-0000-4000-8000-000000000002' WHERE application_id=(SELECT id FROM applications WHERE key='onboarding-legacy-a')"
deny 23514 sql postgres -c "UPDATE application_provider_configurations SET file_reference='20000000-0000-4000-8000-000000000003' WHERE id='10000000-0000-4000-8000-000000000001'"

app_id=$(sql postgres -c "SELECT id FROM applications WHERE key='onboarding-legacy-a'")
operation() {
    sql postgres -v app_id="$app_id" -v kind="$1" -v state="$2" -v dispatch_revision="$3" <<'SQL'
INSERT INTO application_onboarding_operations
 (id,application_id,kind,state,actor_id,session_id,session_epoch,auth_epoch,idempotency_key,request_fingerprint,
  fingerprint_key_version,configuration_id,expected_setup_revision,expected_application_revision,expected_dispatch_revision,origin_epoch,source_epoch)
VALUES ('30000000-0000-4000-8000-000000000001',:app_id,:'kind',:'state',1,
  '30000000-0000-4000-8000-000000000011','30000000-0000-4000-8000-000000000012',1,
  '30000000-0000-4000-8000-000000000021',decode(repeat('00',32),'hex'),1,
  '10000000-0000-4000-8000-000000000001',1,1,:dispatch_revision,
  '30000000-0000-4000-8000-000000000031','30000000-0000-4000-8000-000000000032');
SQL
}
deny 23514 operation INVALID PENDING 1
deny 23514 operation VERIFY INVALID 1
deny 23514 operation ACTIVATE PENDING 0

assert_query t 'onboarding metadata contains no raw credential or secret columns' "SELECT NOT EXISTS(SELECT FROM information_schema.columns WHERE table_schema='public' AND table_name=ANY(ARRAY['application_onboarding','application_provider_configurations','application_onboarding_ingestions','application_onboarding_operations','application_provider_checks','application_runtime_acknowledgements','application_onboarding_approvals','application_onboarding_requests']) AND column_name ~* '(secret|private_key|credential|password|token|service_account_json)' AND column_name<>'lease_token')"
request() {
    sql postgres -v app_id="$app_id" -v kind="$1" <<'SQL'
INSERT INTO application_onboarding_requests
 (application_id,actor_id,kind,idempotency_key,request_fingerprint,fingerprint_key_version,response_data)
VALUES (:app_id,1,:'kind','40000000-0000-4000-8000-000000000001',decode(repeat('00',32),'hex'),1,'{}');
SQL
}
deny 23514 request INVALID
request ENROLLMENT
sql postgres <<'SQL'
INSERT INTO staff_users(role_id,name,email,password_hash)
SELECT id,'Onboarding verifier actor','onboarding-verifier@example.invalid','fixture-hash' FROM roles WHERE name='SUPER_ADMIN';
SQL
staff_id=$(sql postgres -c "SELECT id FROM staff_users WHERE email='onboarding-verifier@example.invalid'")
app_public_id=$(sql postgres -c "SELECT public_id FROM applications WHERE key='onboarding-legacy-a'")
other_public_id=$(sql postgres -c "SELECT public_id FROM applications WHERE key='onboarding-legacy-b'")
verifier_owner='50000000-0000-4000-8000-000000000001'
session_id='50000000-0000-4000-8000-000000000002'

sql postgres <<'SQL'
CREATE ROLE hello_onboarding_provisioner NOLOGIN;
CREATE ROLE hello_onboarding_verifier NOLOGIN;
CREATE ROLE provisioner_a LOGIN IN ROLE hello_onboarding_provisioner;
CREATE ROLE verifier_a LOGIN IN ROLE hello_onboarding_verifier;
CREATE ROLE verifier_b LOGIN IN ROLE hello_onboarding_verifier;
SQL
sql postgres < deploy/postgres/onboarding-grants.sql
sql postgres < deploy/postgres/onboarding-grants.sql

# Runtime groups receive the exact narrow profile, and LOGIN members inherit it.
assert_query t 'provisioner has only its documented application columns and full ingestion reads' \
    "SELECT has_column_privilege('hello_onboarding_provisioner','applications','id','SELECT') AND has_column_privilege('provisioner_a','applications','id','SELECT') AND has_column_privilege('provisioner_a','applications','public_id','SELECT') AND has_column_privilege('provisioner_a','applications','status','SELECT') AND NOT has_column_privilege('provisioner_a','applications','name','SELECT') AND has_table_privilege('provisioner_a','application_onboarding_ingestions','SELECT') AND NOT has_any_column_privilege('provisioner_a','application_provider_configurations','SELECT') AND NOT has_any_column_privilege('provisioner_a','application_provider_checks','SELECT') AND NOT has_any_column_privilege('provisioner_a','application_provider_configurations','INSERT,UPDATE,REFERENCES') AND NOT has_table_privilege('hello_onboarding_provisioner','application_onboarding_ingestions','INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN')"
assert_query t 'verifier can read scoped evidence and only its two execution functions' \
    "SELECT has_table_privilege('hello_onboarding_verifier','application_onboarding','SELECT') AND has_table_privilege('verifier_a','application_onboarding','SELECT') AND has_table_privilege('verifier_a','application_provider_configurations','SELECT') AND has_table_privilege('verifier_a','application_onboarding_operations','SELECT') AND has_column_privilege('verifier_a','applications','revision','SELECT') AND NOT has_column_privilege('verifier_a','applications','name','SELECT') AND has_column_privilege('verifier_a','application_dispatch_policy','dispatch_revision','SELECT') AND has_function_privilege('hello_onboarding_verifier','claim_application_onboarding_verification(uuid,uuid)','EXECUTE') AND has_function_privilege('verifier_a','claim_application_onboarding_verification(uuid,uuid)','EXECUTE') AND has_function_privilege('verifier_a','finish_application_onboarding_verification(uuid,uuid,bigint,text,text)','EXECUTE') AND NOT has_any_column_privilege('verifier_a','application_provider_checks','INSERT,UPDATE,REFERENCES') AND NOT has_function_privilege('verifier_a','revoke_application_lifecycle(uuid,uuid)','EXECUTE')"
assert_query t 'provisioner group cannot execute lifecycle authority' \
    "SELECT NOT has_function_privilege('provisioner_a','apply_application_lifecycle(uuid,uuid)','EXECUTE') AND NOT has_function_privilege('provisioner_a','claim_application_onboarding_verification(uuid,uuid)','EXECUTE')"

# Each privilege drift is rejected transactionally. Removing it restores installation.
sql postgres -c 'GRANT UPDATE ON application_provider_checks TO verifier_a'
deny P0001 sql postgres < deploy/postgres/onboarding-grants.sql
assert_query t 'failed profile installation leaves the drift grant untouched' \
    "SELECT has_table_privilege('verifier_a','application_provider_checks','UPDATE')"
sql postgres -c 'REVOKE UPDATE ON application_provider_checks FROM verifier_a'
sql postgres < deploy/postgres/onboarding-grants.sql

sql postgres -c 'ALTER ROLE verifier_b CREATEROLE'
deny P0001 sql postgres < deploy/postgres/onboarding-grants.sql
assert_query t 'failed profile installation preserves the bad role attribute for review' \
    "SELECT rolcreaterole FROM pg_roles WHERE rolname='verifier_b'"
sql postgres -c 'ALTER ROLE verifier_b NOCREATEROLE'
sql postgres < deploy/postgres/onboarding-grants.sql

sql postgres -c 'GRANT EXECUTE ON FUNCTION revoke_application_lifecycle(uuid,uuid) TO hello_onboarding_verifier'
deny P0001 sql postgres < deploy/postgres/onboarding-grants.sql
assert_query t 'failed profile installation leaves unwanted SECURITY DEFINER grant untouched' \
    "SELECT has_function_privilege('verifier_a','revoke_application_lifecycle(uuid,uuid)','EXECUTE')"
sql postgres -c 'REVOKE EXECUTE ON FUNCTION revoke_application_lifecycle(uuid,uuid) FROM hello_onboarding_verifier'
sql postgres < deploy/postgres/onboarding-grants.sql

sql postgres <<'SQL'
INSERT INTO application_onboarding_verifier_scopes(role_id,application_id)
SELECT 'verifier_a'::regrole::oid,id FROM applications WHERE key='onboarding-legacy-a';
INSERT INTO staff_sessions(session_epoch,id,staff_user_id,auth_epoch,refresh_digest,expires_at)
SELECT c.session_epoch,'50000000-0000-4000-8000-000000000002',s.id,s.auth_epoch,decode(repeat('00',32),'hex'),clock_timestamp()+interval '1 hour'
FROM staff_session_control c CROSS JOIN staff_users s WHERE c.singleton AND s.email='onboarding-verifier@example.invalid';
SQL
session_epoch=$(sql postgres -c 'SELECT session_epoch FROM staff_session_control WHERE singleton')
origin_epoch=$(sql postgres -c 'SELECT origin_epoch FROM notification_runtime WHERE singleton')
source_epoch=$(sql postgres -c 'SELECT source_epoch FROM notification_runtime WHERE singleton')

# Execute the checked-in sqlc source as the restricted provisioner role. A temp
# applications table and the runtime search_path catch any unqualified reference.
probe_app_id='70000000-0000-4000-8000-000000000001'
probe_config_id='70000000-0000-4000-8000-000000000002'
probe_reservation_id='70000000-0000-4000-8000-000000000003'
probe_session_id='70000000-0000-4000-8000-000000000004'
sql postgres -v staff_id="$staff_id" -v session_epoch="$session_epoch" <<'SQL'
INSERT INTO applications(key,name) VALUES('onboarding-provisioner-probe','Provisioner query fixture');
INSERT INTO application_provider_configurations
  (id,application_id,version,environment,target_project_id,service_account_email,file_reference,created_by)
SELECT '70000000-0000-4000-8000-000000000002',id,1,'staging','fixture-provisioner-project',
       'provisioner@example.invalid','70000000-0000-4000-8000-000000000005',:staff_id
FROM applications WHERE key='onboarding-provisioner-probe';
UPDATE application_onboarding SET environment='staging',candidate_configuration_id='70000000-0000-4000-8000-000000000002'
WHERE application_id=(SELECT id FROM applications WHERE key='onboarding-provisioner-probe');
INSERT INTO staff_sessions(session_epoch,id,staff_user_id,auth_epoch,refresh_digest,expires_at)
SELECT :'session_epoch','70000000-0000-4000-8000-000000000004',s.id,s.auth_epoch,
       decode(repeat('77',32),'hex'),clock_timestamp()+interval '1 hour'
FROM staff_users s WHERE s.id=:staff_id;
INSERT INTO application_onboarding_ingestions
  (id,application_id,actor_id,session_id,session_epoch,auth_epoch,expected_revision,idempotency_key,
   request_fingerprint,fingerprint_key_version,configuration_id,environment,target_project_id,
   service_account_email,expires_at)
SELECT '70000000-0000-4000-8000-000000000003',a.id,s.id,f.id,f.session_epoch,s.auth_epoch,o.revision,
       '70000000-0000-4000-8000-000000000006',decode(repeat('77',32),'hex'),1,
       '70000000-0000-4000-8000-000000000002','staging','fixture-provisioner-project',
       'provisioner@example.invalid',clock_timestamp()+interval '1 hour'
FROM applications a JOIN application_onboarding o ON o.application_id=a.id
JOIN staff_users s ON s.id=:staff_id
JOIN staff_sessions f ON f.id='70000000-0000-4000-8000-000000000004'
WHERE a.key='onboarding-provisioner-probe';
SQL
probe_public_id=$(sql postgres -c "SELECT public_id FROM applications WHERE key='onboarding-provisioner-probe'")
extract_reservation_query
{
    cat <<'SQL'
SET search_path=pg_catalog,pg_temp;
CREATE TEMP TABLE applications(id bigint,public_id uuid,status text,revision bigint);
PREPARE reservation_probe(uuid,uuid,uuid,text,text,text) AS
SQL
    cat "$tmp/reservation-query.sql"
    cat <<SQL
;
SELECT current_setting('search_path')='pg_catalog, pg_temp';
EXECUTE reservation_probe('$probe_public_id','70000000-0000-4000-8000-000000000003','70000000-0000-4000-8000-000000000002','staging','fixture-provisioner-project','provisioner@example.invalid');
EXECUTE reservation_probe('$probe_public_id','70000000-0000-4000-8000-000000000003','70000000-0000-4000-8000-000000000002','staging','wrong-project','provisioner@example.invalid');
EXECUTE reservation_probe('$probe_public_id','70000000-0000-4000-8000-000000000003','70000000-0000-4000-8000-000000000002','staging','fixture-provisioner-project','wrong@example.invalid');
EXECUTE reservation_probe('$probe_public_id','70000000-0000-4000-8000-000000000003','70000000-0000-4000-8000-000000000002','production','fixture-provisioner-project','provisioner@example.invalid');
SQL
} > "$tmp/reservation-probe.sql"
probe_results=$(verifier_sql provisioner_a < "$tmp/reservation-probe.sql")
[[ "$probe_results" == $'t\nt\nf\nf\nf' ]] || {
    echo "Unexpected source-query probe results (search path, exact tuple, project, email, environment): $probe_results" >&2
    exit 1
}
{
    cat <<'SQL'
SET search_path=pg_catalog,pg_temp;
CREATE TEMP TABLE applications(id bigint,public_id uuid,status text,revision bigint);
PREPARE reservation_probe(uuid,uuid,uuid,text,text,text) AS
SQL
    cat "$tmp/reservation-query.sql"
    cat <<SQL
;
SELECT current_setting('search_path')='pg_catalog, pg_temp';
EXECUTE reservation_probe('$probe_public_id','70000000-0000-4000-8000-000000000003','70000000-0000-4000-8000-000000000002','staging','fixture-provisioner-project','provisioner@example.invalid');
SQL
} > "$tmp/reservation-single-probe.sql"
deny 42501 verifier_sql provisioner_a -c "UPDATE public.application_onboarding_ingestions SET state='ABANDONED' WHERE id='70000000-0000-4000-8000-000000000003'"
deny 42501 verifier_sql provisioner_a -c "SELECT public.claim_application_onboarding_verification('$probe_public_id','70000000-0000-4000-8000-000000000007')"
sql postgres -c "UPDATE application_onboarding_ingestions SET session_id='70000000-0000-4000-8000-000000000008' WHERE id='$probe_reservation_id'"
probe_session_result=$(verifier_sql provisioner_a < "$tmp/reservation-single-probe.sql")
[[ "$probe_session_result" == $'t\nf' ]] || {
    echo "Unexpected source-query result after reservation session mismatch: $probe_session_result" >&2
    exit 1
}
sql postgres -c "UPDATE application_onboarding_ingestions SET session_id='$probe_session_id' WHERE id='$probe_reservation_id'"
sql postgres -c "UPDATE staff_sessions SET revoked_at=clock_timestamp(),revocation_reason='logout' WHERE id='$probe_session_id'"
probe_revoked_result=$(verifier_sql provisioner_a < "$tmp/reservation-single-probe.sql")
[[ "$probe_revoked_result" == $'t\nf' ]] || {
    echo "Unexpected source-query result after session revocation: $probe_revoked_result" >&2
    exit 1
}

# Check the operation row's immutability and the only non-owner terminal transition.
sql postgres <<'SQL'
INSERT INTO application_onboarding_operations
 (id,application_id,kind,state,actor_id,session_id,session_epoch,auth_epoch,idempotency_key,request_fingerprint,
  fingerprint_key_version,configuration_id,expected_setup_revision,expected_application_revision,expected_dispatch_revision,
  origin_epoch,source_epoch)
SELECT '32000000-0000-4000-8000-000000000001',a.id,'VERIFY','PENDING',s.id,f.id,f.session_epoch,s.auth_epoch,
       '32000000-0000-4000-8000-000000000011',decode(repeat('22',32),'hex'),1,
       '10000000-0000-4000-8000-000000000002',1,a.revision,0,r.origin_epoch,r.source_epoch
FROM applications a JOIN staff_users s ON s.email='onboarding-verifier@example.invalid'
JOIN staff_sessions f ON f.staff_user_id=s.id AND f.id='50000000-0000-4000-8000-000000000002'
CROSS JOIN notification_runtime r WHERE a.key='onboarding-legacy-b' AND r.singleton;
SQL
deny 23514 sql postgres -c "UPDATE application_onboarding_operations SET configuration_id='10000000-0000-4000-8000-000000000001' WHERE id='32000000-0000-4000-8000-000000000001'"
sql postgres -c 'GRANT UPDATE(state,completed_at,failure_code,lease_owner,lease_expires_at) ON application_onboarding_operations TO verifier_a'
deny 42501 verifier_sql verifier_a -c "UPDATE application_onboarding_operations SET state='FAILED',completed_at=clock_timestamp(),failure_code='FIXTURE_FAILURE' WHERE id='32000000-0000-4000-8000-000000000001'"
verifier_sql verifier_a -c "UPDATE application_onboarding_operations SET state='SUPERSEDED',completed_at=clock_timestamp(),failure_code='DISPATCH_HELD' WHERE id='32000000-0000-4000-8000-000000000001'"
assert_query 'SUPERSEDED|DISPATCH_HELD' 'non-owner may only supersede held work' "SELECT state||'|'||failure_code FROM application_onboarding_operations WHERE id='32000000-0000-4000-8000-000000000001'"
sql postgres -c 'REVOKE UPDATE(state,completed_at,failure_code,lease_owner,lease_expires_at) ON application_onboarding_operations FROM verifier_a'

seed_verification() {
    sql postgres -v operation_id="$1" -v idempotency_key="$2" -v setup_revision="$3" -v session_id="$session_id" <<'SQL'
INSERT INTO application_onboarding_operations
 (id,application_id,kind,state,actor_id,session_id,session_epoch,auth_epoch,idempotency_key,request_fingerprint,
  fingerprint_key_version,configuration_id,expected_setup_revision,expected_application_revision,expected_dispatch_revision,
  origin_epoch,source_epoch)
SELECT :'operation_id',a.id,'VERIFY','PENDING',s.id,f.id,f.session_epoch,s.auth_epoch,:'idempotency_key',
       decode(repeat('11',32),'hex'),1,'10000000-0000-4000-8000-000000000001',
       :setup_revision,a.revision,0,r.origin_epoch,r.source_epoch
FROM applications a JOIN staff_users s ON s.email='onboarding-verifier@example.invalid'
JOIN staff_sessions f ON f.staff_user_id=s.id AND f.id=:'session_id'::uuid
JOIN application_dispatch_policy p ON p.application_id=a.id
CROSS JOIN notification_runtime r
WHERE a.key='onboarding-legacy-a' AND r.singleton;
SQL
}
claim_as() {
    verifier_sql "$1" -v project="$app_public_id" -v owner="$verifier_owner" \
        -c "SELECT claim_application_onboarding_verification(:'project'::uuid,:'owner'::uuid)"
}
deny 42501 verifier_sql verifier_a \
    -c "SELECT claim_application_onboarding_verification('$other_public_id','$verifier_owner')"
deny 42501 verifier_sql verifier_b \
    -c "SELECT claim_application_onboarding_verification('$app_public_id','$verifier_owner')"

# A real super-admin session and a persisted VERIFY operation exercise the owner path.
seed_verification '31000000-0000-4000-8000-000000000001' '31000000-0000-4000-8000-000000000011' 1
assert_login_query '31000000-0000-4000-8000-000000000001' 'scoped verifier claims eligible work' verifier_a \
    "SELECT claim_application_onboarding_verification('$app_public_id','$verifier_owner')"
assert_query "RUNNING|$verifier_owner|1|1" 'claim stores owner, first lease token, and attempt' "SELECT state||'|'||lease_owner||'|'||lease_token||'|'||attempts FROM application_onboarding_operations WHERE id='31000000-0000-4000-8000-000000000001'"
assert_login_query "RUNNING|$verifier_owner|1" 'verifier can inspect only the granted lease columns' verifier_a \
    "SELECT state||'|'||lease_owner||'|'||lease_token FROM application_onboarding_operations WHERE id='31000000-0000-4000-8000-000000000001'"
assert_login_query false 'wrong owner cannot complete the lease' verifier_a \
    "SELECT finish_application_onboarding_verification('31000000-0000-4000-8000-000000000001','50000000-0000-4000-8000-000000000099',1,'PASSED',NULL)::text"
assert_login_query false 'wrong lease token cannot complete the lease' verifier_a \
    "SELECT finish_application_onboarding_verification('31000000-0000-4000-8000-000000000001','$verifier_owner',2,'PASSED',NULL)::text"
deny 42501 verifier_sql verifier_a -c "SELECT finish_application_onboarding_verification('31000000-0000-4000-8000-000000000001','$verifier_owner',NULL,'PASSED',NULL)"
deny 42501 verifier_sql verifier_a -c "SELECT finish_application_onboarding_verification('31000000-0000-4000-8000-000000000001','$verifier_owner',0,'PASSED',NULL)"
assert_login_query true 'valid OAuth result completes the operation' verifier_a \
    "SELECT finish_application_onboarding_verification('31000000-0000-4000-8000-000000000001','$verifier_owner',1,'PASSED',NULL)::text"
assert_login_query true 'exact completion retry returns success' verifier_a \
    "SELECT finish_application_onboarding_verification('31000000-0000-4000-8000-000000000001','$verifier_owner',1,'PASSED',NULL)::text"
assert_query 'VERIFIED|2|SUCCEEDED|1' 'success records configuration, setup revision, operation, and one provider check' \
    "SELECT c.state||'|'||o.revision||'|'||op.state||'|'||(SELECT count(*) FROM application_provider_checks WHERE operation_id=op.id) FROM application_provider_configurations c JOIN application_onboarding o ON o.application_id=c.application_id JOIN application_onboarding_operations op ON op.configuration_id=c.id WHERE c.id='10000000-0000-4000-8000-000000000001' AND op.id='31000000-0000-4000-8000-000000000001'"
assert_query '1' 'exact retry writes exactly one audit row' "SELECT count(*) FROM audit_logs WHERE application_id=$app_id AND entity_type='application_onboarding' AND action='APPLICATION_ONBOARDING_OPERATION_COMPLETE' AND entity_id=$app_id"

# Mutating the setup after claim makes the result stale; the verifier must return false.
seed_verification '31000000-0000-4000-8000-000000000002' '31000000-0000-4000-8000-000000000012' 2
assert_login_query '31000000-0000-4000-8000-000000000002' 'claim setup-fence test operation' verifier_a \
    "SELECT claim_application_onboarding_verification('$app_public_id','$verifier_owner')"
sql postgres -c "UPDATE application_onboarding SET revision=revision+1 WHERE application_id=$app_id"
assert_login_query false 'changed setup revision rejects completion' verifier_a \
    "SELECT finish_application_onboarding_verification('31000000-0000-4000-8000-000000000002','$verifier_owner',1,'PASSED',NULL)::text"
sql postgres -c "UPDATE application_onboarding_operations SET state='SUPERSEDED',completed_at=clock_timestamp(),failure_code='FIXTURE_CLEANUP',lease_owner=NULL,lease_expires_at=NULL WHERE id='31000000-0000-4000-8000-000000000002'"

# Changing runtime epochs after claim fences the result.
seed_verification '31000000-0000-4000-8000-000000000003' '31000000-0000-4000-8000-000000000013' 3
assert_login_query '31000000-0000-4000-8000-000000000003' 'claim runtime-epoch test operation' verifier_a \
    "SELECT claim_application_onboarding_verification('$app_public_id','$verifier_owner')"
sql postgres -c "UPDATE notification_runtime SET origin_epoch='60000000-0000-4000-8000-000000000001',source_epoch='60000000-0000-4000-8000-000000000002' WHERE singleton"
assert_login_query false 'changed runtime epochs reject completion' verifier_a \
    "SELECT finish_application_onboarding_verification('31000000-0000-4000-8000-000000000003','$verifier_owner',1,'PASSED',NULL)::text"
sql postgres -c "UPDATE application_onboarding_operations SET state='SUPERSEDED',completed_at=clock_timestamp(),failure_code='FIXTURE_CLEANUP',lease_owner=NULL,lease_expires_at=NULL WHERE id='31000000-0000-4000-8000-000000000003'"

# Revoking the staff session after claim invalidates the verifier result.
seed_verification '31000000-0000-4000-8000-000000000004' '31000000-0000-4000-8000-000000000014' 3
assert_login_query '31000000-0000-4000-8000-000000000004' 'claim actor-revocation test operation' verifier_a \
    "SELECT claim_application_onboarding_verification('$app_public_id','$verifier_owner')"
sql postgres -c "UPDATE staff_sessions SET revoked_at=clock_timestamp(),revocation_reason='logout' WHERE id='$session_id'"
assert_login_query false 'revoked staff session rejects completion' verifier_a \
    "SELECT finish_application_onboarding_verification('31000000-0000-4000-8000-000000000004','$verifier_owner',1,'PASSED',NULL)::text"
sql postgres -c "UPDATE application_onboarding_operations SET state='SUPERSEDED',completed_at=clock_timestamp(),failure_code='FIXTURE_CLEANUP',lease_owner=NULL,lease_expires_at=NULL WHERE id='31000000-0000-4000-8000-000000000004'"

# Restore a fresh real session, then verify the lease retry ceiling records one inconclusive result.
session_id='50000000-0000-4000-8000-000000000003'
sql postgres <<'SQL'
INSERT INTO staff_sessions(session_epoch,id,staff_user_id,auth_epoch,refresh_digest,expires_at)
SELECT c.session_epoch,'50000000-0000-4000-8000-000000000003',s.id,s.auth_epoch,decode(repeat('33',32),'hex'),clock_timestamp()+interval '1 hour'
FROM staff_session_control c CROSS JOIN staff_users s WHERE c.singleton AND s.email='onboarding-verifier@example.invalid';
SQL
seed_verification '31000000-0000-4000-8000-000000000005' '31000000-0000-4000-8000-000000000015' 3
for attempt in 1 2 3; do
    assert_login_query '31000000-0000-4000-8000-000000000005' "claim lease attempt $attempt" verifier_a \
        "SELECT claim_application_onboarding_verification('$app_public_id','$verifier_owner')"
    assert_query "RUNNING|$attempt|$attempt" "persist lease token and attempt $attempt" \
        "SELECT state||'|'||lease_token||'|'||attempts FROM application_onboarding_operations WHERE id='31000000-0000-4000-8000-000000000005'"
    sql postgres -c "UPDATE application_onboarding_operations SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id='31000000-0000-4000-8000-000000000005'"
done
assert_login_query NULL 'fourth claim exhausts retries and returns null' verifier_a \
    "SELECT CASE WHEN claim_application_onboarding_verification('$app_public_id','$verifier_owner') IS NULL THEN 'NULL' ELSE 'NOT_NULL' END"
assert_query 'FAILED|3|FCM_CHECK_INCONCLUSIVE|true|true' 'exhausted operation is terminal with no live lease' \
    "SELECT state||'|'||attempts||'|'||failure_code||'|'||(completed_at IS NOT NULL)||'|'||(lease_owner IS NULL) FROM application_onboarding_operations WHERE id='31000000-0000-4000-8000-000000000005'"
assert_query '1|INCONCLUSIVE|FCM_CHECK_INCONCLUSIVE' 'lease exhaustion records one inconclusive OAuth check without a provider call' \
    "SELECT count(*)||'|'||min(outcome)||'|'||min(failure_code) FROM application_provider_checks WHERE operation_id='31000000-0000-4000-8000-000000000005'"
assert_query '1' 'lease exhaustion writes one completion audit' \
    "SELECT count(*) FROM audit_logs WHERE application_id=$app_id AND action='APPLICATION_ONBOARDING_OPERATION_COMPLETE' AND entity_id=$app_id AND after_json->>'operation_id'='31000000-0000-4000-8000-000000000005'"
assert_query '4|STORED' 'lease exhaustion advances setup revision and returns candidate configuration to stored' \
    "SELECT o.revision||'|'||c.state FROM application_onboarding o JOIN application_provider_configurations c ON c.id=o.candidate_configuration_id WHERE o.application_id=$app_id"
assert_login_query NULL 'repeated claim does not reprocess exhausted lease' verifier_a \
    "SELECT CASE WHEN claim_application_onboarding_verification('$app_public_id','$verifier_owner') IS NULL THEN 'NULL' ELSE 'NOT_NULL' END"
assert_query '4|1|1' 'repeated claim leaves exhaustion revision, check, and audit unchanged' \
    "SELECT o.revision||'|'||(SELECT count(*) FROM application_provider_checks WHERE operation_id='31000000-0000-4000-8000-000000000005')||'|'||(SELECT count(*) FROM audit_logs WHERE application_id=$app_id AND after_json->>'operation_id'='31000000-0000-4000-8000-000000000005') FROM application_onboarding o WHERE o.application_id=$app_id"
# A pending operation captured before the exhaustion revision cannot be claimed.
seed_verification '31000000-0000-4000-8000-000000000006' '31000000-0000-4000-8000-000000000016' 3
assert_login_query NULL 'claim supersedes work captured against an old setup revision' verifier_a \
    "SELECT CASE WHEN claim_application_onboarding_verification('$app_public_id','$verifier_owner') IS NULL THEN 'NULL' ELSE 'NOT_NULL' END"
assert_query 'SUPERSEDED|VERIFICATION_SUPERSEDED' 'old-revision verification is terminally superseded' \
    "SELECT state||'|'||failure_code FROM application_onboarding_operations WHERE id='31000000-0000-4000-8000-000000000006'"
assert_query 'PROVISIONING|1' 'verification leaves application lifecycle and revision unchanged' "SELECT status||'|'||revision FROM applications WHERE id=$app_id"
assert_query 'HELD|1|true|false' 'verification leaves dispatch policy held and unchanged' "SELECT dispatch_mode||'|'||dispatch_revision||'|'||hold_new||'|'||coverage_approved FROM application_dispatch_policy WHERE application_id=$app_id"

deny 23514 sql postgres -c "INSERT INTO audit_logs(actor_user_id,action,entity_type) VALUES($staff_id,'fixture','application_onboarding')"
deny 23514 sql postgres -c "INSERT INTO audit_logs(actor_user_id,action,entity_type) VALUES($staff_id,'fixture','application_onboarding')"

# Retained provider configuration is evidence and must block the 000042 rollback.
if output=$(sql postgres < db/migrations/000042_application_onboarding.down.sql 2>&1); then
    echo '000042 rollback unexpectedly removed retained configuration history' >&2
    exit 1
elif [[ "$output" != *'P0001:'* ]]; then
    echo "$output" >&2
    echo 'Expected rollback refusal with SQLSTATE P0001' >&2
    exit 1
fi

# A second empty database proves the same rollback succeeds before retained history exists.
docker_cmd exec -iu postgres "$node" createdb onboarding_fresh
for migration in db/migrations/0000{01..41}_*.up.sql; do
    [[ "$migration" == *000042* ]] && continue
    sql onboarding_fresh < "$migration"
done
sql onboarding_fresh < db/migrations/000042_application_onboarding.up.sql
sql onboarding_fresh < db/migrations/000042_application_onboarding.down.sql
sql onboarding_fresh < db/migrations/000042_application_onboarding.up.sql
sql onboarding_fresh < db/migrations/000043_onboarding_verification.up.sql
sql onboarding_fresh < db/migrations/000044_onboarding_verification_fences.up.sql
sql onboarding_fresh < db/migrations/000044_onboarding_verification_fences.down.sql
sql onboarding_fresh < db/migrations/000044_onboarding_verification_fences.up.sql
echo 'Onboarding fixture passed: metadata/rollback invariants, verification migration rollback, scoped verifier leases, session and epoch fences, stale setup supersession, idempotent completion, and lifecycle isolation.'
