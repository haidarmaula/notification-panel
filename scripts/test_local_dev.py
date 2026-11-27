"""Local development orchestration safeguards; no Docker or real credentials."""

import importlib.util
import json
from pathlib import Path
import socket
import tempfile
import unittest
from unittest.mock import call, patch
from zipfile import ZipFile

spec = importlib.util.spec_from_file_location("local_dev", Path(__file__).parent / "local-dev/dev.py")
dev = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dev)
apk_spec = importlib.util.spec_from_file_location("build_real_fcm_apk", Path(__file__).parent / "local-dev/build_real_fcm_apk.py")
apk_build = importlib.util.module_from_spec(apk_spec)
apk_spec.loader.exec_module(apk_build)


def configuration():
    services = {name: {"networks": {"delivery": {}}} for name in [
        "postgres", "kafka", "provider-fixture", "delivery-worker", "server", "controlplane", "verifier-pool",
        "controlplane-slot-1", "controlplane-slot-2", "controlplane-slot-3",
    ]}
    services["api-tls"] = {"networks": {"delivery": {}, "ingress": {}}, "ports": [{"host_ip": "127.0.0.1"}]}
    services["provisioner"] = {"network_mode": "service:server"}
    services["verifier-pool"]["volumes"] = [
        {"target": "/fixture-bin", "read_only": True},
        {"target": "/local-config/generated", "read_only": False},
        {"target": "/local-tls/postgres-ca.crt", "read_only": True},
    ]
    for slot in range(1, 4):
        services[f"controlplane-slot-{slot}"]["volumes"] = [
            {"target": "/fixture-bin", "read_only": True},
            {"target": "/policy-input", "read_only": True},
            {"target": "/local-tls/postgres-ca.crt", "read_only": True},
            {"target": "/var/lib/hello/credentials", "read_only": True},
        ]
    return {"services": services, "networks": {"delivery": {"internal": True}}}


