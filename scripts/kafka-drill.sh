#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
monitor=false
if [[ $# == 1 && $1 == --monitor ]]; then
  monitor=true
elif [[ $# != 0 ]]; then
  echo 'Usage: bash scripts/kafka-drill.sh [--monitor]' >&2
  exit 2
fi
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
if [[ "$monitor" == true ]]; then
  agent="$HELLO_KAFKA_DRILL_DIR/node/jmx-agent.jar"
  if [[ -n "${HELLO_KAFKA_JMX_JAR:-}" ]]; then
    cp -- "$HELLO_KAFKA_JMX_JAR" "$agent"
  else
    curl --fail --silent --show-error --location --max-time 30 --retry 3 --retry-max-time 60 --output "$agent" \
      https://github.com/prometheus/jmx_exporter/releases/download/1.6.0/jmx_prometheus_javaagent-1.6.0.jar
  fi
  python3 - "$agent" <<'PY'
import hashlib
import pathlib
import sys

expected = "a95983fd96e865d2bcdf911cc500e7c82808c27ab9fd226bf96732b6c3d8c46e"
if hashlib.sha256(pathlib.Path(sys.argv[1]).read_bytes()).hexdigest() != expected:
    raise SystemExit("Unexpected JMX Exporter release digest")
PY
  chmod 644 "$agent"
  cp deploy/observability/kafka-jmx.yml "$HELLO_KAFKA_DRILL_DIR/node/kafka-jmx.yml"
  compose+=(-f compose.kafka-monitor.yml)
fi
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 GOFLAGS= GOWORK=off go test -c -mod=readonly -tags=integration -trimpath -o "$HELLO_KAFKA_DRILL_DIR/probe" ./scripts/kafka-drill
"${compose[@]}" config --format json | python3 -c '
import json
import sys

config = json.load(sys.stdin)
if not config["networks"]["private"]["internal"] or any(
    service.get("ports") or set(service["networks"]) != {"private"}
    for service in config["services"].values()
):
    raise SystemExit("Kafka drill services must use only the internal network and publish no ports")
'
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
monitor_wait() {
  [[ "$monitor" == true ]] || return 0
  local label=$1 query=$2 encoded
  encoded=$(python3 -c 'import sys,urllib.parse; print(urllib.parse.quote(sys.argv[1],safe=""))' "$query")
  for _ in {1..60}; do
    if "${compose[@]}" exec -T monitor wget -T 3 -qO- "http://127.0.0.1:9090/api/v1/query?query=$encoded" > "$HELLO_KAFKA_DRILL_DIR/metrics.json" &&
      python3 -c 'import json,sys; data=json.load(sys.stdin); sys.exit(not (data["status"]=="success" and data["data"]["result"]))' < "$HELLO_KAFKA_DRILL_DIR/metrics.json"; then
      echo "Monitoring verified: $label"
      return
    fi
    sleep 1
  done
  echo "Timed out waiting for monitoring: $label" >&2
  cat "$HELLO_KAFKA_DRILL_DIR/metrics.json" >&2
  "${compose[@]}" logs --tail 30 monitor b1 c1 >&2
  return 1
}
monitor_healthy() {
  monitor_wait 'all six Kafka exporters reachable' 'count(up{job="hello-kafka"} == 1) == 6'
  monitor_wait 'all brokers report ISR metrics' 'count(hello_kafka_under_min_isr_partitions == 0 and hello_kafka_under_replicated_partitions == 0) == 3'
  monitor_wait 'three controllers report one active controller' 'count(hello_kafka_active_controller) == 3 and sum(hello_kafka_active_controller) == 1'
  monitor_wait 'Kafka alerts clear' 'absent(ALERTS{alertname=~"HelloKafka.*",alertstate="firing"})'
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
monitor_healthy
start_loss=$SECONDS
stopped_controller="c${initial_controller#10}"
"${compose[@]}" stop -t 5 b3 "$stopped_controller"
eventually layout 2
probe failover
elected=$(controller_leader)
[[ "$elected" != "$initial_controller" ]]
echo "Controller $initial_controller replaced by $elected; leader loss through verified canaries and quorum: $((SECONDS - start_loss))s"
monitor_wait 'replication alert fires with two ISR' 'ALERTS{alertname="HelloKafkaUnderReplicated",alertstate="firing"}'
monitor_wait 'two ISR still satisfies minimum' 'max(hello_kafka_under_min_isr_partitions) == 0'
"${compose[@]}" start "$stopped_controller"
"${compose[@]}" stop -t 5 b2
eventually layout 1
probe below-isr
monitor_wait 'minimum ISR alert fires with one ISR' 'ALERTS{alertname="HelloKafkaBelowMinISR",alertstate="firing"}'
if [[ "$monitor" == true ]]; then
  "${compose[@]}" stop -t 5 b1
  monitor_wait 'offline partitions alert fires with all brokers stopped' 'ALERTS{alertname="HelloKafkaOfflinePartitions",alertstate="firing"}'
  monitor_wait 'broker scrape outage is visible' 'ALERTS{alertname="HelloKafkaMetricsUnavailable",alertstate="firing",role="broker"}'
fi
start_recovery=$SECONDS
if [[ "$monitor" == true ]]; then
  "${compose[@]}" start b1
fi
"${compose[@]}" start b2 b3
eventually layout 3
probe recovered
topic_ids > "$HELLO_KAFKA_DRILL_DIR/restored-topic-ids"
cmp "$HELLO_KAFKA_DRILL_DIR/topic-ids" "$HELLO_KAFKA_DRILL_DIR/restored-topic-ids"
echo "Restart through restored full ISR, canaries, checkpoints and topic IDs: $((SECONDS - start_recovery))s"
monitor_healthy
echo 'Verified both physical topic IDs, RF3/minISR2, authenticated role boundaries and resumed offsets after broker recovery.'
