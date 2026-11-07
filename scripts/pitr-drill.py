#!/usr/bin/env python3
"""Exercise PostgreSQL physical recovery with synthetic, network-isolated Docker resources."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
FIXTURE = ROOT / "scripts/pitr-drill"
IMAGE = "postgres:17@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d"
RUN = "hello-pitr-" + uuid.uuid4().hex[:14]
LABEL = "hello.pitr.drill=" + RUN
TARGET = "hello_fixture_checkpoint"
PGDATA = "/var/lib/postgresql/data/pgdata"
ARCHIVE_COMMAND = ("set -eu; if test -e /archive/%f; then cmp %p /archive/%f; "
                   "else cp %p /archive/.%f.tmp && ln /archive/.%f.tmp /archive/%f && rm /archive/.%f.tmp; fi")


def check(condition, message):
    if not condition:
        raise RuntimeError(message)


def docker(*args, stdin=None, timeout=75, required=True):
    result = subprocess.run(["docker", *args], input=stdin, text=True, capture_output=True, timeout=timeout)
    if required and result.returncode:
        raise RuntimeError(f"docker {args[0]} exited {result.returncode}: {result.stderr.strip()}")
    return result


def name(role):
    return RUN + "-" + role


def mount(volume, path, readonly=False):
    return ["--mount", f"type=volume,src={name(volume)},dst={path}" + (",readonly" if readonly else "")]


def helper(volumes, script, *args):
    opts = []
    for spec in volumes:
        opts.extend(mount(*spec))
    return docker("run", "--rm", "--pull", "never", "--network", "none", "--label", LABEL,
                  "--tmpfs", "/work:rw,size=256m", *opts, "--entrypoint", "bash", IMAGE,
                  "-euo", "pipefail", "-c", script, "fixture", *args).stdout.strip()


def sql(node, query, *variables, required=True):
    result = docker("exec", "-i", "--user", "postgres", name(node), "psql", "-XqAt",
                    "-v", "ON_ERROR_STOP=1", "-d", "postgres", *variables, stdin=query, required=required)
    return result.stdout.strip() if result.returncode == 0 else None


def wait_sql(node, query, expected):
    for _ in range(90):
        if sql(node, query, required=False) == expected:
            return
        time.sleep(0.25)
    raise RuntimeError(f"{node} did not reach expected recovery state: {expected}")


def start_db(role, volumes, init_script, *config):
    opts = []
    for spec in volumes:
        opts.extend(mount(*spec))
    docker("run", "-d", "--pull", "never", "--network", "none", "--label", LABEL,
           "--name", name(role), *mount(role, "/var/lib/postgresql/data"), *opts,
           "-e", "PGDATA=" + PGDATA, "-e", "POSTGRES_HOST_AUTH_METHOD=trust",
           "-e", "POSTGRES_INITDB_ARGS=--data-checksums --wal-segsize=1",
           "--entrypoint", "bash", IMAGE, "-ec", init_script + '; exec docker-entrypoint.sh "$@"',
           "fixture", "postgres", "-c", "listen_addresses=", *config)
    confinement = docker("inspect", "--format", "{{.HostConfig.NetworkMode}} {{len .HostConfig.PortBindings}}", name(role)).stdout.strip()
    check(confinement == "none 0", "database network or port isolation failed")


def start_target(role, archive):
    # An independent private data directory is copied from verified recovery inputs.
    helper([("opened", "/opened", True), (role, "/target")],
           "mkdir /target/pgdata; cp -a /opened/base/. /target/pgdata/; "
           "touch /target/pgdata/recovery.signal; chown -R postgres:postgres /target/pgdata; chmod 700 /target/pgdata")
    directory = "/archive" if archive == "gap" else "/archive/wal"
    start_db(role, [(archive, "/archive", True)], ":",
             "-c", "archive_mode=off", "-c", "restore_command=cp " + directory + "/%f %p",
             "-c", "recovery_target_name=" + TARGET, "-c", "recovery_target_timeline=current",
             "-c", "recovery_target_action=pause", "-c", "default_transaction_read_only=on")


def session_active(node, actor):
    query = (ROOT / "db/queries/staff_sessions.sql").read_text().split("-- name: StaffSessionActive :one\n")[1].split(";", 1)[0]
    query = query.replace("sqlc.arg(session_id)", ":'session_id'").replace("sqlc.arg(staff_id)", ":'staff_id'")
    return sql(node, query + ";", "-v", f"session_id=00000000-0000-4000-8000-{actor:012d}", "-v", f"staff_id={actor}")


def evidence(node):
    return sql(node, """SELECT concat_ws('|',r.origin_epoch,r.source_epoch,n.id,
        encode(sha256(convert_to(to_jsonb(o)::text,'UTF8')),'hex')) FROM notification_runtime r
        JOIN notifications n ON (n.origin_epoch,n.source_epoch)=(r.origin_epoch,r.source_epoch)
        JOIN notification_send_outbox o ON o.notification_id=n.id WHERE r.singleton AND n.id=902;""")


def persist(path, payload):
    data = json.dumps(payload, sort_keys=True).encode()
    with path.open("xb") as out:
        out.write(data)
    path.chmod(0o400)
    return hashlib.sha256(data).hexdigest()


def load_pinned(path, digest):
    data = path.read_bytes()
    check(hashlib.sha256(data).hexdigest() == digest, "independent recovery evidence changed")
    return json.loads(data)


def exercise(tmp):
    for v in ("source", "base", "wal", "key", "vault", "opened", "gap", "bad", "restored"):
        docker("volume", "create", "--label", LABEL, name(v))
    start_db("source", [("base", "/base"), ("wal", "/archive")], "chown postgres:postgres /base /archive",
             "-c", "archive_mode=on", "-c", "archive_command=" + ARCHIVE_COMMAND)
    for _ in range(100):
        if docker("exec", name("source"), "cat", "/proc/1/comm").stdout.strip() == "postgres":
            break
        time.sleep(0.2)
    else:
        raise RuntimeError("source initdb did not finish")
    wait_sql("source", "SELECT current_setting('archive_mode')='on' AND pg_postmaster_start_time() IS NOT NULL;", "t")
    migrations = sorted((ROOT / "db/migrations").glob("*.up.sql"))
    sql("source", "BEGIN;\n" + "\n".join(p.read_text() for p in migrations) + "\n" + (FIXTURE/"seed.sql").read_text() + "\nCOMMIT;")
    check(session_active("source", 901) == "t" and session_active("source", 902) == "t", "initial session canaries")
    begin = time.monotonic()
    docker("exec", "--user", "postgres", name("source"), "pg_basebackup", "--pgdata=/base", "--checkpoint=fast",
           "--wal-method=none", "--manifest-checksums=SHA256", "--no-password")
    base_seconds = time.monotonic() - begin
    manifest_hash = docker("exec", name("source"), "sha256sum", "/base/backup_manifest").stdout.split()[0]
    sql("source", "SELECT pg_switch_wal();")
    sql("source", "BEGIN;\n" + (FIXTURE/"target.sql").read_text() + "\nCOMMIT;")
    lsn = sql("source", f"SELECT pg_create_restore_point('{TARGET}');")
    target_time = time.monotonic()
    check(re.fullmatch(r"[A-F0-9]+/[A-F0-9]+", lsn), "invalid restore LSN")
    segment = sql("source", f"SELECT pg_walfile_name('{lsn}'::pg_lsn);")
    check(re.fullmatch(r"[A-F0-9]{24}", segment), "invalid WAL filename")
    system = sql("source", "SELECT system_identifier FROM pg_control_system();")
    checkpoint = {"point": TARGET, "lsn": lsn, "segment": segment, "system": system,
                  "timeline": sql("source", "SELECT timeline_id FROM pg_control_checkpoint();"),
                  "server": sql("source", "SHOW server_version;"), "schema": migrations[-1].name[:6],
                  "base_manifest_sha256": manifest_hash, "intent": evidence("source"),
                  "session_epoch": sql("source", "SELECT session_epoch FROM staff_session_control;")}
    check(bool(checkpoint["intent"]), "missing checkpoint business evidence")
    cp_file = tmp / "point.json"
    cp_hash = persist(cp_file, checkpoint)
    # The later session decision is recorded separately from the recovered business point.
    sql("source", "BEGIN;\n" + (FIXTURE/"after.sql").read_text() + "\nCOMMIT;")
    ledger = json.loads(sql("source", """SELECT json_build_object('actor',s.id,'active',s.is_active,'auth_epoch',s.auth_epoch,
        'session',f.id,'revoked_at',f.revoked_at,'reason',f.revocation_reason)::text FROM staff_users s
        JOIN staff_sessions f ON s.id=f.staff_user_id WHERE s.id=901;"""))
    check(ledger["active"] is False and ledger["reason"] == "logout" and ledger["auth_epoch"] == 2 and ledger["revoked_at"], "incomplete external revocation ledger")
    ledger_file = tmp / "revocations.json"
    ledger_hash = persist(ledger_file, ledger)
    check(session_active("source", 901) == "f" and session_active("source", 902) == "t", "source revocation was ineffective")
    sql("source", "SELECT pg_switch_wal();")
    for _ in range(100):
        if docker("exec", name("source"), "test", "-f", "/archive/" + segment, required=False).returncode == 0:
            break
        time.sleep(0.2)
    else:
        raise RuntimeError("restore-point WAL failed to archive")
    docker("stop", "-t", "20", name("source"))
    point_age = time.monotonic() - target_time
    # Capture one immutable input set. Neither restored DB receives the private key or any archive write access.
    vault_hash = helper([("base", "/base", True), ("wal", "/wal", True), ("vault", "/vault"), ("key", "/keys")],
           """umask 077
