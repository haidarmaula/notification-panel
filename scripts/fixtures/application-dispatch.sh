# Sourced only by the isolated application-lifecycle fixture after reviewed
# enrollment. No caller-supplied DSN or target may reach these checks.
[[ "$node" == hello-lifecycle-* && "$(docker inspect --format '{{.HostConfig.NetworkMode}}' "$node")" == none ]]
sql postgres <<'SQL'
CREATE ROLE hello_dispatch_issuer;
CREATE ROLE hello_dispatch_operator;
CREATE ROLE dispatch_review LOGIN IN ROLE hello_dispatch_issuer;
CREATE ROLE dispatch_operator LOGIN IN ROLE hello_dispatch_operator;
CREATE ROLE dispatch_other LOGIN IN ROLE hello_dispatch_operator;
GRANT EXECUTE ON FUNCTION hold_delivery_dispatch(bigint) TO runtime_fixture;
SQL
sql postgres < deploy/postgres/dispatch-policy-grants.sql
sql postgres < deploy/postgres/dispatch-policy-grants.sql
dispid() { printf '40000000-0000-4000-8000-%012d' "$1"; }
pilot=$(sql postgres -c 'SELECT cohort_id FROM current_binding_routes')
app=$(sql postgres -c "SELECT id FROM applications WHERE public_id='$project'")
state() { sql postgres -c "SELECT dispatch_revision,dispatch_mode,hold_new,coverage_approved,coalesce(dispatch_cohort::text,'') FROM application_dispatch_policy WHERE application_id=$app"; }
dispg() {
    local id=$1 rev=$2 prev=$3 dest=$4 cohort=$5 reason=$6 role=${7:-dispatch_operator} expected_app=${8:-2}
    local expiry
    expiry=$(sql postgres -c "SELECT clock_timestamp()+interval '10 minutes'")
    sql dispatch_review -v grant_id="$(dispid "$id")" -v project="$project" -v executor="$role" -v application_revision="$expected_app" -v dispatch_revision="$rev" \
        -v from_mode="$prev" -v destination="$dest" -v cohort="$cohort" -v reason="$reason" -v origin="$origin" -v source="$source_epoch" \
        -v legacy_sha256="$writer_hash" -v consent_sha256="$cohort_hash" -v coverage_sha256="$ownership_hash" -v quota_sha256="$quota_hash" \
        -v expires_utc="$expiry" < scripts/sql/issue-application-dispatch.sql
}
dispatch() {
    sql "${2:-dispatch_operator}" -v grant_id="$(dispid "$1")" -v project="${3:-$project}" < scripts/sql/apply-application-dispatch.sql
}
quota_hash=$(sql postgres -c "SELECT encode(sha256('quota allocation'),'hex')")
[[ "$(state)" == '1|HELD|t|f|' ]]
dispg 1 1 HELD PILOT "$pilot" pilot
deny 42501 dispatch 1   # global seal and migration hold are still active
# Only the disposable fixture opens its global runtime. Project routines leave it alone.
sql postgres -c 'UPDATE notification_runtime SET sealed=false,migration_hold=false'
sql postgres -c 'ALTER ROLE dispatch_operator CREATEDB'
deny 42501 dispatch 1
sql postgres -c 'ALTER ROLE dispatch_operator NOCREATEDB; ALTER ROLE dispatch_review CREATEROLE'
deny 42501 dispatch 1
sql postgres -c 'ALTER ROLE dispatch_review NOCREATEROLE; GRANT UPDATE(hold_new) ON application_dispatch_policy TO dispatch_review'
deny 42501 dispatch 1
sql postgres -c 'REVOKE UPDATE(hold_new) ON application_dispatch_policy FROM dispatch_review'
deny 42501 dispatch 1 dispatch_other
deny 42501 dispatch 1 dispatch_operator "$other"
deny 42501 dispatch 1 runtime_fixture
deny 42501 sql dispatch_operator -c 'UPDATE application_dispatch_policy SET hold_new=false,coverage_approved=true'
sql postgres -c 'GRANT UPDATE(hold_new,coverage_approved) ON application_dispatch_policy TO runtime_fixture'
deny 42501 sql runtime_fixture -c 'UPDATE application_dispatch_policy SET hold_new=false,coverage_approved=true'
deny 42501 sql dispatch_review -c 'UPDATE application_dispatch_grants SET applied_at=clock_timestamp()'
dispg 2 1 HELD PILOT "$other" pilot
deny 42501 dispatch 2    # no matching binding cohort
dispg 3 1 HELD PILOT "$pilot" pilot dispatch_operator 1
deny 23514 dispatch 3    # stale registry revision
deny 23514 dispg 4 1 HELD FULL "$pilot" full_coverage
dispg 5 1 HELD PILOT "$pilot" pilot
sql dispatch_review -c "SELECT revoke_application_dispatch('$(dispid 5)','$project')" >/dev/null
deny 42501 dispatch 5
dispatch 1 > "$tmp/first" &
first_pid=$!
dispatch 1 > "$tmp/second" &
second_pid=$!
wait "$first_pid"
wait "$second_pid"
[[ "$(<"$tmp/first")" == 2 && "$(<"$tmp/second")" == 2 && "$(state)" == "2|PILOT|f|f|$pilot" ]]
dispg 6 2 PILOT FULL "$pilot" full_coverage
sql runtime_fixture -c "SELECT hold_delivery_dispatch($app)" >/dev/null
[[ "$(state)" == '3|HELD|t|f|' && "$(dispatch 1)" == 2 ]]
deny 23514 dispatch 6   # pending broadcast approval cannot reopen a provider hold
dispg 7 3 HELD PILOT "$pilot" pilot
sql runtime_fixture -c "SELECT hold_delivery_dispatch($app)" >/dev/null
deny 23514 dispatch 7   # a repeated incident invalidates an outstanding grant too
dispg 8 4 HELD PILOT "$pilot" revalidation
[[ "$(dispatch 8)" == 5 ]]
dispg 9 5 PILOT FULL "$other" full_coverage
deny 42501 dispatch 9
dispg 10 5 PILOT FULL "$pilot" full_coverage
[[ "$(dispatch 10)" == 6 && "$(state)" == '6|FULL|f|t|' ]]
dispg 11 6 FULL HELD '' rollback
[[ "$(dispatch 11)" == 7 && "$(dispatch 10)" == 6 && "$(state)" == '7|HELD|t|f|' ]]
sql postgres -c 'GRANT UPDATE(application_id),SELECT(application_id) ON application_dispatch_policy TO runtime_fixture'
deny 23514 sql runtime_fixture -c "UPDATE application_dispatch_policy SET application_id=application_id+1000000 WHERE application_id=$app"
deny 23514 sql postgres -c "DELETE FROM application_dispatch_policy WHERE application_id=$app"
deny 23514 sql postgres -c 'TRUNCATE application_dispatch_policy'
deny 23514 sql postgres -c 'TRUNCATE application_dispatch_grants'
deny P0001 sql postgres < db/migrations/000039_application_dispatch_control.down.sql
[[ "$(sql postgres -c "SELECT count(*) FROM application_dispatch_grants WHERE applied_at IS NOT NULL")" == 4 ]]
[[ "$(sql postgres -c "SELECT dispatch_revision,dispatch_mode FROM application_dispatch_policy WHERE application_id<>(SELECT id FROM applications WHERE public_id='$project')")" == '1|HELD' ]]
[[ "$(sql postgres -c 'SELECT sealed,migration_hold FROM notification_runtime')" == 'f|f' ]]
sql postgres -c 'UPDATE notification_runtime SET sealed=true,migration_hold=true'
echo 'Dispatch fixture passed: two pilot releases, separate full approval, rollback, and fresh generation after every provider hold.'
