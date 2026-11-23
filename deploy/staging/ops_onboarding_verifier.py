"""Provision and start one app-scoped OAuth verifier on the staging VM."""
import hashlib
import json
from pathlib import Path
import re
import secrets
import subprocess
from urllib.parse import quote, urlencode

from ops_common import ROOT, SEED, atomic, db, run

CREDENTIAL_VOLUME = 'hello-staging-onboarding-credentials'
POSTGRES_CA = '/etc/hello/postgres-ca.crt'
ENVIRONMENT = 'staging'


def _canonical_uuid(value):
    import uuid
    try:
        parsed = uuid.UUID(value)
    except (ValueError, TypeError, AttributeError):
        raise RuntimeError('Canonical application/configuration UUID required.')
    if str(parsed) != value or parsed.int == 0:
        raise RuntimeError('Canonical application/configuration UUID required.')
    return value


def _credential_mount_source(app_uuid, config_uuid):
    """Validate the exact protected version inside the named volume."""
    volume = json.loads(run(['docker', 'volume', 'inspect', CREDENTIAL_VOLUME]))[0]
    if volume.get('Name') != CREDENTIAL_VOLUME or not volume.get('Mountpoint'):
        raise RuntimeError('Protected onboarding credential volume is unavailable.')
    mountpoint = Path(volume['Mountpoint'])
    if not mountpoint.is_absolute() or '..' in mountpoint.parts:
        raise RuntimeError('Credential volume mountpoint is invalid.')
    script = r'''
set -eu
root=/credentials
app="$root/$1"
env="$app/staging"
version="$env/$2"
for path in "$root" "$app" "$env" "$version"; do
  test ! -L "$path"
  test -d "$path"
done
test ! -L "$version/service-account.json"
test -f "$version/service-account.json"
stat -c '%u:%g:%a:%F' "$root" "$app" "$env" "$version" "$version/service-account.json"
'''
    output = run(['docker', 'run', '--rm', '--network', 'none', '--read-only', '--user', '0:0',
                  '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--mount',
                  f'type=volume,src={CREDENTIAL_VOLUME},dst=/credentials,readonly',
                  '--entrypoint', 'sh', SEED, '-c', script, 'sh', app_uuid, config_uuid]).decode().splitlines()
    if output != ['0:0:755:directory', '0:65532:750:directory', '0:65532:750:directory',
                  '0:65532:750:directory', '0:65532:640:regular file']:
        raise RuntimeError('Credential path ownership, permissions, or file type is invalid.')
    return str(mountpoint / app_uuid / ENVIRONMENT / config_uuid)


def _inspect_exists(kind, name):
    result = subprocess.run(['docker', kind, 'inspect', name], capture_output=True, timeout=20)
    if result.returncode == 0:
        return json.loads(result.stdout)[0]
    if b'No such ' in result.stderr or b'not found' in result.stderr.lower():
        return None
    raise RuntimeError('Docker inspection failed; protected diagnostics are required.')


def _write_policy_volume(name, files):
    import io
    import tarfile
    if _inspect_exists('volume', name) is None:
        run(['docker', 'volume', 'create', '--label', 'hello.purpose=protected-staging-policy', name])
    archive = io.BytesIO()
    with tarfile.open(fileobj=archive, mode='w') as tar:
        for entry_name, content in files.items():
            item = tarfile.TarInfo(entry_name)
            item.uid, item.gid, item.mode, item.size = 0, 65532, 0o640, len(content)
            tar.addfile(item, io.BytesIO(content))
    run(['docker', 'run', '--rm', '-i', '--network', 'none', '--read-only', '--user', '0:0',
         '--cap-drop', 'ALL', '--cap-add', 'CHOWN', '--security-opt', 'no-new-privileges',
         '--mount', f'type=volume,src={name},dst=/policy', '--entrypoint', 'tar', SEED,
         '-xpf', '-', '-C', '/policy'], archive.getvalue())


