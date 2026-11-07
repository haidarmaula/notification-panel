#!/usr/bin/env bash
# Exercise the offline installer and its actual database role boundaries.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
[[ $# == 0 ]] || exit 2
tmp=$(mktemp -d "${TMPDIR:-/tmp}/hello-lifecycle.XXXXXX")
node=$(basename "$tmp" | tr '[:upper:].' '[:lower:]-')
image='postgres:17@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d'
cleanup() {
    rc=$?
    trap - EXIT
    if [[ "$(docker inspect --format '{{index .Config.Labels "hello.lifecycle"}}' "$node" 2>/dev/null || true)" == "$node" ]]; then
        docker rm -fv "$node" >/dev/null || rc=1
    fi
    rm -f -- "$tmp/first" "$tmp/second" || rc=1
    rmdir "$tmp" || rc=1
    exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
docker run -d --pull=never --name "$node" --label "hello.lifecycle=$node" --network none \
    -e POSTGRES_HOST_AUTH_METHOD=trust --tmpfs /var/lib/postgresql/data "$image" postgres -c listen_addresses= >/dev/null
[[ "$(docker inspect --format '{{.HostConfig.NetworkMode}} {{len .HostConfig.PortBindings}}' "$node")" == 'none 0' ]]
sql() {
    local login=$1
    shift
    docker exec -iu postgres "$node" psql -XqAt -v ON_ERROR_STOP=1 -v VERBOSITY=verbose -U "$login" -d postgres "$@"
}
deny() {
    local code=$1 output
    shift
    if output=$("$@" 2>&1); then echo 'Expected operation to be denied' >&2; exit 1; fi
    if [[ "$output" != *"$code:"* ]]; then echo "$output" >&2; exit 1; fi
}
for _ in {1..80}; do
    if [[ "$(docker exec "$node" cat /proc/1/comm)" == postgres ]] && sql postgres -c 'SELECT 1' >/dev/null 2>&1; then break; fi
    sleep .25
done
for migration in db/migrations/*.up.sql; do sql postgres < "$migration"; done
# The unused migration may roll back safely before any evidence is written.
sql postgres < db/migrations/000039_application_dispatch_control.down.sql
sql postgres < db/migrations/000038_application_lifecycle.down.sql
sql postgres < db/migrations/000038_application_lifecycle.up.sql
sql postgres < db/migrations/000039_application_dispatch_control.up.sql
sql postgres <<'SQL'
CREATE ROLE hello_application_lifecycle_issuer;
CREATE ROLE hello_application_lifecycle_operator;
CREATE ROLE owner_review LOGIN IN ROLE hello_application_lifecycle_issuer;
CREATE ROLE operator_a LOGIN IN ROLE hello_application_lifecycle_operator;
CREATE ROLE operator_b LOGIN IN ROLE hello_application_lifecycle_operator;
CREATE ROLE runtime_fixture LOGIN;
CREATE ROLE retired_writer LOGIN;
INSERT INTO applications(public_id,key,name) VALUES
 ('10000000-0000-4000-8000-000000000001','project-a','A'),('10000000-0000-4000-8000-000000000002','project-b','B');
INSERT INTO application_dispatch_policy(application_id,push_ttl_seconds,key_ttl_seconds) SELECT id,60,3600 FROM applications;
UPDATE notification_runtime SET origin_epoch='20000000-0000-4000-8000-000000000001',source_epoch='20000000-0000-4000-8000-000000000002';
SQL
sql postgres < deploy/postgres/application-lifecycle-grants.sql
sql postgres < deploy/postgres/application-lifecycle-grants.sql
project=10000000-0000-4000-8000-000000000001
other=10000000-0000-4000-8000-000000000002
origin=20000000-0000-4000-8000-000000000001
source_epoch=20000000-0000-4000-8000-000000000002
grant_id() { printf '30000000-0000-4000-8000-%012d' "$1"; }
read -r ownership_hash writer_hash cohort_hash <<< "$(sql postgres -F ' ' -c "SELECT encode(sha256('ownership'),'hex'),encode(sha256('legacy writers'),'hex'),encode(sha256('cohorts'),'hex')")"
issue() {
    local id expiry revision=$2 previous=$3 destination=$4 origin_ref=${5:-$origin} ttl=${6:-'10 minutes'}
    id=$(grant_id "$1")
    expiry=$(sql postgres -c "SELECT clock_timestamp()+interval '$ttl'")
    sql owner_review -v grant_id="$id" -v project="$project" -v executor=operator_a -v origin="$origin_ref" -v source="$source_epoch" \
        -v revision="$revision" -v from_status="$previous" -v destination="$destination" -v ownership_sha256="$ownership_hash" \
        -v writers_sha256="$writer_hash" -v cohorts_sha256="$cohort_hash" -v retired_roles='{retired_writer}' -v expires_utc="$expiry" \
        < scripts/sql/issue-application-lifecycle.sql
}
apply() {
    sql "${2:-operator_a}" -v grant_id="$(grant_id "$1")" -v project="${3:-$project}" < scripts/sql/apply-application-lifecycle.sql
}
issue 1 1 PROVISIONING ACTIVE
deny 23514 sql postgres -c "UPDATE applications SET status='ACTIVE' WHERE public_id='$project'"
deny 42501 apply 1
deny P0001 sql postgres < scripts/sql/install-application-lifecycle.sql
sql postgres < scripts/sql/finalize-application-ownership.sql
sql postgres < scripts/sql/install-application-lifecycle.sql
sql postgres < scripts/sql/install-application-lifecycle.sql
deny 42501 apply 1 operator_b
deny 42501 apply 1 runtime_fixture
deny 42501 apply 1 operator_a "$other"
deny 42501 sql operator_a -c 'UPDATE application_lifecycle_grants SET applied_at=clock_timestamp()'
deny 42501 sql owner_review -c "UPDATE applications SET status='ACTIVE'"  # role has no registry write
deny 42501 apply 1  # reviewed legacy login still enabled
sql postgres -c 'ALTER ROLE retired_writer NOLOGIN; GRANT retired_writer TO runtime_fixture'
deny 42501 apply 1  # login still inherits the retired writer
sql postgres -c 'REVOKE retired_writer FROM runtime_fixture'
deny 42501 sql postgres -c "UPDATE application_dispatch_policy SET coverage_approved=true,hold_new=false WHERE application_id=(SELECT id FROM applications WHERE public_id='$project')"
issue 2 1 PROVISIONING ACTIVE "$source_epoch"
deny 42501 apply 2
issue 3 2 PROVISIONING ACTIVE
deny 23514 apply 3
issue 4 1 PROVISIONING ACTIVE "$origin" '1 second'
sql postgres -c 'SELECT pg_sleep(1.01)' >/dev/null
deny 42501 apply 4
issue 5 1 PROVISIONING ACTIVE
sql owner_review -c "SELECT revoke_application_lifecycle('$(grant_id 5)','$project')" >/dev/null
deny 42501 apply 5
# Even a broadly privileged registry writer cannot forge a lifecycle receipt.
sql postgres -c 'GRANT SELECT,INSERT,UPDATE ON applications TO runtime_fixture; GRANT USAGE ON SEQUENCE applications_id_seq TO runtime_fixture'
sql postgres -c 'GRANT UPDATE(applied_at) ON application_lifecycle_grants TO operator_a; GRANT SELECT(id) ON application_lifecycle_grants TO operator_a'
deny 23514 sql operator_a -c "UPDATE application_lifecycle_grants SET applied_at=clock_timestamp() WHERE id='$(grant_id 1)'"
sql postgres -c 'REVOKE UPDATE(applied_at),SELECT(id) ON application_lifecycle_grants FROM operator_a'
deny 23514 sql runtime_fixture -c "UPDATE applications SET status='ACTIVE',revision=2 WHERE public_id='$project'"
deny 23514 sql runtime_fixture -c "INSERT INTO applications(key,name,status) VALUES('injected','Injected','ACTIVE')"
# Overlapping retries on one authorized revision must return the same receipt.
apply 1 > "$tmp/first" &
first_pid=$!
apply 1 > "$tmp/second" &
second_pid=$!
wait "$first_pid"
wait "$second_pid"
[[ "$(<"$tmp/first")" == 2 && "$(<"$tmp/second")" == 2 ]]
[[ "$(sql postgres -c "SELECT status,revision,hold_new,coverage_approved FROM applications a JOIN application_dispatch_policy p ON p.application_id=a.id WHERE public_id='$project'")" == 'ACTIVE|2|t|f' ]]
# Simulate a separately reviewed live cohort. Holding the project closes its policy,
# and a future enrollment-only reactivation requires fencing that cohort first.
sql postgres <<'SQL'
INSERT INTO users(application_id,external_id) SELECT id,'fixture-user' FROM applications WHERE key='project-a';
INSERT INTO mobile_installations(application_id,owner_user_id,proof_verifier) SELECT application_id,id,sha256('fixture') FROM users;
INSERT INTO installation_bindings(application_id,installation_id,owner_user_id,owner_epoch,platform,provider_namespace,provider_route_key,
    address_type,address_fingerprint,address_ciphertext,encryption_key_version)
SELECT application_id,id,owner_user_id,owner_epoch,'ANDROID','fixture','android','FCM_REGISTRATION_TOKEN',sha256('fixture'),'fixture',1 FROM mobile_installations;
INSERT INTO binding_route_grants(id,application_id,binding_id,operator_role,origin_epoch,source_epoch,expected_revision,destination,cohort_id,evidence_id,
    reason,issued_at,expires_at,applied_at)
SELECT gen_random_uuid(),b.application_id,b.id,'operator_a'::regrole,r.origin_epoch,r.source_epoch,0,'FCM_ACTIVE',gen_random_uuid(),gen_random_uuid(),
    'pilot',now()-interval '1 second',now()+interval '1 day',now() FROM installation_bindings b CROSS JOIN notification_runtime r;
SQL
source scripts/fixtures/application-dispatch.sh
issue 6 2 ACTIVE HELD
[[ "$(apply 6)" == 3 ]]
[[ "$(sql postgres -c "SELECT status,revision,hold_new,coverage_approved FROM applications a JOIN application_dispatch_policy p ON p.application_id=a.id WHERE public_id='$project'")" == 'HELD|3|t|f' ]]
issue 7 3 HELD ACTIVE
deny 42501 apply 7
[[ "$(apply 1)" == 2 ]]  # retry proves the old outcome and cannot reopen HELD
issue 8 3 HELD RETIRED
[[ "$(apply 8)" == 4 ]]
deny 23514 apply 7
deny 23514 sql postgres -c "UPDATE application_lifecycle_grants SET expires_at=clock_timestamp()+interval '1 hour' WHERE id='$(grant_id 8)'"
deny 23514 sql postgres -c 'TRUNCATE application_lifecycle_grants'
deny P0001 sql postgres < db/migrations/000038_application_lifecycle.down.sql
[[ "$(sql postgres -c "SELECT status,revision FROM applications WHERE public_id='$other'")" == 'PROVISIONING|1' ]]
[[ "$(sql postgres -c 'SELECT sealed,migration_hold FROM notification_runtime')" == 't|t' ]]
[[ "$(sql postgres -c 'SELECT count(*) FROM application_lifecycle_grants WHERE applied_at IS NOT NULL')" == 3 ]]
echo 'Lifecycle fixture passed: enrollment ACTIVE → HELD → RETIRED, 3 immutable transitions, 1 untouched project.'
