#!/usr/bin/env python3
"""Prepare protected scheduler/admission policy volumes without starting workers."""
import io
import json
import socket
import subprocess
import tarfile
from pathlib import Path
from urllib.parse import quote, urlencode

SEED = 'postgres@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d'


def command(args, data=None):
    result = subprocess.run(args, input=data, capture_output=True, timeout=30)
    if result.returncode:
        raise SystemExit('Daemon preparation command failed; no secret material printed.')
    return result.stdout


def read_secret(path):
    return command(['docker', 'run', '--rm', '--network', 'none', '--read-only', '--user', '0:0',
        '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--mount',
        f'type=bind,src={path},dst=/input,readonly', '--entrypoint', 'cat', SEED, '/input']).decode()


def main():
    if socket.gethostname() != 'srv-kafka':
        raise SystemExit('Run on srv-kafka.')
    sources = json.loads(Path.home().joinpath('hello-staging-provision/kafka-source-evidence.json').read_text())
    send = [source for source in sources if source['topic'] == 'notification.send.requested']
    if len(send) != 1 or send[0]['active']:
        raise SystemExit('Expected one inactive send source pending review.')
    source = send[0]
    if (source['cluster_identity'], source['topic_incarnation']) != (
            'eGTxoZosTWKe7DWll1Teug', '7DWU8nW_Sku7FufiVO568Q'):
        raise SystemExit('Source differs from inspected metadata.')
    ca = read_secret('/etc/hello/kafka/clients/ca.crt')
    db_ca = read_secret('/etc/hello/postgres-ca.crt')
    for role in ('scheduler', 'admission'):
        login = 'hello_' + role + '_login'
        password = read_secret('/etc/hello/roles/' + login + '.password').strip()
        certificate = read_secret('/etc/hello/kafka/clients/hello-staging-' + role + '.pem')
        # The deployed role PEM contains both the certificate and private key.
        policy = {'database_url': f'postgres://{login}:{quote(password, safe="")}@127.0.0.1:5432/hello_staging?'
                    + urlencode({'sslmode': 'verify-full', 'sslrootcert': '/etc/hello/postgres-ca.crt',
                                 'connect_timeout': '5'}),
                  'log_source_id': source['id'], 'cluster_identity': source['cluster_identity'],
                  'topic_name': source['topic'], 'topic_incarnation': source['topic_incarnation'],
                  'partition_count': 3, 'retry_min_ms': 1000, 'retry_max_ms': 30000,
                  'kafka': {'brokers': ['127.0.0.1:9095'], 'authentication': 'MTLS', 'ca_pem': ca,
                            'client_certificate_pem': certificate, 'client_key_pem': certificate,
                            'sasl_username': '', 'sasl_password': ''}}
        if role == 'scheduler':
            policy.update({'poll_interval_ms': 1000, 'page_size': 100})
            filename = 'scheduler.json'
        else:
            policy.update({'mode': 'admission_only', 'group_id': 'hello-staging-admission-v1'})
            filename = 'worker.json'
        volume = 'hello-staging-' + role + '-policy'
        existing = subprocess.run(['docker', 'volume', 'inspect', volume], capture_output=True)
        if existing.returncode == 0:
            raise SystemExit('Policy volume already exists; inspect before replacing: ' + volume)
        command(['docker', 'volume', 'create', '--label', 'hello.purpose=protected-staging-policy', volume])
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode='w') as bundle:
            for name, content in ((filename, json.dumps(policy)), ('postgres-ca.crt', db_ca)):
                data = content.encode()
                entry = tarfile.TarInfo(name)
                entry.uid, entry.gid, entry.mode, entry.size = 0, 65532, 0o640, len(data)
                bundle.addfile(entry, io.BytesIO(data))
        command(['docker', 'run', '--rm', '-i', '--network', 'none', '--read-only', '--user', '0:0',
                 '--cap-drop', 'ALL', '--cap-add', 'CHOWN', '--security-opt', 'no-new-privileges',
                 '--mount', f'type=volume,src={volume},dst=/policy', '--entrypoint', 'tar', SEED,
                 '-xpf', '-', '-C', '/policy'], archive.getvalue())
        print('Prepared ' + volume + '; source remains inactive, process not started.', flush=True)


if __name__ == '__main__':
    main()
