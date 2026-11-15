#!/usr/bin/env python3
"""Exercise setup and OAuth through the local BFF with generated synthetic keys."""

import http.cookiejar
import json
from pathlib import Path
import sys
import time
import urllib.error
import urllib.request
import uuid


STATE = Path(__file__).resolve().parents[2] / "tmp/local-dev"
ORIGIN = "http://127.0.0.1:5173"
BFF = "http://127.0.0.1:18090/bff/"


class LocalOnboardingClient:
    def __init__(self):
        self.csrf = ""
        self.client = urllib.request.build_opener(
            urllib.request.ProxyHandler({}),
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
        )

    def request(self, path, method="GET", body=None, etag=None, key=None, expected=200):
        headers = {"Origin": ORIGIN, "Content-Type": "application/json", "X-CSRF-Token": self.csrf}
        if etag is not None:
            headers["If-Match"] = etag
        if key is not None:
            headers["Idempotency-Key"] = key
        payload = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(BFF + path, data=payload, method=method, headers=headers)
        try:
            response = self.client.open(request, timeout=15)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw = response.read((1 << 20) + 1)
            if len(raw) > 1 << 20 or response.status != expected:
                raise RuntimeError(f"Local onboarding check failed: {method} {path}: HTTP {response.status}")
            if b'"private_key"' in raw or b"BEGIN PRIVATE KEY" in raw:
                raise RuntimeError("Local onboarding response exposed credential material")
            data = json.loads(raw) if raw else None
            return data, response.headers

    def login(self, settings):
        data, _ = self.request("session")
        self.csrf = data["csrfToken"]
        data, _ = self.request("login", "POST", {"email": settings["email"], "password": settings["password"]})
        self.csrf = data["csrfToken"]


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def verify():
    config = STATE / "config"
    settings = json.loads((config / "settings.json").read_text())
    fixture = json.loads((config / "generated/onboarding.json").read_text())
    account = json.loads((STATE / "tls/service-account.json").read_text())
    require(account["project_id"] == "integration-fixture"
            and account["client_email"] == "fixture@integration-fixture.iam.gserviceaccount.com"
            and account["type"] == "service_account"
            and account["token_uri"] == "https://oauth2.googleapis.com/token",
            "Refusing a non-fixture Firebase identity")
    require(fixture["id"] == "10000000-0000-4000-8000-000000000002", "Unexpected local onboarding app")
    base = f"api/admin/apps/{fixture['id']}/onboarding"
    client = LocalOnboardingClient()
    client.login(settings)
    try:
        data, headers = client.request(base)
        require(headers.get("ETag"), "Readiness ETag is missing")
        require(data["data"]["application_status"] == "PROVISIONING", "Demo app must remain provisioning")
        profiles, _ = client.request(base + "/enrollment-profiles")
        require(any(p["id"] == fixture["profile_id"] for p in profiles["data"]["items"]), "Local enrollment profile is missing")
        enrollment = {"profile_id": fixture["profile_id"], "platforms": [
            {"platform": "ANDROID", "application_identifier": fixture["application_identifier"]}
        ]}
        _, headers = client.request(base + "/enrollment", "PUT", enrollment, headers["ETag"], str(uuid.uuid4()))
        upload = {"environment": "staging", "target_project_id": account["project_id"], "service_account": account}
        upload_key = str(uuid.uuid4())
        upload_etag = headers["ETag"]
        stored, headers = client.request(base + "/fcm", "PUT", upload, upload_etag, upload_key, expected=201)
        candidate = stored["data"]["id"]
        require(headers.get("Cache-Control") == "no-store", "Credential response must not be cached")
        replay, replay_headers = client.request(base + "/fcm", "PUT", upload, upload_etag, upload_key, expected=201)
        require(replay["data"]["id"] == candidate and replay_headers.get("Idempotency-Replayed") == "true",
                "Upload replay created a different credential version")
        verification = {"configuration_id": candidate}
        verify_key = str(uuid.uuid4())
        submitted, operation_headers = client.request(base + "/fcm/verify", "POST", verification, headers["ETag"], verify_key, expected=202)
        operation = submitted["data"]["id"]
        require(operation_headers.get("Location") == "/bff/" + base + "/operations/" + operation,
                "Verification polling location is missing or incorrectly scoped")
        deadline = time.monotonic() + 40
        while time.monotonic() < deadline:
            result, _ = client.request(base + "/operations/" + operation)
            state = result["data"]["state"]
            if state == "SUCCEEDED":
                break
            require(state in ("PENDING", "RUNNING"), "Synthetic OAuth verification did not succeed")
            time.sleep(0.5)
        else:
            raise RuntimeError("Synthetic OAuth verification timed out; inspect ./dev logs")
        final, _ = client.request(base)
        final = final["data"]
        require(final["application_status"] == "PROVISIONING" and final["dispatch_mode"] == "HELD"
                and final["current_configuration_id"] is None,
                "OAuth verification must not activate enrollment or release dispatch")
        require(any(c["code"] == "OAUTH" and c["outcome"] == "PASSED" for c in final["checks"]),
                "Readiness did not expose the successful OAuth check")
        client.request("api/admin/apps/10000000-0000-4000-8000-000000000001/onboarding/operations/" + operation, expected=404)
        print("Local onboarding: profile, credential upload/replay, scoped OAuth worker and polling passed.")
        print("Demo app remains PROVISIONING with dispatch HELD; no notification sent.")
    finally:
        client.request("logout", "POST", {})


if __name__ == "__main__":
    try:
        verify()
    except (OSError, ValueError, KeyError, RuntimeError) as error:
        # Upstream bodies, local settings and service-account data are never printed.
        message = str(error) if isinstance(error, RuntimeError) else "Local onboarding check unavailable; run ./dev up and inspect ./dev logs"
        print(message, file=sys.stderr)
        sys.exit(1)
