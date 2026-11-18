#!/usr/bin/env bash
# Run --preflight as root on a disposable Linux Docker host with empty protected paths.
# --preflight-services runs the real daemons on isolated PostgreSQL and Kafka (also on Desktop).
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
mode=${1:-}
case "$mode" in --preflight|--preflight-services) ;; *) exit 2 ;; esac
fail() { printf '%s\n' "$*" >&2; exit 1; }
if [[ "$mode" == --preflight ]]; then
    [[ $(uname -s) == Linux && $EUID == 0 && -S /var/run/docker.sock ]] ||
        fail 'Full preflight requires root and a local Docker socket on a disposable Linux host'
    unset DOCKER_CONTEXT
    export DOCKER_HOST=unix:///var/run/docker.sock
    for path in /etc/hello /var/lib/hello; do
        [[ ! -e "$path" && ! -L "$path" ]] || fail "Refusing to touch existing protected path: $path"
    done
fi

go_bin=${GO:-go}
timeout_bin=$(command -v timeout || command -v gtimeout) || fail 'timeout or gtimeout is required'
command -v "$go_bin" >/dev/null || fail 'Go is required (set GO to its executable if not on PATH)'
command -v python3 >/dev/null || fail 'Python 3 is required'
d() { "$timeout_bin" -k 3s 35s docker "$@"; }
pg_image='postgres:17@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d'
kafka_image='apache/kafka:4.3.1@sha256:77e3df9054047a88b520d0cc46e16696d3b22022e1d580aeccd2632df6532837'
for image in "$pg_image" "$kafka_image"; do
    d image inspect "$image" >/dev/null || fail "Cache required image before running: $image"
done

umask 077
tmp=$(mktemp -d "${TMPDIR:-/tmp}/hello-preflight.XXXXXXXX")
label=hello.preflight.fixture
name=$(basename "$tmp" | tr '[:upper:].' '[:lower:]-')
pg=$name-db
broker=$name-kafka
worker=$name-worker
scheduler=$name-scheduler
admission=$name-admission
server=$name-server
worker_tag="hello-preflight-fixture:$name"
shared_tag="hello-preflight-fixture:$name-shared"
image_owned=false
shared_image_owned=false
app=''
etc_owned=false
var_owned=false
network_owned=false
executor_network_owned=false
cleanup() {
    local rc=$? id ids
    trap - EXIT
    if (( rc != 0 )); then
        for id in "$pg" "$broker" "$worker" "$scheduler" "$admission" "$server"; do
            if [[ $(d inspect --format "{{index .Config.Labels \"$label\"}}" "$id" 2>/dev/null) == "$name" ]]; then
                d logs --tail 20 "$id" >&2 || true
            fi
        done
    fi
    if [[ -n "$app" ]]; then
        ids=$(d ps -aq --filter "label=hello.onboarding.application=$app") || rc=1
        for id in $ids; do
            if (( rc != 0 )); then d logs --tail 5 "$id" >&2 || true; fi
            d rm -fv "$id" >/dev/null || rc=1
        done
    fi
    for id in "$worker" "$scheduler" "$admission" "$server" "$pg" "$broker"; do
        if [[ $(d inspect --format "{{index .Config.Labels \"$label\"}}" "$id" 2>/dev/null) == "$name" ]]; then
            d rm -fv "$id" >/dev/null || rc=1
        fi
    done
    if $executor_network_owned; then d network rm "$name-executor" >/dev/null || rc=1; fi
    if $network_owned; then d network rm "$name" >/dev/null || rc=1; fi
    if $image_owned; then d image rm "$worker_tag" >/dev/null || rc=1; fi
    if $shared_image_owned; then d image rm "$shared_tag" >/dev/null || rc=1; fi
    if $etc_owned; then rm -rf -- /etc/hello || rc=1; fi
    if $var_owned; then rm -rf -- /var/lib/hello || rc=1; fi
    rm -rf -- "$tmp" || rc=1
    exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

inputs() { python3 scripts/application-onboarding-preflight-fixture.py "$1" "$tmp" "${@:2}"; }
copy_archive() {
    local container=$1
    shift
    inputs archive "$@" | d cp -a - "$container:/"
}
"$timeout_bin" -k 5s 120s "$go_bin" run scripts/integration-certs.go "$tmp/certs"
arch=$(d version --format '{{.Server.Arch}}')
case "$arch" in arm64|amd64) ;; *) fail 'Unsupported Docker architecture' ;; esac
mkdir "$tmp/image"
"$timeout_bin" -k 5s 180s env CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$go_bin" build -trimpath -o "$tmp/image/worker" ./cmd/worker
worker_image=$worker_tag
if [[ "$mode" == --preflight ]]; then
    printf '%s\n' 'FROM scratch' 'COPY --chmod=0555 worker /worker' 'ENTRYPOINT ["/worker"]' |
        "$timeout_bin" -k 5s 90s docker build -q --platform "linux/$arch" -t "$worker_tag" -f - "$tmp/image" >/dev/null
    image_owned=true
    worker_image=$(d image inspect --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}' "$worker_tag")
    [[ "$worker_image" == hello-preflight-fixture@sha256:* ]] || fail 'Docker image store did not supply a pinned local worker digest'
