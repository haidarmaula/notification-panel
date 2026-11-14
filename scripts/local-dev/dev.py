#!/usr/bin/env python3
"""Persistent, isolated development stack. Never reads the repository .env."""

import argparse
import contextlib
from datetime import datetime, timedelta, timezone
import fcntl
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import shutil
import signal
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
STATE = ROOT / "tmp/local-dev"
CONFIG = STATE / "config"
TLS = STATE / "tls"
BIN = STATE / "bin"
LOGS = STATE / "logs"
PROJECT = "hello-local"
SERVICES = ["server", "api-tls", "admission", "delivery-worker", "scheduler"]
TOPICS = ["notification.send.requested", "notification.delivery.updated"]


def run(args, *, data=None, capture=False, timeout=180, env=None, cwd=ROOT):
    result = subprocess.run(args, input=data, stdout=subprocess.PIPE if capture else None,
                            stderr=subprocess.PIPE if capture else None, timeout=timeout,
                            cwd=cwd, env=env or os.environ.copy())
    if result.returncode:
        if capture:
            sys.stderr.buffer.write(result.stderr)
        raise RuntimeError(f"Command failed ({result.returncode}): {args[0]}; see logs if redirected")
    return result.stdout if capture else b""


def private_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    temporary = path.with_suffix(".new")
    temporary.write_text(json.dumps(value, indent=2) + "\n")
    temporary.chmod(0o600)
    temporary.replace(path)


def require_owner():
    owner = STATE / "owner.json"
    if not owner.exists() or json.loads(owner.read_text()) != {"repository": str(ROOT), "scope": "synthetic-local-v1"}:
        raise RuntimeError("Local environment is not initialized for this checkout; run ./dev up first")


def compose(*args, **kwargs):
    env = os.environ.copy()
    env.update(HELLO_INTEGRATION_CERTS_DIR=str(TLS), HELLO_INTEGRATION_BIN_DIR=str(BIN),
               HELLO_LOCAL_CONFIG_DIR=str(CONFIG))
    return run(["docker", "compose", "--env-file", "/dev/null", "-p", PROJECT,
                "-f", "compose.integration.yml", "-f", "compose.local.yml", *args], env=env, **kwargs)


def sql(statement, login="integration"):
    return compose("exec", "-T", "postgres", "psql", "-XqAt", "-v", "ON_ERROR_STOP=1",
                   "-U", login, "-d", "notification_integration", data=statement.encode(), capture=True).decode().strip()


def toolchain():
    # Respect a compatible active Node before selecting an already-installed nvm version.
    try:
        major = int(run(["node", "-p", "process.versions.node.split('.')[0]"], capture=True).strip())
    except (FileNotFoundError, ValueError):
        major = 0
    if major < 22:
        candidates = sorted((Path.home() / ".nvm/versions/node").glob("v22.*/bin/node"),
                            key=lambda p: tuple(map(int, p.parts[-3][1:].split("."))))
        if not candidates:
            raise RuntimeError("Node 22.12+ is required. Run nvm use 22.23.4 first.")
        os.environ["PATH"] = str(candidates[-1].parent) + os.pathsep + os.environ["PATH"]
    for name in ("go", "docker", "pnpm"):
        if not shutil.which(name):
            raise RuntimeError(f"Required command missing: {name}")


def docker_ready():
    endpoint = run(["docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}"], capture=True, timeout=15).decode().strip()
    if not endpoint.startswith("unix://") or not os.environ.get("DOCKER_HOST", "unix://").startswith("unix://"):
        raise RuntimeError("This development stack requires a local Docker Unix socket.")
    architecture = run(["docker", "info", "--format", "{{.Architecture}}"], capture=True, timeout=20).decode().strip()
    return {"aarch64": "arm64", "arm64": "arm64", "amd64": "amd64", "x86_64": "amd64"}[architecture]


