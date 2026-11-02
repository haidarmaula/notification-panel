#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
export GOWORK=off
if [[ $# != 0 ]]; then
  printf 'Usage: bash scripts/integration.sh\n' >&2
  exit 2
fi

for tool in docker postman migrate go python3; do
  command -v "$tool" >/dev/null || { printf 'Missing prerequisite: %s\n' "$tool" >&2; exit 1; }
done
docker info >/dev/null
compose=(docker compose --env-file /dev/null -f compose.integration.yml -p hello-integration)
"${compose[@]}" config --quiet

# Fixed project/ports must never be shared with another active run.
lock_dir="${TMPDIR:-/tmp}/hello-integration.lock"
mkdir "$lock_dir" || { printf 'Integration lock exists: %s\n' "$lock_dir" >&2; exit 1; }
log_dir=""
simulation_pid=""
owns_compose=false
cleanup() {
  result=$?
  trap - EXIT
  if [[ -n "$simulation_pid" ]] && kill -0 "$simulation_pid" 2>/dev/null; then
    kill -INT "$simulation_pid" || true
    for _ in {1..30}; do
      kill -0 "$simulation_pid" 2>/dev/null || break
      sleep 0.1
    done
    if kill -0 "$simulation_pid" 2>/dev/null; then
      kill -TERM "$simulation_pid" || true
      for _ in {1..20}; do
        kill -0 "$simulation_pid" 2>/dev/null || break
        sleep 0.1
      done
      if kill -0 "$simulation_pid" 2>/dev/null; then kill -KILL "$simulation_pid" || true; fi
    fi
    wait "$simulation_pid" || true
  fi
  if [[ "$result" != 0 && -n "$log_dir" ]]; then
    tail -n 20 "$log_dir/simulation.err" "$log_dir/simulation.ndjson" >&2 || true
  fi
  if [[ "$owns_compose" == true ]]; then
    "${compose[@]}" down --volumes --remove-orphans || result=1
  fi
  if [[ -n "$log_dir" ]]; then rm -rf -- "$log_dir"; fi
  rmdir "$lock_dir" || result=1
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
if [[ -n "$("${compose[@]}" ps -a -q)" ]]; then
  printf 'Refusing to reuse existing hello-integration containers. Inspect them first.\n' >&2
  exit 1
fi
python3 - <<'PY'
import socket
for port in (15432, 19092, 14010):
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', port))
PY
log_dir="$(mktemp -d "${TMPDIR:-/tmp}/hello-integration.XXXXXX")"
owns_compose=true
"${compose[@]}" up -d --wait --wait-timeout 120

db_url='postgres://integration:integration_only@127.0.0.1:15432/notification_integration?sslmode=disable&connect_timeout=5&statement_timeout=10000&lock_timeout=5000'
# Only the freshly created fixture DB is migrated down; never accept a user DSN.
migrate -path db/migrations -database "$db_url" up
migrate -path db/migrations -database "$db_url" down -all
migrate -path db/migrations -database "$db_url" up
for topic in notification.send.requested notification.delivery.updated; do
  "${compose[@]}" exec -T kafka /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server kafka:9094 --create --topic "$topic" \
    --partitions 1 --replication-factor 1 --config min.insync.replicas=1
done

postman simulation run postman/simulations/notification-dependencies.sim.yaml \
  --port 14010 --no-history --output ndjson \
  >"$log_dir/simulation.ndjson" 2>"$log_dir/simulation.err" &
simulation_pid=$!
python3 - "$simulation_pid" <<'PY'
import json, os, sys, time, urllib.request
pid = int(sys.argv[1])
deadline = time.monotonic() + 20
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
while time.monotonic() < deadline:
    os.kill(pid, 0)
    try:
        with opener.open('http://127.0.0.1:14010/fcm/__health', timeout=1) as r:
            if json.load(r) == {'fixture': 'fcm', 'ready': True}:
                break
    except (OSError, ValueError):
        time.sleep(0.2)
else:
    raise SystemExit('FCM simulation readiness timed out')
PY
HELLO_INTEGRATION=1 go test -mod=readonly -tags=integration -count=1 -timeout=90s -v ./tests/integration
python3 - "$log_dir/simulation.ndjson" <<'PY'
import collections, json, sys, time
deadline = time.monotonic() + 5
while True:
    events = []
    with open(sys.argv[1]) as f:
        for line in f:
            try:
                events.append(json.loads(line))
            except ValueError:
                pass
    requests = [e for e in events if e.get('event') == 'request' and e.get('method') == 'POST'
                and e.get('path') == '/v1/projects/integration-fixture/messages:send']
    statuses = collections.Counter(e['statusCode'] for e in requests)
    if {200, 404, 429, 503}.issubset(statuses) and statuses[200] >= 2:
        break
    if time.monotonic() >= deadline:
        raise SystemExit(f'Missing SDK-to-fixture evidence: {statuses}')
    time.sleep(0.1)
print(f'Simulation evidence: {len(requests)} actual SDK HTTP requests; statuses {dict(statuses)}')
PY
