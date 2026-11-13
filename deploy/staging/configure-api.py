#!/usr/bin/env python3
"""Prepare protected staging API policies and bootstrap the initial administrator.

Run as root on srv-kafka after prepare-host.sh. No dispatch holds are released.
"""
import base64
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
from urllib.parse import quote, urlencode


ROOT = Path('/etc/hello')
TLS = ROOT / 'staging-tls'
ASSETS = ROOT / 'server-assets'
GATEWAY = ROOT / 'gateway-assets'
REVISION = '4695a2c9730740a9fb88e009fd2c00599edc9ba6'


def run(args, **kwargs):
    result = subprocess.run(args, capture_output=True, text=True, **kwargs)
    if result.returncode:
        # Commands may handle credentials; never echo arguments, input or output.
        raise SystemExit('Provisioning command failed; inspect the relevant service privately.')
    return result.stdout.strip()


def sql(statement):
    return run(['docker', 'exec', '-i', 'hello-staging-postgres', 'psql',
                '-X', '-U', 'hello_staging', '-d', 'hello_staging',
                '-v', 'ON_ERROR_STOP=1', '-At'], input=statement)


def store(path, content, group=65532, mode=0o640):
    if path.is_symlink():
        raise SystemExit('Refusing symlink destination.')
    fd, temporary = tempfile.mkstemp(dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.chown(temporary, 0, group)
        os.chmod(temporary, mode)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def key():
    return base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip('=')


def dsn(user, password):
    return (f'postgres://{user}:{quote(password, safe="")}@127.0.0.1:5432/hello_staging?'
            + urlencode({'sslmode': 'verify-full', 'sslrootcert': str(ASSETS / 'db-ca.crt'),
                         'connect_timeout': '5'}))


def main():
    if os.geteuid() != 0 or socket.gethostname() != 'srv-kafka':
        raise SystemExit('Run with sudo on srv-kafka.')
    os.umask(0o077)
    if sql("SELECT version::text || ':' || dirty::text FROM schema_migrations;") != '41:false':
        raise SystemExit('Expected clean migration 41.')
    if sql("SELECT count(*) FROM pg_roles WHERE rolname='hello_server' AND NOT rolsuper AND NOT rolcanlogin;") != '1':
        raise SystemExit('Dedicated server grant role is missing or unsafe.')
    for path in (ROOT, ASSETS, GATEWAY):
        if path.is_symlink():
            raise SystemExit('Refusing symlink directory.')
        path.mkdir(exist_ok=True)
        os.chown(path, 0, 65532)
        os.chmod(path, 0o750)
    store(ASSETS / 'db-ca.crt', (ROOT / 'postgres/server.crt').read_text())
    password_file = ASSETS / 'database-password'
    exists = sql("SELECT count(*) FROM pg_roles WHERE rolname='hello_server_login';") == '1'
    if exists and not password_file.exists():
        raise SystemExit('Existing server login has no managed password; refusing replacement.')
    if not password_file.exists():
        store(password_file, secrets.token_hex(32), group=0, mode=0o600)
    password = password_file.read_text().strip()
    if len(password) != 64 or any(c not in '0123456789abcdef' for c in password):
        raise SystemExit('Unexpected server password format.')
    if not exists:
        sql("BEGIN; CREATE ROLE hello_server_login LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS "
            f"PASSWORD '{password}'; GRANT hello_server TO hello_server_login; COMMIT;")
    elif sql("SELECT count(*) FROM pg_roles WHERE rolname='hello_server_login' AND NOT rolsuper "
             "AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT rolbypassrls "
             "AND pg_has_role(oid,'hello_server','member');") != '1':
        raise SystemExit('Existing server login has unexpected privileges.')
    policy_path = ROOT / 'server.json'
    if not policy_path.exists():
        staff = {'issuer': 'hello-staging'}
        for purpose in ('access', 'refresh'):
            staff[purpose] = {'audience': 'hello-staging-' + purpose,
                              'active_kid': purpose + '-v1',
                              'keys': [{'kid': purpose + '-v1', 'key': key()}]}
        policy = {'database_url': dsn('hello_server_login', password), 'api_key': key(),
                  'mobile_policy_file': '', 'staff_tokens': staff,
                  'ingress': {'endpoint': {'listen': '127.0.0.1:8080',
                              'server_certificate_pem': (TLS / 'api.crt').read_text(),
                              'server_key_pem': (TLS / 'api.key').read_text(),
                              'client_ca_pem': (TLS / 'gateway-client-ca.crt').read_text()},
                              'gateway_ips': ['127.0.0.1'], 'ip_per_minute': 120,
                              'auth_ip_per_minute': 8, 'auth_total_per_minute': 60,
                              'concurrent_requests': 16}}
        store(policy_path, json.dumps(policy))
    for filename in ('gateway.crt', 'gateway.key', 'gateway-client.crt', 'gateway-client.key', 'server-ca.crt'):
        store(GATEWAY / filename, (TLS / filename).read_text())
    store(GATEWAY / 'nginx.conf', '''worker_processes 1;
pid /tmp/nginx.pid;
error_log /dev/stderr warn;
events { worker_connections 256; }
http {
  access_log /dev/stdout;
  client_body_temp_path /tmp/client_body;
  proxy_temp_path /tmp/proxy;
  fastcgi_temp_path /tmp/fastcgi;
  uwsgi_temp_path /tmp/uwsgi;
  scgi_temp_path /tmp/scgi;
  server {
    listen 10.1.16.109:8443 ssl;
    server_name 10.1.16.109;
    ssl_certificate /gateway/gateway.crt;
    ssl_certificate_key /gateway/gateway.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    allow 10.0.0.0/8;
    allow 172.16.0.0/12;
    allow 192.168.0.0/16;
    deny all;
    client_max_body_size 2m;
    location / {
      proxy_pass https://127.0.0.1:8080;
      proxy_ssl_certificate /gateway/gateway-client.crt;
      proxy_ssl_certificate_key /gateway/gateway-client.key;
      proxy_ssl_trusted_certificate /gateway/server-ca.crt;
      proxy_ssl_verify on;
      proxy_ssl_server_name on;
      proxy_ssl_name localhost;
      proxy_set_header Host $host;
      proxy_set_header X-Forwarded-Proto https;
      proxy_set_header X-Forwarded-For $remote_addr;
      proxy_connect_timeout 5s;
      proxy_read_timeout 30s;
    }
  }
}
''')
    admin_file = ROOT / 'staging-admin.json'
    if not admin_file.exists():
        store(admin_file, json.dumps({'name': 'Haidar (Staging)',
              'email': 'haidarmaulareynaldy@gmail.com', 'password': key()}), group=0, mode=0o600)
    admin = json.loads(admin_file.read_text())
    bootstrap = {'database_url': dsn('hello_staging', (ROOT / 'postgres/password').read_text().strip()),
                 'admin_name': admin['name'], 'admin_email': admin['email'],
                 'admin_password': admin['password']}
    # Keep the privileged policy out of server mounts and delete after the one-shot job.
    bootstrap_path = ROOT / 'bootstrap.json'
    store(bootstrap_path, json.dumps(bootstrap))
    try:
        run(['docker', 'run', '--rm', '--network', 'host', '--read-only', '--cap-drop', 'ALL',
             '--security-opt', 'no-new-privileges', '--mount',
             f'type=bind,src={bootstrap_path},dst=/etc/hello/bootstrap.json,readonly',
             '--mount', f'type=bind,src={ASSETS}/db-ca.crt,dst={ASSETS}/db-ca.crt,readonly',
             'hello-bootstrap:' + REVISION], timeout=60)
    finally:
        bootstrap_path.unlink(missing_ok=True)
    print('Prepared API/gateway policies and bootstrapped staging administrator.')
    print('Administrator credentials: /etc/hello/staging-admin.json (root-only).')
    print('No persistent application services started; dispatch remains held.')


if __name__ == '__main__':
    main()
