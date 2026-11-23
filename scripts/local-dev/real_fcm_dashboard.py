"""Authenticated host-side operator controls for the isolated real-FCM stack."""

import contextlib
import fcntl
import hashlib
import hmac
import http.client
import http.server
import ipaddress
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid

PREFIX = "/__hello_dev"
PRIVATE_NETWORKS = tuple(ipaddress.ip_network(cidr) for cidr in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"))


class ActionError(Exception):
    def __init__(self, status, message):
        self.status, self.message = status, message


class IssuerConnection(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__("localhost", timeout=4)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(str(self.path))


def lan_host():
    configured = os.environ.get("HELLO_LOCAL_FCM_LAN_HOST")
    try:
        if configured:
            host = configured
        elif sys.platform == "darwin":
            host = subprocess.check_output(["ipconfig", "getifaddr", "en0"], text=True, timeout=3, stderr=subprocess.DEVNULL).strip()
        else:
            with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as probe:
                probe.connect(("192.0.2.1", 9))
                host = probe.getsockname()[0]
        address = ipaddress.IPv4Address(host)
    except (ValueError, OSError, subprocess.SubprocessError):
        return None
    return str(address) if any(address in net for net in PRIVATE_NETWORKS) else None


class Dashboard:
    def __init__(self, dev):
        self.dev = dev
        self.origin = dev.dashboard_origin()
        self.app = dev.REAL_FCM_APP_ID
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.actions = threading.Lock()
        self.issuer = self.apk_build = None
        self.issuer_host = None
        self.control_directory = tempfile.TemporaryDirectory(prefix="hello-fcm-", dir="/tmp")
        self.control_socket = Path(self.control_directory.name) / "lan.sock"
        self.ca = dev.CONFIG / "lan-tls/hello-lan-ca.crt"
        self.apk = dev.STATE / "hello-fcm-smoke-lan.apk"
        self.apk_ca = dev.STATE / "hello-fcm-smoke-lan.ca-sha256"

    @staticmethod
    def stop(process):
        if process is not None and process.poll() is None:
            with contextlib.suppress(ProcessLookupError):
                os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                with contextlib.suppress(ProcessLookupError):
                    os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=2)

    def close(self):
        self.stop(self.issuer)
        self.stop(self.apk_build)
        self.control_directory.cleanup()

    def ensure_lan(self):
        host = lan_host()
        if not host:
            self.stop(self.issuer)
            return None, "Connect the Mac to a private Wi-Fi network or set HELLO_LOCAL_FCM_LAN_HOST to its Wi-Fi IPv4 address."
        if self.issuer is None or self.issuer.poll() is not None or self.issuer_host != host:
            self.stop(self.issuer)
            self.control_socket.unlink(missing_ok=True)
            dev = self.dev
            with (dev.LOGS / "real-fcm-lan.log").open("ab") as log:
                self.issuer = subprocess.Popen([
                    str(dev.BIN / "mobile-issuer"), "serve", "--config", str(dev.CONFIG / "mobile-issuer/issuer.json"),
                    "--lan", f"{host}:{dev.REAL_FCM_LAN_PORT}", "--lan-tls-dir", str(self.ca.parent),
                    "--qr-api", f"http://127.0.0.1:{dev.REAL_FCM_MOBILE_PORT}", "--control-socket", str(self.control_socket),
                ], stdout=log, stderr=log, start_new_session=True)
            self.issuer_host = host
            for _ in range(30):
                if self.control_socket.is_socket() or self.issuer.poll() is not None:
                    break
                time.sleep(0.05)
        if self.issuer.poll() is not None or not self.control_socket.is_socket():
            return None, "LAN pairing could not start. Close any manual pairing terminal on port 8443 and check the real-fcm-lan.log file."
        return f"https://{host}:{self.dev.REAL_FCM_LAN_PORT}", None

    def bff(self, headers, path, method="GET", payload=None, key=None):
        forwarded = {"Cookie": headers.get("Cookie", ""), "Origin": self.origin, "X-CSRF-Token": headers.get("X-CSRF-Token", ""),
                     "Content-Type": "application/json"}
        if key:
            forwarded["Idempotency-Key"] = key
        data = json.dumps(payload).encode() if payload is not None else None
        request = urllib.request.Request(self.dev.dashboard_proxy() + "/bff/" + path, data=data, method=method, headers=forwarded)
        try:
            with self.opener.open(request, timeout=12) as response:
                return json.load(response) if response.status != 204 else None
        except urllib.error.HTTPError as error:
            if error.code in (401, 403):
                raise ActionError(error.code, "Sign in to the real-FCM dashboard and try again.") from error
            raise ActionError(error.code, "The campaign API could not complete this request. Check the campaign before retrying.") from error
        except (OSError, ValueError) as error:
            raise ActionError(503, "The local dashboard API is unavailable. Check ./dev logs --real-fcm.") from error

    def authorize(self, headers, write):
        if headers.get("Host") != self.origin.removeprefix("http://") or headers.get("Sec-Fetch-Site") == "cross-site":
            raise ActionError(403, "Use the real-FCM dashboard on localhost.")
        origin = headers.get("Origin")
        if (origin and origin != self.origin) or (write and origin != self.origin):
            raise ActionError(403, "Use the real-FCM dashboard on localhost.")
        session = self.bff(headers, "session")
        if not session.get("authenticated"):
            raise ActionError(401, "Sign in to the real-FCM dashboard to manage test devices.")
        if write and not hmac.compare_digest(headers.get("X-CSRF-Token", "").encode(), session["csrfToken"].encode()):
            raise ActionError(403, "Reload the dashboard before retrying this action.")
        self.bff(headers, f"api/apps/{self.app}")

    def roster(self):
        return json.loads(self.dev.sql(f"""SELECT COALESCE(json_agg(json_build_object(
            'installation_id',i.public_id,'name','Android '||left(i.public_id::text,8),'platform',b.platform,
            'created_at',i.created_at,'last_seen_at',i.last_seen_at,
            'status',CASE WHEN p.dispatch_mode='PILOT' AND r.destination='FCM_ACTIVE'
                AND (r.cohort_id,r.origin_epoch,r.source_epoch)=(p.dispatch_cohort,p.dispatch_origin_epoch,p.dispatch_source_epoch)
                THEN 'selected' ELSE 'enrolled' END) ORDER BY i.last_seen_at DESC),'[]'::json)
            FROM applications a JOIN application_dispatch_policy p ON p.application_id=a.id
            JOIN installation_bindings b ON b.application_id=a.id AND b.retired_at IS NULL
            JOIN mobile_installations i ON (i.application_id,i.id)=(b.application_id,b.installation_id)
            LEFT JOIN current_binding_routes r ON (r.application_id,r.binding_id)=(b.application_id,b.id)
            WHERE a.public_id='{self.app}'::uuid AND b.platform='ANDROID' AND b.address_type='FCM_FID'
              AND b.provider_namespace='hello-local-fcm' AND b.provider_route_key='android-fid';"""))

    def last_test(self):
        marker = self.dev.STATE / "last-dashboard-test.json"
        if not marker.exists():
            return None
        campaign = int(json.loads(marker.read_text())["campaign_id"])
        row = self.dev.sql(f"""SELECT json_build_object('campaign_id',n.id::text,'status',n.status,
            'accepted_at',n.queued_at,'provider_accepted_at',d.provider_accepted_at,
            'device_observed_at',d.observed_delivered_at,'reason',COALESCE(d.terminal_reason,n.lifecycle_reason,n.preparation_hold_reason))
            FROM notifications n JOIN applications a ON a.id=n.application_id
            LEFT JOIN notification_delivery_intents d ON (d.application_id,d.notification_id)=(n.application_id,n.id)
            WHERE a.public_id='{self.app}'::uuid AND n.id={campaign} LIMIT 1;""")
        return json.loads(row) if row else None

    def apk_status(self):
        if self.apk_build is not None and self.apk_build.poll() is None:
            return "building"
        if self.apk.is_file() and self.apk_ca.is_file() and self.ca.is_file():
            if self.apk_ca.read_text().strip() == hashlib.sha256(self.ca.read_bytes()).hexdigest():
                return "ready"
        if self.apk_build is not None and self.apk_build.returncode:
            return "failed"
        return "missing"

    def status(self):
        origin, issue = self.ensure_lan()
        devices = self.roster()
        labels = self.dev.STATE / "test-device-names.json"
        if labels.is_file():
            names = json.loads(labels.read_text())
            for device in devices:
                device["name"] = names.get(device["installation_id"], device["name"])
        target = next((item for item in devices if item["status"] == "selected"), None)
        apk = self.apk_status()
        issues = [issue] if issue else []
        if apk == "failed":
            issues.append("APK build failed. Check the real-fcm-apk.log file and retry.")
        return {"ready": origin is not None, "origin": origin, "apk_url": PREFIX + "/apk" if apk == "ready" else None,
                "apk_status": apk, "devices": devices, "target": target, "last_test": self.last_test(), "issues": issues}

    def pair(self):
        origin, issue = self.ensure_lan()
        if not origin:
            raise ActionError(503, issue)
        connection = IssuerConnection(self.control_socket)
        try:
            connection.request("POST", "/pair", body=b"")
            response = connection.getresponse()
            if response.status != 200:
                raise ActionError(response.status, "Could not generate a fresh QR. Try again.")
            return json.load(response)["data"]
        finally:
            connection.close()

    def build_apk(self):
        if not self.ca.is_file():
            origin, issue = self.ensure_lan()
            if not origin:
                raise ActionError(503, issue)
        if self.apk_status() == "building":
            return {"status": "building"}
        dev = self.dev
        with (dev.LOGS / "real-fcm-apk.log").open("ab") as log:
            self.apk_build = subprocess.Popen([
                sys.executable, str(Path(__file__).with_name("build_real_fcm_apk.py")), str(self.ca), str(self.apk), str(self.apk_ca),
                str(dev.ROOT.parent / "hello-fcm-smoke"),
            ], stdout=log, stderr=log, start_new_session=True)
        return {"status": "building"}

    @contextlib.contextmanager
    def command_lock(self):
        with (self.dev.STATE / "command.lock").open("a") as lock:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError as error:
                raise ActionError(409, "Another local development command is running. Try again shortly.") from error
            yield

    def change_target(self, payload, forget=False):
        value = payload.get("installation_id")
        try:
            identifier = str(uuid.UUID(value))
        except (ValueError, AttributeError, TypeError) as error:
            raise ActionError(400, "Select an enrolled Android test device.") from error
        with self.command_lock():
            if forget:
                self.dev.forget_real_fcm_device(identifier)
            else:
                self.dev.release_real_fcm_pilot(identifier)
        return self.status()

    def name_device(self, payload):
        identifier = payload.get("installation_id")
        name = payload.get("name")
        if not isinstance(name, str) or not name.strip() or len(name.strip()) > 60 or not name.isprintable():
            raise ActionError(400, "Enter a device name between 1 and 60 characters.")
        if not any(item["installation_id"] == identifier for item in self.roster()):
            raise ActionError(404, "That Android device is no longer enrolled.")
        path = self.dev.STATE / "test-device-names.json"
        names = json.loads(path.read_text()) if path.is_file() else {}
        names[identifier] = name.strip()
        self.dev.private_json(path, names)
        return self.status()

    def send(self, headers, payload):
        title = payload.get("title", "Hello test push")
        body = payload.get("body", "Sent from your local development dashboard.")
        if not isinstance(title, str) or not isinstance(body, str) or not title.strip() or not body.strip() or len(title) > 120 or len(body) > 1000:
            raise ActionError(400, "Enter a test title and message within the length limits.")
        with self.command_lock():
            selected = [item["installation_id"] for item in self.roster() if item["status"] == "selected"]
            if not selected:
                raise ActionError(409, "Choose one device as the test target first.")
            if selected != [payload.get("installation_id")]:
                raise ActionError(409, "The test target changed. Check the device list and send again.")
            self.dev.ensure_real_fcm_idle()
            users = self.bff(headers, f"api/apps/{self.app}/users?limit=20")["data"]["items"]
            target = next((item for item in users if item.get("external_id") == "local-android-test"), None)
            if target is None:
                raise ActionError(409, "The local test user is missing. Restart the real-FCM profile.")
            path = f"api/apps/{self.app}/campaigns"
            draft = self.bff(headers, path, "POST", {"title": title.strip(), "body": body.strip(),
                "targets": [{"type": "INDIVIDUAL", "id": target["id"]}], "template_id": None})["data"]
            campaign = str(draft["id"])
            self.dev.private_json(self.dev.STATE / "last-dashboard-test.json", {"campaign_id": campaign})
            self.bff(headers, f"{path}/{campaign}/send", "POST", key=f"local-test-dashboard:{campaign}")
            return {"campaign_id": campaign, "status": "QUEUED"}


def serve(dev):
    dashboard = Dashboard(dev)

    class Handler(http.server.BaseHTTPRequestHandler):
        def setup(self):
            self.request.settimeout(15)
            super().setup()

        def log_message(self, *_):
            pass

        def respond(self, status, payload):
            body = json.dumps(payload).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Cache-Control", "no-store")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self):
            self.dispatch(False)

        def do_POST(self):
            self.dispatch(True)

        def dispatch(self, write):
            try:
                dashboard.authorize(self.headers, write)
                if write:
                    length = int(self.headers.get("Content-Length", "0"))
                    if not 0 <= length <= 4096:
                        raise ActionError(400, "Invalid request body.")
                    payload = json.loads(self.rfile.read(length)) if length else {}
                    if not isinstance(payload, dict):
                        raise ActionError(400, "Invalid request body.")
                if not write and self.path == PREFIX + "/apk":
                    with dashboard.actions:
                        if dashboard.apk_status() != "ready":
                            raise ActionError(404, "Build the Android test APK before downloading it.")
                        artifact = dashboard.apk.open("rb")
                    with artifact:
                        self.send_response(200)
                        self.send_header("Content-Type", "application/vnd.android.package-archive")
                        self.send_header("Content-Disposition", 'attachment; filename="hello-fcm-smoke-lan.apk"')
                        self.send_header("Content-Length", str(os.fstat(artifact.fileno()).st_size))
                        self.end_headers()
                        try:
                            shutil.copyfileobj(artifact, self.wfile)
                        except (OSError, TimeoutError):
                            pass
                    return
                with dashboard.actions:
                    if not write and self.path == PREFIX + "/status":
                        result = dashboard.status()
                    elif write and self.path == PREFIX + "/pair":
                        result = dashboard.pair()
                    elif write and self.path == PREFIX + "/build-apk":
                        result = dashboard.build_apk()
                    elif write and self.path in (PREFIX + "/target", PREFIX + "/forget"):
                        result = dashboard.change_target(payload, self.path.endswith("/forget"))
                    elif write and self.path == PREFIX + "/name":
                        result = dashboard.name_device(payload)
                    elif write and self.path == PREFIX + "/send":
                        result = dashboard.send(self.headers, payload)
                    else:
                        raise ActionError(404, "Local test device action not found.")
                self.respond(200, result)
            except ActionError as error:
                self.respond(error.status, {"message": error.message})
            except (ValueError, UnicodeError):
                self.respond(400, {"message": "Invalid request body."})
            except (OSError, RuntimeError, subprocess.SubprocessError, http.client.HTTPException) as error:
                print(f"Local test device action failed: {error}", file=sys.stderr, flush=True)
                message = str(error) if isinstance(error, dev.LocalFCMActionError) else "Local test device action failed. Check ./dev logs --real-fcm."
                self.respond(409, {"message": message})

    def finish(*_):
        raise SystemExit(0)

    signal.signal(signal.SIGTERM, finish)
    signal.signal(signal.SIGINT, finish)
    try:
        with http.server.ThreadingHTTPServer(("127.0.0.1", dev.REAL_FCM_CONTROL_PORT), Handler) as server:
            server.serve_forever()
    finally:
        dashboard.close()