fi
inputs render "$worker_image" "$name" "$mode"
{ read -r app; read -r config; read -r reservation; read -r source_id; } < "$tmp/ids"
file_digest() {
    local sum
    sum=$(shasum -a 256 "$1") || return
    printf '%s\n' "${sum%% *}"
}
policy_digest=$(file_digest "$tmp/policy.json")
worker_digest=$(file_digest "$tmp/worker.json")
report=$(printf '{"application_id":"%s","policy_sha256":"%s"}' "$app" "$worker_digest")

if [[ "$mode" == --preflight ]]; then
    "$timeout_bin" -k 5s 180s env CGO_ENABLED=0 "$go_bin" build -o "$tmp/controlplane" ./cmd/controlplane
    mkdir -m 0755 /etc/hello && etc_owned=true
    mkdir -m 0755 /var/lib/hello && var_owned=true
    [[ $etc_owned == true && $var_owned == true ]] || fail 'Could not exclusively create protected paths'
    install -d -m 0750 -o 0 -g 65532 /var/lib/hello/runtime
    credential_dir="/var/lib/hello/credentials/$app/staging/$config"
    install -d -m 0755 "$credential_dir"
    install -m 0640 -o 0 -g 65532 "$tmp/certs/service-account.json" "$credential_dir/service-account.json"
    install -m 0644 "$tmp/certs/ca.crt" /etc/hello/runtime-preparation-postgres-ca.crt
    install -m 0600 "$tmp/policy.json" /etc/hello/runtime-preparation-policy.json
    install -m 0600 "$tmp/worker.json" /etc/hello/runtime-preparation-worker.json
fi

d network create --internal --label "$label=$name" "$name" >/dev/null
network_owned=true
[[ $(d network inspect --format '{{.Internal}}' "$name") == true ]]
pg_network=(--network "$name" --network-alias postgres)
if [[ "$mode" == --preflight ]]; then
    d network create --label "$label=$name" "$name-executor" >/dev/null
    executor_network_owned=true
    pg_network=(--network "$name-executor" -p '127.0.0.1::5432')
fi
d create --pull=never --name "$pg" --label "$label=$name" "${pg_network[@]}" \
    --tmpfs /var/lib/postgresql/data -e POSTGRES_PASSWORD=owner_only \
    -e POSTGRES_INITDB_ARGS=--auth-host=scram-sha-256 "$pg_image" \
    postgres -c ssl=on -c ssl_cert_file=/fixture/server.crt -c ssl_key_file=/fixture/server.key >/dev/null
if $executor_network_owned; then d network connect --alias postgres "$name" "$pg"; fi
copy_archive "$pg" -:fixture:0755:0.0 certs/server.crt:fixture/server.crt:0644:0.0 \
    certs/server.key:fixture/server.key:0600:999.999
