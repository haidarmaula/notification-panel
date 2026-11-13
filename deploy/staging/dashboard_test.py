"""First dashboard activation boundaries without VM access."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import ops_dashboard as dashboard

class DashboardTests(unittest.TestCase):
    def test_routes_preserve_docs_and_pin_separate_api_and_bff(self):
        original=b'''http {\n  server {\n    location /docs/ { return 200; }\n    location / {\n      proxy_pass https://127.0.0.1:8080;\n    }\n  }\n}\n'''
        updated=dashboard.routing(original).decode()
        self.assertIn('location /api/v1/ {',updated)
        self.assertIn('location /docs/ { return 200; }',updated)
        self.assertEqual(updated.count('location / {'),1)
        self.assertIn('proxy_pass https://127.0.0.1:18443;',updated)
        self.assertIn('proxy_ssl_verify on;',updated)
        self.assertIn('proxy_set_header X-Forwarded-For $remote_addr;',updated)
        with self.assertRaises(RuntimeError):dashboard.routing(updated.encode())
    def test_incomplete_activation_restores_gateway_and_prior_journal(self):
        calls=[]
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)
            (root/'dashboard-bootstrap.json').write_text('{"phase":"prepared"}')
            (root/'gateway-before-dashboard.conf').write_bytes(b'prior gateway')
            (root/'deployment-before-dashboard.json').write_bytes(b'{"phase":"committed","release":"prior"}')
            (root/'dashboard-enabled.json').write_text('{}')
            from types import SimpleNamespace
            with patch.multiple(dashboard,ROOT=root,write_config=lambda data:calls.append(data),gateway_reload=lambda:None,
                container_exists=lambda _:True,run=lambda args,*_:calls.append(args)):
                with patch.object(dashboard.subprocess,'run',return_value=SimpleNamespace(returncode=0)):
                    dashboard.recover()
            self.assertIn(b'prior gateway',calls)
            self.assertIn(['docker','rm','-f','hello-staging-dashboard'],calls)
            self.assertEqual(json.loads((root/'deployment.json').read_text())['release'],'prior')
            self.assertFalse((root/'dashboard-enabled.json').exists())
            self.assertFalse((root/'dashboard-bootstrap.json').exists())
    def test_provisioned_material_has_dedicated_transport_identities(self):
        import subprocess
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);key=root/'key.pem';cert=root/'ca.pem'
            subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-subj','/CN=fixture-ca',
                '-addext','basicConstraints=critical,CA:TRUE','-keyout',str(key),'-out',str(cert)],capture_output=True,check=True)
            def source(name):return key.read_bytes() if name.endswith('.key') else cert.read_bytes()
            with patch.multiple(dashboard,ROOT=root,host_tls=source):
                raw,assets=dashboard.make_material({'api_key':'synthetic-key','monitoring':{'listen':'127.0.0.1:19101'}})
            policy=json.loads(raw)
            self.assertEqual(policy['api_key'],'synthetic-key')
            self.assertEqual(policy['monitoring']['listen'],'127.0.0.1:19105')
            self.assertEqual(set(assets),set(dashboard.ASSETS))
            self.assertNotEqual(policy['ingress']['endpoint']['client_ca_pem'],policy['upstream_ca_pem'])
            self.assertNotEqual(assets['dashboard-client.key'].decode(),policy['upstream_client_key_pem'])

    def test_inventory_refresh_updates_existing_import_references(self):
        import ops_common as common
        original=dict(common.ROLES);order=list(common.ORDER);reference=common.ROLES
        try:
            with tempfile.TemporaryDirectory() as directory,patch.object(common,'ROOT',Path(directory)):
                common.refresh_inventory();self.assertNotIn('dashboard',reference)
                (Path(directory)/'dashboard-enabled.json').write_text('{}')
                common.refresh_inventory();self.assertIn('dashboard',reference)
                self.assertEqual(common.ORDER[0],'dashboard')
                common.refresh_inventory();self.assertEqual(common.ORDER.count('dashboard'),1)
        finally:
            common.ROLES.clear();common.ROLES.update(original);common.ORDER[:]=order
    def test_bootstrap_baseline_is_not_an_incomplete_release(self):
        import ops_release as release
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);(root/'deployment.json').write_text('{"phase":"baseline"}')
            with patch.object(release,'ROOT',root),self.assertRaisesRegex(RuntimeError,'No incomplete deployment'):
                release.recover()

    def test_policy_archive_never_exposes_runtime_writable_secrets(self):
        import io,tarfile
        with tarfile.open(fileobj=io.BytesIO(dashboard.archive({'dashboard.json':b'{}'}))) as archive:
            entry=archive.getmembers()[0]
            self.assertEqual((entry.uid,entry.gid,entry.mode),(0,65532,0o640))