def _verify_existing_container(name, image, app_uuid, configuration_uuid, credential_dir, policy_volume):
    result = _inspect_exists('container', name)
    if result is None:
        return False
    config = result.get('Config', {})
    host = result.get('HostConfig', {})
    labels = config.get('Labels') or {}
    mounts = result.get('Mounts', [])
    expected = {
        ('volume', policy_volume, '/etc/hello'),
        ('bind', credential_dir, '/etc/hello/fcm/' + configuration_uuid),
    }
    observed = {(m.get('Type'), m.get('Name') if m.get('Type') == 'volume' else m.get('Source'), m.get('Destination')) for m in mounts}
    if (result.get('Image') != image or config.get('User') != '65532:65532' or
            labels.get('hello.role') != 'onboarding-verifier' or
            labels.get('hello.application_id') != app_uuid or observed != expected or
            any(m.get('RW') for m in mounts) or host.get('ReadonlyRootfs') is not True or
            host.get('NetworkMode') != 'host' or host.get('Privileged') is True):
        raise RuntimeError('Existing verifier container differs from its recorded configuration.')
    return True


def start_verifier(app_uuid, revision, image, configuration_uuid):
    """Create or resume one verifier for the reviewed app/configuration.

    `image` must be a verified immutable controlplane image ID. Credentials
    are checked in the named protected volume, and only the exact version
    directory is mounted read-only.
    """
    app_uuid = _canonical_uuid(app_uuid)
    configuration_uuid = _canonical_uuid(configuration_uuid)
    if not isinstance(revision, int) or revision < 1:
        raise RuntimeError('Positive reviewed onboarding revision required.')
    if not re.fullmatch(r'sha256:[0-9a-f]{64}', image):
        raise RuntimeError('Immutable verified image ID required.')
    credential_dir = _credential_mount_source(app_uuid, configuration_uuid)
    row = db("SELECT row_to_json(x)::text FROM (SELECT a.id,a.status,o.revision,o.environment,o.candidate_configuration_id::text candidate,o.current_configuration_id::text current FROM applications a JOIN application_onboarding o ON o.application_id=a.id WHERE a.public_id='" + app_uuid + "') x;")
    if not row:
        raise RuntimeError('Staging application was not found.')
    app = json.loads(row)
    if (app['revision'] != revision or app['environment'] != ENVIRONMENT or
            app['status'] not in ('PROVISIONING', 'ACTIVE') or
            configuration_uuid not in (app['candidate'], app['current'])):
        raise RuntimeError('Application revision, environment, status, or configuration changed; review again.')

    image_obj = json.loads(run(['docker', 'image', 'inspect', image]))[0]
    if (image_obj.get('Id') != image or image_obj.get('Config', {}).get('User') != '65532:65532' or
            image_obj.get('Config', {}).get('Entrypoint') != ['/controlplane']):
        raise RuntimeError('Image is not a verified controlplane runtime image.')

    suffix = app_uuid.replace('-', '')[:20]
    login = 'hello_onboard_' + suffix + '_login'
    container = 'hello-staging-onboarding-verifier-' + suffix
    policy_volume = 'hello-staging-onboarding-verifier-' + suffix + '-policy'
    state_dir = ROOT / 'onboarding-verifiers' / app_uuid
    record_path = state_dir / 'verifier.json'
    password_path = state_dir / 'db-password'
    record = {'application_id': app_uuid, 'configuration_id': configuration_uuid,
              'revision': revision, 'image': image, 'container': container,
              'policy_volume': policy_volume, 'login': login}

    if record_path.exists():
        if json.loads(record_path.read_text()) != record:
            raise RuntimeError('Existing verifier configuration differs; inspect before changing it.')
    else:
        if _inspect_exists('container', container) or _inspect_exists('volume', policy_volume):
            raise RuntimeError('Verifier resources exist without a matching private record.')
        state_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
        state_dir.chmod(0o700)
        atomic(record_path, (json.dumps(record, sort_keys=True, indent=2) + '\n').encode())

    if password_path.exists():
        password = password_path.read_text().strip()
        if not re.fullmatch(r'[A-Za-z0-9_-]{60,}', password):
            raise RuntimeError('Private verifier credential record is invalid.')
    else:
        password = secrets.token_urlsafe(48)
        atomic(password_path, (password + '\n').encode())

    role_state = json.loads(db("SELECT json_build_object('login',EXISTS(SELECT FROM pg_roles WHERE rolname='" + login + "'), 'group',EXISTS(SELECT FROM pg_roles WHERE rolname='hello_onboarding_verifier'), 'member',CASE WHEN to_regrole('" + login + "') IS NOT NULL THEN pg_has_role(to_regrole('" + login + "'),to_regrole('hello_onboarding_verifier'),'MEMBER') ELSE false END, 'scope',(SELECT application_id FROM application_onboarding_verifier_scopes WHERE role_id=to_regrole('" + login + "')::oid))::text;"))
    if not role_state['group']:
        raise RuntimeError('Restricted onboarding verifier group is not installed.')
    if role_state['login']:
        if not role_state['member'] or role_state['scope'] not in (None, app['id']):
            raise RuntimeError('Existing verifier role has unexpected membership or scope.')
    else:
        db("BEGIN; SET LOCAL lock_timeout='3s'; CREATE ROLE " + login + " LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '" + password + "'; GRANT hello_onboarding_verifier TO " + login + "; INSERT INTO application_onboarding_verifier_scopes(role_id,application_id) VALUES('" + login + "'::regrole::oid," + str(app['id']) + "); COMMIT;")
    if role_state['login'] and role_state['scope'] is None:
        db("BEGIN; SET LOCAL lock_timeout='3s'; INSERT INTO application_onboarding_verifier_scopes(role_id,application_id) VALUES('" + login + "'::regrole::oid," + str(app['id']) + "); COMMIT;")

    ca = run(['docker', 'run', '--rm', '--network', 'none', '--read-only', '--user', '0:0',
              '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--mount',
              f'type=bind,src={POSTGRES_CA},dst=/input,readonly', '--entrypoint', 'cat', SEED, '/input'])
    database_url = ('postgres://' + login + ':' + quote(password, safe='') + '@127.0.0.1:5432/hello_staging?' +
                    urlencode({'sslmode': 'verify-full', 'sslrootcert': '/etc/hello/postgres-ca.crt', 'connect_timeout': '5'}))
    policy = {'application_id': app_uuid, 'environment': ENVIRONMENT, 'credential_root': '/etc/hello/fcm',
              'database_url': database_url, 'poll_interval_ms': 1000}
    policy_bytes = (json.dumps(policy, separators=(',', ':')) + '\n').encode()
    _write_policy_volume(policy_volume, {'controlplane.json': policy_bytes, 'postgres-ca.crt': ca})

    if not _verify_existing_container(container, image, app_uuid, configuration_uuid, credential_dir, policy_volume):
        args = ['docker', 'create', '--name', container, '--restart', 'unless-stopped', '--network', 'host',
                '--read-only', '--user', '65532:65532', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
                '--pids-limit', '64', '--memory', '256m', '--cpus', '0.5', '--log-opt', 'max-size=10m', '--log-opt', 'max-file=3',
                '--label', 'hello.role=onboarding-verifier', '--label', 'hello.application_id=' + app_uuid,
                '--mount', f'type=volume,src={policy_volume},dst=/etc/hello,readonly',
                '--mount', f'type=bind,src={credential_dir},dst=/etc/hello/fcm/' + configuration_uuid + ',readonly', image]
        run(args)
    run(['docker', 'start', container])
    return {'container': container, 'state': 'started', 'application_id': app_uuid,
            'configuration_id': configuration_uuid, 'image': image,
            'policy_sha256': hashlib.sha256(policy_bytes).hexdigest()}
