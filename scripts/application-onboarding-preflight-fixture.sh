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
provider=$name-provider
verifier=$name-oauth-verifier
bootstrap=$name-bootstrap
activation_container=$name-activation
activation_tag="hello-preflight-fixture:$name-activation"
activation_image_owned=false
provisioner=$name-provisioner
bootstrap_tag="hello-preflight-fixture:$name-bootstrap"
bootstrap_image_owned=false
release_container=$name-release
cohort_approver=$name-cohort-approver
cohort_coordinator=$name-cohort-coordinator
cohort_verifier=$name-cohort-verifier
worker_tag="hello-preflight-fixture:$name"
shared_tag="hello-preflight-fixture:$name-shared"
release_tag="hello-preflight-fixture:$name-release"
cohort_tag="hello-preflight-fixture:$name-cohort"
cohort_images=''
release_image_owned=false
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
        for id in "$pg" "$broker" "$worker" "$scheduler" "$admission" "$server" "$verifier" "$bootstrap" "$provisioner"; do
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
    for id in "$worker" "$scheduler" "$admission" "$server" "$provider" "$verifier" "$provisioner" "$bootstrap" "$activation_container" "$release_container" "$cohort_approver" "$cohort_coordinator" "$cohort_verifier" "$pg" "$broker"; do
        if [[ $(d inspect --format "{{index .Config.Labels \"$label\"}}" "$id" 2>/dev/null) == "$name" ]]; then
            d rm -fv "$id" >/dev/null || rc=1
        fi
    done
    if $executor_network_owned; then d network rm "$name-executor" >/dev/null || rc=1; fi
    if $network_owned; then d network rm "$name" >/dev/null || rc=1; fi
    if $image_owned; then d image rm "$worker_tag" >/dev/null || rc=1; fi
    if $release_image_owned; then d image rm "$release_tag" >/dev/null || rc=1; fi
    for id in $cohort_images; do d image rm "$id" >/dev/null || rc=1; done
    if $activation_image_owned; then d image rm "$activation_tag" >/dev/null || rc=1; fi
    if $bootstrap_image_owned; then d image rm "$bootstrap_tag" >/dev/null || rc=1; fi
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
sql -v source="$source_id" -v capacity="$policy_digest" <<'SQL'
INSERT INTO application_runtime_capacity_profile(environment,revision,max_applications,max_overlap,max_database_connections,delivery_database_connections,policy_sha256)
VALUES('staging',1,1,1,4,2,decode(:'capacity','hex'));
INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active)
VALUES(:'source','notification.delivery.updated','MkU3OEVBNTcwNTJENDM2Qk','preflight-fixture',true);
UPDATE notification_runtime SET sealed=false,migration_hold=false WHERE singleton;
SQL
if [[ "$mode" == --preflight ]]; then
sql -v app="$app" -v config="$config" -v worker="$worker_digest" <<'SQL'
INSERT INTO applications(public_id,key,name) VALUES (:'app','preflight-fixture','Preflight Fixture');
INSERT INTO application_dispatch_policy(application_id,push_ttl_seconds,key_ttl_seconds)
SELECT id,300,300 FROM applications WHERE public_id=:'app';
INSERT INTO application_provider_configurations(id,application_id,version,environment,target_project_id,service_account_email,file_reference,state,created_by)
SELECT :'config',id,1,'staging','integration-fixture','fixture@integration-fixture.iam.gserviceaccount.com',:'config','VERIFIED',1
FROM applications WHERE public_id=:'app';
UPDATE application_onboarding SET environment='staging',candidate_configuration_id=config.id
FROM application_provider_configurations config WHERE config.application_id=application_onboarding.application_id;
INSERT INTO application_runtime_executor_scopes(role_id,application_id,environment,profile_revision)
SELECT 'fixture_executor'::regrole::oid,id,'staging',1 FROM applications WHERE public_id=:'app';
INSERT INTO application_worker_preflight_scopes(application_id,configuration_id,environment,generation,profile_revision,delivery_policy_sha256,worker_role)
SELECT id,:'config','staging',1,1,decode(:'worker','hex'),'fixture_worker'::regrole::oid FROM applications WHERE public_id=:'app';
SQL
fi

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
if [[ "$mode" == --preflight ]]; then
    initial_lifecycle=$(lifecycle)
    [[ -n "$initial_lifecycle" ]] || fail 'Fixture lifecycle missing'
    assert_no_delivery
fi

if [[ "$mode" == --preflight-services ]]; then
    for command in server provisioner; do
        "$timeout_bin" -k 5s 180s env CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$go_bin" build -trimpath -o "$tmp/image/$command" "./cmd/$command"
    done
    "$timeout_bin" -k 5s 180s env CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$go_bin" build -trimpath -o "$tmp/image/mobile-client" scripts/fixture-onboarding-mobile.go
    inputs archive -:etc:0755:0.0 -:etc/hello:0755:0.0 \
        bootstrap-server.json:etc/hello/server.json:0640:0.65532 \
        provisioner.json:etc/hello/provisioner.json:0600:0.0 \
        -:var:0755:0.0 -:var/lib:0755:0.0 -:var/lib/hello:0755:0.0 -:var/lib/hello/credentials:0750:0.65532 \
        -:fixtures:0755:0.0 certs/service-account.json:fixtures/service-account.json:0640:0.65532 \
        certs/ca.crt:etc/hello/postgres-ca.crt:0644:0.0 certs/ca.crt:etc/hello/fixture-ca.crt:0640:0.65532 \
        certs/client.crt:etc/hello/fixture-client.crt:0640:0.65532 certs/client.key:etc/hello/fixture-client.key:0640:0.65532 \
        > "$tmp/image/bootstrap.tar"
    printf '%s\n' 'FROM scratch' 'COPY --chmod=0555 server provisioner mobile-client /' 'ADD bootstrap.tar /' |
        "$timeout_bin" -k 5s 90s docker build -q --platform "linux/$arch" -t "$bootstrap_tag" -f - "$tmp/image" >/dev/null
    bootstrap_image_owned=true
    sql <<'SQL'
