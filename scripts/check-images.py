#!/usr/bin/env python3
"""Build and exercise all six production entrypoints with disposable offline policies."""

import base64
import io
import json
import os
from pathlib import Path
import secrets
import subprocess
import tarfile
import tempfile


ROOT = Path(__file__).resolve().parents[1]
SEED = "postgres:17@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d"
ROLES = ("server", "bootstrap", "worker", "scheduler", "requeue", "retention")
OFFLINE = ["--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges"]


def docker(*args, **kwargs):
    return subprocess.run(["docker", *args], cwd=ROOT, check=True, **kwargs)


def fixture_key():
    return base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip("=")


def policies(directory):
    config = directory / "openssl.cnf"
    config.write_text("[req]\ndistinguished_name=dn\nx509_extensions=ca\n[dn]\n[ca]\nbasicConstraints=critical,CA:TRUE\nkeyUsage=critical,digitalSignature,keyCertSign\n")
    key, cert = directory / "key.pem", directory / "cert.pem"
    subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
                    "-subj", "/CN=offline-image-fixture", "-config", str(config), "-out", str(cert), "-keyout", str(key)],
                   check=True, capture_output=True)
    endpoint = {"listen": "127.0.0.1:8080", "client_ca_pem": cert.read_text(),
                "server_certificate_pem": cert.read_text(), "server_key_pem": key.read_text()}
    staff = {"issuer": "offline-fixture"}
    for purpose in ("access", "refresh"):
        staff[purpose] = {"audience": "fixture-" + purpose, "active_kid": purpose,
                         "keys": [{"kid": purpose, "key": fixture_key()}]}
    closed_db = "postgres://offline:fixture@127.0.0.1:5432/app?sslmode=verify-full&connect_timeout=1"
    return {"server.json": {"database_url": closed_db, "api_key": fixture_key(), "mobile_policy_file": "",
                            "staff_tokens": staff, "ingress": {"endpoint": endpoint, "gateway_ips": ["127.0.0.1"],
                            "ip_per_minute": 120, "auth_ip_per_minute": 8, "auth_total_per_minute": 60, "concurrent_requests": 4}},
            "bootstrap.json": {"database_url": closed_db, "admin_name": "Fixture", "admin_email": "fixture@example.invalid",
                               "admin_password": fixture_key()}}


def mount_policies(volume, bundle, mode):
    archive = io.BytesIO()
    with tarfile.open(fileobj=archive, mode="w") as tar:
        for name, policy in bundle.items():
            content = json.dumps(policy).encode()
            info = tarfile.TarInfo(name)
            info.gid, info.mode, info.size = 65532, mode, len(content)
            tar.addfile(info, io.BytesIO(content))
    docker("run", "--rm", "--network", "none", "-i", "-v", volume + ":/policy", "--entrypoint", "tar",
           SEED, "--numeric-owner", "-xpf", "-", "-C", "/policy", input=archive.getvalue())


def expect_denial(flags, image, reason):
    run = subprocess.run(["docker", "run", *flags, image], capture_output=True, timeout=30)
    if run.returncode != 1 or reason.encode() not in run.stderr:
        raise RuntimeError(f"{image}: expected exit 1 and {reason!r}; got {run.returncode} and {run.stderr[-500:]!r}")


def main():
    endpoint = os.environ.get("DOCKER_HOST") or docker("context", "inspect", "--format", '{{(index .Endpoints "docker").Host}}', capture_output=True, text=True).stdout.strip()
    if not endpoint.startswith("unix:///"):
        raise RuntimeError("the image fixture requires a local Docker Unix socket")
    images = {}
    for role in ROLES:
        tag = f"hello-{role}:local-verify"
        docker("build", "--target", role, "--tag", tag, ".")
        metadata = json.loads(docker("image", "inspect", tag, capture_output=True).stdout)[0]
        entrypoint = ["/" + role] + (["--deployment"] if role in ("server", "bootstrap") else [])
        if metadata["Config"]["User"] != "65532:65532" or metadata["Config"]["Entrypoint"] != entrypoint:
            raise RuntimeError(f"{role}: unexpected production identity or entrypoint")
        images[role] = metadata["Id"]
    volume = docker("volume", "create", "--label", "hello.fixture=image-smoke", capture_output=True, text=True).stdout.strip()
    try:
        flags = [*OFFLINE, "-v", volume + ":/etc/hello:ro"]
        with tempfile.TemporaryDirectory(prefix="hello-image-smoke-") as tmp:
            bundle = policies(Path(tmp))
            mount_policies(volume, bundle, 0o640)
            for role, image in images.items():
                if role in ("server", "bootstrap"):
                    expect_denial(flags, image, role + " database startup failed")
                else:
                    docker("run", *OFFLINE, image, "--help", timeout=30)
                print(f"{role} {image} passed offline startup", flush=True)
            mount_policies(volume, bundle, 0o660)
            for role in ("server", "bootstrap"):
                expect_denial(flags, images[role], "invalid " + role + " configuration")
    finally:
        docker("volume", "rm", volume)
    print("All six images passed. Group-writable server and bootstrap policies were rejected.")


if __name__ == "__main__":
    main()
