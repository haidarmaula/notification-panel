#!/usr/bin/env python3
"""Prepare a one-replica delivery policy without starting provider execution."""
import base64
import io
import json
from pathlib import Path
import socket
import subprocess
import tarfile
from urllib.parse import quote, urlencode

SEED = 'postgres@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d'
VOLUME = 'hello-staging-delivery-policy'
PROJECT = '63677e77-2661-43b8-8253-35c1134bcc60'


def command(args, data=None):
    result = subprocess.run(args, input=data, capture_output=True, timeout=30)
    if result.returncode:
        raise SystemExit('Delivery preparation failed; no secret values printed.')
    return result.stdout


def read_secret(path):
    return command(['docker', 'run', '--rm', '--network', 'none', '--read-only', '--user', '0:0',
        '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--mount',
        f'type=bind,src={path},dst=/input,readonly', '--entrypoint', 'cat', SEED, '/input'])


def main():
    if socket.gethostname() != 'srv-kafka':
        raise SystemExit('Run on srv-kafka.')
    if subprocess.run(['docker', 'volume', 'inspect', VOLUME], capture_output=True).returncode == 0:
        raise SystemExit('Delivery policy already exists; inspect instead of replacing keys.')
    archive = command(['docker', 'cp', 'hello-staging-server:/etc/hello/mobile-policy.json', '-'])
    with tarfile.open(fileobj=io.BytesIO(archive)) as bundle:
        mobile = json.load(bundle.extractfile('mobile-policy.json'))
    projects = mobile['projects']
    if len(projects) != 1 or projects[0]['app_id'] != PROJECT:
        raise SystemExit('Mobile key policy has unexpected scope.')
    sources = json.loads((Path.home()/'hello-staging-provision/kafka-source-review.json').read_text())['sources']
    source = next(s for s in sources if s['topic'] == 'notification.delivery.updated')
    if not source['active'] or source['topic_incarnation'] != 'Kll9XLH2R2eZUHpILxg2lg':
        raise SystemExit('Delivery source is not the approved active identity.')
    account_data = read_secret('/etc/hello/fcm/service-account.json')
    account = json.loads(account_data)
    if account.get('project_id') != 'push-notifications-stagi-a6a41':
        raise SystemExit('Unexpected Firebase sender project.')
    pem = command(['openssl', 'genpkey', '-algorithm', 'ED25519'])
    private = command(['openssl', 'pkey', '-outform', 'DER'], pem)
    public = command(['openssl', 'pkey', '-pubout', '-outform', 'DER'], pem)
    if len(private) != 48 or private[:16].hex() != '302e020100300506032b657004220420' or len(public) != 44 or public[:12].hex() != '302a300506032b6570032100':
        raise SystemExit('Unexpected Ed25519 key encoding.')
    signing = base64.b64encode(private[16:] + public[12:]).decode()
    certificate = read_secret('/etc/hello/kafka/clients/hello-staging-delivery.pem').decode()
    password = read_secret('/etc/hello/roles/hello_delivery_login.password').decode().strip()
    policy = {'mode': 'delivery_only', 'database_url':
        f'postgres://hello_delivery_login:{quote(password,safe="")}@127.0.0.1:5432/hello_staging?'
        + urlencode({'sslmode':'verify-full','sslrootcert':'/etc/hello/postgres-ca.crt','connect_timeout':'5'}),
        'log_source_id':source['id'],'cluster_identity':source['cluster_identity'],
        'topic_name':source['topic'],'topic_incarnation':source['topic_incarnation'],
        'partition_count':3,'retry_min_ms':1000,'retry_max_ms':30000,
        'kafka':{'brokers':['127.0.0.1:9095'],'authentication':'MTLS',
                 'ca_pem':read_secret('/etc/hello/kafka/clients/ca.crt').decode(),
                 'client_certificate_pem':certificate,'client_key_pem':certificate,
                 'sasl_username':'','sasl_password':''},
        'delivery':{'application_id':PROJECT,'environment':'staging','project_id':account['project_id'],
            'routes':[{'namespace':r['provider_namespace'],'key':r['provider_route_key'],
                       'platform':r['platform'],'address_type':r['address_type']} for r in projects[0]['routes']],
            'service_account_file':'/etc/hello/service-account.json',
            'credential_project_id':account['project_id'],'service_account_email':account['client_email'],
            'cross_project_approved':False,'lease_seconds':60,'renew_seconds':15,
            'request_timeout_seconds':10,'retry_floor_seconds':10,'max_attempts':1,
            'receipt_key_id':'staging-receipt-v1','observation_window_seconds':86400,
            'requests_per_second':1,'project_requests_per_second':1,'replica_count':1,'concurrency':1,
            'breaker_threshold':3,'breaker_cooldown_seconds':30,'poll_interval_ms':1000,'page_size':1,
            'current_key_version':mobile['current_key_version'],'encryption_keys':mobile['encryption_keys'],
            'fingerprint_key':mobile['fingerprint_key'],
            'signing_keys':[{'id':'staging-receipt-v1','key':signing}],
            'database_pool':{'max_connections_per_replica':2,'deployment_connection_budget':2}}}
    files = {'worker.json':json.dumps(policy).encode(),'postgres-ca.crt':read_secret('/etc/hello/postgres-ca.crt'),
             'service-account.json':account_data,
             'receipt-public.json':json.dumps({'environment':'staging','id':'staging-receipt-v1',
                 'key':base64.b64encode(public[12:]).decode()}).encode()}
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode='w') as bundle:
        for name, data in files.items():
            entry=tarfile.TarInfo(name);entry.uid,entry.gid,entry.mode,entry.size=0,65532,0o640,len(data)
            bundle.addfile(entry,io.BytesIO(data))
    command(['docker','volume','create','--label','hello.purpose=protected-staging-policy',VOLUME])
    command(['docker','run','--rm','-i','--network','none','--read-only','--user','0:0',
             '--cap-drop','ALL','--cap-add','CHOWN','--security-opt','no-new-privileges',
             '--mount',f'type=volume,src={VOLUME},dst=/policy','--entrypoint','tar',SEED,
             '-xpf','-','-C','/policy'],output.getvalue())
    print('Prepared delivery policy: one replica, one concurrent request, 1 request/s, one attempt per intent.')
    print('Shared enrollment address keys retained; fresh receipt signer stored protected. No process started.')


if __name__ == '__main__':
    main()
