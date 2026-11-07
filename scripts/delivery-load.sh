#!/usr/bin/env bash
# A separate project and internal network keep this experiment off integration
# ports and prevent the worker from reaching Google or a host database.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
export GOWORK=off

usage() {
  echo 'Usage: bash scripts/delivery-load.sh [devices=32] [campaigns=1] [healthy|transient|quota] [burst|soak] [requests-per-second=400] [database-connections=2] [active-poll-ms=100]' >&2
  exit 2
}
if [[ $# -gt 7 || ${1:-} == --help ]]; then usage; fi
count=${1:-32}
waves=${2:-1}
mode=${3:-healthy}
shape=${4:-burst}
pace=${5:-400}
pool_size=${6:-2}
active_ms=${7:-100}
[[ $count =~ ^[1-9][0-9]{0,4}$ && $waves =~ ^[1-9][0-9]?$ && $pace =~ ^[1-9][0-9]{0,3}$ && $pool_size =~ ^[1-9][0-9]?$ && $active_ms =~ ^[1-9][0-9]{1,2}$ ]] || usage
[[ ($mode == healthy || $mode == transient || $mode == quota) && ($shape == burst || $shape == soak) ]] || usage
((count <= 50000 && waves <= 25 && count * waves <= 100000 && pool_size >= 2 && pool_size <= 32 && active_ms <= 100)) || usage

context_endpoint=$(docker context inspect "$(docker context show)" --format '{{ .Endpoints.docker.Host }}')
if [[ $context_endpoint != unix://* || ${DOCKER_HOST:-unix://} != unix://* ]]; then
  echo "The load fixture requires a local Docker Unix socket." >&2
  exit 1
fi
compose=(docker compose --env-file /dev/null -p hello-delivery-load -f compose.integration.yml -f compose.delivery-load.yml)
lock="${TMPDIR:-/tmp}/hello-delivery-load.lock"
mkdir "$lock" || { echo "Fixture already locked: $lock" >&2; exit 1; }
bundle=""
owns_compose=false
cleanup() {
  status=$?
  trap - EXIT
  if [[ $owns_compose == true ]]; then "${compose[@]}" down --volumes --remove-orphans || status=1; fi
  if [[ -n "$bundle" ]]; then rm -rf -- "$bundle"; fi
  rmdir "$lock" || status=1
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
bundle="$(mktemp -d "${TMPDIR:-/tmp}/hello-load.XXXXXX")"
export HELLO_INTEGRATION_CERTS_DIR="$bundle/tls" HELLO_INTEGRATION_BIN_DIR="$bundle/bin"
mkdir "$bundle/bin"
GOFLAGS= go run ./scripts/integration-certs.go "$bundle/tls"

case "$(docker info --format '{{.Architecture}}')" in
  arm64|aarch64) arch=arm64 ;;
  amd64|x86_64) arch=amd64 ;;
  *) echo "Unsupported Docker architecture." >&2; exit 2 ;;
esac
echo "fixture arch=$arch toolchain=$(go version) docker=$(docker version --format '{{.Server.Version}}') vm_cpus=$(docker info --format '{{.NCPU}}') vm_ram_bytes=$(docker info --format '{{.MemTotal}}')"
build() {
  GOOS=linux GOARCH="$arch" CGO_ENABLED=0 GOFLAGS= go build -mod=readonly -trimpath -o "$bundle/bin/$1" "$2"
}
build worker ./cmd/worker
build provider ./scripts/integration-provider.go
build load ./scripts/delivery-load.go
"${compose[@]}" config --format json | python3 -c '
import json, sys
c = json.load(sys.stdin)
if not c["networks"]["delivery"]["internal"] or any(s.get("ports") or list(s["networks"]) != ["delivery"] for s in c["services"].values()):
    raise SystemExit("Every fixture service must use only the private network and zero published ports.")
'
[[ -z "$("${compose[@]}" ps -a -q)" ]] || { echo 'Existing load containers, refusing reuse.' >&2; exit 1; }
owns_compose=true
"${compose[@]}" up -d --wait --wait-timeout 120
[[ $(docker network inspect -f '{{.Internal}}' hello-delivery-load_delivery) == true ]]
for service in postgres kafka worker-fixture provider-fixture; do
  container=$("${compose[@]}" ps -q "$service")
  [[ $(docker inspect --format '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{end}}' "$container") == hello-delivery-load_delivery ]]
done
for migration in db/migrations/*.up.sql; do
  "${compose[@]}" exec -T postgres psql -Xq -v ON_ERROR_STOP=1 -1 -U integration -d notification_integration < "$migration"
done
source scripts/fixtures/load-application.sh
for topic in notification.send.requested notification.delivery.updated; do
  "${compose[@]}" exec -T kafka /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server kafka:9094 --create --topic "$topic" --partitions 1 --replication-factor 1
done
"${compose[@]}" exec -T worker-fixture /fixture-bin/load "$count" "$waves" "$mode" "$shape" "$pace" "$pool_size" "$active_ms"
