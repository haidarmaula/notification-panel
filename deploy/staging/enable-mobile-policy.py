#!/usr/bin/env python3
"""Install an isolated smoke mobile policy from stdin, with API rollback on failure."""
import io
import json
import socket
import ssl
import subprocess
import sys
import tarfile
import time
import urllib.error
import urllib.request

IMAGE = 'hello-server:4695a2c9730740a9fb88e009fd2c00599edc9ba6'
SEED = 'postgres@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d'
VOLUME = 'hello-staging-server-mobile-policy'
SERVER = 'hello-staging-server'
BACKUP = 'hello-staging-server-pre-mobile'


def command(args, data=None):
    r = subprocess.run(args, input=data, capture_output=True, timeout=45)
    if r.returncode:
        raise RuntimeError('Staging mobile provisioning command failed; no secrets printed.')
    return r.stdout


def copy_file(path):
    raw = command(['docker', 'cp', SERVER + ':' + path, '-'])
    with tarfile.open(fileobj=io.BytesIO(raw)) as bundle:
        return bundle.extractfile(path.rsplit('/', 1)[-1]).read()


def main():
    if socket.gethostname() != 'srv-kafka':
        raise SystemExit('Run on srv-kafka.')
    mobile = json.load(sys.stdin)
    project = mobile.get('projects', [])
    if len(project) != 1 or project[0]['app_id'] != '63677e77-2661-43b8-8253-35c1134bcc60':
        raise SystemExit('Unexpected mobile application scope.')
    for obj in (VOLUME, BACKUP):
        kind = 'volume' if obj == VOLUME else 'container'
        if subprocess.run(['docker', kind, 'inspect', obj], capture_output=True).returncode == 0:
            raise SystemExit('Existing mobile preparation requires inspection: ' + obj)
    server = json.loads(copy_file('/etc/hello/server.json'))
    if server['mobile_policy_file']:
        raise SystemExit('Existing mobile policy requires reconciliation.')
    server['mobile_policy_file'] = '/etc/hello/mobile-policy.json'
    db_ca = copy_file('/etc/hello/server-assets/db-ca.crt')
    archive = io.BytesIO()
    with tarfile.open(fileobj=archive, mode='w') as bundle:
        directory = tarfile.TarInfo('server-assets')
        directory.type, directory.mode, directory.gid = tarfile.DIRTYPE, 0o750, 65532
        bundle.addfile(directory)
        for name, data in [('server.json', json.dumps(server).encode()),
                           ('mobile-policy.json', json.dumps(mobile).encode()),
                           ('server-assets/db-ca.crt', db_ca)]:
            entry = tarfile.TarInfo(name)
            entry.uid, entry.gid, entry.mode, entry.size = 0, 65532, 0o640, len(data)
            bundle.addfile(entry, io.BytesIO(data))
    command(['docker', 'volume', 'create', '--label', 'hello.purpose=protected-staging-policy', VOLUME])
    command(['docker', 'run', '--rm', '-i', '--network', 'none', '--read-only', '--user', '0:0',
             '--cap-drop', 'ALL', '--cap-add', 'CHOWN', '--security-opt', 'no-new-privileges',
             '--mount', f'type=volume,src={VOLUME},dst=/policy', '--entrypoint', 'tar', SEED,
             '-xpf', '-', '-C', '/policy'], archive.getvalue())
    ca = command(['docker', 'exec', 'hello-staging-gateway', 'cat', '/gateway/server-ca.crt']).decode()
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}),
        urllib.request.HTTPSHandler(context=ssl.create_default_context(cadata=ca)))
    command(['docker', 'stop', SERVER])
    command(['docker', 'rename', SERVER, BACKUP])
    try:
        command(['docker', 'run', '-d', '--name', SERVER, '--restart', 'unless-stopped',
                 '--network', 'host', '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
                 '--pids-limit', '128', '--memory', '512m', '--cpus', '1',
                 '--log-opt', 'max-size=10m', '--log-opt', 'max-file=3',
                 '--mount', f'type=volume,src={VOLUME},dst=/etc/hello,readonly', IMAGE])
        path = 'https://10.1.16.109:8443/api/v1/apps/63677e77-2661-43b8-8253-35c1134bcc60/mobile/installations'
        for attempt in range(12):
            try:
                req = urllib.request.Request(path, data=b'{}', headers={'Content-Type': 'application/json'})
                with client.open(req, timeout=2) as response:
                    status = response.status
            except urllib.error.HTTPError as error:
                status = error.code
                error.close()
            except (OSError, urllib.error.URLError):
                status = 0
            if status == 401:
                print('PASS mobile enrollment route enabled and rejects unauthenticated requests (401).')
                print('Protected mobile policy installed; application lifecycle and dispatch holds unchanged.')
                return
            time.sleep(1)
        raise RuntimeError('Mobile route readiness did not pass.')
    except Exception:
        subprocess.run(['docker', 'rm', '-f', SERVER], capture_output=True)
        command(['docker', 'rename', BACKUP, SERVER])
        command(['docker', 'start', SERVER])
        raise RuntimeError('Mobile deployment failed; prior API container restored.')


if __name__ == '__main__':
    try:
        main()
    except Exception:
        raise SystemExit('Mobile provisioning failed; inspect redacted service status. No secret values printed.')
