"""Public enrollment activation boundaries without VM access."""
import importlib.util
import json
from pathlib import Path
import unittest

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('enrollment', HERE / 'enable-public-enrollment.py')
enrollment = importlib.util.module_from_spec(spec)
spec.loader.exec_module(enrollment)


def policy(**overrides):
    project = {'app_id': '63677e77-2661-43b8-8253-35c1134bcc60', 'issuer': 'hello-local-mobile-test',
               'audience': 'hello-local-mobile-client', 'purpose': 'hello_mobile_installation',
               'namespace': 'hello-staging-mobile', 'verification_key': 'old-mobile',
               'origin_issuer': 'hello-local-origin-test', 'origin_audience': 'hello-local-origin-client',
               'origin_key': 'old-origin', 'routes': [{'platform': 'ANDROID'}],
               'receipts': {'environment': 'staging', 'verification_keys': [{'id': 'staging-receipt-v1'}]}}
    project.update(overrides)
    return {'current_key_version': 1, 'fingerprint_key': 'fp', 'encryption_keys': [{'version': 1, 'key': 'enc'}],
            'projects': [project]}


class PatchPolicyTests(unittest.TestCase):
    def test_rotation_replaces_only_verification_keys(self):
        old = json.dumps(policy()).encode()
        fresh = json.dumps(policy(verification_key='new-mobile', origin_key='new-origin')).encode()
        patched = json.loads(enrollment.patch_policy(old, fresh))
        project = patched['projects'][0]
        self.assertEqual(project['verification_key'], 'new-mobile')
        self.assertEqual(project['origin_key'], 'new-origin')
        self.assertEqual(project['receipts'], policy()['projects'][0]['receipts'])
        self.assertEqual(project['routes'], [{'platform': 'ANDROID'}])
        self.assertEqual(patched['encryption_keys'], [{'version': 1, 'key': 'enc'}])
        self.assertEqual(patched['fingerprint_key'], 'fp')

    def test_identity_scope_change_is_refused(self):
        old = json.dumps(policy()).encode()
        for field, value in [('namespace', 'other-namespace'), ('issuer', 'other-issuer'), ('app_id', 'other-app')]:
            fresh = json.dumps(policy(**{field: value})).encode()
            with self.assertRaisesRegex(RuntimeError, 'identity scope'):
                enrollment.patch_policy(old, fresh)

    def test_multiple_projects_are_refused(self):
        doubled = policy()
        doubled['projects'].append(dict(doubled['projects'][0]))
        with self.assertRaises(RuntimeError):
            enrollment.patch_policy(json.dumps(doubled).encode(), json.dumps(policy()).encode())


class PatchGatewayTests(unittest.TestCase):
    def base_config(self):
        spec = importlib.util.spec_from_file_location('configure_api', HERE / 'configure-api.py')
        configure = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(configure)
        return configure.gateway_config().encode()

    def test_routes_land_in_every_server_block(self):
        import re
        import ops_gateway_docs
        import ops_dashboard
        template = self.base_config().decode()
        live_like = re.sub(r'    location = /v1/(?:session|origin) \{[^}]*\}\n', '', template).encode()
        live = ops_dashboard.routing(ops_gateway_docs.routing(live_like))
        patched = enrollment.patch_gateway(live).decode()
        self.assertEqual(patched.count(enrollment.MARKER), 2)
        self.assertEqual(patched.count('location = /v1/session {'), 2)
        self.assertEqual(patched.count('location = /v1/origin {'), 2)
        self.assertEqual(patched.count('    location / {'), live.decode().count('    location / {'))
        self.assertIn('HELLO_DASHBOARD', patched)
        self.assertIn('HELLO_API_DOCS', patched)
        with self.assertRaisesRegex(RuntimeError, 'already present'):
            enrollment.patch_gateway(self.base_config())

    def test_legacy_single_server_is_supported(self):
        legacy = b'http {\n  server {\n    ssl_protocols TLSv1.2 TLSv1.3;\n    location / {\n      proxy_pass https://127.0.0.1:8080;\n    }\n  }\n}\n'
        patched = enrollment.patch_gateway(legacy).decode()
        self.assertEqual(patched.count(enrollment.MARKER), 1)

    def test_duplicate_or_foreign_layout_is_refused(self):
        legacy = b'http {\n  server {\n    ssl_protocols TLSv1.2 TLSv1.3;\n    location / {\n      proxy_pass https://127.0.0.1:8080;\n    }\n  }\n}\n'
        once = enrollment.patch_gateway(legacy)
        with self.assertRaisesRegex(RuntimeError, 'already present'):
            enrollment.patch_gateway(once)
        with self.assertRaisesRegex(RuntimeError, 'Unexpected gateway layout'):
            enrollment.patch_gateway(b'http {\n  server {\n    location / {}\n  }\n}\n')


if __name__ == '__main__':
    unittest.main()