def validate_compose():
    config = json.loads(compose("config", "--format", "json", capture=True))
    for name, service in config["services"].items():
        if set(service["networks"]) != ({"delivery", "ingress"} if name == "api-tls" else {"delivery"}):
            raise RuntimeError(f"Unexpected external network on {name}")
        ports = service.get("ports", [])
        if name != "api-tls" and ports:
            raise RuntimeError(f"Unexpected published port on {name}")
        if any(port.get("host_ip") != "127.0.0.1" for port in ports):
            raise RuntimeError("Development listeners must remain on loopback")
    if not config["networks"]["delivery"].get("internal"):
        raise RuntimeError("Provider isolation is required")


def build(architecture):
    env = os.environ.copy()
    env.update(GOOS="linux", GOARCH=architecture, CGO_ENABLED="0", GOWORK="off")
    for name, target in (("server", "./cmd/server"), ("worker", "./cmd/worker"),
                         ("scheduler", "./cmd/scheduler"), ("provider", "./scripts/integration-provider.go"),
                         ("setup", "./scripts/local-dev/setup.go")):
        print(f"Building {name}…", flush=True)
        temporary = BIN / (name + ".new")
        run(["go", "build", "-o", str(temporary), target], env=env)
        temporary.replace(BIN / name)
    temporary = BIN / "dashboard.new"
    run(["go", "build", "-o", str(temporary), "./cmd/dashboard"])
    temporary.replace(BIN / "dashboard")


def configure():
    for directory in (STATE, CONFIG, CONFIG / "generated", TLS, BIN, LOGS):
        directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    settings = CONFIG / "settings.json"
    if not settings.exists():
        private_json(settings, {"email": "developer@hello.local", "password": secrets.token_hex(16),
                                "api_key": secrets.token_hex(32), "access": secrets.token_hex(32),
                                "refresh": secrets.token_hex(32)})
    values = json.loads(settings.read_text())
    env = {"DB_HOST": "postgres", "DB_PORT": "5432", "DB_USER": "integration", "DB_PASSWORD": "integration_only",
           "DB_NAME": "notification_integration", "DB_SSLMODE": "disable", "API_KEY": values["api_key"],
           "ACCESS_SECRET": values["access"], "REFRESH_SECRET": values["refresh"],
           "HTTP_LISTEN_ADDRESS": "0.0.0.0:8080", "CORS_ALLOWED_ORIGINS": ""}
    (CONFIG / "server.env").write_text("".join(f"{key}={value}\n" for key, value in env.items()))
    (CONFIG / "server.env").chmod(0o600)
    (CONFIG / "api-key").write_text(values["api_key"])
    (CONFIG / "api-key").chmod(0o600)
    private_json(CONFIG / "dashboard.json", {"origin": "http://127.0.0.1:5173", "upstream": "https://127.0.0.1:18082",
                 "api_key_file": str(CONFIG / "api-key"), "upstream_ca_file": str(TLS / "ca.crt"),
                 "upstream_cert_file": str(TLS / "client.crt"), "upstream_key_file": str(TLS / "client.key"),
                 "listen_address": "127.0.0.1:18090", "assets_directory": "", "development": True, "max_sessions": 100})


def migrate(execute=None):
    execute = execute or sql
    execute("CREATE TABLE IF NOT EXISTS local_dev_migrations(name text PRIMARY KEY, checksum text NOT NULL);")
    for path in sorted((ROOT / "db/migrations").glob("*.up.sql")):
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        previous = execute(f"SELECT checksum FROM local_dev_migrations WHERE name='{path.name}';")
        if previous:
            if previous != digest:
                raise RuntimeError(f"Applied migration changed: {path.name}; restore it or deliberately reset local data")
            continue
        print(f"Migrating {path.name}", flush=True)
        execute("BEGIN;\n" + path.read_text() + f"\nINSERT INTO local_dev_migrations VALUES('{path.name}','{digest}');\nCOMMIT;")