d start "$pg" >/dev/null
sql() { d exec -i -u postgres "$pg" psql -U postgres -d postgres -XqAt -v ON_ERROR_STOP=1 "$@"; }
ready=false
for _ in {1..30}; do
    if d exec "$pg" sh -c 'test "$(ps -p 1 -o comm=)" = postgres' && sql -c 'SELECT 1' >/dev/null 2>&1; then
        ready=true; break
    fi
    sleep .5
done
$ready || fail 'PostgreSQL did not start'
for migration in db/migrations/*.up.sql; do sql < "$migration"; done
sql <<'SQL'
CREATE ROLE hello_scheduler NOLOGIN;
CREATE ROLE hello_admission NOLOGIN;
CREATE ROLE hello_delivery NOLOGIN;
CREATE ROLE fixture_worker LOGIN IN ROLE hello_delivery PASSWORD 'worker_only';
CREATE ROLE hello_onboarding_runtime_executor NOLOGIN;
CREATE ROLE fixture_executor LOGIN IN ROLE hello_onboarding_runtime_executor PASSWORD 'executor_only';
SQL
d cp deploy/postgres "$pg:/fixture/grants"
sql -f /fixture/grants/daemon-grants.sql
sql -f /fixture/grants/onboarding-runtime-grants.sql
sql -v app="$app" -v config="$config" -v source="$source_id" -v capacity="$policy_digest" -v worker="$worker_digest" <<'SQL'
INSERT INTO applications(public_id,key,name) VALUES (:'app','preflight-fixture','Preflight Fixture');
INSERT INTO application_dispatch_policy(application_id,push_ttl_seconds,key_ttl_seconds)
SELECT id,300,300 FROM applications WHERE public_id=:'app';
INSERT INTO application_provider_configurations(id,application_id,version,environment,target_project_id,service_account_email,file_reference,state,created_by)
SELECT :'config',id,1,'staging','integration-fixture','fixture@integration-fixture.iam.gserviceaccount.com',:'config','VERIFIED',1
FROM applications WHERE public_id=:'app';
UPDATE application_onboarding SET environment='staging',candidate_configuration_id=config.id
FROM application_provider_configurations config WHERE config.application_id=application_onboarding.application_id;
INSERT INTO application_runtime_capacity_profile(environment,revision,max_applications,max_overlap,max_database_connections,delivery_database_connections,policy_sha256)
VALUES('staging',1,1,1,4,2,decode(:'capacity','hex'));
INSERT INTO application_runtime_executor_scopes(role_id,application_id,environment,profile_revision)
SELECT 'fixture_executor'::regrole::oid,id,'staging',1 FROM applications WHERE public_id=:'app';
INSERT INTO application_worker_preflight_scopes(application_id,configuration_id,environment,generation,profile_revision,delivery_policy_sha256,worker_role)
SELECT id,:'config','staging',1,1,decode(:'worker','hex'),'fixture_worker'::regrole::oid FROM applications WHERE public_id=:'app';
INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active)
VALUES(:'source','notification.delivery.updated','MkU3OEVBNTcwNTJENDM2Qk','preflight-fixture',true);
UPDATE notification_runtime SET sealed=false,migration_hold=false WHERE singleton;
SQL

# The plaintext listener is local to this isolated broker and used only by fixture assertions.
d create --pull=never --name "$broker" --label "$label=$name" --network "$name" --network-alias kafka \
    -e CLUSTER_ID=MkU3OEVBNTcwNTJENDM2Qk -e KAFKA_NODE_ID=1 -e KAFKA_PROCESS_ROLES=broker,controller \
    -e KAFKA_LISTENERS=INTERNAL://:9094,CONTROLLER://:9093,DELIVERY://:9096 \
    -e KAFKA_ADVERTISED_LISTENERS=INTERNAL://kafka:9094,DELIVERY://kafka:9096 \
    -e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=INTERNAL:PLAINTEXT,CONTROLLER:PLAINTEXT,DELIVERY:SSL \
    -e KAFKA_INTER_BROKER_LISTENER_NAME=INTERNAL -e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER \
    -e KAFKA_CONTROLLER_QUORUM_VOTERS=1@kafka:9093 -e KAFKA_AUTO_CREATE_TOPICS_ENABLE=false \
    -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 -e KAFKA_SSL_KEYSTORE_TYPE=PEM \
    -e KAFKA_SSL_KEYSTORE_LOCATION=/fixture/server.pem -e KAFKA_SSL_TRUSTSTORE_TYPE=PEM \
    -e KAFKA_SSL_TRUSTSTORE_LOCATION=/fixture/ca.crt -e KAFKA_SSL_CLIENT_AUTH=required "$kafka_image" >/dev/null
copy_archive "$broker" -:fixture:0755:0.0 certs/server.pem:fixture/server.pem:0644:0.0 certs/ca.crt:fixture/ca.crt:0644:0.0
d start "$broker" >/dev/null
kafka() { d exec "$broker" "/opt/kafka/bin/$1.sh" --bootstrap-server kafka:9094 "${@:2}"; }
for topic in notification.delivery.updated notification.send.requested; do
    kafka kafka-topics --create --topic "$topic" --partitions 1 --replication-factor 1 >/dev/null
done
offsets() { kafka kafka-get-offsets --topic 'notification\.(delivery\.updated|send\.requested)' --time latest; }
ledger() {
    sql <<'SQL'
SELECT (SELECT count(*) FROM notifications),
       (SELECT count(*) FROM notification_preparations),
       (SELECT count(*) FROM notification_delivery_intents),
       (SELECT count(*) FROM notification_deliveries),
       (SELECT count(*) FROM notification_receipt_metadata),
       (SELECT count(*) FROM notification_observations),
       (SELECT count(*) FROM notification_send_outbox),
       (SELECT count(*) FROM notification_update_outbox),
       (SELECT count(*) FROM application_runtime_acknowledgements);
SQL
}
lifecycle() {
    sql "-c SELECT to_jsonb(a),to_jsonb(s),to_jsonb(d) FROM applications a JOIN application_onboarding s ON s.application_id=a.id JOIN application_dispatch_policy d ON d.application_id=a.id WHERE a.public_id='$app'"
}
assert_no_delivery() {
    [[ $(ledger) == '0|0|0|0|0|0|0|0|0' ]] || fail 'Delivery or runtime acknowledgement evidence exists'
    [[ $(offsets | sort) == $'notification.delivery.updated:0:0\nnotification.send.requested:0:0' ]] || fail 'Unexpected Kafka end offsets'
    [[ $(lifecycle) == "$initial_lifecycle" ]] || fail 'Lifecycle or dispatch state changed'
}
initial_lifecycle=$(lifecycle)
[[ -n "$initial_lifecycle" ]] || fail 'Fixture lifecycle missing'
assert_no_delivery

if [[ "$mode" == --preflight-services ]]; then
    read -r send_source_id < "$tmp/send-source-id"
    scheduler_digest=$(file_digest "$tmp/scheduler.json")
    admission_digest=$(file_digest "$tmp/admission.json")
    "$timeout_bin" -k 5s 180s env CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$go_bin" build -trimpath -o "$tmp/image/scheduler" ./cmd/scheduler
    "$timeout_bin" -k 5s 180s env CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$go_bin" build -trimpath -o "$tmp/image/server" ./cmd/server
    server_digest=$(python3 - "$tmp" <<'PY'
import hashlib
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
parts = (hashlib.sha256((root / name).read_bytes()).digest() for name in ("server.json", "mobile.json"))
print(hashlib.sha256(b"".join(parts)).hexdigest())
PY
    )
    inputs archive -:etc:0755:0.0 -:etc/hello:0755:0.0 \
        server.json:etc/hello/server.json:0640:0.65532 mobile.json:etc/hello/mobile.json:0640:0.65532 \
        scheduler.json:etc/hello/scheduler.json:0640:0.65532 admission.json:etc/hello/worker.json:0640:0.65532 \
        certs/ca.crt:etc/hello/postgres-ca.crt:0644:0.0 > "$tmp/image/shared.tar"
    printf '%s\n' 'FROM scratch' 'COPY --chmod=0555 worker scheduler server /' 'ADD shared.tar /' |
        "$timeout_bin" -k 5s 90s docker build -q --platform "linux/$arch" -t "$shared_tag" -f - "$tmp/image" >/dev/null
    shared_image_owned=true
    inputs archive -:etc:0755:0.0 -:etc/hello:0755:0.0 -:etc/hello/fcm:0755:0.0 \
        worker.json:etc/hello/worker.json:0640:0.65532 certs/ca.crt:etc/hello/postgres-ca.crt:0644:0.0 \
        certs/service-account.json:etc/hello/fcm/service-account.json:0640:0.65532 > "$tmp/image/policy.tar"
    printf '%s\n' 'FROM scratch' 'COPY --chmod=0555 worker /worker' \
        'ADD policy.tar /' 'ENTRYPOINT ["/worker"]' |
        "$timeout_bin" -k 5s 90s docker build -q --platform "linux/$arch" -t "$worker_tag" -f - "$tmp/image" >/dev/null
    image_owned=true
    create_daemon() {
        local container=$1
        shift
        d create --pull=never --name "$container" --label "$label=$name" --network "$name" \
            --user 65532:65532 --read-only --cap-drop ALL --security-opt no-new-privileges \
            --memory 128m --cpus .5 --pids-limit 64 "$@" >/dev/null
    }
    create_daemon "$worker" "$worker_image" --check-delivery-policy
    [[ $(d start -a "$worker") == "$report" ]] || fail 'Unexpected worker preflight report'
    [[ $(d inspect --format '{{.State.Status}} {{.State.ExitCode}}' "$worker") == 'exited 0' ]]
    assert_no_delivery
    sql -v app="$app" -v config="$config" -v reservation="$reservation" -v worker="$worker_digest" \
        -v scheduler="$scheduler_digest" -v admission="$admission_digest" -v server="$server_digest" -v source="$send_source_id" <<'SQL'
CREATE ROLE fixture_server LOGIN PASSWORD 'server_only';
CREATE ROLE fixture_scheduler LOGIN IN ROLE hello_scheduler PASSWORD 'scheduler_only';
CREATE ROLE fixture_admission LOGIN IN ROLE hello_admission PASSWORD 'admission_only';
GRANT EXECUTE ON FUNCTION public.acknowledge_shared_application_runtime(text,bytea,uuid)
TO fixture_scheduler, fixture_admission, fixture_server;
INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active)
VALUES(:'source','notification.send.requested','MkU3OEVBNTcwNTJENDM2Qk','preflight-fixture',true);
INSERT INTO application_runtime_reservations(id,application_id,configuration_id,environment,generation,profile_revision,
    setup_revision,origin_epoch,source_epoch,executor_role,database_connections)
SELECT :'reservation',a.id,:'config','staging',1,1,s.revision,n.origin_epoch,n.source_epoch,'fixture_executor'::regrole::oid,2
FROM applications a JOIN application_onboarding s ON s.application_id=a.id
CROSS JOIN notification_runtime n WHERE a.public_id=:'app' AND n.singleton;
INSERT INTO application_runtime_component_scopes(application_id,generation,component,login_role,policy_sha256)
SELECT id,1,component,login_role::regrole::oid,decode(digest,'hex')
FROM applications CROSS JOIN (VALUES
    ('DELIVERY','fixture_worker',:'worker'),
    ('SCHEDULER','fixture_scheduler',:'scheduler'),
    ('ADMISSION','fixture_admission',:'admission'),
    ('SERVER','fixture_server',:'server')
) AS approvals(component,login_role,digest) WHERE public_id=:'app';
GRANT EXECUTE ON FUNCTION acknowledge_application_runtime(uuid,bigint,text,bytea,uuid) TO fixture_worker;
SQL
    # Admission rejects a missing commit; seed the empty topic's numeric offset without a record.
    kafka kafka-consumer-groups --group "$name" --topic notification.send.requested \
        --reset-offsets --to-offset 0 --execute >/dev/null
    d rm "$worker" >/dev/null
    create_daemon "$worker" "$worker_image"
    create_daemon "$scheduler" --entrypoint /scheduler "$shared_tag"
    create_daemon "$admission" --entrypoint /worker "$shared_tag"
    create_daemon "$server" --entrypoint /server "$shared_tag" --deployment
    d start "$worker" "$scheduler" "$admission" "$server" >/dev/null
    assert_running() {
        [[ $(d inspect --format '{{.State.Running}}' "$1") == true ]] || fail "$1 exited unexpectedly"
    }
    live_lease() {
        sql -c "SELECT executor_id,lease_expires_at FROM application_runtime_acknowledgements
            WHERE application_id=(SELECT id FROM applications WHERE public_id='$app')
            AND configuration_id='$config' AND component='$1' AND generation=1
            AND policy_sha256=decode('$2','hex') AND lease_expires_at>clock_timestamp()"
    }
    assert_renewal() {
        local container=$1 component=$2 digest=$3 first='' current until=$((SECONDS + 30))
        while (( SECONDS < until )); do
            assert_running "$container"
            current=$(live_lease "$component" "$digest")
            if [[ -n "$current" ]]; then
                if [[ -z "$first" ]]; then first=$current
                elif [[ "${current%%|*}" == "${first%%|*}" && "${current#*|}" > "${first#*|}" ]]; then return
                elif [[ "${current%%|*}" != "${first%%|*}" ]]; then fail "$component lease changed executor"; fi
            fi
            sleep .25
        done
        fail "$component daemon did not renew its lease"
    }
    assert_renewal "$worker" DELIVERY "$worker_digest"
    assert_renewal "$scheduler" SCHEDULER "$scheduler_digest"
    assert_renewal "$admission" ADMISSION "$admission_digest"
    assert_renewal "$server" SERVER "$server_digest"
    prepared="SELECT onboarding_runtime_prepared(id,1,'$config') FROM applications WHERE public_id='$app'"
    [[ $(sql -c "$prepared") == t ]] || fail 'Four live processes did not satisfy the prepared runtime gate'
    sql -c 'UPDATE notification_runtime SET sealed=true WHERE singleton'
    [[ $(sql -c "$prepared") == f ]] || fail 'Sealed runtime remained prepared'
    [[ $(d wait "$worker") == 1 ]] || fail 'Delivery daemon did not fail closed after the runtime was sealed'
    # Shared daemons stay running, but a sealed runtime must stop extending their leases.
    deadline=$((SECONDS + 20))
    while true; do
        scheduler_lease=$(live_lease SCHEDULER "$scheduler_digest")
        admission_lease=$(live_lease ADMISSION "$admission_digest")
        server_lease=$(live_lease SERVER "$server_digest")
        [[ -n "$scheduler_lease" || -n "$admission_lease" || -n "$server_lease" ]] || break
        (( SECONDS < deadline )) || fail 'Shared runtime lease remained live after sealing'
        sleep .25
    done
    assert_running "$scheduler"
    assert_running "$admission"
    assert_running "$server"
    [[ $(ledger) == '0|0|0|0|0|0|0|0|4' ]] || fail 'Unexpected durable delivery or acknowledgement evidence'
    [[ $(offsets | sort) == $'notification.delivery.updated:0:0\nnotification.send.requested:0:0' ]] || fail 'Unexpected Kafka end offsets'
    [[ $(lifecycle) == "$initial_lifecycle" ]] || fail 'Lifecycle or dispatch state changed'
    printf '%s\n' 'Services fixture passed: four live components satisfy runtime readiness; sealing revokes readiness without activation or delivery'
    exit
fi

port=$(d port "$pg" 5432/tcp)
[[ "$port" =~ ^127\.0\.0\.1:[0-9]+$ ]] || fail 'Expected an ephemeral localhost PostgreSQL port'
IFS='|' read -r origin_epoch source_epoch < <(sql -c 'SELECT origin_epoch,source_epoch FROM notification_runtime WHERE singleton')
python3 - "$port" "$app" "$config" "$reservation" "$origin_epoch" "$source_epoch" > /etc/hello/runtime-preparation.json <<'PY'
import json
import sys
host, app, config, reservation, origin, source = sys.argv[1:]
print(json.dumps(dict(
    database_url=f"postgres://fixture_executor:executor_only@{host}/postgres"
                 "?sslmode=verify-full&sslrootcert=/etc/hello/runtime-preparation-postgres-ca.crt",
    application_id=app, configuration_id=config, reservation_id=reservation,
    environment="staging", generation=1, setup_revision=1, origin_epoch=origin, source_epoch=source,
)))
PY
cli() { "$timeout_bin" -k 3s 35s "$tmp/controlplane" "${1:-preflight-delivery}"; }
allocations() {
    d ps -aq --no-trunc --filter "label=hello.onboarding.application=$app" \
        --filter 'label=hello.onboarding.state=delivery-preflight'
}
reservation_row() {
    sql "-c SELECT to_jsonb(r) FROM application_runtime_reservations r WHERE r.id='$reservation' AND r.executor_role='fixture_executor'::regrole::oid AND r.released_at IS NULL AND r.database_connections=2 AND r.generation=1"
}
worker_state() {
    d inspect --format '{{.Id}} {{.State.Status}} {{.State.ExitCode}} {{.State.StartedAt}} {{.State.FinishedAt}}' "$id"
}
success='Delivery worker policy preflight passed'
[[ -z $(allocations) && -z $(reservation_row) ]] || fail 'Unexpected initial runtime allocation'
[[ $(cli prepare-runtime) == 'Stopped runtime prepared; enrollment and dispatch remain unchanged' ]] ||
    fail 'Stopped preparation failed'
initial_id=$(d ps -aq --no-trunc --filter "label=hello.onboarding.application=$app")
[[ "$initial_id" =~ ^[0-9a-f]{64}$ ]] || fail 'Expected one stopped runtime allocation'
[[ $(d inspect --format '{{.State.Status}} {{.HostConfig.NetworkMode}}' "$initial_id") == 'created none' ]] ||
    fail 'Preparation did not leave an inert allocation'
initial_reservation=$(reservation_row)
[[ -n "$initial_reservation" ]] || fail 'Stopped runtime has no reservation'
[[ $(cli) == "$success" ]] || fail 'First CLI preflight failed'
[[ $(reservation_row) == "$initial_reservation" ]] || fail 'Preflight changed the stopped reservation'
id=$(allocations)
[[ "$id" =~ ^[0-9a-f]{64}$ ]] || fail 'Expected one real preflight allocation'
state=$(worker_state)
[[ "$state" == "$id exited 0 "* && $(d logs "$id") == "$report" ]] || fail 'Unexpected worker outcome'
saved_reservation=$(reservation_row)
[[ -n "$saved_reservation" && $(sql -c 'SELECT count(*) FROM application_runtime_reservations') == 1 ]] || fail 'Expected one restricted reservation'
publication="/var/lib/hello/runtime/$app/staging/$config/1/worker.json"
cmp -s "$tmp/worker.json" "$publication" || fail 'Published policy differs from approved bytes'
assert_no_delivery

# Reuse the completed report without recreating/restarting the container or extending the reservation.
[[ $(cli) == "$success" ]] || fail 'Exact CLI replay failed'
[[ $(allocations) == "$id" && $(worker_state) == "$state" && $(d logs "$id") == "$report" ]] || fail 'Replay changed worker evidence'
[[ $(reservation_row) == "$saved_reservation" ]] || fail 'Replay changed the reservation'
cmp -s "$tmp/worker.json" "$publication" || fail 'Replay changed published policy'
assert_no_delivery

printf '\n' >> /etc/hello/runtime-preparation-worker.json
if rejected=$(cli 2>&1); then fail 'M46 accepted changed policy bytes'; fi
[[ "$rejected" == 'delivery preflight authority unavailable' ]] || fail 'Changed policy was not rejected at the M46 guard'
[[ $(allocations) == "$id" && $(worker_state) == "$state" && $(reservation_row) == "$saved_reservation" ]] || fail 'Rejected policy changed runtime evidence'
cmp -s "$tmp/worker.json" "$publication" || fail 'Rejected policy changed published bytes'
assert_no_delivery
printf '%s\n' 'Preflight fixture passed: real CLI, M45/M46, Docker worker, exact replay, digest rejection, no delivery evidence'
