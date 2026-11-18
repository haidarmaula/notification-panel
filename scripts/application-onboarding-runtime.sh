#!/usr/bin/env bash
# Isolated PostgreSQL fixture for runtime reservation, preflight and delivery row isolation.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
[[ $# == 0 ]] || exit 2
image='postgres:17@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d'
timeout_bin=$(command -v timeout || command -v gtimeout || true)
[[ -n "$timeout_bin" ]] || { echo 'timeout or gtimeout is required' >&2; exit 2; }
tmp=$(mktemp -d "${TMPDIR:-/tmp}/hello-runtime-reservation.XXXXXX")
node=$(basename "$tmp" | tr '[:upper:].' '[:lower:]-')
log="$tmp/run.log"
exec 3>&1 4>&2
exec >"$log" 2>&1
docker_cmd() { "$timeout_bin" -k 3s 30s docker "$@"; }
cleanup() {
    status=$?
    trap - EXIT
    if [[ "$(docker_cmd inspect --format '{{ index .Config.Labels "hello.runtime-reservation" }}' "$node" 2>/dev/null || true)" == "$node" ]]; then
        docker_cmd rm -fv "$node" >/dev/null || status=1
    fi
    exec 1>&3 2>&4
    if (( status != 0 )); then
        cat "$log" >&2
        echo "Full log preserved at $log" >&2
    else
        echo 'Runtime reservation PostgreSQL fixture passed (no activation or device delivery claimed)' >&3
        rm -rf -- "$tmp" || status=1
    fi
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
docker_cmd run -d --pull=never --name "$node" --label "hello.runtime-reservation=$node" --network none \
    -e POSTGRES_HOST_AUTH_METHOD=trust --tmpfs /var/lib/postgresql/data \
    -v "$PWD/db/migrations:/migrations:ro" -v "$PWD/deploy/postgres:/grants:ro" "$image" postgres -c listen_addresses= >/dev/null
[[ "$(docker_cmd inspect --format '{{.HostConfig.NetworkMode}} {{len .HostConfig.PortBindings}}' "$node")" == 'none 0' ]]
sql() { docker_cmd exec -iu postgres "$node" psql -U "$1" -d postgres -XqAt -v ON_ERROR_STOP=1 -v VERBOSITY=verbose "${@:2}"; }
assert_query() {
    local expected=$1 login=$2 description=$3 query=$4 actual
    actual=$(sql "$login" -c "$query")
    [[ "$actual" == "$expected" ]] || { echo "Failed: $description (expected '$expected', got '$actual')" >&2; exit 1; }
}
deny() {
    local code=$1 output
    shift
    if output=$("$@" 2>&1); then echo "Expected SQLSTATE $code" >&2; exit 1; fi
    [[ "$output" == *"$code:"* ]] || { echo "$output" >&2; echo "Expected SQLSTATE $code" >&2; exit 1; }
}
ready=false
for _ in {1..30}; do
    if [[ "$("$timeout_bin" -k 1s 3s docker exec "$node" cat /proc/1/comm)" == postgres ]] && sql postgres -c 'SELECT 1' >/dev/null 2>&1; then ready=true; break; fi
    sleep .25
done
[[ "$ready" == true ]] || { echo 'PostgreSQL startup deadline exceeded' >&2; exit 1; }
for migration in db/migrations/*.up.sql; do sql postgres < "$migration"; done
sql postgres <<'SQL'
CREATE ROLE hello_onboarding_runtime_executor NOLOGIN;
CREATE ROLE runtime_a LOGIN IN ROLE hello_onboarding_runtime_executor;
CREATE ROLE runtime_b LOGIN IN ROLE hello_onboarding_runtime_executor;
CREATE ROLE runtime_c LOGIN IN ROLE hello_onboarding_runtime_executor;
CREATE ROLE runtime_unscoped LOGIN IN ROLE hello_onboarding_runtime_executor;
CREATE ROLE hello_delivery NOLOGIN;
CREATE ROLE hello_admission NOLOGIN;
CREATE ROLE hello_scheduler NOLOGIN;
CREATE ROLE fixture_legacy_worker LOGIN IN ROLE hello_delivery;
CREATE ROLE fixture_worker_a LOGIN IN ROLE hello_delivery;
CREATE ROLE fixture_worker_b LOGIN IN ROLE hello_delivery;
SQL
sql postgres -f /grants/daemon-grants.sql
sql postgres < deploy/postgres/onboarding-runtime-grants.sql
sql postgres < deploy/postgres/onboarding-runtime-grants.sql
sql postgres <<'SQL'
INSERT INTO application_runtime_capacity_profile(environment,revision,max_applications,max_overlap,max_database_connections,delivery_database_connections,policy_sha256)
VALUES('staging',1,2,1,6,2,decode(repeat('ab',32),'hex'));
INSERT INTO applications(public_id,key,name) VALUES
    ('10000000-0000-4000-8000-000000000001','runtime-app-a','Runtime A'),
    ('10000000-0000-4000-8000-000000000002','runtime-app-b','Runtime B'),
    ('10000000-0000-4000-8000-000000000003','runtime-app-c','Runtime C');
INSERT INTO application_provider_configurations(id,application_id,version,environment,target_project_id,service_account_email,file_reference,state,created_by)
SELECT ('20000000-0000-4000-8000-'||lpad((row_number() OVER(ORDER BY id))::text,12,'0'))::uuid,
    id,1,'staging','fixture-project','fixture@example.invalid',
    ('20000000-0000-4000-8000-'||lpad((row_number() OVER(ORDER BY id))::text,12,'0'))::uuid,'VERIFIED',1
FROM applications WHERE key LIKE 'runtime-app-%';
UPDATE application_onboarding setup SET environment='staging',candidate_configuration_id=config.id
FROM application_provider_configurations config WHERE config.application_id=setup.application_id;
INSERT INTO application_runtime_executor_scopes(role_id,application_id,environment,profile_revision)
SELECT ('runtime_'||right(a.key,1))::regrole::oid,a.id,'staging',1 FROM applications a WHERE a.key LIKE 'runtime-app-%';
INSERT INTO application_worker_preflight_scopes(application_id,configuration_id,environment,generation,profile_revision,delivery_policy_sha256,worker_role)
SELECT setup.application_id,setup.candidate_configuration_id,'staging',1,1,decode(repeat('ef',32),'hex'),
    ('fixture_worker_'||right(app.key,1))::regrole::oid
FROM application_onboarding setup JOIN applications app ON app.id=setup.application_id
WHERE app.key IN ('runtime-app-a','runtime-app-b');
INSERT INTO application_dispatch_policy(application_id,hold_new,push_ttl_seconds,key_ttl_seconds)
SELECT id,false,300,300 FROM applications WHERE key LIKE 'runtime-app-%';
UPDATE notification_runtime SET sealed=false,migration_hold=false WHERE singleton;
SQL
assert_query '1' fixture_worker_a 'A sees only its app' "SELECT count(*) FROM applications WHERE public_id IN ('10000000-0000-4000-8000-000000000001','10000000-0000-4000-8000-000000000002')"
assert_query '10000000-0000-4000-8000-000000000002' fixture_worker_b 'B sees its own app after SET ROLE' 'SET ROLE hello_delivery; SELECT public_id FROM applications'
assert_query '3' fixture_legacy_worker 'unscoped legacy worker remains unchanged' 'SELECT count(*) FROM applications'
assert_query '1|f' fixture_worker_a 'reserved generation is not active' "SELECT * FROM delivery_worker_scope('10000000-0000-4000-8000-000000000001')"
assert_query '-1|f' fixture_worker_a 'different app cannot start with scoped worker login' "SELECT * FROM delivery_worker_scope('10000000-0000-4000-8000-000000000002')"
assert_query '0|f' fixture_legacy_worker 'legacy worker has no generation lease' "SELECT * FROM delivery_worker_scope('10000000-0000-4000-8000-000000000001')"
assert_query '0' fixture_worker_a 'direct cross-app writes see no rows' "WITH updated AS (UPDATE applications SET id=id WHERE public_id='10000000-0000-4000-8000-000000000002' RETURNING id) SELECT count(*) FROM updated"
app_b_id=$(sql postgres -c "SELECT id FROM applications WHERE key='runtime-app-b'")
app_a_id=$(sql postgres -c "SELECT id FROM applications WHERE key='runtime-app-a'")
sql fixture_worker_a -c "SET ROLE hello_delivery; SELECT hold_delivery_dispatch($app_b_id); SELECT hold_delivery_dispatch($app_a_id)"
assert_query 't|f' postgres 'definer holds only the calling worker app' "SELECT (SELECT hold_new FROM application_dispatch_policy WHERE application_id=$app_a_id),(SELECT hold_new FROM application_dispatch_policy WHERE application_id=$app_b_id)"
deny P0001 sql postgres < db/migrations/000047_onboarding_delivery_isolation.down.sql
origin=$(sql postgres -c 'SELECT origin_epoch FROM notification_runtime WHERE singleton')
source=$(sql postgres -c 'SELECT source_epoch FROM notification_runtime WHERE singleton')
app_a='10000000-0000-4000-8000-000000000001'
app_b='10000000-0000-4000-8000-000000000002'
app_c='10000000-0000-4000-8000-000000000003'
config_a='20000000-0000-4000-8000-000000000001'
config_b='20000000-0000-4000-8000-000000000002'
config_c='20000000-0000-4000-8000-000000000003'
reserve_a='30000000-0000-4000-8000-000000000001'
reserve_b='30000000-0000-4000-8000-000000000002'
reserve_c='30000000-0000-4000-8000-000000000003'
reserve() { printf "SELECT reserve_application_onboarding_runtime('%s','%s','%s',%s,1,1,'%s','%s')" "$1" "$2" "$3" "$4" "$origin" "$source"; }
check_a="SELECT check_application_onboarding_runtime('$app_a','$reserve_a',decode(repeat('ab',32),'hex'))"
preflight() { printf "SELECT check_application_onboarding_worker_preflight('%s','%s',decode(repeat('%s',32),'hex'),'%s','%s',decode(repeat('%s',32),'hex'),'%s')" "$1" "$2" "$3" "$4" "$5" "$6" "$7"; }
preflight_a=$(preflight "$app_a" "$reserve_a" ab fixture-project fixture@example.invalid ef fixture_worker_a)
assert_query t postgres 'installer grants scoped functions and no lifecycle or data authority' "SELECT has_function_privilege('runtime_a','reserve_application_onboarding_runtime(uuid,uuid,uuid,bigint,bigint,bigint,uuid,uuid)','EXECUTE') AND has_function_privilege('runtime_a','check_application_onboarding_runtime(uuid,uuid,bytea)','EXECUTE') AND has_function_privilege('runtime_a','release_application_onboarding_runtime(uuid,uuid,boolean)','EXECUTE') AND has_function_privilege('runtime_a','check_application_onboarding_worker_preflight(uuid,uuid,bytea,text,text,bytea,text)','EXECUTE') AND NOT has_function_privilege('runtime_a','revoke_application_lifecycle(uuid,uuid)','EXECUTE') AND NOT has_table_privilege('runtime_a','application_runtime_reservations','SELECT,INSERT,UPDATE,DELETE') AND NOT has_table_privilege('runtime_a','application_onboarding','SELECT,INSERT,UPDATE,DELETE') AND NOT has_table_privilege('runtime_a','application_worker_preflight_scopes','SELECT,INSERT,UPDATE,DELETE')"
deny P0001 sql runtime_a < deploy/postgres/onboarding-runtime-grants.sql
sql postgres -c 'ALTER ROLE hello_onboarding_runtime_executor LOGIN'
deny P0001 sql postgres < deploy/postgres/onboarding-runtime-grants.sql
sql postgres -c 'ALTER ROLE hello_onboarding_runtime_executor NOLOGIN'
assert_query "$reserve_a" runtime_a 'first reservation' "$(reserve "$app_a" "$reserve_a" "$config_a" 1)"
assert_query "$reserve_a" runtime_a 'exact retry' "$(reserve "$app_a" "$reserve_a" "$config_a" 1)"
assert_query t runtime_a 'scoped fresh preparation guard' "$check_a"
assert_query f runtime_b 'cross-app guard fails closed' "$check_a"
assert_query f runtime_a 'wrong policy digest fails closed' "SELECT check_application_onboarding_runtime('$app_a','$reserve_a',decode(repeat('cd',32),'hex'))"
assert_query t runtime_a 'worker policy, LOGIN and immutable provider metadata match' "$preflight_a"
sql postgres <<'SQL'
CREATE ROLE fixture_server LOGIN;
CREATE ROLE fixture_admission LOGIN;
CREATE ROLE fixture_scheduler LOGIN;
GRANT EXECUTE ON FUNCTION acknowledge_application_runtime(uuid,bigint,text,bytea,uuid)
    TO fixture_worker_a,fixture_worker_b,fixture_server,fixture_admission,fixture_scheduler;
INSERT INTO application_runtime_component_scopes(application_id,generation,component,login_role,policy_sha256)
SELECT a.id,1,s.component,s.login::regrole::oid,decode(repeat('ef',32),'hex')
FROM applications a CROSS JOIN (VALUES ('SERVER','fixture_server'),('ADMISSION','fixture_admission'),
    ('SCHEDULER','fixture_scheduler'),('DELIVERY','fixture_worker_a')) s(component,login)
WHERE a.key='runtime-app-a';
SQL
ack() { printf "SELECT acknowledge_application_runtime('%s',1,'%s',decode(repeat('%s',32),'hex'),'%s')" "$1" "$2" "$3" "$4"; }
instance_a='40000000-0000-4000-8000-000000000001'
instance_b='40000000-0000-4000-8000-000000000002'
ack_delivery=$(ack "$app_a" DELIVERY ef "$instance_a")
assert_query f fixture_worker_b 'other worker cannot acknowledge approved policy' "$ack_delivery"
assert_query f fixture_worker_a 'delivery digest mismatch' "$(ack "$app_a" DELIVERY ab "$instance_a")"
assert_query f fixture_worker_a 'delivery cannot acknowledge for server' "$(ack "$app_a" SERVER ef "$instance_a")"
assert_query t fixture_worker_a 'approved delivery process acknowledges' "$ack_delivery"
assert_query f fixture_worker_a 'different instance cannot replace a live lease' "$(ack "$app_a" DELIVERY ef "$instance_b")"
assert_query t fixture_worker_a 'same process renews lease' "$ack_delivery"
sql postgres -c 'GRANT EXECUTE ON FUNCTION acknowledge_shared_application_runtime(text,bytea,uuid) TO fixture_server,fixture_admission,fixture_scheduler'
shared_ack() { printf "SELECT acknowledge_shared_application_runtime('%s',decode(repeat('%s',32),'hex'),'%s')" "$1" "$2" "$instance_a"; }
assert_query 0 fixture_server 'server digest mismatch renews no lease' "$(shared_ack SERVER ab)"
assert_query 0 fixture_server 'server cannot renew for another component' "$(shared_ack ADMISSION ef)"
assert_query 0 fixture_server 'shared heartbeat cannot renew delivery' "$(shared_ack DELIVERY ef)"
assert_query 1 fixture_server 'approved server acknowledges' "$(shared_ack SERVER ef)"
assert_query 1 fixture_admission 'approved admission acknowledges' "$(shared_ack ADMISSION ef)"
assert_query 1 fixture_scheduler 'approved scheduler acknowledges' "$(shared_ack SCHEDULER ef)"
deny 42501 sql fixture_worker_a -c "$(shared_ack ADMISSION ef)"
assert_query 4 postgres 'one readiness acknowledgement per runtime component' 'SELECT count(*) FROM application_runtime_acknowledgements'
prepared="SELECT onboarding_runtime_prepared($app_a_id,1,'$config_a')"
assert_query t postgres 'all four live approvals make the exact runtime prepared' "$prepared"
assert_query f postgres 'different configuration cannot use runtime evidence' "SELECT onboarding_runtime_prepared($app_a_id,1,'$config_b')"
deny 42501 sql fixture_worker_a -c "$prepared"
deny 23514 sql postgres -c "UPDATE application_runtime_component_scopes SET policy_sha256=decode(repeat('ab',32),'hex') WHERE component='SERVER'"
sql postgres -c 'REVOKE EXECUTE ON FUNCTION acknowledge_shared_application_runtime(text,bytea,uuid) FROM fixture_scheduler'
assert_query f postgres 'revoked shared authority immediately invalidates prepared evidence' "$prepared"
sql postgres -c 'GRANT EXECUTE ON FUNCTION acknowledge_shared_application_runtime(text,bytea,uuid) TO fixture_scheduler'
sql postgres -c "UPDATE application_runtime_acknowledgements SET acknowledged_at=clock_timestamp()-interval '30 seconds',lease_expires_at=clock_timestamp()-interval '15 seconds' WHERE component='DELIVERY'"
assert_query f postgres 'expired worker lease invalidates prepared runtime' "$prepared"
assert_query t fixture_worker_a 'replacement takes over only after lease expiry' "$(ack "$app_a" DELIVERY ef "$instance_b")"
assert_query f fixture_worker_a 'former process cannot retake live lease' "$ack_delivery"
sql postgres -c 'UPDATE notification_runtime SET sealed=true WHERE singleton'
assert_query f fixture_worker_a 'runtime seal blocks live acknowledgement' "$(ack "$app_a" DELIVERY ef "$instance_b")"
sql postgres -c 'UPDATE notification_runtime SET sealed=false WHERE singleton'
deny 42501 sql fixture_worker_a -c 'DELETE FROM application_runtime_acknowledgements'
deny P0001 sql postgres < db/migrations/000048_onboarding_runtime_acknowledgement.down.sql
assert_query f runtime_b 'worker preflight cross-app LOGIN denied' "$preflight_a"
assert_query f runtime_unscoped 'worker preflight requires app scope' "$preflight_a"
deny 42501 sql fixture_worker_a -c "$preflight_a"
deny 42501 sql postgres -c "$preflight_a"
assert_query f runtime_a 'worker preflight checks capacity profile digest' "$(preflight "$app_a" "$reserve_a" cd fixture-project fixture@example.invalid ef fixture_worker_a)"
assert_query f runtime_a 'worker preflight checks target project' "$(preflight "$app_a" "$reserve_a" ab other-project fixture@example.invalid ef fixture_worker_a)"
assert_query f runtime_a 'worker preflight checks service account' "$(preflight "$app_a" "$reserve_a" ab fixture-project other@example.invalid ef fixture_worker_a)"
assert_query f runtime_a 'worker preflight checks exact delivery policy digest' "$(preflight "$app_a" "$reserve_a" ab fixture-project fixture@example.invalid cd fixture_worker_a)"
assert_query f runtime_a 'worker preflight checks approved worker role' "$(preflight "$app_a" "$reserve_a" ab fixture-project fixture@example.invalid ef fixture_worker_b)"
deny 23514 sql postgres -c "UPDATE application_worker_preflight_scopes SET worker_role='fixture_worker_b'::regrole::oid WHERE worker_role='fixture_worker_a'::regrole::oid"
sql postgres -c 'REVOKE hello_delivery FROM fixture_worker_a'
assert_query f runtime_a 'worker preflight checks current membership' "$preflight_a"
sql postgres -c 'GRANT hello_delivery TO fixture_worker_a'
assert_query t runtime_a 'restored dedicated worker membership' "$preflight_a"
deny 23505 sql runtime_a -c "$(reserve "$app_a" "$reserve_a" "$config_a" 2)"
deny 42501 sql runtime_b -c "$(reserve "$app_a" "$reserve_b" "$config_a" 1)"
deny 42501 sql runtime_unscoped -c "$(reserve "$app_a" "$reserve_b" "$config_a" 1)"
deny 42501 sql postgres -c "$(reserve "$app_a" "$reserve_b" "$config_a" 1)"
deny 42501 sql runtime_a -c "UPDATE application_onboarding SET runtime_generation=1"
deny 42501 sql runtime_a -c "INSERT INTO application_runtime_executor_scopes VALUES('runtime_unscoped'::regrole::oid,1,'staging',1)"
deny 42501 sql runtime_a -c "SELECT release_application_onboarding_runtime('$app_a','$reserve_a',false)"
deny 55000 sql runtime_a -c "SELECT release_application_onboarding_runtime('$app_a','$reserve_a',true)"
deny 55000 sql runtime_a -c "$(reserve "$app_a" '30000000-0000-4000-8000-000000000009' "$config_b" 2)"
sql postgres -c 'UPDATE notification_runtime SET sealed=true WHERE singleton'
assert_query f runtime_a 'sealed runtime blocks preparation' "$check_a"
assert_query f runtime_a 'sealed runtime blocks worker preflight' "$preflight_a"
deny 55000 sql runtime_b -c "$(reserve "$app_b" "$reserve_b" "$config_b" 1)"
sql postgres -c 'UPDATE notification_runtime SET sealed=false,migration_hold=true WHERE singleton'
assert_query f runtime_a 'migration hold blocks preparation' "$check_a"
assert_query f runtime_a 'migration hold blocks worker preflight' "$preflight_a"
deny 55000 sql runtime_b -c "$(reserve "$app_b" "$reserve_b" "$config_b" 1)"
sql postgres -c "UPDATE notification_runtime SET migration_hold=false,source_epoch=gen_random_uuid() WHERE singleton"
assert_query f runtime_a 'source epoch change fences preparation' "$check_a"
assert_query f runtime_a 'source epoch change fences worker preflight' "$preflight_a"
deny 55000 sql runtime_b -c "$(reserve "$app_b" "$reserve_b" "$config_b" 1)"
sql postgres -c "UPDATE notification_runtime SET source_epoch='$source' WHERE singleton"

# B holds the sole remaining application slot while C races its commit.
sql runtime_b -c "SET application_name='runtime-capacity-fixture'; BEGIN; $(reserve "$app_b" "$reserve_b" "$config_b" 1); SELECT pg_sleep(1); COMMIT" >"$tmp/concurrent.log" 2>&1 &
concurrent_pid=$!
sleeping=false
for _ in {1..30}; do
    if [[ "$(sql postgres -c "SELECT EXISTS(SELECT FROM pg_stat_activity WHERE application_name='runtime-capacity-fixture' AND wait_event='PgSleep')")" == t ]]; then sleeping=true; break; fi
    sleep .05
done
[[ "$sleeping" == true ]] || { wait "$concurrent_pid"; cat "$tmp/concurrent.log"; echo 'Concurrent reservation did not reach capacity lock' >&2; exit 1; }
deny 53300 sql runtime_c -c "$(reserve "$app_c" "$reserve_c" "$config_c" 1)"
wait "$concurrent_pid"
assert_query 2 postgres 'global capacity preserved across LOGINs' 'SELECT count(*) FROM application_runtime_reservations WHERE released_at IS NULL'

# One replacement consumes overlap; another is refused without losing records.
replacement_a='30000000-0000-4000-8000-000000000004'
assert_query "$replacement_a" runtime_a 'replacement overlap reservation' "$(reserve "$app_a" "$replacement_a" "$config_a" 2)"
assert_query f runtime_a 'worker preflight requires approval for exact generation' "$(preflight "$app_a" "$replacement_a" ab fixture-project fixture@example.invalid ef fixture_worker_a)"
deny 53300 sql runtime_b -c "$(reserve "$app_b" '30000000-0000-4000-8000-000000000005' "$config_b" 2)"
assert_query 6 postgres 'connection budget includes overlap' 'SELECT sum(database_connections) FROM application_runtime_reservations WHERE released_at IS NULL'

# Owner-only fault injection simulates a controller disappearing after an
# uncertain host effect. Expiry fences new preparation and retains capacity.
sql postgres -c "ALTER TABLE application_runtime_reservations DISABLE TRIGGER protect_runtime_reservation; UPDATE application_runtime_reservations SET reserved_at=clock_timestamp()-interval '2 minutes',prepare_before=clock_timestamp()-interval '10 seconds' WHERE id='$reserve_a'; ALTER TABLE application_runtime_reservations ENABLE TRIGGER protect_runtime_reservation"
assert_query f runtime_a 'expired preparation is fenced during release grace' "$check_a"
deny 55000 sql runtime_a -c "SELECT release_application_onboarding_runtime('$app_a','$reserve_a',true)"
assert_query 3 postgres 'release grace preserves capacity' 'SELECT count(*) FROM application_runtime_reservations WHERE released_at IS NULL'
sql postgres -c "ALTER TABLE application_runtime_reservations DISABLE TRIGGER protect_runtime_reservation; UPDATE application_runtime_reservations SET reserved_at=clock_timestamp()-interval '2 minutes',prepare_before=clock_timestamp()-interval '1 minute' WHERE id='$reserve_a'; ALTER TABLE application_runtime_reservations ENABLE TRIGGER protect_runtime_reservation"
assert_query f runtime_a 'expired reservation cannot prepare' "$check_a"
assert_query f runtime_a 'expired reservation cannot authorize worker preflight' "$preflight_a"
assert_query "$reserve_a" runtime_a 'expired replay keeps original allocation' "$(reserve "$app_a" "$reserve_a" "$config_a" 1)"
assert_query 3 postgres 'expired reservation still occupies capacity' 'SELECT count(*) FROM application_runtime_reservations WHERE released_at IS NULL'
deny 42501 sql runtime_b -c "SELECT release_application_onboarding_runtime('$app_b','$reserve_a',true)"
assert_query t runtime_a 'explicit absence release' "SELECT release_application_onboarding_runtime('$app_a','$reserve_a',true)"
assert_query t runtime_a 'release replay' "SELECT release_application_onboarding_runtime('$app_a','$reserve_a',true)"
assert_query f runtime_a 'released reservation cannot prepare' "$check_a"
assert_query 3 postgres 'immutable history retained' 'SELECT count(*) FROM application_runtime_reservations'
sql postgres -c 'UPDATE application_onboarding SET revision=revision+1'
assert_query f runtime_b 'setup revision change fences preparation' "SELECT check_application_onboarding_runtime('$app_b','$reserve_b',decode(repeat('ab',32),'hex'))"
sql postgres -c 'GRANT UPDATE ON application_runtime_reservations TO runtime_a'
deny P0001 sql postgres < deploy/postgres/onboarding-runtime-grants.sql
deny 42501 sql runtime_a -c "UPDATE application_runtime_reservations SET released_at=clock_timestamp() WHERE id='$replacement_a'"
sql postgres -c 'REVOKE UPDATE ON application_runtime_reservations FROM runtime_a'
sql postgres -c 'REVOKE hello_onboarding_runtime_executor FROM runtime_a'
deny 42501 sql runtime_a -c "$check_a"
deny 42501 sql runtime_a -c "$preflight_a"
assert_query '3|0' postgres 'preparation never activates or installs configuration' "SELECT count(*)||'|'||count(*) FILTER(WHERE current_configuration_id IS NOT NULL) FROM application_onboarding setup JOIN applications app ON app.id=setup.application_id WHERE app.status='PROVISIONING'"
deny P0001 sql postgres < db/migrations/000045_onboarding_runtime_preparation.down.sql
deny P0001 sql postgres < db/migrations/000046_onboarding_worker_preflight.down.sql
assert_query 3 postgres 'rollback cannot discard retained history' 'SELECT count(*) FROM application_runtime_reservations'
# Rollback/reapply is safe only before any profile, scope or reservation exists.
sql postgres -c 'CREATE DATABASE runtime_empty'
empty_sql() { docker_cmd exec -iu postgres "$node" psql -d runtime_empty -XqAt -v ON_ERROR_STOP=1 -v VERBOSITY=verbose; }
for migration in db/migrations/*.up.sql; do empty_sql < "$migration"; done
empty_sql < db/migrations/000051_onboarding_prepared_runtime_gate.down.sql
empty_sql < db/migrations/000050_onboarding_shared_runtime_leases.down.sql
empty_sql < db/migrations/000049_onboarding_delivery_lease_startup.down.sql
empty_sql < db/migrations/000048_onboarding_runtime_acknowledgement.down.sql
empty_sql < db/migrations/000047_onboarding_delivery_isolation.down.sql
empty_sql < db/migrations/000046_onboarding_worker_preflight.down.sql
empty_sql < deploy/postgres/onboarding-runtime-grants.sql
empty_sql < db/migrations/000045_onboarding_runtime_preparation.down.sql
empty_sql < db/migrations/000045_onboarding_runtime_preparation.up.sql
empty_sql < db/migrations/000046_onboarding_worker_preflight.up.sql
[[ "$(printf '%s\n' 'SELECT count(*) FROM application_runtime_reservations' | empty_sql)" == 0 ]] || { echo 'Empty migration rollback/reapply failed' >&2; exit 1; }