def prepare_application(execute=None):
    execute = execute or sql
    execute((ROOT / "scripts/local-dev/bootstrap.sql").read_text())
    for name in ("scripts/sql/finalize-application-ownership.sql", "scripts/sql/install-application-lifecycle.sql",
                 "deploy/postgres/application-lifecycle-grants.sql", "deploy/postgres/dispatch-policy-grants.sql"):
        execute((ROOT / name).read_text())
    if execute("SELECT status FROM applications WHERE key='load-fixture';") == "PROVISIONING":
        grant = execute("SELECT id FROM application_lifecycle_grants WHERE project_id='10000000-0000-4000-8000-000000000001' AND applied_at IS NULL AND revoked_at IS NULL AND expires_at>clock_timestamp()+interval '1 minute' AND destination='ACTIVE' ORDER BY issued_at DESC LIMIT 1;")
        if not grant:
            grant = execute("SELECT gen_random_uuid();")
            origin, source, expiry, digest = execute("SELECT origin_epoch,source_epoch,(clock_timestamp()+interval '1 hour')::text,encode(sha256('local synthetic evidence'),'hex') FROM notification_runtime WHERE singleton;").split("|")
            variables = {"project":"10000000-0000-4000-8000-000000000001", "grant_id":grant,"executor":"load_executor",
                         "origin":origin,"source":source,"revision":"1","from_status":"PROVISIONING","destination":"ACTIVE",
                         "ownership_sha256":digest,"writers_sha256":digest,"cohorts_sha256":digest,
                         "retired_roles":"{load_retired_writer}","expires_utc":expiry}
            issue = "".join(rf"\set {key} '{value}'" + "\n" for key,value in variables.items())
            execute(issue+(ROOT/"scripts/sql/issue-application-lifecycle.sql").read_text(),login="load_reviewer")
        apply = r"\set project '10000000-0000-4000-8000-000000000001'" + "\n" + rf"\set grant_id '{grant}'" + "\n"
        execute(apply+(ROOT/"scripts/sql/apply-application-lifecycle.sql").read_text(),login="load_executor")


def seed_application():
    prepare_application()
    compose("exec", "-T", "worker-fixture", "/fixture-bin/setup")
    settings = json.loads((CONFIG / "settings.json").read_text())
    settings["email"] = sql("SELECT s.email FROM local_dev_seed d JOIN staff_users s ON s.id=d.staff_id;")
    private_json(CONFIG / "settings.json", settings)



def test_seed_recovery():
    sql("CREATE DATABASE hello_local_seed_test;")
    expired_inserted = False
    def test_sql(statement, login="integration"):
        nonlocal expired_inserted
        if statement.startswith("SELECT id FROM application_lifecycle_grants") and not expired_inserted:
            expired_inserted = True
            origin, source = test_sql("SELECT origin_epoch,source_epoch FROM notification_runtime WHERE singleton;").split("|")
            test_sql("GRANT INSERT(issued_at) ON application_lifecycle_grants TO load_reviewer;")
            try:
                test_sql(f"""INSERT INTO application_lifecycle_grants
                  (id,project_id,operator_role,origin_epoch,source_epoch,expected_revision,previous_status,destination,
                   ownership_evidence,writer_fence_evidence,cohort_fence_evidence,retired_writer_roles,issued_at,expires_at)
                  VALUES(gen_random_uuid(),'10000000-0000-4000-8000-000000000001','load_executor'::regrole,
                   '{origin}','{source}',1,'PROVISIONING','ACTIVE',sha256('expired local evidence'),
                   sha256('expired local evidence'),sha256('expired local evidence'),ARRAY['load_retired_writer']::name[],
                   clock_timestamp()-interval '2 hours',clock_timestamp()-interval '1 hour');""",login="load_reviewer")
            finally:
                test_sql("REVOKE INSERT(issued_at) ON application_lifecycle_grants FROM load_reviewer;")
        return compose("exec", "-T", "postgres", "psql", "-XqAt", "-v", "ON_ERROR_STOP=1", "-U", login,
                       "-d", "hello_local_seed_test", data=statement.encode(), capture=True).decode().strip()
    try:
        migrate(test_sql)
        prepare_application(test_sql)
        if test_sql("SELECT count(*) FROM application_lifecycle_grants WHERE applied_at IS NULL AND expires_at<clock_timestamp();") != "1":
            raise RuntimeError("Expired pending approval was not preserved")
        print("Expired pending application approval replaced and prior evidence preserved.")
        compose("exec", "-T", "worker-fixture", "/fixture-bin/setup", "--test-seed-recovery")
    finally:
        sql("DROP DATABASE hello_local_seed_test;")


