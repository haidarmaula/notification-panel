#!/usr/bin/env python3
"""Rotate the staging issuer identity and serve mobile enrollment at the gateway.

The previous temporary issuer's private keys are unrecoverable, so this
activation generates a fresh identity, re-pins the server mobile policy,
runs a hardened loopback issuer, and routes /v1/session and /v1/origin
through the gateway. The enrolled pilot device must re-enroll afterwards;
dispatch holds and application state are unchanged. Run with sudo on
srv-kafka after a completed CD deployment whose manifest includes the
issuer candidate image.
"""
import http.client
import importlib.util
import io
import json
import os
from pathlib import Path
import socket
import ssl
import tarfile
import time
import urllib.error
import urllib.parse
import urllib.request

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('pilot', HERE / 'run-one-pilot.py')
pilot = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pilot)
from ops_common import ROOT, read_container, save, state
from ops_gateway_docs import write_config, api_ready

ISSUER_VOLUME = 'hello-staging-issuer-policy'
BACKUP_VOLUME = 'hello-staging-issuer-policy-backup'
SERVER_VOLUME = 'hello-staging-server-mobile-policy'
CONTAINER = 'hello-staging-issuer'
CONTROL_DIR = Path('/etc/hello/issuer-control')
MARKER = '# HELLO_ISSUER_ROUTES'
PUBLIC_ORIGIN = 'https://api.hello-staging.example'
IDENTITY_FIELDS = ('app_id', 'issuer', 'audience', 'purpose', 'namespace', 'origin_issuer', 'origin_audience')
ISSUER_BLOCK = '''    # HELLO_ISSUER_ROUTES: mobile enrollment authority on loopback.
    location = /v1/session {
      proxy_pass http://127.0.0.1:8181;
      proxy_set_header Host $host;
      proxy_set_header X-Forwarded-Proto https;
      proxy_set_header X-Forwarded-For $remote_addr;
      proxy_connect_timeout 5s;
      proxy_read_timeout 30s;
    }
    location = /v1/origin {
      proxy_pass http://127.0.0.1:8181;
      proxy_set_header Host $host;
      proxy_set_header X-Forwarded-Proto https;
      proxy_set_header X-Forwarded-For $remote_addr;
      proxy_connect_timeout 5s;
      proxy_read_timeout 30s;
    }'''


def patch_policy(old, fresh):
    policy, new = json.loads(old), json.loads(fresh)
    if len(policy['projects']) != 1 or len(new['projects']) != 1:
        raise RuntimeError('Expected exactly one mobile project.')
    project, source = policy['projects'][0], new['projects'][0]
    for field in IDENTITY_FIELDS:
        if project.get(field) != source.get(field):
            raise RuntimeError('Issuer identity scope changed; review before rotation.')
    project['verification_key'] = source['verification_key']
    project['origin_key'] = source['origin_key']
    return json.dumps(policy).encode()


def patch_gateway(config):
    text = config.decode()
    if MARKER in text or 'location = /v1/session' in text:
        raise RuntimeError('Issuer routes already present.')
    anchor = 'ssl_protocols TLSv1.2 TLSv1.3;'
    if text.count(anchor) not in (1, 2):
        raise RuntimeError('Unexpected gateway layout.')
    return text.replace(anchor, anchor + '\n' + ISSUER_BLOCK).encode()


def volume_write(volume, name, data, mode=0o640):
    archive = io.BytesIO()
    with tarfile.open(fileobj=archive, mode='w') as bundle:
        entry = tarfile.TarInfo(name)
        entry.uid, entry.gid, entry.mode, entry.size = 0, 65532, mode, len(data)
        bundle.addfile(entry, io.BytesIO(data))
    pilot.raw(['docker', 'run', '--rm', '-i', '--network', 'none', '--read-only',
               '--user', '0:0', '--cap-drop', 'ALL', '--cap-add', 'CHOWN',
               '--security-opt', 'no-new-privileges', '--mount', f'type=volume,src={volume},dst=/policy',
               '--entrypoint', 'tar', pilot.SEED, '-xpf', '-', '-C', '/policy'], archive.getvalue())


def volume_read(volume, name):
    return pilot.raw(['docker', 'run', '--rm', '--network', 'none', '--read-only',
                      '--user', '0:0', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
                      '--mount', f'type=volume,src={volume},dst=/policy,readonly',
                      '--entrypoint', 'cat', pilot.SEED, '/policy/' + name])


