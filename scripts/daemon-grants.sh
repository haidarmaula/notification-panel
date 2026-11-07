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
tmp=$(mktemp -d "${TMPDIR:-/tmp}/hello-daemon-grants.XXXXXX")
node=$(basename "$tmp" | tr '[:upper:].' '[:lower:]-')
label="hello.daemon.grants=$node"
cleanup() {
    status=$?
    trap - EXIT
    if [[ "$(docker inspect --format '{{ index .Config.Labels "hello.daemon.grants" }}' "$node" 2>/dev/null || true)" == "$node" ]]; then
        docker rm -fv "$node" >/dev/null || status=1
    fi
    rm -rf -- "$tmp" || status=1
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 GOFLAGS= GOWORK=off go test -mod=readonly -tags=integration -trimpath -c -o "$tmp/check" ./scripts/daemon-grants
docker run -d --pull never --network none --name "$node" --label "$label" \
    -e POSTGRES_HOST_AUTH_METHOD=trust --tmpfs /var/lib/postgresql/data \
    -v "$tmp/check:/check:ro" -v "$PWD/deploy/postgres:/grants:ro" -v "$PWD/scripts/fixtures:/fixtures:ro" -v "$PWD/internal/observability/workload.sql:/workload.sql:ro" \
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
CREATE ROLE hello_scheduler;
CREATE ROLE hello_admission;
CREATE ROLE hello_delivery;
CREATE ROLE fixture_scheduler LOGIN;
CREATE ROLE fixture_admission LOGIN;
CREATE ROLE fixture_delivery LOGIN;
GRANT hello_scheduler TO fixture_scheduler;
GRANT hello_admission TO fixture_admission;
GRANT hello_delivery TO fixture_delivery;
CREATE ROLE fixture_unrelated LOGIN;
SQL
for migration in db/migrations/*.up.sql; do owner < "$migration"; done
owner -c 'GRANT SELECT(name) ON applications TO fixture_unrelated'
owner -f /grants/daemon-grants.sql
owner -f /grants/daemon-grants.sql
# An unexpected right must fail before granting more or touching unrelated ACLs.
owner -c 'GRANT DELETE ON notification_runtime TO hello_delivery'
if owner -f /grants/daemon-grants.sql > "$tmp/drift.log" 2>&1; then echo 'Accepted unsafe privilege drift' >&2; exit 1; fi
grep -q 'unexpected effective DELETE on notification_runtime' "$tmp/drift.log"
owner -c 'REVOKE DELETE ON notification_runtime FROM hello_delivery'
owner -f /grants/daemon-grants.sql
if owner < db/migrations/000035_protected_daemon_grants.down.sql > "$tmp/rollback.log" 2>&1; then echo 'Dropped guards under active grants' >&2; exit 1; fi
grep -q 'remove lock-only column grants' "$tmp/rollback.log"
docker exec -iu postgres -e HELLO_DAEMON_GRANTS_FIXTURE=1 "$node" /check -test.run='^TestProtectedDatabaseRoles$' -test.v -test.timeout=60s
