#!/usr/bin/env python3
"""Apply the owner's explicit enrollment-only approval with distinct DB identities."""
import datetime
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import uuid

SEED = 'postgres@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d'
PROJECT = '63677e77-2661-43b8-8253-35c1134bcc60'


def command(args, data=None, env=None):
    result = subprocess.run(args, input=data, capture_output=True, text=True, env=env, timeout=30)
    if result.returncode:
        # These commands only return database diagnostics, never input statements.
        print(result.stderr[-1600:], file=__import__('sys').stderr)
        raise SystemExit('Enrollment activation failed; credentials were not printed.')
    return result.stdout.strip()


def main():
    if socket.gethostname() != 'srv-kafka':
        raise SystemExit('Run on srv-kafka.')
    os.umask(0o077)
    directory = Path.home() / 'hello-staging-provision'
    review = json.loads((directory / 'android-enrollment-review.json').read_text())
    owner = ['docker', 'exec', '-i', 'hello-staging-postgres', 'psql', '-X', '-U',
             'hello_staging', '-d', 'hello_staging', '-v', 'ON_ERROR_STOP=1', '-At']
    snapshot = json.loads(command(owner, """SELECT json_build_object(
      'status',(SELECT status FROM applications WHERE public_id='63677e77-2661-43b8-8253-35c1134bcc60'),
      'revision',(SELECT revision FROM applications WHERE public_id='63677e77-2661-43b8-8253-35c1134bcc60'),
      'ownership_finalized',application_ownership_finalized(),
      'runtime',(SELECT row_to_json(r) FROM notification_runtime r),
      'policy',(SELECT row_to_json(p) FROM application_dispatch_policy p WHERE application_id=1),
      'active_routes',(SELECT count(*) FROM current_binding_routes WHERE application_id=1 AND destination='FCM_ACTIVE'),
      'mapped_test_users',(SELECT count(*) FROM mobile_identities m JOIN users u ON u.id=m.user_id AND u.application_id=m.application_id
        WHERE m.application_id=1 AND m.identity_namespace='hello-staging-mobile' AND m.subject='android-smoke-haidar'
        AND u.external_id='staging-android-smoke-haidar' AND u.status='ACTIVE'));
    """))
    runtime, policy = snapshot['runtime'], snapshot['policy']
    if not (snapshot['status'] == 'PROVISIONING' and snapshot['revision'] == 1
            and snapshot['ownership_finalized'] and snapshot['mapped_test_users'] == 1
            and runtime['sealed'] and runtime['migration_hold']
            and runtime['origin_epoch'] == review['runtime']['origin_epoch']
            and runtime['source_epoch'] == review['runtime']['source_epoch']
            and policy['hold_new'] and not policy['coverage_approved']
            and policy['dispatch_mode'] == 'HELD' and snapshot['active_routes'] == 0):
        raise SystemExit('Live state differs from the approved enrollment proposal.')
    states = command(['docker', 'inspect', '--format', '{{.Name}} {{.State.Status}}',
                      'hello-staging-server-pre-mobile', 'hello-staging-scheduler', 'hello-staging-admission'])
    if any(not line.endswith(' exited') for line in states.splitlines()):
        raise SystemExit('Expected prior API and processing containers stopped.')
    evidence = {
        'ownership': {'finalized': True, 'application': PROJECT, 'mapped_test_users': 1,
                      'schema_preparation_log_sha256': hashlib.sha256((directory/'lifecycle-schema-prep.log').read_bytes()).hexdigest()},
        'writers': {'greenfield': True, 'retired_writer_roles': [], 'processing_container_states': states,
                    'runtime_sealed': True, 'migration_hold': True, 'application_dispatch_mode': 'HELD'},
        'cohorts': {'application': PROJECT, 'android_package': review['android_package'],
                    'test_identity': review['test_identity'], 'active_fcm_routes': 0,
                    'authority': 'temporary Mac loopback-only one-use pairing over ADB reverse',
                    'approved_scope': 'Android enrollment only; notification execution remains blocked',
                    'owner_approval': 'haidar explicitly approved in this chat on 2026-11-12: i approve'},
    }
    digests = {}
    for name, value in evidence.items():
        data = (json.dumps(value, sort_keys=True, indent=2) + '\n').encode()
        (directory / ('android-enrollment-' + name + '-evidence.json')).write_bytes(data)
        digests[name] = hashlib.sha256(data).hexdigest()
    credentials = json.loads(command(['docker', 'run', '--rm', '--network', 'none', '--read-only',
        '--user', '0:0', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
        '--mount', 'type=volume,src=hello-staging-lifecycle-credentials,dst=/policy,readonly',
        '--entrypoint', 'cat', SEED, '/policy/credentials.json']))
    grant = str(uuid.uuid4())
    expires = (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(hours=1)).strftime('%Y-%m-%dT%H:%M:%SZ')

    def role_sql(login, sql):
        env = os.environ.copy()
        env['PGPASSWORD'] = credentials[login]
        return command(['docker', 'run', '--rm', '-i', '--network', 'host', '--read-only',
            '--env', 'PGPASSWORD', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
            '--mount', 'type=bind,src=/etc/hello/postgres-ca.crt,dst=/ca.crt,readonly',
            '--entrypoint', 'psql', SEED, '-X',
            f'postgres://{login}@127.0.0.1:5432/hello_staging?sslmode=verify-full&sslrootcert=/ca.crt',
            '-v', 'ON_ERROR_STOP=1', '-At'], sql, env)

    review.update(review_status='owner-approved', grant_id=grant, evidence_sha256=digests,
                  review_reference='Explicit owner approval in chat on 2026-11-12: i approve')
    (directory / 'android-enrollment-review.json').write_text(json.dumps(review, indent=2) + '\n')
    role_sql('hello_lifecycle_reviewer_login', f"""INSERT INTO public.application_lifecycle_grants
      (id,project_id,operator_role,origin_epoch,source_epoch,expected_revision,previous_status,destination,
       ownership_evidence,writer_fence_evidence,cohort_fence_evidence,retired_writer_roles,expires_at)
      VALUES ('{grant}','{PROJECT}','hello_lifecycle_executor_login'::regrole::oid,
      '{runtime['origin_epoch']}','{runtime['source_epoch']}',1,'PROVISIONING','ACTIVE',
      decode('{digests['ownership']}','hex'),decode('{digests['writers']}','hex'),
      decode('{digests['cohorts']}','hex'),'{{}}'::name[],'{expires}');""")
    revision = role_sql('hello_lifecycle_executor_login',
        f"SELECT public.apply_application_lifecycle('{grant}'::uuid,'{PROJECT}'::uuid);")
    if revision != '2':
        raise SystemExit('Unexpected lifecycle revision; inspect before further work.')
    review.update(review_status='approved-and-applied', applied_revision=2)
    (directory / 'android-enrollment-review.json').write_text(json.dumps(review, indent=2) + '\n')
    print('Activated hello-staging for enrollment at revision 2 using separate reviewer/executor logins.')
    print('Global seal, migration hold and application send hold were not changed.')


if __name__ == '__main__':
    main()