class UnixConnection(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__('localhost', timeout=5)
        self.socket_path = str(path)

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(5)
        self.sock.connect(self.socket_path)


def pairing_request(body=b''):
    connection = UnixConnection(CONTROL_DIR / 'issuer.sock')
    try:
        connection.request('POST', '/pair', body=body, headers={'Content-Type': 'application/json'})
        response = connection.getresponse()
        if response.status != 200:
            raise RuntimeError('Issuer control socket refused pairing.')
        return json.load(response)['data']
    finally:
        connection.close()


def gateway_client():
    ca = read_container('hello-staging-gateway', '/gateway/server-ca.crt').decode()
    return urllib.request.build_opener(urllib.request.ProxyHandler({}),
        urllib.request.HTTPSHandler(context=ssl.create_default_context(cadata=ca)))


def verify():
    data = pairing_request()
    code = urllib.parse.parse_qs(urllib.parse.urlparse(data['link']).query)['code'][0]
    client = gateway_client()
    session = urllib.request.Request('https://10.1.16.109:8443/v1/session',
        data=json.dumps({'app_id': pilot.APP, 'pairing_code': code}).encode(),
        headers={'Content-Type': 'application/json'}, method='POST')
    with client.open(session, timeout=5) as response:
        credential = json.load(response)['data']['credential']
    origin = urllib.request.Request('https://10.1.16.109:8443/v1/origin',
        data=json.dumps({'app_id': pilot.APP}).encode(),
        headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + credential}, method='POST')
    with client.open(origin, timeout=5) as response:
        assertion = json.load(response)['data']['origin']
    if not credential or not assertion:
        raise RuntimeError('Enrollment verification returned empty material.')


def recover():
    path = ROOT / 'public-enrollment-bootstrap.json'
    if not path.exists():
        return
    journal = json.loads(path.read_text())
    if journal['phase'] == 'committed':
        return
    if pilot.raw(['docker', 'volume', 'ls', '--filter', 'name=^' + BACKUP_VOLUME + '$', '--format', '{{.Name}}']).strip():
        volume_write(SERVER_VOLUME, 'mobile-policy.json', volume_read(BACKUP_VOLUME, 'mobile-policy.json'))
        pilot.raw(['docker', 'restart', 'hello-staging-server'])
    if pilot.raw(['docker', 'ps', '-a', '--filter', 'name=^' + CONTAINER + '$', '--format', '{{.Names}}']).strip():
        pilot.raw(['docker', 'rm', '-f', CONTAINER])
    if pilot.raw(['docker', 'volume', 'ls', '--filter', 'name=^' + ISSUER_VOLUME + '$', '--format', '{{.Name}}']).strip():
        pilot.raw(['docker', 'volume', 'rm', ISSUER_VOLUME])
    before = ROOT / 'gateway-before-issuer.conf'
    if before.exists():
        write_config(before.read_bytes())
        pilot.raw(['docker', 'exec', 'hello-staging-gateway', 'nginx', '-c', '/gateway/nginx.conf', '-s', 'reload'])
    if CONTROL_DIR.exists():
        for item in CONTROL_DIR.iterdir():
            item.unlink()
        CONTROL_DIR.rmdir()
    (ROOT / 'public-enrollment.json').unlink(missing_ok=True)
    path.unlink()