CREATE ROLE hello_server NOLOGIN;
CREATE ROLE fixture_server LOGIN IN ROLE hello_server PASSWORD 'server_only';
CREATE ROLE hello_onboarding_provisioner NOLOGIN;
CREATE ROLE fixture_provisioner LOGIN IN ROLE hello_onboarding_provisioner PASSWORD 'provisioner_only';
CREATE ROLE hello_onboarding_verifier NOLOGIN;
CREATE ROLE fixture_verify LOGIN IN ROLE hello_onboarding_verifier PASSWORD 'verify_only';
SQL
    sql -f /fixture/grants/server-grants.sql
    sql -f /fixture/grants/onboarding-grants.sql
    staff_hash=$(d run --rm --network none --read-only --entrypoint /mobile-client "$bootstrap_tag" staff-hash)
    sql -v hash="$staff_hash" <<'SQL'
INSERT INTO staff_users(role_id,name,email,password_hash)
SELECT id,'Activation Fixture','activation@fixture.invalid',:'hash' FROM roles WHERE name='SUPER_ADMIN';
SQL
    d create --pull=never --name "$bootstrap" --label "$label=$name" --network "$name" --user 65532:65532 \
        --read-only --cap-drop ALL --security-opt no-new-privileges --entrypoint /server "$bootstrap_tag" --deployment >/dev/null
    d start "$bootstrap" >/dev/null
    d create --pull=never --name "$provisioner" --label "$label=$name" --network "container:$bootstrap" --user 0:0 \
        --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges --entrypoint /provisioner "$bootstrap_tag" >/dev/null
    d start "$provisioner" >/dev/null
    staff_token=''
    for _ in {1..20}; do
        if staff_token=$(d exec "$bootstrap" /mobile-client staff-login 2>/dev/null); then break; fi
        sleep .5
    done
    [[ -n "$staff_token" ]] || fail 'Fixture staff could not log in'
    app=$(printf '%s\n' "$staff_token" | d exec -i "$bootstrap" /mobile-client register)
    [[ "$app" =~ ^[0-9a-f-]{36}$ ]] || fail 'Expected application ID from registry API'
    printf '%s\n' "$staff_token" | d exec -i "$bootstrap" /mobile-client enrollment "$app" 'c1000000-0000-4000-8000-000000000001'
    config=$(printf '%s\n' "$staff_token" | d exec -i "$bootstrap" /mobile-client upload "$app" 'c1000000-0000-4000-8000-000000000004')
    [[ "$config" =~ ^[0-9a-f-]{36}$ ]] || fail 'Expected configuration ID from credential upload API'
    d cp "$provisioner:/var/lib/hello/credentials/$app/staging/$config/service-account.json" "$tmp/certs/service-account.json"
    d rm -f "$provisioner" "$bootstrap" >/dev/null
    inputs render "$worker_image" "$name" "$mode" "$app" "$config" "$reservation" "$source_id"
    worker_digest=$(file_digest "$tmp/worker.json")
    report=$(printf '{"application_id":"%s","policy_sha256":"%s"}' "$app" "$worker_digest")
    sql -v app="$app" -v config="$config" -v worker="$worker_digest" <<'SQL'