openssl req -x509 -newkey rsa:3072 -nodes -days 1 -subj /CN=synthetic-pitr -keyout /keys/key.pem -out /keys/cert.pem 2>/dev/null
pg_verifybackup -q --wal-directory=/wal /base
tar -czf /work/input.tgz -C / base wal
openssl cms -encrypt -binary -aes-256-gcm -outform DER -in /work/input.tgz -out /vault/input.cms /keys/cert.pem
sha256sum /vault/input.cms | cut -d' ' -f1""")
    check(re.fullmatch(r"[a-f0-9]{64}", vault_hash), "invalid vault digest")
    begin = time.monotonic()
    helper([("vault", "/vault", True), ("key", "/keys", True), ("opened", "/opened")],
           """umask 077
printf '%s  /vault/input.cms\n' "$1" | sha256sum --check --status
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out /work/wrong.pem 2>/dev/null
if openssl cms -decrypt -binary -inform DER -in /vault/input.cms -inkey /work/wrong.pem -out /work/wrong 2>/dev/null; then exit 9; fi
openssl cms -decrypt -binary -inform DER -in /vault/input.cms -recip /keys/cert.pem -inkey /keys/key.pem -out /work/verified.tgz
tar -xzf /work/verified.tgz -C /opened
printf '%s  /opened/base/backup_manifest\n' "$2" | sha256sum --check --status
pg_verifybackup -q --wal-directory=/opened/wal /opened/base""", vault_hash, manifest_hash)
    decrypted_seconds = time.monotonic() - begin
    # Physically elide the named WAL segment in a different volume.
    helper([("opened", "/opened", True), ("gap", "/gap")],
           'test -s "/opened/wal/$1"; for f in /opened/wal/*; do leaf=${f##*/}; test "$leaf" = "$1" || cp -p "$f" /gap/; done; test ! -e "/gap/$1"', segment)
    start_target("bad", "gap")
    exit_code = docker("wait", name("bad"), timeout=45).stdout.strip()
    bad_log = docker("logs", name("bad"))
    why = bad_log.stdout + bad_log.stderr
    check(exit_code != "0" and segment in why and "recovery ended before configured recovery target was reached" in why,
          "target with missing required WAL did not fail closed at the requested point: " + why[-3200:])
    check(sql("bad", "SELECT 1;", required=False) is None, "failed PITR still accepting SQL")
    recover_start = time.monotonic()
    start_target("restored", "opened")
    wait_sql("restored", "SELECT pg_is_in_recovery() AND pg_get_wal_replay_pause_state()='paused';", "t")
    point = load_pinned(cp_file, cp_hash)
    check(sql("restored", "SELECT system_identifier FROM pg_control_system();") == point["system"], "system ID mismatch")
    check(sql("restored", "SELECT timeline_id FROM pg_control_checkpoint();") == point["timeline"], "timeline mismatch")
    check(sql("restored", "SELECT pg_last_wal_replay_lsn();") == point["lsn"], "recovery paused at unexpected LSN")
    check(sql("restored", "SELECT string_agg(id::text,',' ORDER BY id) FROM notifications;") == "901,902", "before/at/after canary mismatch")
    check(evidence("restored") == point["intent"], "restored outbox or business lineage changed")
    check(sql("restored", "SELECT NOT sealed AND NOT migration_hold FROM notification_runtime;") == "t", "unexpected restored gate")
    check(sql("restored", "SHOW transaction_read_only;") == "on", "PITR did not pause read-only")
    check(session_active("restored", 901) == "t" and session_active("restored", 902) == "t", "revoked-family rewind was not reproduced")
    sql("restored", "SELECT pg_wal_replay_resume();")
    wait_sql("restored", "SELECT pg_is_in_recovery();", "f")
    rev = load_pinned(ledger_file, ledger_hash)
    sql("restored", """BEGIN READ WRITE;