def supervisor_pid():
    try:
        pid = int((STATE / "supervisor.pid").read_text())
        command = subprocess.check_output(["ps", "-p", str(pid), "-o", "command="], text=True)
        return pid if str(Path(__file__).resolve()) in command and "_serve" in command else None
    except (FileNotFoundError, ValueError, subprocess.CalledProcessError):
        return None


def stop_host():
    pid = supervisor_pid()
    if pid:
        os.kill(pid, signal.SIGTERM)
        for _ in range(60):
            if not supervisor_pid():
                break
            time.sleep(0.2)
        else:
            raise RuntimeError("Local supervisor did not stop; inspect its log before proceeding")


def reload_backend(architecture):
    with (STATE / "command.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if not (STATE / "enabled").exists():
            return False
        build(architecture)
        compose("stop", *SERVICES)
        migrate()
        compose("up", "-d", "--force-recreate", *SERVICES)
        stop_host()
        start_host()
        verify()
        return True


def wait_url(url, timeout=40):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=2) as response:
                if response.status == 200:
                    return
        except (urllib.error.URLError, TimeoutError):
            pass
        time.sleep(0.5)
    raise RuntimeError(f"Startup timed out: {url}; inspect {LOGS}")


def serve():
    children = []
    def terminate(*_):
        for child in children:
            if child.poll() is None:
                os.killpg(child.pid, signal.SIGTERM)
        for child in children:
            with contextlib.suppress(subprocess.TimeoutExpired):
                child.wait(timeout=8)
            if child.poll() is None:
                os.killpg(child.pid, signal.SIGKILL)
        raise SystemExit(0)
    signal.signal(signal.SIGTERM, terminate)
    signal.signal(signal.SIGINT, terminate)
    env = os.environ.copy()
    env["HELLO_DASHBOARD_CONFIG"] = str(CONFIG / "dashboard.json")
    env["HELLO_DASHBOARD_PROXY"] = "http://127.0.0.1:18090"
    for name, args, cwd in (("bff", [str(BIN / "dashboard")], ROOT),
                            ("frontend", ["pnpm", "dev", "--port", "5173", "--strictPort"], ROOT / "web/dashboard")):
        with (LOGS / f"{name}.log").open("ab") as log:
            children.append(subprocess.Popen(args, cwd=cwd, env=env, stdout=log, stderr=log, start_new_session=True))
    while all(child.poll() is None for child in children):
        time.sleep(1)
    terminate()


def start_host():
    for port in (5173, 18090):
        with socket.socket() as check:
            try:
                check.bind(("127.0.0.1", port))
            except OSError as error:
                raise RuntimeError(f"Port {port} is occupied; stop its owner or choose another environment") from error
    with (LOGS / "supervisor.log").open("ab") as log:
        process = subprocess.Popen([sys.executable, str(Path(__file__).resolve()), "_serve"], cwd=ROOT,
                                   stdout=log, stderr=log, start_new_session=True)
    (STATE / "supervisor.pid").write_text(str(process.pid))
    wait_url("http://127.0.0.1:18090/bff/session")
    wait_url("http://127.0.0.1:5173/login")