INSERT INTO application_runtime_executor_scopes(role_id,application_id,environment,profile_revision)
SELECT 'fixture_executor'::regrole::oid,id,'staging',1 FROM applications WHERE public_id=:'app';
SET SESSION AUTHORIZATION fixture_executor;
SELECT bootstrap_onboarding_held_dispatch(:'app','staging',1,300,300);
RESET SESSION AUTHORIZATION;
INSERT INTO application_onboarding_verifier_scopes(role_id,application_id)
SELECT 'fixture_verify'::regrole::oid,id FROM applications WHERE public_id=:'app';
INSERT INTO application_worker_preflight_scopes(application_id,configuration_id,environment,generation,profile_revision,delivery_policy_sha256,worker_role)
SELECT id,:'config','staging',1,1,decode(:'worker','hex'),'fixture_worker'::regrole::oid FROM applications WHERE public_id=:'app';
SQL
    initial_lifecycle=$(lifecycle)
    [[ -n "$initial_lifecycle" ]] || fail 'Fixture lifecycle missing'
    assert_no_delivery
    read -r send_source_id < "$tmp/send-source-id"
    scheduler_digest=$(file_digest "$tmp/scheduler.json")
    admission_digest=$(file_digest "$tmp/admission.json")
    "$timeout_bin" -k 5s 180s env CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$go_bin" build -trimpath -o "$tmp/image/scheduler" ./cmd/scheduler
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
        certs/ca.crt:etc/hello/fixture-ca.crt:0640:0.65532 certs/client.crt:etc/hello/fixture-client.crt:0640:0.65532 \
        certs/client.key:etc/hello/fixture-client.key:0640:0.65532 \
        scheduler.json:etc/hello/scheduler.json:0640:0.65532 admission.json:etc/hello/worker.json:0640:0.65532 \
        certs/ca.crt:etc/hello/postgres-ca.crt:0644:0.0 > "$tmp/image/shared.tar"
    "$timeout_bin" -k 5s 180s env CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$go_bin" build -trimpath -o "$tmp/image/controlplane" ./cmd/controlplane
    printf '%s\n' 'FROM scratch' 'COPY --chmod=0555 worker scheduler server mobile-client controlplane /' 'ADD shared.tar /' |
        "$timeout_bin" -k 5s 90s docker build -q --platform "linux/$arch" -t "$shared_tag" -f - "$tmp/image" >/dev/null
    shared_image_owned=true
    "$timeout_bin" -k 5s 180s env CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$go_bin" build -trimpath -o "$tmp/image/provider" scripts/integration-provider.go
    printf '{"application_id":"%s","environment":"staging","credential_root":"/etc/hello/fcm/verifier","database_url":"postgres://fixture_verify:verify_only@postgres:5432/postgres?sslmode=verify-full&sslrootcert=/etc/hello/postgres-ca.crt"}\n' "$app" > "$tmp/controlplane.json"
    inputs archive -:etc:0755:0.0 -:etc/hello:0755:0.0 -:etc/hello/fcm:0755:0.0 \
        -:etc/ssl:0755:0.0 -:etc/ssl/certs:0755:0.0 certs/ca.crt:etc/ssl/certs/ca-certificates.crt:0644:0.0 \
        -:fixtures:0755:0.0 certs/oauth-public.pem:fixtures/oauth-public.pem:0644:0.0 \
        certs/provider.crt:fixtures/provider.crt:0644:0.0 certs/provider.key:fixtures/provider.key:0640:0.65532 \
        worker.json:etc/hello/worker.json:0640:0.65532 certs/ca.crt:etc/hello/postgres-ca.crt:0644:0.0 \
        controlplane.json:etc/hello/controlplane.json:0640:0.65532 \
        -:etc/hello/fcm/verifier:0755:0.0 "-:etc/hello/fcm/verifier/$config:0755:0.0" \
        "certs/service-account.json:etc/hello/fcm/verifier/$config/service-account.json:0640:0.65532" \
        certs/service-account.json:etc/hello/fcm/service-account.json:0640:0.65532 > "$tmp/image/policy.tar"
    printf '%s\n' 'FROM scratch' 'COPY --chmod=0555 worker provider controlplane /' \
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
CREATE ROLE fixture_scheduler LOGIN IN ROLE hello_scheduler PASSWORD 'scheduler_only';
CREATE ROLE fixture_admission LOGIN IN ROLE hello_admission PASSWORD 'admission_only';
GRANT EXECUTE ON FUNCTION public.acknowledge_shared_application_runtime(text,bytea,uuid)
TO fixture_scheduler, fixture_admission;
INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active)
VALUES(:'source','notification.send.requested','MkU3OEVBNTcwNTJENDM2Qk','preflight-fixture',true);
GRANT EXECUTE ON FUNCTION acknowledge_application_runtime(uuid,bigint,text,bytea,uuid) TO fixture_worker;
SQL
    sql -f /fixture/grants/server-grants.sql
    sql -c 'GRANT EXECUTE ON FUNCTION public.acknowledge_shared_application_runtime(text,bytea,uuid) TO fixture_server'
    d cp scripts/sql "$pg:/fixture-sql"
    # Admission rejects a missing commit; seed the empty topic's numeric offset without a record.
    kafka kafka-consumer-groups --group "$name" --topic notification.send.requested \
        --reset-offsets --to-offset 0 --execute >/dev/null
    d rm "$worker" >/dev/null
    create_daemon "$worker" "$worker_image" --run-approved-delivery
    create_daemon "$scheduler" --entrypoint /scheduler "$shared_tag"
    create_daemon "$admission" --entrypoint /worker "$shared_tag"
    create_daemon "$server" --entrypoint /server "$shared_tag" --deployment
    d create --pull=never --name "$provider" --label "$label=$name" --network "$name" \
        --network-alias oauth2.googleapis.com --network-alias fcm.googleapis.com \
        --user 65532:65532 --read-only --cap-drop ALL --security-opt no-new-privileges \
        --tmpfs /tmp -e HELLO_PROVIDER_ONBOARDING=synthetic --entrypoint /provider "$worker_image" >/dev/null
    d start "$provider" >/dev/null
    create_daemon "$verifier" --entrypoint /controlplane "$worker_image"
    d start "$verifier" "$server" >/dev/null
    staff_token=''
    for _ in {1..20}; do
        if staff_token=$(d exec "$server" /mobile-client staff-login 2>/dev/null); then break; fi
        sleep .5
    done
    [[ -n "$staff_token" ]] || fail 'Fixture staff could not log in'
    activation_key='c1000000-0000-4000-8000-000000000003'
    printf '%s\n' "$staff_token" | d exec -i "$server" /mobile-client verify "$app" "$config" 'c1000000-0000-4000-8000-000000000002' >/dev/null
    activation_operation=$(printf '%s\n' "$staff_token" | d exec -i "$server" /mobile-client activate "$app" "$config" "$activation_key" 202 false)
    [[ $(printf '%s\n' "$staff_token" | d exec -i "$server" /mobile-client activate "$app" "$config" "$activation_key" 202 true) == "$activation_operation" ]] ||
        fail 'Activation request did not replay'
    sql -v app="$app" -v config="$config" -v reservation="$reservation" -v worker="$worker_digest" \
        -v scheduler="$scheduler_digest" -v admission="$admission_digest" -v server="$server_digest" <<'SQL'
SELECT setup.revision AS setup_revision,n.origin_epoch,n.source_epoch FROM applications a
JOIN application_onboarding setup ON setup.application_id=a.id
CROSS JOIN notification_runtime n WHERE a.public_id=:'app' AND n.singleton \gset
SET SESSION AUTHORIZATION fixture_executor;
SELECT reserve_application_onboarding_runtime(:'app',:'reservation',:'config',1,1,:setup_revision,:'origin_epoch',:'source_epoch');
RESET SESSION AUTHORIZATION;
INSERT INTO application_runtime_component_scopes(application_id,generation,component,login_role,policy_sha256)
SELECT id,1,component,login_role::regrole::oid,decode(digest,'hex')
FROM applications CROSS JOIN (VALUES
    ('DELIVERY','fixture_worker',:'worker'),
    ('SCHEDULER','fixture_scheduler',:'scheduler'),
    ('ADMISSION','fixture_admission',:'admission'),
    ('SERVER','fixture_server',:'server')
) AS approvals(component,login_role,digest) WHERE public_id=:'app';
SQL
    d start "$worker" "$scheduler" "$admission" >/dev/null
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
    app_id=$(sql -c "SELECT id FROM applications WHERE public_id='$app'")
    sql -f /fixture-sql/finalize-application-ownership.sql
    sql -f /fixture-sql/install-application-lifecycle.sql
    sql <<'SQL'
CREATE ROLE hello_application_lifecycle_issuer NOLOGIN;
CREATE ROLE hello_application_lifecycle_operator NOLOGIN;
CREATE ROLE hello_onboarding_activation_issuer NOLOGIN;
CREATE ROLE fixture_activation_issuer LOGIN IN ROLE hello_onboarding_activation_issuer PASSWORD 'activation_issuer_only';
CREATE ROLE fixture_activation_operator LOGIN IN ROLE hello_application_lifecycle_operator PASSWORD 'activation_operator_only';
SQL
    sql -f /fixture/grants/application-lifecycle-grants.sql
    sql -f /fixture/grants/onboarding-activation-grants.sql
    sql -v app_id="$app_id" <<'SQL'
