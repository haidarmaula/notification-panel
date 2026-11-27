"""Gateway two-host split regressions; no VM access."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('configure_api', Path(__file__).parent / 'configure-api.py')
configure = importlib.util.module_from_spec(spec)
spec.loader.exec_module(configure)


class GatewayConfigTests(unittest.TestCase):
    def test_staff_stays_default_and_restricted_while_public_is_open(self):
        text = configure.gateway_config()
        staff, marker, public = text.partition(configure.PUBLIC_MARKER)
        self.assertTrue(marker)
        self.assertIn(configure.STAFF_MARKER, staff)
        self.assertIn('listen 10.1.16.109:8443 ssl default_server;', staff)
        self.assertIn('deny all;', staff)
        self.assertIn('allow 10.0.0.0/8;', staff)
        self.assertNotIn('deny all;', public)
        self.assertNotIn('allow 10.', public)
        self.assertIn(f'server_name {configure.PUBLIC_HOST};', public)
        self.assertIn('ssl_certificate /gateway/public.crt;', public)
        self.assertIn('ssl_certificate_key /gateway/public.key;', public)
        self.assertIn('ssl_certificate /gateway/gateway.crt;', staff)

    def test_both_hosts_keep_the_same_mutual_tls_upstream(self):
        text = configure.gateway_config()
        self.assertEqual(text.count('proxy_pass https://127.0.0.1:8080;'), 2)
        self.assertEqual(text.count('proxy_ssl_certificate /gateway/gateway-client.crt;'), 2)
        self.assertEqual(text.count('proxy_ssl_trusted_certificate /gateway/server-ca.crt;'), 2)
        self.assertEqual(text.count('proxy_ssl_verify on;'), 2)

    def test_public_host_is_configurable_for_the_infra_handover(self):
        text = configure.gateway_config('api.notifications.example.org')
        self.assertIn('server_name api.notifications.example.org;', text)
        self.assertNotIn(configure.PUBLIC_HOST, text)


class PublicPlaceholderTests(unittest.TestCase):
    def test_placeholder_is_generated_once_with_locked_custody(self):
        with tempfile.TemporaryDirectory() as directory:
            tls = Path(directory)
            chowns = []
            with patch.object(configure.os, 'chown', side_effect=lambda p, u, g: chowns.append((Path(p).name, u, g))):
                crt, key = configure.ensure_public_tls(tls)
                self.assertTrue(crt.is_file() and key.is_file())
                self.assertEqual(key.stat().st_mode & 0o777, 0o600)
                self.assertEqual(crt.stat().st_mode & 0o777, 0o640)
                self.assertIn(('public.key', 0, 0), chowns)
                self.assertIn(('public.crt', 0, 65532), chowns)
                before = crt.read_bytes(), key.read_bytes()
                configure.ensure_public_tls(tls)
                self.assertEqual(before, (crt.read_bytes(), key.read_bytes()))
            import subprocess
            text = subprocess.run(['openssl', 'x509', '-in', str(crt), '-noout', '-text'],
                                  capture_output=True, text=True, check=True).stdout
            self.assertIn('DNS:' + configure.PUBLIC_HOST, text)

    def test_partial_or_symlinked_pair_is_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            tls = Path(directory)
            (tls / 'public.crt').write_text('partial')
            with self.assertRaises(SystemExit):
                configure.ensure_public_tls(tls)
            (tls / 'public.key').write_text('partial')
            (tls / 'public.crt').unlink()
            (tls / 'public.crt').symlink_to(tls / 'public.key')
            with self.assertRaises(SystemExit):
                configure.ensure_public_tls(tls)


if __name__ == '__main__':
    unittest.main()