def session_client():
    cookies = http.cookiejar.CookieJar()
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(cookies))
    csrf = ""
    def request(path, method="GET", body=None, key=None):
        headers = {"Origin": "http://127.0.0.1:5173", "Content-Type": "application/json", "X-CSRF-Token": csrf}
        if key:
            headers["Idempotency-Key"] = key
        payload = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request("http://127.0.0.1:18090/bff/" + path, data=payload, method=method, headers=headers)
        try:
            with client.open(req, timeout=15) as response:
                return json.load(response) if response.status != 204 else None
        except urllib.error.HTTPError as error:
            raise RuntimeError(f"Local API verification failed: {method} {path}: HTTP {error.code}") from error
    csrf = request("session")["csrfToken"]
    settings = json.loads((CONFIG / "settings.json").read_text())
    csrf = request("login", "POST", {"email": settings["email"], "password": settings["password"]})["csrfToken"]
    return request


def verify(send=False):
    validate_compose()
    for service in [*SERVICES, "postgres", "kafka", "provider-fixture"]:
        if not compose("ps", "--status", "running", "-q", service, capture=True).strip():
            raise RuntimeError(f"Local service is not running: {service}; run ./dev logs")
    request = session_client()
    app = json.loads((CONFIG / "generated/application.json").read_text())["id"]
    apps = request("api/apps")["data"]["items"]
    if not any(item["id"] == app for item in apps):
        raise RuntimeError("Local application grant is missing")
    users = request(f"api/apps/{app}/users?limit=20")["data"]["items"]
    if not users:
        raise RuntimeError("Local synthetic audience missing")
    if send:
        for scheduled in (False, True):
            title = "Local scheduled smoke" if scheduled else "Local immediate smoke"
            draft = request(f"api/apps/{app}/campaigns", "POST", {"title": title, "body": "Synthetic local provider only.",
                            "targets": [{"type": "INDIVIDUAL", "id": users[0]["id"]}], "template_id": None})["data"]
            base = f"api/apps/{app}/campaigns/{draft['id']}"
            if scheduled:
                at = (datetime.now(timezone.utc) + timedelta(seconds=5)).isoformat()
                request(base + "/schedule", "POST", {"scheduled_at": at})
            else:
                request(base + "/send", "POST", key="local-smoke:" + secrets.token_hex(12))
            deadline = time.monotonic() + 60
            while time.monotonic() < deadline:
                statistics = request(base + "/statistics")["data"]
                if statistics.get("snapshot_complete") and (statistics.get("counts") or {}).get("provider_accepted_devices") == 1:
                    print(f"{title}: campaign {draft['id']} accepted by simulator through the real pipeline.")
                    break
                time.sleep(1)
            else:
                raise RuntimeError(f"Campaign {draft['id']} did not reach simulated provider acceptance; inspect ./dev logs")
    request("logout", "POST", {})
    print("Local login, application grant, audience and process checks passed.")