def main():
    if socket.gethostname() != 'srv-kafka' or os.geteuid() != 0:
        raise SystemExit('Run with sudo on srv-kafka.')
    recover()
    if (ROOT / 'public-enrollment.json').exists():
        verify()
        print('Public enrollment already enabled; verification passed.')
        return
    cd = json.loads((ROOT / 'cd/status.json').read_text())
    if cd.get('phase') != 'deployed':
        raise RuntimeError('Require a completed CD deployment.')
    artifact = json.loads((ROOT / 'releases' / cd['release'] / 'release.json').read_text())
    image = artifact.get('candidate_images', {}).get('issuer')
    if not image:
        raise RuntimeError('Checked release lacks an issuer image; deploy reviewed source first.')
    mounts = json.loads(pilot.raw(['docker', 'inspect', 'hello-staging-server']))[0]['Mounts']
    if not any(m.get('Name') == SERVER_VOLUME and m['Destination'] == '/etc/hello' for m in mounts):
        raise RuntimeError('Unexpected active server policy volume.')
    old = pilot.server_file('/etc/hello/mobile-policy.json')
    before = state()

    keygen = 'hello-staging-issuer-keygen'
    if pilot.raw(['docker', 'volume', 'ls', '--filter', 'name=^' + keygen + '$', '--format', '{{.Name}}']).strip():
        raise RuntimeError('Stale issuer keygen volume requires inspection.')
    pilot.raw(['docker', 'volume', 'create', '--label', 'hello.purpose=issuer-keygen', keygen])
    try:
        pilot.raw(['docker', 'run', '--rm', '--network', 'none', '-v', keygen + ':/work',
                   '--entrypoint', 'sh', pilot.SEED, '-c', 'mkdir -p /work && chown 65532:65532 /work && chmod 0750 /work'])
        pilot.raw(['docker', 'run', '--rm', '--network', 'none', '--read-only', '-v', keygen + ':/work', image,
                   'init', '--dir', '/work/keys', '--app-id', pilot.APP, '--namespace', 'hello-staging-mobile',
                   '--subject', 'android-smoke-haidar', '--provider-namespace', 'push-notifications-stagi-a6a41',
                   '--provider-route-key', 'android-fid'])
        issuer_config = volume_read(keygen, 'keys/issuer.json')
        fresh_policy = volume_read(keygen, 'keys/mobile-policy.json')
    finally:
        pilot.raw(['docker', 'volume', 'rm', keygen])
    patched = patch_policy(old, fresh_policy)

    if not pilot.raw(['docker', 'volume', 'ls', '--filter', 'name=^' + BACKUP_VOLUME + '$', '--format', '{{.Name}}']).strip():
        pilot.raw(['docker', 'volume', 'create', '--label', 'hello.purpose=issuer-policy-backup', BACKUP_VOLUME])
        volume_write(BACKUP_VOLUME, 'mobile-policy.json', old, mode=0o600)
    if pilot.raw(['docker', 'volume', 'ls', '--filter', 'name=^' + ISSUER_VOLUME + '$', '--format', '{{.Name}}']).strip():
        raise RuntimeError('Existing issuer policy volume requires inspection.')
    pilot.raw(['docker', 'volume', 'create', '--label', 'hello.purpose=issuer-policy', ISSUER_VOLUME])
    volume_write(ISSUER_VOLUME, 'issuer.json', issuer_config)
    CONTROL_DIR.mkdir(mode=0o700, exist_ok=True)
    os.chown(CONTROL_DIR, 65532, 65532)
    config = read_container('hello-staging-gateway', '/gateway/nginx.conf')
    candidate = patch_gateway(config)
    save(ROOT / 'gateway-before-issuer.conf', config)
    save(ROOT / 'public-enrollment-bootstrap.json', {'phase': 'prepared', 'before': before, 'sha': cd['sha']})
    try:
        volume_write(SERVER_VOLUME, 'mobile-policy.json', patched)
        pilot.raw(['docker', 'restart', 'hello-staging-server'])
        api_ready()
        pilot.raw(['docker', 'run', '-d', '--name', CONTAINER, '--restart', 'unless-stopped',
                   '--network', 'host', '--read-only', '--user', '65532:65532', '--cap-drop', 'ALL',
                   '--security-opt', 'no-new-privileges', '--pids-limit', '64', '--memory', '128m', '--cpus', '.5',
                   '--log-opt', 'max-size=10m', '--log-opt', 'max-file=3',
                   '--tmpfs', '/tmp:rw,noexec,nosuid,size=16m,uid=65532,gid=65532',
                   '--mount', f'type=volume,src={ISSUER_VOLUME},dst=/etc/hello,readonly',
                   '--mount', f'type=bind,src={CONTROL_DIR},dst=/control',
                   image, 'serve', '--config', '/etc/hello/issuer.json', '--listen', '127.0.0.1:8181',
                   '--control-socket', '/control/issuer.sock', '--public-origin', PUBLIC_ORIGIN])
        for _ in range(40):
            if (CONTROL_DIR / 'issuer.sock').is_socket():
                break
            time.sleep(0.25)
        else:
            raise RuntimeError('Issuer control socket never appeared.')
        write_config(candidate)
        pilot.raw(['docker', 'exec', 'hello-staging-gateway', 'nginx', '-c', '/gateway/nginx.conf', '-t'])
        pilot.raw(['docker', 'exec', 'hello-staging-gateway', 'nginx', '-c', '/gateway/nginx.conf', '-s', 'reload'])
        verify()
        if state() != before:
            raise RuntimeError('Dispatch policy changed during activation; preserve the incident state.')
        save(ROOT / 'public-enrollment.json', {'volume': ISSUER_VOLUME, 'container': CONTAINER,
            'sha': cd['sha'], 'public_origin': PUBLIC_ORIGIN, 'completed': time.time()})
        journal = json.loads((ROOT / 'public-enrollment-bootstrap.json').read_text())
        journal['phase'] = 'committed'
        save(ROOT / 'public-enrollment-bootstrap.json', journal)
    except BaseException:
        recover()
        raise
    print('Public enrollment enabled: /v1/session and /v1/origin are served through the gateway.')
    print('The previously enrolled pilot device must re-enroll; dispatch holds are unchanged.')
    print('Generate pairing QRs with: sudo python3 deploy/staging/staging-pairing.py')


if __name__ == '__main__':
    main()