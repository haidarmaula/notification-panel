#!/usr/bin/env python3
"""Exercise staging staff authentication without printing credentials or tokens."""
import io
import json
import os
from pathlib import Path
import socket
import ssl
import subprocess
import sys
import tarfile
import urllib.error
import urllib.request
import uuid


def command(args):
    result = subprocess.run(args, capture_output=True, timeout=30)
    if result.returncode:
        raise SystemExit('Unable to read required staging inputs; no contents printed.')
    return result.stdout


def main():
    if sys.argv[1:] not in ([], ['--provision-app'], ['--prepare-pilot']):
        raise SystemExit('Usage: smoke-auth.py [--provision-app|--prepare-pilot]')
    if socket.gethostname() != 'srv-kafka':
        raise SystemExit('Run on srv-kafka using the Docker-enabled staging account.')
    os.umask(0o077)
    archive = command(['docker', 'cp', 'hello-staging-server:/etc/hello/server.json', '-'])
    with tarfile.open(fileobj=io.BytesIO(archive)) as bundle:
        policy = json.load(bundle.extractfile('server.json'))
    # The authorized provisioning account already controls Docker. Read only this
    # root-owned bootstrap result through a network-isolated one-shot container.
    admin = json.loads(command(['docker', 'run', '--rm', '--network', 'none', '--read-only',
        '--user', '0:0', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
        '--mount', 'type=bind,src=/etc/hello/staging-admin.json,dst=/input.json,readonly',
        '--entrypoint', 'cat',
        'postgres@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d',
        '/input.json']))
    ca = command(['docker', 'exec', 'hello-staging-gateway', 'cat', '/gateway/server-ca.crt']).decode()
    context = ssl.create_default_context(cadata=ca)
    # Do not inherit proxy settings for this private, explicitly selected endpoint.
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                         urllib.request.HTTPSHandler(context=context))

    def request(path, payload=None, token=None, api_key=True, method=None, extra_headers=None):
        headers = {'Content-Type': 'application/json'}
        if api_key:
            headers['X-API-Key'] = policy['api_key']
        if token:
            headers['Authorization'] = 'Bearer ' + token
        headers.update(extra_headers or {})
        req = urllib.request.Request('https://10.1.16.109:8443/api/v1/' + path,
            data=json.dumps(payload).encode() if payload is not None else None, headers=headers, method=method)
        try:
            response = client.open(req, timeout=10)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            body = response.read(131072)
            try:
                result = json.loads(body)
            except ValueError:
                result = {}
            return response.status, result, response.headers

    def expect(label, response, status):
        if response[0] != status:
            raise RuntimeError(f'{label}: expected HTTP {status}, received {response[0]}')
        print(f'PASS {label}: HTTP {status}', flush=True)
        return response

    refresh = None
    try:
        expect('API key required', request('apps', api_key=False), 401)
        login = expect('administrator login', request('auth/login',
            {'email': admin['email'], 'password': admin['password']}), 200)
        if login[2].get('Cache-Control') != 'no-store':
            raise RuntimeError('Login response missing no-store policy')
        access = login[1]['data']['access_token']
        refresh = login[1]['data']['refresh_token']
        expect('protected application list', request('apps', token=access), 200)
        if sys.argv[1:] == ['--prepare-pilot']:
            project = '63677e77-2661-43b8-8253-35c1134bcc60'
            profile = expect('administrator profile', request('profile', token=access), 200)[1]['data']
            grant_path = f"admin/apps/{project}/staff-grants/{profile['id']}"
            grant = request(grant_path, token=access)
            if grant[0] == 404:
                expect('grant staging administrator application access', request(grant_path,
                    {'is_active': True}, token=access, method='PUT',
                    extra_headers={'If-None-Match': '*'}), 201)
            elif grant[0] != 200 or not grant[1]['data']['is_active']:
                raise RuntimeError('Existing application grant requires reconciliation')
            target = command(['docker', 'exec', 'hello-staging-postgres', 'psql', '-X',
                '-U', 'hello_staging', '-d', 'hello_staging', '-At', '-v', 'ON_ERROR_STOP=1',
                '-c', "SELECT u.id FROM users u JOIN applications a ON a.id=u.application_id "
                      "WHERE a.public_id='63677e77-2661-43b8-8253-35c1134bcc60' "
                      "AND u.external_id='staging-android-smoke-haidar' AND u.status='ACTIVE';"]).decode().strip()
            if not target.isdecimal() or int(target) < 1:
                raise RuntimeError('Expected one synthetic staging target')
            record = Path.home() / 'hello-staging-provision/pilot-campaign.json'
            if record.exists():
                campaign = json.loads(record.read_text())
                if campaign['application_id'] != project or campaign['target_user_id'] != target:
                    raise RuntimeError('Existing pilot differs from the intended scope')
                detail = expect('existing pilot draft', request(
                    f"apps/{project}/campaigns/{campaign['campaign_id']}", token=access), 200)
                if detail[1]['data']['status'] != 'DRAFT':
                    raise RuntimeError('Pilot already left DRAFT; do not create another')
            else:
                title = 'Hello staging'
                body = "Staging push test to Haidar's Android phone."
                created = expect('one-device campaign draft', request(f'apps/{project}/campaigns',
                    {'title': title, 'body': body, 'targets': [{'type': 'INDIVIDUAL', 'id': target}]},
                    token=access), 201)[1]['data']
                if created['status'] != 'DRAFT':
                    raise RuntimeError('Unexpected campaign creation state')
                campaign = {'application_id': project, 'campaign_id': created['id'],
                            'target_user_id': target, 'title': title, 'body': body,
                            'status': 'DRAFT', 'idempotency_key': str(uuid.uuid4())}
                record.write_text(json.dumps(campaign, indent=2) + '\n')
            print('Prepared DRAFT pilot campaign ' + campaign['campaign_id'] + '; no send requested.', flush=True)
        if sys.argv[1:] == ['--provision-app']:
            listed = expect('staging registry lookup',
                request('admin/apps?key=hello-staging', token=access), 200)
            items = listed[1]['data']['items']
            if not items:
                created = expect('create provisioning application', request('admin/apps',
                    {'key': 'hello-staging', 'name': 'Hello Staging'}, token=access), 201)
                app = created[1]['data']
            elif len(items) == 1:
                app = items[0]
            else:
                raise RuntimeError('Unexpected duplicate staging registry entries')
            if app['key'] != 'hello-staging' or app['status'] != 'PROVISIONING':
                raise RuntimeError('Staging application has unexpected lifecycle state')
            print('Staging application: ' + app['id'] + ' (PROVISIONING)', flush=True)
        rotated = expect('refresh rotation', request('auth/refresh', {'refresh_token': refresh}), 200)
        access = rotated[1]['data']['access_token']
        refresh = rotated[1]['data']['refresh_token']
        expect('rotated access token', request('apps', token=access), 200)
        expect('logout', request('auth/logout', {'refresh_token': refresh}), 200)
        refresh = None
        expect('logout invalidates access', request('apps', token=access), 401)
    finally:
        if refresh:
            request('auth/logout', {'refresh_token': refresh})
    print('Authentication smoke passed. No credentials or tokens printed.')


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        # HTTP/client exceptions can carry request material; only our assertion
        # diagnostics contain reviewed, bounded text.
        if type(error) is RuntimeError:
            raise SystemExit(str(error))
        raise SystemExit('Authentication smoke failed; inspect service diagnostics privately.')