class LocalDevelopmentTests(unittest.TestCase):
    def test_standalone_apk_rejects_expo_launcher_even_with_embedded_bundle(self):
        with tempfile.TemporaryDirectory() as directory:
            artifact = Path(directory) / "hello-fcm-smoke-lan.apk"
            with ZipFile(artifact, "w") as apk:
                apk.writestr("assets/index.android.bundle", b"bundle")
                apk.writestr("classes.dex", b"Lcom/example/hellofcmsmoke/MainActivity;")
            apk_build.check_standalone_apk(artifact)
            with ZipFile(artifact, "a") as apk:
                apk.writestr("classes2.dex", b"Lexpo/modules/devlauncher/DevLauncherController;")
            with self.assertRaisesRegex(RuntimeError, "Expo development launcher"):
                apk_build.check_standalone_apk(artifact)

    def test_public_apk_must_not_embed_any_custom_ca(self):
        with tempfile.TemporaryDirectory() as directory:
            artifact = Path(directory) / "hello-fcm-smoke-public.apk"
            with ZipFile(artifact, "w") as apk:
                apk.writestr("assets/index.android.bundle", b"bundle")
                apk.writestr("classes.dex", b"Lcom/example/hellofcmsmoke/MainActivity;")
            apk_build.check_standalone_apk(artifact, public=True)
            self.assertEqual(apk_build.public_cache_key(), "standalone-public-v1")
            with ZipFile(artifact, "a") as apk:
                apk.writestr("res/raw/hello_lan_ca.crt", b"certificate")
            with self.assertRaisesRegex(RuntimeError, "must not embed any custom CA"):
                apk_build.check_standalone_apk(artifact, public=True)
            apk_build.check_standalone_apk(artifact)

    def test_public_build_overrides_expo_debug_cleartext_manifests(self):
        with tempfile.TemporaryDirectory() as directory:
            project = Path(directory)
            for variant in ("debug", "debugOptimized"):
                path = project / "android/app/src" / variant / "AndroidManifest.xml"
                path.parent.mkdir(parents=True)
                path.write_text('<application android:usesCleartextTraffic="true" tools:replace="android:usesCleartextTraffic" />')
            apk_build.disable_debug_cleartext(project)
            for variant in ("debug", "debugOptimized"):
                text = (project / "android/app/src" / variant / "AndroidManifest.xml").read_text()
                self.assertIn('usesCleartextTraffic="false"', text)
                self.assertIn('tools:replace="android:usesCleartextTraffic"', text)
            (project / "android/app/src/debug/AndroidManifest.xml").unlink()
            with self.assertRaisesRegex(RuntimeError, "debug Android manifest"):
                apk_build.disable_debug_cleartext(project)

    def test_real_fcm_credentials_require_explicit_project_and_private_regular_file(self):
        with tempfile.TemporaryDirectory() as directory:
            credential = Path(directory) / "account.json"
            credential.write_text(json.dumps({
                "type": "service_account", "project_id": "android-pilot",
                "client_email": "sender@android-pilot.iam.gserviceaccount.com",
            }))
            credential.chmod(0o600)
            self.assertEqual(dev.real_fcm_credential_identity(credential, "android-pilot"),
                             (credential.resolve(), "android-pilot", "sender@android-pilot.iam.gserviceaccount.com"))
            with self.assertRaisesRegex(RuntimeError, "explicitly approved"):
                dev.real_fcm_credential_identity(credential, "other-project")
            credential.chmod(0o640)
            with self.assertRaisesRegex(RuntimeError, "owner-only"):
                dev.real_fcm_credential_identity(credential, "android-pilot")

    def test_real_fcm_credentials_reject_duplicate_identity_fields_and_symlinks(self):
        with tempfile.TemporaryDirectory() as directory:
            credential = Path(directory) / "account.json"
            credential.write_text('{"type":"service_account","project_id":"android-pilot",'
                                  '"project_id":"other","client_email":"sender@android-pilot.iam.gserviceaccount.com"}')
            credential.chmod(0o600)
            with self.assertRaisesRegex(RuntimeError, "unavailable or invalid"):
                dev.real_fcm_credential_identity(credential, "android-pilot")
            alias = Path(directory) / "alias.json"
            alias.symlink_to(credential)
            with self.assertRaisesRegex(RuntimeError, "owner-only"):
                dev.real_fcm_credential_identity(alias, "android-pilot")

    def test_real_fcm_profile_uses_independent_state_and_compose_project(self):
        default_state = dev.STATE
        default_project = dev.PROJECT
        dev.select_profile(True)
        self.assertEqual(dev.STATE, dev.ROOT / "tmp/local-fcm")
        self.assertEqual(dev.PROJECT, "hello-local-real-fcm")
        dev.select_profile(False)
        self.assertEqual(dev.STATE, default_state)
        self.assertEqual(dev.PROJECT, default_project)

    def test_docs_build_uses_local_release_and_replaces_previous_output(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            with patch.object(dev, "STATE", state):
                dev.build_docs()
                output = state / "api-docs"
                self.assertIn('/docs/releases/local', (output / "index.html").read_text())
                self.assertIn('Local API reference', (output / "index.html").read_text())
                self.assertIn('isolated provider simulator', (output / "index.html").read_text())
                self.assertNotIn('Staging API reference', (output / "index.html").read_text())
                catalog = json.loads((output / "catalog.json").read_text())
                self.assertEqual(len(catalog["guides"]), 3)
                (output / "obsolete.txt").write_text("old")
                dev.build_docs()
                self.assertFalse((output / "obsolete.txt").exists())

    def validate(self, value):
        with patch.object(dev, "compose", return_value=json.dumps(value)):
            dev.validate_compose()

    def test_only_tls_gateway_can_publish_loopback_port(self):
        self.validate(configuration())
        for service in ("postgres", "provider-fixture", "delivery-worker", "controlplane", "verifier-pool",
                        "controlplane-slot-1", "controlplane-slot-2", "controlplane-slot-3"):
            value = configuration()
            value["services"][service]["ports"] = [{"host_ip": "127.0.0.1"}]
            with self.assertRaises(RuntimeError):
                self.validate(value)
        value = configuration()
        value["services"]["api-tls"]["ports"][0]["host_ip"] = "0.0.0.0"
        with self.assertRaises(RuntimeError):
            self.validate(value)

    def test_provider_and_delivery_cannot_join_external_network(self):
        for service in ("provider-fixture", "delivery-worker", "postgres", "kafka", "controlplane", "verifier-pool",
                        "controlplane-slot-1", "controlplane-slot-2", "controlplane-slot-3"):
            value = configuration()
            value["services"][service]["networks"]["ingress"] = {}
            with self.assertRaises(RuntimeError):
                self.validate(value)
        value = configuration()
        value["networks"]["delivery"]["internal"] = False
        with self.assertRaises(RuntimeError):
            self.validate(value)

    def test_verifier_pool_and_scoped_workers_have_minimal_mounts(self):
        self.validate(configuration())
        value = configuration()
        value["services"]["verifier-pool"]["volumes"].append(
            {"target": "/local-config/settings.json", "read_only": True})
        with self.assertRaises(RuntimeError):
            self.validate(value)
        value = configuration()
        value["services"]["controlplane-slot-1"]["volumes"][3]["read_only"] = False
        with self.assertRaises(RuntimeError):
            self.validate(value)

    def test_up_cleanup_removes_only_regenerable_slot_policies(self):
        with tempfile.TemporaryDirectory() as directory:
            generated = Path(directory) / "generated"
            generated.mkdir()
            for name in ("controlplane-slot-1.json", "controlplane-slot-2.json", "controlplane-slot-3.json",
                         "application.json", "onboarding.json"):
                (generated / name).write_text("synthetic")
            with patch.object(dev, "CONFIG", Path(directory)):
                dev.clear_verifier_slot_policies()
            self.assertEqual(sorted(path.name for path in generated.iterdir()), ["application.json", "onboarding.json"])

    def test_provisioner_is_private_to_server_namespace(self):
        for change in (
            {"network_mode": "host"},
            {"network_mode": "service:server", "networks": {"delivery": {}}},
            {"network_mode": "service:server", "ports": [{"host_ip": "127.0.0.1"}]},
        ):
            value = configuration()
            value["services"]["provisioner"].update(change)
            with self.assertRaises(RuntimeError):
                self.validate(value)

    def test_onboarding_policies_use_synthetic_scoped_credentials(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            config, tls = state / "config", state / "tls"
            config.mkdir()
            tls.mkdir()
            for name, value in {
                "ca.crt": "fixture-ca", "provisioner.crt": "fixture-provisioner-cert",
                "provisioner.key": "fixture-provisioner-key", "onboarding-client.crt": "fixture-client-cert",
                "onboarding-client.key": "fixture-client-key",
            }.items():
                (tls / name).write_text(value)
            with patch.object(dev, "CONFIG", config), patch.object(dev, "TLS", tls):
                dev.configure_onboarding()
            server = json.loads((config / "onboarding-policy.json").read_text())
            provisioner = json.loads((config / "provisioner-policy.json").read_text())
            controlplane = json.loads((config / "controlplane-policy.json").read_text())
            metadata = json.loads((config / "generated/onboarding.json").read_text())
            self.assertEqual(server["provisioner_url"], "https://localhost:8443")
            self.assertEqual(server["client_certificate_pem"], "fixture-client-cert")
            self.assertEqual(provisioner["listen_address"], "127.0.0.1:8443")
            self.assertEqual(provisioner["ingress_identity"], "spiffe://hello/local/server")
            self.assertEqual(provisioner["credential_root"], dev.CREDENTIAL_ROOT)
            self.assertEqual(controlplane["application_id"], dev.ONBOARDING_APP_ID)
            self.assertEqual(controlplane["environment"], "staging")
            self.assertEqual(controlplane["credential_root"], f"{dev.CREDENTIAL_ROOT}/{dev.ONBOARDING_APP_ID}/staging")
            self.assertEqual(metadata, {
                "id": dev.ONBOARDING_APP_ID,
                "profile_id": dev.ONBOARDING_PROFILE_ID,
                "application_identifier": dev.ONBOARDING_IDENTIFIER,
            })
            for path in (config / "onboarding-policy.json", config / "provisioner-policy.json",
                         config / "controlplane-policy.json", config / "onboarding-fingerprint-key"):
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            first_key = (config / "onboarding-fingerprint-key").read_bytes()
            with patch.object(dev, "CONFIG", config), patch.object(dev, "TLS", tls):
                dev.configure_onboarding()
            self.assertEqual((config / "onboarding-fingerprint-key").read_bytes(), first_key)

    def test_local_oauth_key_pair_survives_tls_rotation(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            previous, next_tls = root / "previous", root / "next"
            previous.mkdir()
            next_tls.mkdir()
            account = previous / "service-account.json"
            public_key = previous / "oauth-public.pem"
            account.write_text("synthetic account")
            public_key.write_text("synthetic public key")
            dev.preserve_synthetic_account(previous, next_tls)
            self.assertEqual((next_tls / account.name).read_text(), "synthetic account")
            self.assertEqual((next_tls / public_key.name).read_text(), "synthetic public key")
            public_key.unlink()
            with self.assertRaises(RuntimeError):
                dev.preserve_synthetic_account(previous, next_tls)

    def test_applied_migrations_are_not_replayed_and_changes_fail_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            migrations = root / "db/migrations"
            migrations.mkdir(parents=True)
            path = migrations / "000001_fixture.up.sql"
            path.write_text("SELECT 1;")
            calls = []
            checksum = dev.hashlib.sha256(path.read_bytes()).hexdigest()
            def sql(statement):
                calls.append(statement)
                return checksum if statement.startswith("SELECT checksum") else ""
            with patch.object(dev, "ROOT", root), patch.object(dev, "sql", side_effect=sql):
                dev.migrate()
                self.assertFalse(any("BEGIN;" in call for call in calls))
                path.write_text("SELECT 2;")
                with self.assertRaises(RuntimeError):
                    dev.migrate()

    def test_private_configuration_is_not_world_readable(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "settings.json"
            dev.private_json(path, {"password": "synthetic"})
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertEqual(json.loads(path.read_text()), {"password": "synthetic"})

    def test_configure_preserves_existing_mobile_setting_without_exporting_it(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            config = state / "config"
            settings = config / "settings.json"
            settings.parent.mkdir()
            values = {
                "email": "developer@hello.local", "password": "password",
                "api_key": "api-key", "access": "access", "refresh": "refresh",
                "mobile": "existing-mobile-secret",
            }
            settings.write_text(json.dumps(values))
            with patch.object(dev, "STATE", state), patch.object(dev, "CONFIG", config), \
                    patch.object(dev, "TLS", state / "tls"), patch.object(dev, "BIN", state / "bin"), \
                    patch.object(dev, "LOGS", state / "logs"):
                dev.configure()
            self.assertEqual(json.loads(settings.read_text()), values)
            server_env = (config / "server.env").read_text()
            for obsolete in ("MOBILE_JWT_SECRET", "KAFKA_BROKER", "KAFKA_SEND_TOPIC"):
                self.assertNotIn(obsolete, server_env)

    def test_new_settings_do_not_generate_mobile_secret(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            config = state / "config"
            with patch.object(dev, "STATE", state), patch.object(dev, "CONFIG", config), \
                    patch.object(dev, "TLS", state / "tls"), patch.object(dev, "BIN", state / "bin"), \
                    patch.object(dev, "LOGS", state / "logs"):
                dev.configure()
            self.assertNotIn("mobile", json.loads((config / "settings.json").read_text()))

    def test_port_check_reuses_closed_connections_and_checks_for_a_listener(self):
        with patch.object(dev.socket, "socket") as factory:
            probe = factory.return_value.__enter__.return_value
            dev.check_host_port(5173)
            self.assertEqual(probe.method_calls, [
                call.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1),
                call.bind(("127.0.0.1", 5173)),
                call.listen(),
            ])

    def test_stale_supervisor_pid_never_terminates_unrelated_process(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            (state / "supervisor.pid").write_text("1234")
            with patch.object(dev, "STATE", state), patch.object(dev.subprocess, "check_output", return_value="unrelated-worker"), patch.object(dev.os, "kill") as kill:
                dev.stop_host()
                kill.assert_not_called()

    def test_stale_real_fcm_supervisor_pid_never_stops_the_simulator(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            (state / "supervisor.pid").write_text("1234")
            simulator = f"python3 {Path(dev.__file__).resolve()} _serve"
            with patch.object(dev, "STATE", state), patch.object(dev, "REAL_FCM", True), \
                    patch.object(dev.subprocess, "check_output", return_value=simulator), patch.object(dev.os, "kill") as kill:
                dev.stop_host()
                kill.assert_not_called()

    def test_real_fcm_release_rejection_does_not_start_daemons(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            (state / "enabled").touch()
            (state / "offset-initialized").touch()
            def compose(*args, **kwargs):
                if "real-fcm-release" in args:
                    raise RuntimeError("installation is outside the pilot cohort")
            with patch.object(dev, "STATE", state), patch.object(dev, "validate_real_fcm_compose"), \
                    patch.object(dev, "install_real_fcm_public_roots"), \
                    patch.object(dev, "current_real_fcm_target", return_value=None), patch.object(dev, "sql", return_value="t"), \
                    patch.object(dev, "ensure_real_fcm_idle"), patch.object(dev, "pause_real_fcm_workers"), \
                    patch.object(dev, "real_fcm_compose_identity", return_value=(state / "account.json", "test-firebase", "fixture@example.com")), \
                    patch.object(dev, "compose", side_effect=compose) as command:
                with self.assertRaisesRegex(RuntimeError, "outside the pilot cohort"):
                    dev.release_real_fcm_pilot("20000000-0000-4000-8000-000000000001")
                self.assertEqual([call.args[4] for call in command.call_args_list], ["real-fcm-policy", "real-fcm-release"])

    def test_watcher_cannot_resurrect_a_stopped_stack(self):
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(dev, "STATE", Path(directory)), patch.object(dev, "build") as build:
                self.assertFalse(dev.reload_backend("arm64"))
                build.assert_not_called()

    def test_watcher_reload_cannot_race_an_active_mutation(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            (state / "enabled").touch()
            with (state / "command.lock").open("a") as lock:
                dev.fcntl.flock(lock, dev.fcntl.LOCK_EX | dev.fcntl.LOCK_NB)
                with patch.object(dev, "STATE", state), patch.object(dev, "build") as build:
                    with self.assertRaises(BlockingIOError):
                        dev.reload_backend("arm64")
                    build.assert_not_called()


if __name__ == "__main__":
    unittest.main()
