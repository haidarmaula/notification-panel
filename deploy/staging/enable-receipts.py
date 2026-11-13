#!/usr/bin/env python3
"""Enable the existing staging worker's public receipt key without rotating secrets."""
import importlib.util
import io
import json
from pathlib import Path
import socket
import ssl
import tarfile
import time
import urllib.error
import urllib.request

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('pilot', HERE / 'run-one-pilot.py')
pilot = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pilot)
VOLUME = 'hello-staging-server-mobile-policy'


def install(data):
    archive = io.BytesIO()
    with tarfile.open(fileobj=archive, mode='w') as bundle:
        entry = tarfile.TarInfo('mobile-policy.json')
        entry.uid, entry.gid, entry.mode, entry.size = 0, 65532, 0o640, len(data)
        bundle.addfile(entry, io.BytesIO(data))
    pilot.raw(['docker', 'run', '--rm', '-i', '--network', 'none', '--read-only',
               '--user', '0:0', '--cap-drop', 'ALL', '--cap-add', 'CHOWN',
               '--security-opt', 'no-new-privileges', '--mount', f'type=volume,src={VOLUME},dst=/policy',
               '--entrypoint', 'tar', pilot.SEED, '-xpf', '-', '-C', '/policy'], archive.getvalue())


def main():
    if socket.gethostname() != 'srv-kafka':
        raise SystemExit('Run on srv-kafka.')
    mounts = json.loads(pilot.raw(['docker', 'inspect', 'hello-staging-server']))[0]['Mounts']
    if not any(m.get('Name') == VOLUME and m['Destination'] == '/etc/hello' for m in mounts):
        raise RuntimeError('Unexpected active server policy volume.')
    old = pilot.server_file('/etc/hello/mobile-policy.json')
    policy = json.loads(old)
    if len(policy['projects']) != 1 or policy['projects'][0]['app_id'] != pilot.APP:
        raise RuntimeError('Unexpected project scope.')
    key = json.loads(pilot.raw(['docker', 'run', '--rm', '--network', 'none', '--read-only',
        '--user', '0:0', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
        '--mount', 'type=volume,src=hello-staging-delivery-policy,dst=/policy,readonly',
        '--entrypoint', 'cat', pilot.SEED, '/policy/receipt-public.json']))
    if key['environment'] != 'staging' or key['id'] != 'staging-receipt-v1':
        raise RuntimeError('Unexpected receipt signing identity.')
    receipt = {'environment': 'staging', 'verification_keys': [{'id': key['id'], 'key': key['key']}],
        'trusted_tls_proxies': ['127.0.0.1'], 'ip_per_minute': 120,
        'principal_per_minute': 120, 'installation_per_minute': 60}
    project = policy['projects'][0]
    if project.get('receipts') not in (None, receipt):
        raise RuntimeError('Existing receipt policy requires review.')
    project['receipts'] = receipt
    # The backup contains other mobile secrets; retain it in a protected Docker volume.
    backup = 'hello-staging-receipt-policy-backup'
    existed = pilot.raw(['docker', 'volume', 'ls', '--filter', 'name=^'+backup+'$', '--format', '{{.Name}}']).strip()
    if not existed:
        pilot.raw(['docker', 'volume', 'create', backup])
        pilot.raw(['docker', 'run', '--rm', '-i', '--network', 'none', '--read-only',
            '--user', '0:0', '--cap-drop', 'ALL', '--mount', f'type=volume,src={backup},dst=/backup',
            '--entrypoint', 'sh', pilot.SEED, '-c', 'umask 077; cat > /backup/mobile-policy.json'], old)
    ca = pilot.text(['docker', 'exec', 'hello-staging-gateway', 'cat', '/gateway/server-ca.crt'])
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}),
        urllib.request.HTTPSHandler(context=ssl.create_default_context(cadata=ca)))
    try:
        install(json.dumps(policy).encode())
        pilot.raw(['docker', 'restart', 'hello-staging-server'])
        for attempt in range(20):
            try:
                req = urllib.request.Request(f'https://10.1.16.109:8443/api/v1/apps/{pilot.APP}/mobile/notifications/1/receipt',
                    data=b'{}', headers={'Content-Type': 'application/json'}, method='POST')
                client.open(req, timeout=3)
            except urllib.error.HTTPError as error:
                if error.code == 401:
                    print('Receipt verifier enabled; TLS route requires mobile authentication. Pilot state unchanged.')
                    return
            except (OSError, urllib.error.URLError):
                pass
            time.sleep(1)
        raise RuntimeError('Receipt route verification failed.')
    except BaseException:
        install(old)
        pilot.raw(['docker', 'restart', 'hello-staging-server'])
        raise


if __name__ == '__main__':
    main()