INSERT INTO application_onboarding_activation_issuer_scopes(application_id,issuer_role,executor_role)
VALUES(:app_id,'fixture_activation_issuer'::regrole::oid,'fixture_activation_operator'::regrole::oid);
SQL
    activation_grant=$(sql -c 'SELECT gen_random_uuid()')
    activation_instance=$(sql -c 'SELECT gen_random_uuid()')
    python3 - "$tmp" "$app" "$activation_operation" "$activation_grant" "$activation_instance" <<'PY'
import hashlib
import json
import pathlib
import sys

tmp, app, operation, grant, instance = sys.argv[1:]
root = pathlib.Path(tmp)
def dsn(role):
    return f"postgres://fixture_activation_{role}:activation_{role}_only@postgres:5432/postgres?sslmode=verify-full&sslrootcert=/etc/hello/postgres-ca.crt"

issuer = dict(database_url=dsn("issuer"), application_id=app, operation_id=operation,
              grant_id=grant, executor="fixture_activation_operator", generation=1, retired_writer_roles=[])
for label in ("ownership", "writer_fence", "cohort_fence"):
    issuer[f"{label}_sha256"] = hashlib.sha256(f"fixture reviewed {label} {app}".encode()).hexdigest()
executor = dict(database_url=dsn("operator"), operation_id=operation, grant_id=grant, instance_id=instance)
(root / "first-activation-approval.json").write_text(json.dumps(issuer))
(root / "first-activation.json").write_text(json.dumps(executor))
PY
    for command in issue-first-activation apply-first-activation; do
        case "$command" in
            issue-first-activation) activation_policy=first-activation-approval.json ;;
            apply-first-activation) activation_policy=first-activation.json ;;
        esac
        inputs archive "$activation_policy:etc/hello/$activation_policy:0640:0.65532" > "$tmp/image/activation.tar"
        printf '%s\n' "FROM $shared_tag" 'ADD activation.tar /' |
            "$timeout_bin" -k 5s 90s docker build -q --platform "linux/$arch" -t "$activation_tag" -f - "$tmp/image" >/dev/null
        activation_image_owned=true
        create_daemon "$activation_container" --entrypoint /controlplane "$activation_tag" "$command"
        first_result=$(d start -a "$activation_container")
        [[ -n "$first_result" && $(d start -a "$activation_container") == "$first_result" ]] ||
            fail "$command did not replay"
        d rm "$activation_container" >/dev/null
        d image rm "$activation_tag" >/dev/null
        activation_image_owned=false
    done
    [[ $(sql -c "SELECT a.status || '|' || d.dispatch_mode || '|' || o.state FROM applications a
        JOIN application_dispatch_policy d ON d.application_id=a.id JOIN application_onboarding_operations o ON o.application_id=a.id
        WHERE a.public_id='$app' AND o.id='$activation_operation'") == 'ACTIVE|HELD|SUCCEEDED' ]] ||
        fail 'Delegated activation did not enter ACTIVE while dispatch stayed HELD'
    [[ $(sql -c "SELECT count(*) FROM audit_logs WHERE application_id=$app_id
        AND action='APPLICATION_ONBOARDING_OPERATION_COMPLETE' AND after_json->>'operation_id'='$activation_operation'") == 1 ]] ||
        fail 'Delegated activation replay duplicated completion audit'
    enrollment_state=$(lifecycle)
    sql -v app_id="$app_id" <<'SQL'
WITH seeded AS (INSERT INTO users(application_id,external_id) VALUES(:app_id,'fixture-device') RETURNING id)
INSERT INTO mobile_identities(application_id,identity_namespace,subject,user_id)
SELECT :app_id,'fixture-users','fixture-device',id FROM seeded;
SQL
    installation=$(d exec "$server" /mobile-client "$app")
    [[ "$installation" =~ ^[0-9a-f-]{36}$ ]] || fail 'Expected an installation ID from the mobile API'
    [[ $(sql -c "SELECT count(*) FROM mobile_installations WHERE application_id=$app_id AND public_id='$installation'") == 1 ]] ||
        fail 'Mobile API did not persist an isolated test installation'
    [[ $(lifecycle) == "$enrollment_state" ]] || fail 'Enrollment changed lifecycle or dispatch state'
    sql -f /fixture-sql/fixture-onboarding-pilot.sql >/dev/null
    [[ $(printf '%s\n' "$staff_token" | d exec -i "$server" /mobile-client test-installations "$app" "$installation") == "$installation" ]] ||
        fail 'Staff API did not list the enrolled test installation'
    sql -v app_id="$app_id" <<'SQL'
CREATE ROLE fixture_cohort_importer LOGIN PASSWORD 'cohort_importer_only';
CREATE ROLE fixture_cohort_coordinator LOGIN PASSWORD 'cohort_coordinator_only';
CREATE ROLE fixture_cohort_verifier LOGIN PASSWORD 'cohort_verifier_only';
INSERT INTO application_onboarding_cohort_scopes VALUES
    ('fixture_cohort_importer'::regrole::oid,:app_id,'staging','APPROVAL_IMPORTER'),
    ('fixture_cohort_coordinator'::regrole::oid,:app_id,'staging','COORDINATOR'),
    ('fixture_cohort_verifier'::regrole::oid,:app_id,'staging','VERIFIER');
GRANT EXECUTE ON FUNCTION approve_onboarding_cohort(uuid,text,uuid,uuid,uuid[],text,bytea,timestamptz) TO fixture_cohort_importer;
GRANT EXECUTE ON FUNCTION find_onboarding_cohort_operation(uuid,text),initialize_onboarding_cohort(uuid,text,uuid,uuid),
    finalize_onboarding_cohort(uuid,text,uuid,uuid,bigint) TO fixture_cohort_coordinator;
GRANT EXECUTE ON FUNCTION claim_onboarding_cohort_target(uuid,text,uuid),
    finish_onboarding_cohort_target(uuid,text,uuid,uuid,uuid,uuid,uuid,bigint,uuid,bigint,text,text) TO fixture_cohort_verifier;
SQL
    cohort_approval=$(sql -c 'SELECT gen_random_uuid()')
    cohort_key=$(sql -c 'SELECT gen_random_uuid()')
    python3 - "$tmp" "$app" "$config" "$installation" "$cohort_approval" <<'PYTHON'
from datetime import datetime, timedelta, timezone
import hashlib
import json
import pathlib
import sys

tmp, app, config, installation, approval = sys.argv[1:]
root = pathlib.Path(tmp)
delivery = json.loads((root / 'worker.json').read_bytes())['delivery']
def database(role):
    return (f'postgres://fixture_cohort_{role}:cohort_{role}_only@postgres:5432/postgres'
            '?sslmode=verify-full&sslrootcert=/etc/hello/postgres-ca.crt')
scope = dict(application_id=app, environment='staging')
policies = {
    'cohort-approval.json': dict(
        **scope, database_url=database('importer'), configuration_id=config,
        approval_id=approval, installation_ids=[installation], issuer_key_id='fixture-consent',
        evidence_sha256=hashlib.sha256(f'fixture reviewed consent {installation}'.encode()).hexdigest(),
        expires_at=(datetime.now(timezone.utc) + timedelta(minutes=5)).isoformat(),
    ),
    'cohort-coordinator.json': dict(**scope, database_url=database('coordinator')),
    'cohort-verifier.json': dict(
        verifier=dict(**scope, database_url=database('verifier'), credential_root='/etc/hello/cohort-credentials'),
        current_encryption_version=delivery['current_key_version'],
        fingerprint_key=delivery['fingerprint_key'], encryption_keys=delivery['encryption_keys'],
    ),
}
for name, policy in policies.items():
    (root / name).write_text(json.dumps(policy) + '\n')
PYTHON
    build_cohort_image() {
        local role=$1
        shift
        inputs archive -:etc:0755:0.0 -:etc/hello:0755:0.0 certs/ca.crt:etc/hello/postgres-ca.crt:0644:0.0 \
            "$@" > "$tmp/image/cohort.tar"
        printf '%s\n' 'FROM scratch' 'COPY --chmod=0555 controlplane /' 'ADD cohort.tar /' |
            "$timeout_bin" -k 5s 90s docker build -q --platform "linux/$arch" -t "$cohort_tag-$role" -f - "$tmp/image" >/dev/null
        cohort_images="$cohort_images $cohort_tag-$role"
    }
    build_cohort_image approver cohort-approval.json:etc/hello/cohort-approval.json:0640:0.65532
    build_cohort_image coordinator cohort-coordinator.json:etc/hello/cohort-coordinator.json:0640:0.65532
    build_cohort_image verifier cohort-verifier.json:etc/hello/cohort-verifier.json:0640:0.65532 \
        -:etc/hello/cohort-credentials:0755:0.0 "-:etc/hello/cohort-credentials/$config:0750:0.65532" \
        "certs/service-account.json:etc/hello/cohort-credentials/$config/service-account.json:0640:0.65532" \
        -:etc/ssl:0755:0.0 -:etc/ssl/certs:0755:0.0 certs/ca.crt:etc/ssl/certs/ca-certificates.crt:0644:0.0
    create_daemon "$cohort_approver" --entrypoint /controlplane "$cohort_tag-approver" approve-cohort
    [[ $(d start -a "$cohort_approver") == "$cohort_approval" ]] || fail 'Protected cohort approval failed'
    [[ $(d start -a "$cohort_approver") == "$cohort_approval" ]] || fail 'Protected cohort approval did not replay'
    d rm "$cohort_approver" >/dev/null
    cohort=$(printf '%s\n' "$staff_token" | d exec -i "$server" /mobile-client test-cohort \
        "$app" "$cohort_approval" "$installation" "$cohort_key" 202 false)
    [[ "$cohort" =~ ^[0-9a-f-]{36}$ ]] || fail 'Expected cohort operation ID from staff API'
    [[ $(printf '%s\n' "$staff_token" | d exec -i "$server" /mobile-client test-cohort \
        "$app" "$cohort_approval" "$installation" "$cohort_key" 202 true) == "$cohort" ]] || fail 'Cohort API did not replay'
    baseline_ledger=$(ledger)
    baseline_offsets=$(offsets | sort)
    create_daemon "$cohort_coordinator" --entrypoint /controlplane "$cohort_tag-coordinator" coordinate-cohort
    create_daemon "$cohort_verifier" --entrypoint /controlplane "$cohort_tag-verifier" verify-cohort
    d start "$cohort_coordinator" "$cohort_verifier" >/dev/null
    cohort_deadline=$((SECONDS + 45))
    target_status=PENDING
    while (( SECONDS < cohort_deadline )); do
        assert_running "$cohort_coordinator"
        assert_running "$cohort_verifier"
        target_status=$(printf '%s\n' "$staff_token" | d exec -i "$server" /mobile-client test-cohort-targets \
            "$app" "$cohort" "$installation")
        case "$target_status" in
            PASSED) [[ $(sql -c "SELECT state FROM application_onboarding_operations WHERE id='$cohort'") == SUCCEEDED ]] && break ;;
            FAILED|INCONCLUSIVE) fail "Cohort dry-run ended in $target_status" ;;
        esac
        sleep .25
    done
    [[ "$target_status" == PASSED && $(sql -c "SELECT state FROM application_onboarding_operations WHERE id='$cohort'") == SUCCEEDED ]] ||
        fail 'Cohort operation did not reach SUCCEEDED with a PASSED target'
    [[ $(d logs "$provider" 2>&1 | awk '/accepted synthetic FCM validation/ { n++ } END { print n+0 }') == 1 ]] ||
        fail 'Expected exactly one synthetic FCM dry-run'
    [[ $(ledger) == "$baseline_ledger" && $(offsets | sort) == "$baseline_offsets" ]] ||
        fail 'Dry-run created delivery evidence'
    d rm -fv "$cohort_coordinator" "$cohort_verifier" >/dev/null
    d image rm "$cohort_tag-approver" "$cohort_tag-coordinator" "$cohort_tag-verifier" >/dev/null
    cohort_images=''
    sql -c "ALTER ROLE fixture_dispatch_operator PASSWORD 'release_fixture_only'"
    queue_release() {
        printf '%s\n' "$staff_token" | d exec -i "$server" /mobile-client release "$app" "$1" "$cohort" "$2" "$3" "$4" "$5"
    }
    # DBA provisioning is fixture infrastructure. Every approval and grant for
    # the tested operation goes through the same protected command as production.
    sql -c "ALTER ROLE fixture_dispatch_issuer PASSWORD 'release_fixture_only';" >/dev/null
    sql -v project_id="$app" -v environment=staging -v reviewer=fixture_dispatch_issuer \
        -v executor=fixture_dispatch_operator < deploy/postgres/onboarding-release-review-grants.sql

    release_review() {
        local command=$1 policy_path=$2 expected=$3 first replay
        inputs archive -:etc:0755:0.0 -:etc/hello:0755:0.0 \
            "release-review.json:etc/hello/$policy_path:0640:0.65532" > "$tmp/image/release.tar"
        printf '%s\n' "FROM $shared_tag" 'ADD release.tar /' |
            "$timeout_bin" -k 5s 90s docker build -q --platform "linux/$arch" -t "$release_tag" -f - "$tmp/image" >/dev/null
        create_daemon "$release_container" --entrypoint /controlplane "$release_tag" "$command"
        first=$(d start -a "$release_container") || first=''
        replay=$(d start -a "$release_container") || replay=''
        d rm "$release_container" >/dev/null
        d image rm "$release_tag" >/dev/null
        [[ "$first" == "$expected" && "$replay" == "$expected" ]] || fail "$command did not commit and replay"
        printf '%s\n' "$first"
    }

    # Route review follows a successful cohort and can select only a member
    # of that exact approved installation roster.
    sql -c "ALTER ROLE fixture_route_issuer PASSWORD 'route_fixture_only'; ALTER ROLE fixture_route_operator PASSWORD 'route_fixture_only';" >/dev/null
    sql -v project_id="$app" -v environment=staging -v reviewer=fixture_route_issuer \
        -v executor=fixture_route_operator < deploy/postgres/onboarding-pilot-route-grants.sql
    route_grant=$(sql -c 'SELECT gen_random_uuid()')
    python3 - "$app" "$cohort" "$installation" "$route_grant" > "$tmp/release-review.json" <<'PYTHON'
import json
import sys
from datetime import datetime, timedelta, timezone

app, cohort, installation, grant = sys.argv[1:]
json.dump(dict(
    database_url='postgres://fixture_route_issuer:route_fixture_only@postgres:5432/postgres?sslmode=verify-full&sslrootcert=/etc/hello/postgres-ca.crt',
    application_id=app, environment='staging', cohort_id=cohort,
    installation_id=installation, grant_id=grant,
    expires_at=(datetime.now(timezone.utc) + timedelta(minutes=5)).isoformat(),
), sys.stdout)
PYTHON
    release_review approve-pilot-route pilot-route-approval.json "$route_grant" >/dev/null
    python3 - "$app" "$route_grant" > "$tmp/release-review.json" <<'PYTHON'
import json
import sys

app, grant = sys.argv[1:]
json.dump(dict(
    database_url='postgres://fixture_route_operator:route_fixture_only@postgres:5432/postgres?sslmode=verify-full&sslrootcert=/etc/hello/postgres-ca.crt',
    application_id=app, environment='staging', grant_id=grant,
), sys.stdout)
PYTHON
    release_review apply-pilot-route pilot-route-application.json 1 >/dev/null
    [[ $(sql -c "SELECT count(*) FROM current_binding_routes WHERE application_id='$app_id' AND cohort_id='$cohort' AND destination='FCM_ACTIVE'") == 1 ]] ||
        fail 'Protected route workflow did not apply exactly one approved route'

    release_approval() {
        local approval
        approval=$(sql -c 'SELECT gen_random_uuid()')
        python3 - "$app" "$config" "$cohort" "$1" "$approval" > "$tmp/release-review.json" <<'PYTHON'
import hashlib
import json
import sys
from datetime import datetime, timedelta, timezone

app, config, cohort, mode, approval = sys.argv[1:]
json.dump(dict(
    database_url='postgres://fixture_dispatch_issuer:release_fixture_only@postgres:5432/postgres?sslmode=verify-full&sslrootcert=/etc/hello/postgres-ca.crt',
    application_id=app, environment='staging', approval_id=approval, mode=mode,
    configuration_id=config, cohort_id=cohort, expires_at=(datetime.now(timezone.utc) + timedelta(minutes=5)).isoformat(),
    evidence_sha256=hashlib.sha256(f'fixture review {mode} {app} {cohort}'.encode()).hexdigest(),
), sys.stdout)
PYTHON
        release_review approve-release dispatch-release-approval.json "$approval"
    }

    issue_release_grant() {
        local operation=$1 validity=$2 grant generation
        grant=$(sql -c 'SELECT gen_random_uuid()')
        generation=$(sql -c "SELECT runtime_generation FROM application_onboarding WHERE application_id=$app_id")
        python3 - "$app" "$operation" "$grant" "$generation" "$validity" > "$tmp/release-review.json" <<'PYTHON'
import hashlib
import json
import sys
from datetime import datetime, timedelta, timezone

app, operation, grant, generation, validity = sys.argv[1:]
evidence = {f'{kind}_sha256': hashlib.sha256(f'fixture {kind} {operation}'.encode()).hexdigest()
            for kind in ('legacy', 'consent', 'coverage', 'quota')}
json.dump(dict(
    database_url='postgres://fixture_dispatch_issuer:release_fixture_only@postgres:5432/postgres?sslmode=verify-full&sslrootcert=/etc/hello/postgres-ca.crt',
    application_id=app, environment='staging', operation_id=operation, grant_id=grant,
    runtime_generation=int(generation), expires_at=(datetime.now(timezone.utc) + timedelta(seconds=int(validity))).isoformat(),
    **evidence,
), sys.stdout)
PYTHON
        release_review delegate-release dispatch-release-delegation.json "$grant"
    }
    apply_release_cli() {
        local operation=$1 grant=$2 instance=$3 revision=$4
        python3 - "$operation" "$grant" "$instance" > "$tmp/release.json" <<'PY'
import json
import sys

operation, grant, instance = sys.argv[1:]
json.dump(dict(database_url='postgres://fixture_dispatch_operator:release_fixture_only@postgres:5432/postgres?sslmode=verify-full&sslrootcert=/etc/hello/postgres-ca.crt',
               operation_id=operation, grant_id=grant, instance_id=instance), sys.stdout)
PY
        inputs archive -:etc:0755:0.0 -:etc/hello:0755:0.0 \
            release.json:etc/hello/dispatch-release.json:0640:0.65532 > "$tmp/image/release.tar"
        printf '%s\n' "FROM $shared_tag" 'ADD release.tar /' |
            "$timeout_bin" -k 5s 90s docker build -q --platform "linux/$arch" -t "$release_tag" -f - "$tmp/image" >/dev/null
        release_image_owned=true
        create_daemon "$release_container" --entrypoint /controlplane "$release_tag" apply-release
        [[ $(d start -a "$release_container") == "$revision" ]] || fail 'Release entrypoint did not commit'
        [[ $(d start -a "$release_container") == "$revision" ]] || fail 'Release entrypoint did not replay'
        [[ $(sql -c "SELECT count(*) FROM audit_logs WHERE application_id=$app_id AND action='APPLICATION_ONBOARDING_OPERATION_COMPLETE' AND after_json->>'operation_id'='$operation'") == 1 ]] || fail 'Release replay duplicated or omitted completion audit'
        d rm "$release_container" >/dev/null
        d image rm "$release_tag" >/dev/null
        release_image_owned=false
    }

    unbound_approval=$(release_approval PILOT)
    unbound_key=$(sql -c 'SELECT gen_random_uuid()')
    unbound_operation=$(queue_release PILOT "$unbound_approval" "$unbound_key" 202 false)
    # Simulate an operator revocation before the grant is issued.
    sql -c "UPDATE application_onboarding_approvals SET revoked_at=clock_timestamp() WHERE id='$unbound_approval'" >/dev/null
    pilot_approval=$(release_approval PILOT)
    expired_key=$(sql -c 'SELECT gen_random_uuid()')
    expired_operation=$(queue_release PILOT "$pilot_approval" "$expired_key" 202 false)
    [[ $(sql -c "SELECT state FROM application_onboarding_operations WHERE id='$unbound_operation'") == SUPERSEDED ]] || fail 'Revoked approval left a grant-less operation stuck'
    [[ $(queue_release PILOT "$unbound_approval" "$unbound_key" 202 true) == "$unbound_operation" ]] || fail 'Grant-less old key did not replay original acceptance'
    conflict_key=$(sql -c 'SELECT gen_random_uuid()')
    queue_release PILOT "$pilot_approval" "$conflict_key" 409 false >/dev/null
    expired_grant=$(issue_release_grant "$expired_operation" 25)
    if raw_error=$(sql -c "SET SESSION AUTHORIZATION fixture_dispatch_operator; SELECT apply_application_dispatch('$expired_grant','$app');" 2>&1) ||
        [[ "$raw_error" != *'delegated onboarding release required'* ]]; then
        fail "Direct grant application bypassed onboarding: $raw_error"
    fi
    queue_release PILOT "$pilot_approval" "$conflict_key" 409 false >/dev/null
    deadline=$((SECONDS + 30))
    until [[ $(sql -c "SELECT expires_at<=clock_timestamp() FROM application_dispatch_grants WHERE id='$expired_grant'") == t ]]; do
        (( SECONDS < deadline )) || fail 'Short-lived release grant did not expire'
        sleep .25
    done
    revoked_operation=$(queue_release PILOT "$pilot_approval" "$conflict_key" 202 false)
    [[ $(queue_release PILOT "$pilot_approval" "$expired_key" 202 true) == "$expired_operation" ]] || fail 'Old key did not replay original acceptance'
    revoked_grant=$(issue_release_grant "$revoked_operation" 300)
    sql -c "SET SESSION AUTHORIZATION fixture_dispatch_issuer; SELECT revoke_application_dispatch('$revoked_grant','$app');" >/dev/null
    pilot_key=$(sql -c 'SELECT gen_random_uuid()')
    pilot_operation=$(queue_release PILOT "$pilot_approval" "$pilot_key" 202 false)
    [[ $(sql -c "SELECT count(*) FROM application_onboarding_operations WHERE id IN ('$expired_operation','$revoked_operation') AND state='SUPERSEDED' AND failure_code='RELEASE_GRANT_UNAVAILABLE'") == 2 ]] || fail 'Unusable delegations were not terminally superseded'
    [[ $(sql -c "SELECT count(*) FROM audit_logs WHERE application_id=$app_id AND action='APPLICATION_ONBOARDING_OPERATION_COMPLETE' AND after_json->>'outcome'='SUPERSEDED' AND after_json->>'operation_id' IN ('$expired_operation','$revoked_operation')") == 2 ]] || fail 'Missing supersede audit'
    pilot_instance=$(sql -c 'SELECT gen_random_uuid()')
    if sql -c "SET SESSION AUTHORIZATION fixture_dispatch_operator; SELECT apply_onboarding_release('$revoked_operation','$revoked_grant','$pilot_instance');" >/dev/null 2>&1; then
        fail 'Superseded operation could still be executed'
    fi
    pilot_grant=$(issue_release_grant "$pilot_operation" 300)
    pilot_revision=$(sql -c "SELECT expected_revision+1 FROM application_dispatch_grants WHERE id='$pilot_grant'")
    apply_release_cli "$pilot_operation" "$pilot_grant" "$pilot_instance" "$pilot_revision"
    [[ $(sql -c "SELECT dispatch_mode FROM application_dispatch_policy WHERE application_id=$app_id") == PILOT ]] || fail 'PILOT policy was not persisted'
    # Read fixture identities for the business API. Only HTTPS writes the
    # staff grant, campaign draft, send key and acceptance outbox.
    campaign_staff=$(sql -c "SELECT id FROM staff_users WHERE email='activation@fixture.invalid'")
    campaign_user=$(sql -c "SELECT owner_user_id FROM mobile_installations WHERE application_id=$app_id AND public_id='$installation'")
    campaign_key=$(sql -c 'SELECT gen_random_uuid()')
    campaign=$(printf '%s\n' "$staff_token" | d exec -i "$server" /mobile-client pilot-campaign \
        "$app" "$campaign_staff" "$campaign_user" "$campaign_key")
    [[ "$campaign" =~ ^[0-9]+$ ]] || fail 'Fixture did not accept one campaign through HTTPS'
    deadline=$((SECONDS + 35))
    while [[ $(sql -c "SELECT count(*) FROM notification_delivery_intents WHERE application_id=$app_id AND notification_id=$campaign AND provider_accepted_at IS NOT NULL") != 1 ]]; do
        if (( SECONDS >= deadline )); then
            sql -c "SELECT status,attempt_count,last_result_disposition FROM notification_delivery_intents WHERE application_id=$app_id AND notification_id=$campaign" >&2 || true
            d logs --tail 12 "$provider" >&2 || true
            fail 'Simulator did not accept the pilot notification'
        fi
        sleep .25
    done
    [[ $(sql -c "SELECT count(*) FROM notification_delivery_intents x JOIN mobile_installations i ON (i.application_id,i.id)=(x.application_id,x.installation_id) WHERE x.application_id=$app_id AND x.notification_id=$campaign AND i.public_id='$installation'") == 1 ]] || fail 'Pilot delivery did not target the enrolled installation'
    [[ $(sql -c "SELECT count(*) FROM notification_delivery_intents WHERE application_id=$app_id AND notification_id=$campaign") == 1 ]] || fail 'Pilot created additional delivery intents'
    full_approval=$(release_approval FULL)
    full_key=$(sql -c 'SELECT gen_random_uuid()')
    full_operation=$(queue_release FULL "$full_approval" "$full_key" 202 false)
    full_grant=$(issue_release_grant "$full_operation" 300)
    full_instance=$(sql -c 'SELECT gen_random_uuid()')
    apply_full="SET SESSION AUTHORIZATION fixture_dispatch_operator; SELECT apply_onboarding_release('$full_operation','$full_grant','$full_instance');"
    if full_error=$(sql -c "BEGIN; UPDATE staff_sessions SET revoked_at=clock_timestamp(),revocation_reason='logout' WHERE id=(SELECT session_id FROM application_onboarding_operations WHERE id='$full_operation'); $apply_full ROLLBACK;" 2>&1) ||
        [[ "$full_error" != *'release session unavailable'* ]]; then
        fail "Revoked session was not fenced: $full_error"
    fi
    if full_error=$(sql -c "$apply_full" 2>&1) || [[ "$full_error" != *'onboarding full prerequisites unavailable'* ]]; then
        fail "Provider acceptance alone did not fence FULL: $full_error"
    fi
    binding=$(sql -c "SELECT x.binding_id FROM notification_delivery_intents x JOIN mobile_installations i ON (i.application_id,i.id)=(x.application_id,x.installation_id) WHERE x.application_id=$app_id AND x.notification_id=$campaign AND i.public_id='$installation'")
    [[ "$binding" =~ ^[0-9]+$ ]] || fail 'Expected a single binding for the pilot payload'
    d exec "$provider" /provider read-onboarding-payload | d exec -i "$server" /mobile-client "$app" "$campaign" "$binding"
    [[ $(sql -c "SELECT count(*) FROM notification_observations o JOIN notification_delivery_intents x ON (x.application_id,x.id)=(o.application_id,o.delivery_id) WHERE x.application_id=$app_id AND x.notification_id=$campaign AND o.event='DELIVERED'") == 1 ]] || fail 'Expected one durable receipt after exact replay'
    # Roll back fixture-only mutations and the SQL trial before exercising the release binary.
    if full_error=$(sql -c "BEGIN; UPDATE notification_observations SET event='OPENED' WHERE application_id=$app_id; $apply_full ROLLBACK;" 2>&1) ||
        [[ "$full_error" != *'onboarding full prerequisites unavailable'* ]]; then
        fail "OPENED was not fenced: $full_error"
    fi
    if full_error=$(sql -c "BEGIN; UPDATE hello_delivery_internal.onboarding_pilot_attempts SET runtime_generation=runtime_generation+1 WHERE application_id=$app_id; $apply_full ROLLBACK;" 2>&1) ||
        [[ "$full_error" != *'onboarding full prerequisites unavailable'* ]]; then
        fail "Wrong runtime generation was not fenced: $full_error"
    fi
    if full_error=$(sql -c "BEGIN; UPDATE application_onboarding SET enrollment_platforms=enrollment_platforms || '[{\"platform\":\"IOS\",\"application_identifier\":\"fixture.ios\"}]'::jsonb,revision=revision+1 WHERE application_id=$app_id; $apply_full ROLLBACK;" 2>&1) ||
        [[ "$full_error" != *'release prerequisites unavailable'* ]]; then
        fail "Stale setup revision was not fenced: $full_error"
    fi
    [[ $(sql -c "BEGIN; $apply_full ROLLBACK;") == "$((pilot_revision + 1))" ]] || fail 'Signed pilot receipt did not unlock FULL'
    [[ $(sql -c "SELECT consumed_at IS NULL FROM application_onboarding_approvals WHERE id='$full_approval'") == t ]] || fail 'Failed or rolled-back execution consumed FULL approval'
    apply_release_cli "$full_operation" "$full_grant" "$full_instance" "$((pilot_revision + 1))"
    [[ $(sql -c "SELECT count(*) FROM application_onboarding_approvals WHERE id IN ('$pilot_approval','$full_approval') AND consumed_at IS NOT NULL") == 2 ]] || fail 'Successful releases did not consume their approvals'
    [[ $(sql -c "SELECT dispatch_mode FROM application_dispatch_policy WHERE application_id=$app_id") == FULL ]] || fail 'FULL policy was not persisted'
    released_state=$(lifecycle)
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
    [[ $(sql -c 'SELECT count(*) FROM application_runtime_acknowledgements') == 4 ]] || fail 'Expected four runtime acknowledgements'
    [[ $(lifecycle) == "$released_state" ]] || fail 'Sealing changed lifecycle or dispatch state'
    printf '%s\n' 'Services fixture passed: delegated activation, HTTPS enrollment while held, one simulated PILOT push, signed DELIVERED receipt, and FULL release with CLI replay'
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