UPDATE notification_runtime SET sealed=true,migration_hold=true WHERE singleton;
UPDATE staff_users SET is_active=false WHERE id=:'actor'::bigint;
UPDATE staff_sessions SET revoked_at=:'revoked_at'::timestamptz,revocation_reason=:'reason'
    WHERE id=:'session'::uuid AND staff_user_id=:'actor'::bigint;
UPDATE staff_session_control SET session_epoch=gen_random_uuid() WHERE singleton;
COMMIT;""", "-v", "actor="+str(rev["actor"]), "-v", "session="+rev["session"], "-v", "reason="+rev["reason"], "-v", "revoked_at="+rev["revoked_at"])
    check(session_active("restored", 901) == "f" and session_active("restored", 902) == "f", "old family can still authorize")
    check(sql("restored", """SELECT s.auth_epoch=:'auth_epoch'::bigint AND NOT s.is_active AND f.revoked_at=:'revoked_at'::timestamptz
        AND f.revocation_reason='logout' FROM staff_users s JOIN staff_sessions f ON s.id=f.staff_user_id WHERE s.id=901;""",
        "-v", "auth_epoch="+str(rev["auth_epoch"]), "-v", "revoked_at="+rev["revoked_at"]) == "t", "security ledger mismatch")
    check(sql("restored", "SELECT revoked_at IS NULL FROM staff_sessions WHERE staff_user_id=902;") == "t", "global lineage test was masked by logout")
    check(sql("restored", "SELECT session_epoch FROM staff_session_control;") != point["session_epoch"], "global lineage unchanged")
    check(sql("restored", "SELECT sealed AND migration_hold FROM notification_runtime;") == "t", "restored service unexpectedly enabled")
    check(sql("restored", "SELECT count(*) FROM notifications WHERE id=903;") == "0" and evidence("restored") == point["intent"], "checkpoint drift after promotion")
    print(f"PostgreSQL {point['server']}, migration {point['schema']}, system {system}, timeline {point['timeline']}, target {lsn}, WAL {segment}")
    print(f"base manifest sha256={manifest_hash} checkpoint sha256={cp_hash} encrypted input sha256={vault_hash}")
    print(f"Target age at source shutdown {point_age:.3f}s; one later synthetic campaign excluded")
    print(f"base backup {base_seconds:.3f}s; decrypt and verify {decrypted_seconds:.3f}s; restore through sealed security reconciliation {time.monotonic()-recover_start:.3f}s")
    print("Wrong key and missing WAL rejected. Scope, outbox, session logout, global lineage and offline seal verified.")


def cleanup():
    ids = docker("ps", "-aq", "--filter", "label=" + LABEL).stdout.split()
    if ids:
        docker("rm", "-fv", *ids)
    volumes = docker("volume", "ls", "-q", "--filter", "label=" + LABEL).stdout.split()
    if volumes:
        docker("volume", "rm", *volumes)


def interrupted(signum, frame):
    raise KeyboardInterrupt


if __name__ == "__main__":
    argparse.ArgumentParser(description=__doc__).parse_args()
    endpoint = os.getenv("DOCKER_HOST") or docker("context", "inspect", "--format", '{{(index .Endpoints "docker").Host}}').stdout.strip()
    check(endpoint.startswith("unix:///"), "this fixture requires a local Unix Docker endpoint")
    signal.signal(signal.SIGTERM, interrupted)
    started = time.monotonic()
    try:
        with tempfile.TemporaryDirectory(prefix=RUN + "-") as local:
            exercise(Path(local))
    finally:
        cleanup()
    print(f"Full isolated drill including cleanup {time.monotonic()-started:.3f}s")
