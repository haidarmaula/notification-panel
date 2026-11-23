import json
import unittest
from unittest.mock import patch

import ops_onboarding as onboarding


APP = '11111111-1111-4111-8111-111111111111'
PROJECT = 'approved-staging-project'


class AppApprovalTest(unittest.TestCase):
    def test_approval_applies_server_binding_before_relaxing_provisioner_project(self):
        previous = {'onboarding': {'profiles': [{'id': onboarding.PROFILE}]}}
        provider = {'environment': 'staging', 'target_project_id': onboarding.PROJECT}
        current = {'sealed': False, 'migration_hold': True, 'policies': [{'dispatch_mode': 'HELD'}]}
        app = {'key': 'approved-app', 'status': 'PROVISIONING', 'candidate': None, 'current': None}
        events = []

        def write_server(_, payload):
            events.append(('server', json.loads(payload)))

        def write_provisioner(files):
            events.append(('provisioner', json.loads(files['provisioner.json'][0])))

        with (patch.object(onboarding, 'release'), patch.object(onboarding, 'state', return_value=current),
              patch.object(onboarding, 'db', return_value=json.dumps(app)),
              patch.object(onboarding, 'read_container', side_effect=[json.dumps(previous), json.dumps(provider)]),
              patch.object(onboarding, 'containers', return_value=True), patch.object(onboarding, 'backup'),
              patch.object(onboarding, 'material'), patch.object(onboarding, 'run'),
              patch.object(onboarding, 'write_policy', side_effect=write_server),
              patch.object(onboarding, 'write_provisioner_files', side_effect=write_provisioner),
              patch.object(onboarding, 'check_provisioner'), patch.object(onboarding, 'check_server')):
            result = onboarding.approve_app(APP, PROJECT, 'com.example.staging', 'com.example.staging-ios')

        self.assertEqual([name for name, _ in events], ['server', 'provisioner'])
        smoke, approved = events[0][1]['onboarding']['profiles']
        self.assertEqual(smoke['staging_project_id'], onboarding.PROJECT)
        self.assertEqual(approved['staging_app_id'], APP)
        self.assertEqual(approved['staging_project_id'], PROJECT)
        self.assertEqual(approved['id'], result['profile'])
        self.assertEqual(events[1][1], {'environment': 'staging'})

    def test_invalid_identifiers_fail_before_accessing_staging(self):
        with patch.object(onboarding, 'release') as release:
            with self.assertRaisesRegex(RuntimeError, 'Invalid ANDROID'):
                onboarding.approve_app(APP, PROJECT, 'invalid', None)
            release.assert_not_called()