def up():
    architecture = docker_ready()
    owner = STATE / "owner.json"
    if owner.exists():
        if json.loads(owner.read_text()) != {"repository": str(ROOT), "scope": "synthetic-local-v1"}:
            raise RuntimeError("Local state belongs to another checkout")
    elif compose("ps", "-aq", capture=True).strip():
        raise RuntimeError("Existing hello-local resources have no ownership marker; refusing reuse")
    else:
        private_json(owner, {"repository": str(ROOT), "scope": "synthetic-local-v1"})
    (STATE / "enabled").unlink(missing_ok=True)
    configure()
    validate_compose()
    stop_host()
    compose("stop", timeout=60)
    build(architecture)
    fresh_tls = STATE / "tls-new"
    if fresh_tls.exists():
        shutil.rmtree(fresh_tls)
    run(["go", "run", "./scripts/integration-certs.go", str(fresh_tls), "--local-dev"])
    shutil.rmtree(TLS)
    fresh_tls.replace(TLS)
    # The private parent is owner-only; protect the BFF client key explicitly too.
    for path in TLS.glob("*.key"):
        path.chmod(0o600)
    compose("up", "-d", "--wait", "--wait-timeout", "120", "postgres", "kafka", "provider-fixture", "worker-fixture")
    migrate()
    for topic in TOPICS:
        compose("exec", "-T", "kafka", "/opt/kafka/bin/kafka-topics.sh", "--bootstrap-server", "kafka:9094",
                "--create", "--if-not-exists", "--topic", topic, "--partitions", "1", "--replication-factor", "1")
    seed_application()
    if not (STATE / "offset-initialized").exists():
        compose("exec", "-T", "kafka", "/opt/kafka/bin/kafka-consumer-groups.sh", "--bootstrap-server", "kafka:9094",
                "--group", "hello-local-admission-v1", "--topic", TOPICS[0], "--reset-offsets", "--to-earliest", "--execute")
        (STATE / "offset-initialized").touch()
    compose("up", "-d", *SERVICES)
    run(["pnpm", "install", "--frozen-lockfile"], cwd=ROOT / "web/dashboard", timeout=240)
    start_host()
    verify()
    (STATE / "enabled").touch()
    print(f"Local dashboard: http://127.0.0.1:5173/login\nLocal credentials: {CONFIG / 'settings.json'}\nLogs: {LOGS}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["up", "down", "status", "logs", "watch", "check", "smoke", "test-seed", "credentials", "_serve"])
    args = parser.parse_args()
    if args.command == "credentials":
        require_owner()
        settings = json.loads((CONFIG / "settings.json").read_text())
        print(f"Email: {settings['email']}\nPassword: {settings['password']}")
        return
    toolchain()
    if args.command != "up":
        require_owner()
    if args.command == "_serve":
        serve()
    elif args.command == "up":
        up()
    elif args.command == "down":
        (STATE / "enabled").unlink(missing_ok=True)
        stop_host()
        compose("stop", timeout=60)
        print("Stopped local services. PostgreSQL/Kafka data retained.")
    elif args.command == "status":
        compose("ps", "-a")
        print(f"Frontend/BFF supervisor: {'running' if supervisor_pid() else 'stopped'}")
    elif args.command == "test-seed":
        test_seed_recovery()
    elif args.command in ("check", "smoke"):
        verify(send=args.command == "smoke")
    elif args.command == "logs":
        compose("logs", "--tail", "80", *SERVICES)
        print(f"Frontend/BFF logs: {LOGS}")
    else:
        architecture = docker_ready()
        watcher = (STATE / "watcher.lock").open("a")
        fcntl.flock(watcher, fcntl.LOCK_EX | fcntl.LOCK_NB)
        def snapshot():
            return {str(p): p.stat().st_mtime_ns for base in ("cmd", "internal", "pkg", "db/migrations") for p in (ROOT / base).rglob("*") if p.suffix in (".go", ".sql")}
        previous = snapshot()
        print("Watching Go/SQL changes. Frontend uses Vite HMR. Ctrl+C stops this watcher only.", flush=True)
        while True:
            time.sleep(1)
            current = snapshot()
            if current != previous:
                previous = current
                try:
                    if reload_backend(architecture):
                        print("Backend reloaded.", flush=True)
                except BlockingIOError:
                    print("Another local command is active; reload deferred.", flush=True)
                    previous = {}
                except (RuntimeError, subprocess.TimeoutExpired):
                    print("Reload failed; fix the error and save again. Local data is retained.", file=sys.stderr)


if __name__ == "__main__":
    try:
        STATE.mkdir(parents=True, exist_ok=True, mode=0o700)
        with (STATE / "command.lock").open("w") as lock:
            if any(command in sys.argv[1:] for command in ("up", "down", "smoke", "test-seed")):
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            main()
    except KeyboardInterrupt:
        pass
    except BlockingIOError:
        print("Another local mutation command is active; try again after it finishes.", file=sys.stderr)
        sys.exit(1)
    except (RuntimeError, subprocess.TimeoutExpired, FileNotFoundError) as error:
        print(f"Local startup failed: {error}", file=sys.stderr)
        sys.exit(1)
