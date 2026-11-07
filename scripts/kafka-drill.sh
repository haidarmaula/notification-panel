#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
[[ $# == 0 ]] || { echo 'Usage: bash scripts/kafka-drill.sh' >&2; exit 2; }
case "$(docker info --format '{{.Architecture}}')" in
  aarch64|arm64) arch=arm64 ;;
  x86_64|amd64) arch=amd64 ;;
  *) exit 1 ;;
esac
export HELLO_KAFKA_DRILL_DIR
HELLO_KAFKA_DRILL_DIR=$(mktemp -d "${TMPDIR:-/tmp}/hello-kafka-drill.XXXXXX")
project="$(basename "$HELLO_KAFKA_DRILL_DIR" | tr '[:upper:].' '[:lower:]-')"
compose=(docker compose --env-file /dev/null -p "$project" -f compose.kafka-drill.yml)
owned=false
cleanup() {
  result=$?
  trap - EXIT
  if [[ "$owned" = true ]]; then
    "${compose[@]}" down -v --remove-orphans || result=1
  fi
  rm -rf -- "$HELLO_KAFKA_DRILL_DIR" || result=1
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
bash scripts/kafka-drill/certs.sh "$HELLO_KAFKA_DRILL_DIR"
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 GOFLAGS= GOWORK=off go test -c -mod=readonly -tags=integration -trimpath -o "$HELLO_KAFKA_DRILL_DIR/probe" ./scripts/kafka-drill
"${compose[@]}" config -q
[[ -z "$("${compose[@]}" ps -qa)" ]] || exit 1
owned=true
"${compose[@]}" up --pull never -d
[[ "$(docker network inspect --format '{{.Internal}}' "${project}_private")" == true ]]
probe() { "${compose[@]}" exec -T -e HELLO_KAFKA_DRILL_FIXTURE=1 -e "HELLO_KAFKA_DRILL_STAGE=$1" -e "HELLO_KAFKA_DRILL_ISR=${2:-}" probe /probe -test.run='^TestSyntheticKafkaDrill$' -test.v; }
eventually() {
  for _ in {1..40}; do
    if probe "$@" > "$HELLO_KAFKA_DRILL_DIR/attempt.log" 2>&1; then cat "$HELLO_KAFKA_DRILL_DIR/attempt.log"; return; fi
    sleep 1
  done
  cat "$HELLO_KAFKA_DRILL_DIR/attempt.log" >&2
  echo "Timed out waiting for $1" >&2
  return 1
}
admin() {
  local command="$1"
  shift
  "${compose[@]}" exec -T probe timeout 30s "/opt/kafka/bin/$command" \
    --bootstrap-server b1:9092,b2:9092,b3:9092 --command-config /fixture/admin.properties "$@"
}
topic_ids() {
  admin kafka-topics.sh --describe --topic 'notification[.].*' | python3 -c '
import re, sys
ids = sorted(re.findall(r"^Topic:\s+(\S+)\s+TopicId:\s+([A-Za-z0-9_-]{22})(?:\s|$)", sys.stdin.read(), re.M))
if [topic for topic, _ in ids] != ["notification.delivery.updated", "notification.send.requested"]:
    raise SystemExit("Could not verify both Kafka topic IDs")
for topic, identity in ids:
    print(topic, identity)
'
}
controller_leader() {
  admin kafka-metadata-quorum.sh describe --status > "$HELLO_KAFKA_DRILL_DIR/quorum"
  cat "$HELLO_KAFKA_DRILL_DIR/quorum" >&2
  awk '$1=="LeaderId:" && $2 ~ /^10[123]$/ {id=$2} END {if (!id) exit 1; print id}' "$HELLO_KAFKA_DRILL_DIR/quorum"
}
eventually ready
probe plan
probe apply
eventually check
probe apply
topic_ids > "$HELLO_KAFKA_DRILL_DIR/topic-ids"
cat "$HELLO_KAFKA_DRILL_DIR/topic-ids"
initial_controller=$(controller_leader)
admin kafka-features.sh describe
admin kafka-configs.sh --alter --entity-type topics --entity-name notification.send.requested --add-config retention.ms=604799999
if probe apply > "$HELLO_KAFKA_DRILL_DIR/drift.log" 2>&1; then echo 'Provisioning accepted drift' >&2; exit 1; fi
grep -q 'topic config drift' "$HELLO_KAFKA_DRILL_DIR/drift.log"
admin kafka-configs.sh --alter --entity-type topics --entity-name notification.send.requested --add-config retention.ms=604800000
eventually check
eventually offsets-ready
probe baseline
eventually layout 3
start_loss=$SECONDS
stopped_controller="c${initial_controller#10}"
"${compose[@]}" stop -t 5 b3 "$stopped_controller"
eventually layout 2
probe failover
elected=$(controller_leader)
[[ "$elected" != "$initial_controller" ]]
echo "Controller $initial_controller replaced by $elected; leader loss through verified canaries and quorum: $((SECONDS - start_loss))s"
"${compose[@]}" start "$stopped_controller"
"${compose[@]}" stop -t 5 b2
eventually layout 1
probe below-isr
start_recovery=$SECONDS
"${compose[@]}" start b2 b3
eventually layout 3
probe recovered
topic_ids > "$HELLO_KAFKA_DRILL_DIR/restored-topic-ids"
cmp "$HELLO_KAFKA_DRILL_DIR/topic-ids" "$HELLO_KAFKA_DRILL_DIR/restored-topic-ids"
echo "Restart through restored full ISR, canaries, checkpoints and topic IDs: $((SECONDS - start_recovery))s"
echo 'Verified both physical topic IDs, RF3/minISR2, authenticated role boundaries and resumed offsets after broker recovery.'
