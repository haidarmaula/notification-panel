#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
[[ $# == 0 ]] || exit 2
target=${DOCKER_HOST:-$(docker context inspect --format '{{(index .Endpoints "docker").Host}}')}
[[ "$target" == unix:///* ]] || { echo 'A local Unix Docker socket is required' >&2; exit 2; }
case "$(docker info --format '{{.Architecture}}')" in
    arm64|aarch64) arch=arm64 ;;
    x86_64|amd64) arch=amd64 ;;
    *) exit 2 ;;
esac
image='postgres:17@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d'
tmp=$(mktemp -d "${TMPDIR:-/tmp}/hello-server-grants.XXXXXX")
node=$(basename "$tmp" | tr '[:upper:].' '[:lower:]-')
label="hello.server.grants=$node"
cleanup() {
    status=$?
    trap - EXIT
    if [[ "$(docker inspect --format '{{ index .Config.Labels "hello.server.grants" }}' "$node" 2>/dev/null || true)" == "$node" ]]; then
        docker rm -fv "$node" >/dev/null || status=1
    fi
    rm -rf -- "$tmp" || status=1
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
docker run -d --pull never --network none --name "$node" --label "$label" \
    -e POSTGRES_HOST_AUTH_METHOD=trust --tmpfs /var/lib/postgresql/data \
    -v "$PWD/db/migrations:/migrations:ro" -v "$PWD/deploy/postgres:/grants:ro" \
    "$image" postgres -c listen_addresses= >/dev/null
[[ "$(docker inspect --format '{{.HostConfig.NetworkMode}} {{len .HostConfig.PortBindings}}' "$node")" == 'none 0' ]]
admin() { docker exec -iu postgres "$node" psql -XqAt -v ON_ERROR_STOP=1 -d postgres "$@"; }
owner() { admin -U fixture_owner "$@"; }
for _ in {1..60}; do
    if [[ "$(docker exec "$node" cat /proc/1/comm)" == postgres ]] && admin -c 'SELECT 1' >/dev/null 2>&1; then break; fi
    sleep .25
done
admin <<'SQL'
CREATE ROLE fixture_owner LOGIN;
ALTER DATABASE postgres OWNER TO fixture_owner;
CREATE ROLE hello_server NOLOGIN;
CREATE ROLE fixture_server LOGIN;
GRANT hello_server TO fixture_server;
SQL
for migration in db/migrations/*.up.sql; do owner < "$migration"; done
owner -f /grants/server-grants.sql
owner -f /grants/server-grants.sql
owner -Atc "SELECT has_table_privilege('fixture_server','public.staff_users','SELECT')
    AND has_column_privilege('fixture_server','public.notifications','status','UPDATE')
    AND NOT has_table_privilege('fixture_server','public.notifications','UPDATE')
    AND has_column_privilege('fixture_server','public.device_tokens','platform','SELECT')
    AND NOT has_column_privilege('fixture_server','public.device_tokens','push_token','SELECT')
    AND has_column_privilege('fixture_server','public.mobile_identities','user_id','UPDATE')
    AND has_column_privilege('fixture_server','public.mobile_origin_uses','nonce_digest','SELECT')
    AND NOT has_column_privilege('fixture_server','public.mobile_origin_uses','expires_at','SELECT')
    AND NOT has_table_privilege('fixture_server','public.mobile_origin_uses','SELECT')
    AND NOT has_table_privilege('fixture_server','public.notification_replay_requests','SELECT')
    AND has_sequence_privilege('fixture_server','public.notification_send_outbox_id_seq','USAGE')" | grep -qx t
owner -c 'GRANT DELETE ON TABLE public.device_tokens TO hello_server'
if owner -f /grants/server-grants.sql >"$tmp/drift.log" 2>&1; then
    echo 'Accepted unsafe server privilege drift' >&2
    exit 1
fi
grep -q 'unexpected effective DELETE on device_tokens' "$tmp/drift.log"
owner -c 'REVOKE DELETE ON TABLE public.device_tokens FROM hello_server'
owner -f /grants/server-grants.sql
owner <<'SQL'
INSERT INTO applications(key,name) VALUES('server-policy-test','Server policy test');
INSERT INTO users(application_id,external_id,name)
SELECT id,'server-policy-user-1','Server policy user 1' FROM applications WHERE key='server-policy-test';
INSERT INTO users(application_id,external_id,name)
SELECT id,'server-policy-user-2','Server policy user 2' FROM applications WHERE key='server-policy-test';
INSERT INTO mobile_identities(application_id,identity_namespace,subject,user_id)
SELECT a.id,'server-policy','server-policy-subject',u.id FROM applications a
JOIN users u ON u.application_id=a.id WHERE a.key='server-policy-test' AND u.external_id='server-policy-user-1';
INSERT INTO roles(name,description) VALUES('SERVER_POLICY_TEST','Server grant fixture');
INSERT INTO staff_users(role_id,name,email,password_hash)
SELECT id,'Server policy staff','server-policy@example.test','fixture-hash' FROM roles WHERE name='SERVER_POLICY_TEST';
INSERT INTO notifications(application_id,title,body,created_by)
SELECT a.id,'Server policy campaign','Fixture body',s.id FROM applications a CROSS JOIN staff_users s
WHERE a.key='server-policy-test' AND s.email='server-policy@example.test';
INSERT INTO notification_send_keys(application_id,staff_user_id,key_source,key,notification_id,queued_at,deadline_at,expires_at)
SELECT a.id,s.id,'client','server-policy-key',n.id,clock_timestamp(),clock_timestamp()+interval '1 minute',clock_timestamp()+interval '1 hour'
FROM applications a CROSS JOIN staff_users s CROSS JOIN notifications n
WHERE a.key='server-policy-test' AND s.email='server-policy@example.test' AND n.title='Server policy campaign';
SQL
admin <<'SQL'
SET SESSION AUTHORIZATION fixture_server;
BEGIN;
WITH scope AS MATERIALIZED (
    SELECT public_id FROM applications WHERE key='server-policy-test' FOR SHARE
)
INSERT INTO mobile_origin_uses(application_public_id,issuer,nonce_digest,expires_at)
SELECT public_id,'server-policy-origin',decode(repeat('00',32),'hex'),clock_timestamp()+interval '1 minute' FROM scope
ON CONFLICT(application_public_id,issuer,nonce_digest) DO NOTHING;
WITH scope AS MATERIALIZED (
    SELECT public_id FROM applications WHERE key='server-policy-test' FOR SHARE
)
INSERT INTO mobile_origin_uses(application_public_id,issuer,nonce_digest,expires_at)
SELECT public_id,'server-policy-origin',decode(repeat('00',32),'hex'),clock_timestamp()+interval '1 minute' FROM scope
ON CONFLICT(application_public_id,issuer,nonce_digest) DO NOTHING;
ROLLBACK;
SQL
# Planning validates privileges even when the fixture has no delivery rows.
admin <<'SQL' >/dev/null
SET SESSION AUTHORIZATION fixture_server;
EXPLAIN SELECT dispatch_revision FROM public.application_dispatch_policy;
EXPLAIN SELECT prepared_pilot_cohort FROM public.notification_preparations;
EXPLAIN INSERT INTO public.notification_observations(application_id,delivery_id,event,received_at,observed_at)
VALUES(1,'105d8c55-8e36-4758-965a-1d6b6db77e43','DELIVERED',clock_timestamp(),NULL)
ON CONFLICT(delivery_id,event) DO NOTHING;
SQL
# Receipt identity checks reuse execution routing, including resume authorization.
admin -c "SET SESSION AUTHORIZATION fixture_server; SELECT application_id,binding_id,revision,destination,origin_epoch,source_epoch,resume_from,cohort_id FROM public.current_binding_routes LIMIT 1" >/dev/null
admin -c "SET SESSION AUTHORIZATION fixture_server; SELECT user_id FROM public.mobile_identities WHERE subject='server-policy-subject' FOR SHARE" >/dev/null
if admin -c "SET SESSION AUTHORIZATION fixture_server; UPDATE public.mobile_identities SET user_id=(SELECT id FROM public.users WHERE external_id='server-policy-user-2') WHERE subject='server-policy-subject'" >"$tmp/identity.log" 2>&1; then
    echo 'Server changed an immutable mobile identity' >&2
    exit 1
fi
grep -q 'server lock-only identity is immutable' "$tmp/identity.log"
admin -c "SET SESSION AUTHORIZATION fixture_server; SELECT key FROM public.notification_send_keys WHERE key='server-policy-key' FOR UPDATE" >/dev/null
if admin -c "SET SESSION AUTHORIZATION fixture_server; UPDATE public.notification_send_keys SET key='server-policy-key-mutated' WHERE key='server-policy-key'" >"$tmp/send-key.log" 2>&1; then
    echo 'Server changed an immutable send key' >&2
    exit 1
fi
grep -q 'server lock-only identity is immutable' "$tmp/send-key.log"
if owner < db/migrations/000041_guard_server_lock_only_updates.down.sql >"$tmp/rollback.log" 2>&1; then
    echo 'Removed lock-only guards while server grants remain' >&2
    exit 1
fi
grep -q 'revoke server lock-only column grants' "$tmp/rollback.log"
