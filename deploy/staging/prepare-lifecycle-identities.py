#!/usr/bin/env python3
"""Provision separate restricted lifecycle logins; do not issue or apply grants."""
import io
import json
import secrets
import socket
import subprocess
import tarfile

SEED = 'postgres@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d'
VOLUME = 'hello-staging-lifecycle-credentials'
ROLES = {'hello_lifecycle_reviewer_login': 'hello_application_lifecycle_issuer',
         'hello_lifecycle_executor_login': 'hello_application_lifecycle_operator'}


def command(args, data=None):
    result = subprocess.run(args, input=data, capture_output=True, timeout=30)
    if result.returncode:
        raise SystemExit('Lifecycle identity provisioning failed; no credential values printed.')
    return result.stdout


def main():
    if socket.gethostname() != 'srv-kafka':
        raise SystemExit('Run on srv-kafka.')
    owner = ['docker', 'exec', '-i', 'hello-staging-postgres', 'psql', '-X',
             '-U', 'hello_staging', '-d', 'hello_staging', '-v', 'ON_ERROR_STOP=1', '-At']
    if command(owner, b"SELECT count(*) FROM pg_roles WHERE rolname IN ('hello_lifecycle_reviewer_login','hello_lifecycle_executor_login');").strip() != b'0':
        raise SystemExit('Lifecycle logins already exist; inspect before reprovisioning.')
    if subprocess.run(['docker', 'volume', 'inspect', VOLUME], capture_output=True).returncode == 0:
        raise SystemExit('Lifecycle credential volume already exists; inspect before replacing.')
    passwords = {login: secrets.token_hex(32) for login in ROLES}
    archive = io.BytesIO()
    with tarfile.open(fileobj=archive, mode='w') as bundle:
        data = json.dumps(passwords).encode()
        entry = tarfile.TarInfo('credentials.json')
        entry.mode, entry.size = 0o600, len(data)
        bundle.addfile(entry, io.BytesIO(data))
    command(['docker', 'volume', 'create', '--label', 'hello.purpose=restricted-lifecycle-credentials', VOLUME])
    command(['docker', 'run', '--rm', '-i', '--network', 'none', '--read-only', '--user', '0:0',
             '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
             '--mount', f'type=volume,src={VOLUME},dst=/policy', '--entrypoint', 'tar', SEED,
             '-xpf', '-', '-C', '/policy'], archive.getvalue())
    sql = ['BEGIN;']
    for login, group in ROLES.items():
        sql.append(f"CREATE ROLE {login} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '{passwords[login]}';")
        sql.append(f'GRANT {group} TO {login};')
    sql.append('COMMIT;')
    command(owner, '\n'.join(sql).encode())
    print('Created distinct restricted lifecycle reviewer/executor logins.')
    print('Credentials stored root-only in a dedicated Docker volume; no lifecycle grant issued.')


if __name__ == '__main__':
    main()
