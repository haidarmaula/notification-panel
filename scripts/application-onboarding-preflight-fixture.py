#!/usr/bin/env python3
"""Render synthetic preflight inputs and Docker copy archives."""

import base64
import json
import pathlib
import sys
import tarfile
import uuid


def render(root, image, network, mode):
    ids = {key: str(uuid.uuid4()) for key in (
        "application_id", "configuration_id", "reservation_id", "log_source_id"
    )}
    (root / "ids").write_text("\n".join(ids.values()) + "\n")
    encode = lambda key: base64.b64encode(key).decode("ascii")
    scope = dict(application_id=ids["application_id"], environment="staging")
    # RFC 8032 test vector: the Go encoding concatenates the seed and public key.
    signing = bytes.fromhex(
        "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
        "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"
    )
    certs = root / "certs"
    account = json.loads((certs / "service-account.json").read_bytes())
    worker = dict(
        mode="delivery_only", log_source_id=ids["log_source_id"],
        database_url="postgres://fixture_worker:worker_only@postgres:5432/postgres"
                     "?sslmode=verify-full&sslrootcert=/etc/hello/postgres-ca.crt",
        cluster_identity="MkU3OEVBNTcwNTJENDM2Qk", topic_name="notification.delivery.updated",
        topic_incarnation="preflight-fixture", partition_count=1, retry_min_ms=100, retry_max_ms=1000,
        kafka=dict(brokers=["kafka:9096"], authentication="MTLS", sasl_username="", sasl_password="",
                   ca_pem=(certs / "ca.crt").read_text(),
                   client_certificate_pem=(certs / "client.crt").read_text(),
                   client_key_pem=(certs / "client.key").read_text()),
        delivery=dict(
            **scope, project_id=account["project_id"], credential_project_id=account["project_id"],
            service_account_email=account["client_email"], cross_project_approved=False,
            service_account_file="/etc/hello/fcm/service-account.json",
            routes=[dict(namespace="mobile", key="android", platform="ANDROID",
                         address_type="FCM_REGISTRATION_TOKEN")],
            lease_seconds=60, renew_seconds=15, request_timeout_seconds=10, retry_floor_seconds=10,
            max_attempts=3, receipt_key_id="receipt", observation_window_seconds=7200,
            replica_count=1, requests_per_second=1, project_requests_per_second=1, concurrency=1,
            breaker_threshold=5, breaker_cooldown_seconds=15, poll_interval_ms=250, page_size=20,
            database_pool=dict(max_connections_per_replica=2, deployment_connection_budget=2),
            current_key_version=1, fingerprint_key=encode(bytes([1]) * 32),
            encryption_keys=[dict(version=1, key=encode(bytes([2]) * 32))],
            signing_keys=[dict(id="receipt", key=encode(signing))],
        ),
    )
    policy = dict(version=1, environment="staging", revision=1, delivery_image=image,
                  network=network, max_applications=1, max_overlap=1, max_database_connections=4,
                  delivery=dict(memory_bytes=134217728, nano_cpus=500000000, pids_limit=64,
                                max_database_connections=2))
    policies = {"worker.json": worker, "policy.json": policy}
    if mode == "--preflight-services":
        send_source_id = str(uuid.uuid4())
        (root / "send-source-id").write_text(send_source_id + "\n")
        shared = dict(
            log_source_id=send_source_id, cluster_identity=worker["cluster_identity"],
            topic_name="notification.send.requested", topic_incarnation=worker["topic_incarnation"],
            partition_count=1, retry_min_ms=100, retry_max_ms=1000, kafka=worker["kafka"],
        )

        def database_url(role):
            return (f"postgres://fixture_{role}:{role}_only@postgres:5432/postgres"
                    "?sslmode=verify-full&sslrootcert=/etc/hello/postgres-ca.crt")

        policies["scheduler.json"] = dict(
            **shared, database_url=database_url("scheduler"), poll_interval_ms=250, page_size=20,
        )
        policies["admission.json"] = dict(
            **shared, database_url=database_url("admission"), mode="admission_only", group_id=network,
        )
    for name, value in policies.items():
        (root / name).write_text(json.dumps(value, separators=(",", ":")) + "\n")


def archive(root, entries):
    with tarfile.open(fileobj=sys.stdout.buffer, mode="w|") as tar:
        for entry in entries:
            source, target, mode, owner = entry.split(":")
            uid, gid = map(int, owner.split("."))
            if source == "-":
                info = tarfile.TarInfo(target)
                info.type = tarfile.DIRTYPE
                data = None
            else:
                data = (root / source).open("rb")
                info = tar.gettarinfo(fileobj=data, arcname=target)
            info.mode, info.uid, info.gid = int(mode, 8), uid, gid
            try:
                tar.addfile(info, data)
            finally:
                if data is not None:
                    data.close()


if __name__ == "__main__":
    action, directory, *args = sys.argv[1:]
    if action == "render":
        render(pathlib.Path(directory), *args)
    elif action == "archive":
        archive(pathlib.Path(directory), args)
    else:
        sys.exit("Unknown fixture action")
