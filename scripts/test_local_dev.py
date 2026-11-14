"""Local development orchestration safeguards; no Docker or real credentials."""

import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("local_dev", Path(__file__).parent / "local-dev/dev.py")
dev = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dev)


def configuration():
    services = {name: {"networks": {"delivery": {}}} for name in ["postgres", "kafka", "provider-fixture", "delivery-worker"]}
    services["api-tls"] = {"networks": {"delivery": {}, "ingress": {}}, "ports": [{"host_ip": "127.0.0.1"}]}
    return {"services": services, "networks": {"delivery": {"internal": True}}}


class LocalDevelopmentTests(unittest.TestCase):
    def validate(self, value):
        with patch.object(dev, "compose", return_value=json.dumps(value)):
            dev.validate_compose()

    def test_only_tls_gateway_can_publish_loopback_port(self):
        self.validate(configuration())
        for service in ("postgres", "provider-fixture", "delivery-worker"):
            value = configuration()
            value["services"][service]["ports"] = [{"host_ip": "127.0.0.1"}]
            with self.assertRaises(RuntimeError):
                self.validate(value)
        value = configuration()
        value["services"]["api-tls"]["ports"][0]["host_ip"] = "0.0.0.0"
        with self.assertRaises(RuntimeError):
            self.validate(value)

    def test_provider_and_delivery_cannot_join_external_network(self):
        for service in ("provider-fixture", "delivery-worker", "postgres", "kafka"):
            value = configuration()
            value["services"][service]["networks"]["ingress"] = {}
            with self.assertRaises(RuntimeError):
                self.validate(value)
        value = configuration()
        value["networks"]["delivery"]["internal"] = False
        with self.assertRaises(RuntimeError):
            self.validate(value)

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

    def test_stale_supervisor_pid_never_terminates_unrelated_process(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            (state / "supervisor.pid").write_text("1234")
            with patch.object(dev, "STATE", state), patch.object(dev.subprocess, "check_output", return_value="unrelated-worker"), patch.object(dev.os, "kill") as kill:
                dev.stop_host()
                kill.assert_not_called()

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
