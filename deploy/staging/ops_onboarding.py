"""Enable the single reviewed smoke onboarding profile on the staging VM."""
import base64
import io
import json
import secrets
import ssl
import subprocess
import tarfile
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

from ops_backup import backup
from ops_common import ROOT, SEED, db, inspect, read_container, run, save, state, write_policy
from ops_health import probe

PROFILE = '1ed36b4f-fc83-4891-be84-cbd06f833ba6'
PROJECT = 'push-notifications-stagi-a6a41'
IDENTITY = 'spiffe://hello-staging/onboarding-api'
PROVISIONER = 'hello-staging-provisioner'
CREDENTIALS = 'hello-staging-onboarding-credentials'
POLICY = 'hello-staging-provisioner-policy'
SETUP = ROOT / 'onboarding' / 'material.json'
URL = 'https://localhost:18543'
BASE = ['docker', 'run', '--rm', '--network', 'none', '--read-only', '--user', '0:0',
        '--cap-drop', 'ALL', '--cap-add', 'CHOWN', '--security-opt', 'no-new-privileges']


def containers(name):
    return subprocess.run(['docker', 'container', 'inspect', name], capture_output=True).returncode == 0


def source(grants):
    if not (grants / 'server-grants.sql').is_file():
        raise RuntimeError('Signed release is missing server grant definitions.')
    return (grants / 'onboarding-grants.sql').read_text()


def release():
    journal = json.loads((ROOT / 'deployment.json').read_text())
    cd = json.loads((ROOT / 'cd/status.json').read_text())
    if journal['phase'] != 'committed' or cd['phase'] != 'deployed' or journal['release'] != cd['release']:
        raise RuntimeError('Onboarding requires the current signed staging deployment.')
    directory = ROOT / 'releases' / journal['release']
    manifest = json.loads((directory / 'release.json').read_text())
    if manifest['revision'] != cd['sha'] or manifest['images']['server'] != inspect('hello-staging-server')['Image']:
        raise RuntimeError('Signed staging release and running server differ.')
    return directory, manifest


def material():
    if SETUP.exists():
        return json.loads(SETUP.read_text())
    with tempfile.TemporaryDirectory(prefix='hello-onboarding-tls-') as temp:
        root = Path(temp)
        ca, key = root / 'ca.crt', root / 'ca.key'
        run(['openssl','req','-x509','-newkey','rsa:3072','-nodes','-days','365',
             '-subj','/CN=hello-staging-onboarding-ca','-addext','basicConstraints=critical,CA:TRUE',
             '-addext','keyUsage=critical,keyCertSign,cRLSign','-keyout',str(key),'-out',str(ca)])
        def issue(name, usage, san):
            crt, csr, secret = (root / (name + suffix) for suffix in ('.crt','.csr','.key'))
            extensions = root / (name + '.ext')
            extensions.write_text(f'basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage={usage}\nsubjectAltName={san}\n')
            run(['openssl','req','-new','-newkey','rsa:2048','-nodes','-subj',f'/CN={name}',
                 '-keyout',str(secret),'-out',str(csr)])
            run(['openssl','x509','-req','-in',str(csr),'-CA',str(ca),'-CAkey',str(key),'-days','365',
                 '-set_serial',str(secrets.randbits(120)+1),'-extfile',str(extensions),'-out',str(crt)])
            return crt.read_text(),secret.read_text()
        server, server_key = issue('hello-staging-provisioner','serverAuth','DNS:localhost')
        client, client_key = issue('hello-staging-onboarding-client','clientAuth','URI:'+IDENTITY)
        data={'server':server,'server_key':server_key,'client':client,'client_key':client_key,
              'ca':ca.read_text(),'database_password':secrets.token_hex(32),
              'fingerprint_key':base64.b64encode(secrets.token_bytes(32)).decode()}
        save(SETUP,data)
        return data


def roles(password, grants):
    if len(password)!=64 or any(c not in '0123456789abcdef' for c in password):
        raise RuntimeError('Invalid private onboarding login material.')
    db(f"""BEGIN;
    DO $$ BEGIN
      IF NOT EXISTS(SELECT FROM pg_roles WHERE rolname='hello_onboarding_provisioner') THEN
        CREATE ROLE hello_onboarding_provisioner NOLOGIN;
      END IF;
      IF NOT EXISTS(SELECT FROM pg_roles WHERE rolname='hello_onboarding_verifier') THEN
        CREATE ROLE hello_onboarding_verifier NOLOGIN;
      END IF;
      IF NOT EXISTS(SELECT FROM pg_roles WHERE rolname='hello_staging_onboarding_provisioner') THEN
        CREATE ROLE hello_staging_onboarding_provisioner LOGIN PASSWORD '{password}';
      END IF;
    END $$;
    GRANT hello_onboarding_provisioner TO hello_staging_onboarding_provisioner;
    COMMIT;""")
    db(source(grants))


def provisioner_config(image, tls):
    metadata=json.loads(run(['docker','image','inspect',image]))[0]
    if metadata['Config']['User']!='0:0' or metadata['Config']['Entrypoint']!=['/provisioner'] or metadata['Os']!='linux':
        raise RuntimeError('Unexpected signed credential provisioner image.')
    password=tls['database_password']
    config={'listen_address':'127.0.0.1:18543','credential_root':'/var/lib/hello/credentials',
            'database_url':f'postgresql://hello_staging_onboarding_provisioner:{password}@127.0.0.1:5432/hello_staging?sslmode=verify-full&sslrootcert=/etc/hello/postgres-ca.crt&connect_timeout=3',
            'ingress_identity':IDENTITY,'environment':'staging','target_project_id':PROJECT,'ca_pem':tls['ca'],
            'server_certificate_pem':tls['server'],'server_private_key_pem':tls['server_key']}
    ca=run([*BASE,'--mount','type=bind,src=/etc/hello/postgres-ca.crt,dst=/ca.pem,readonly',
            '--entrypoint','cat',SEED,'/ca.pem'])
    for name in (POLICY,CREDENTIALS):
        run(['docker','volume','create',name])
    run([*BASE,'--mount',f'type=volume,src={CREDENTIALS},dst=/credentials',
         '--entrypoint','install',SEED,'-d','-o','0','-g','65532','-m','0750','/credentials'])
    buffer=io.BytesIO()
    with tarfile.open(fileobj=buffer,mode='w') as archive:
        for name,data,mode in [('provisioner.json',json.dumps(config).encode(),0o640),('postgres-ca.crt',ca,0o644)]:
            entry=tarfile.TarInfo(name); entry.uid=0; entry.gid=65532; entry.mode=mode; entry.size=len(data)
            archive.addfile(entry,io.BytesIO(data))
    run([*BASE,'-i','--mount',f'type=volume,src={POLICY},dst=/etc/hello',
         '--entrypoint','tar',SEED,'-xpf','-','-C','/etc/hello'],buffer.getvalue())


def check_provisioner(tls):
    with tempfile.TemporaryDirectory(prefix='hello-onboarding-probe-') as temp:
        directory=Path(temp)
        cert,key=directory/'client.crt',directory/'client.key'
        cert.write_text(tls['client']); key.write_text(tls['client_key'])
        ctx=ssl.create_default_context(cadata=tls['ca'])
        ctx.load_cert_chain(cert,key)
        browser=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=ctx))
        for attempt in range(10):
            try:
                with browser.open(URL+'/v1/stages',timeout=2):
                    raise RuntimeError('Unexpected provisioner probe response.')
            except urllib.error.HTTPError as error:
                if error.code==404:
                    return
                raise RuntimeError('Unexpected provisioner probe response.') from None
            except urllib.error.URLError:
                if attempt==9:raise RuntimeError('The staging credential provisioner did not become ready.') from None
                time.sleep(.5)

def check_server():
    for attempt in range(20):
        try:
            probe('server')
            return
        except (RuntimeError, OSError):
            if attempt==19:
                raise
            time.sleep(.5)

def enable():
    current=state()
    if current['schema']!={'version':61,'dirty':False} or current['sealed'] or not current['migration_hold'] or any(p['dispatch_mode']!='HELD' for p in current['policies']):
        raise RuntimeError('Staging onboarding requires a clean schema with runtime and dispatch held.')
    directory,manifest=release()
    image=manifest['candidate_images']['provisioner']
    old=read_container('hello-staging-server','/etc/hello/server.json')
    server=json.loads(old)
    if server.get('onboarding'):
        if not containers(PROVISIONER) or server['onboarding']['profiles'][0]['id']!=PROFILE:
            raise RuntimeError('An unrelated onboarding policy is already active.')
        check_provisioner(material())
        return {'onboarding':'ready','profile':PROFILE}
    backup()
    tls=material()
    grants=directory/'source'/'deploy'/'postgres'
    roles(tls['database_password'],grants)
    db((grants/'server-grants.sql').read_text())
    db((grants/'staging-onboarding-verifier-scope.sql').read_text())
    provisioner_config(image,tls)
    if not containers(PROVISIONER):
        run(['docker','run','-d','--name',PROVISIONER,'--restart','unless-stopped','--network','host',
             '--read-only','--cap-drop','ALL','--cap-add','CHOWN','--security-opt','no-new-privileges',
             '--pids-limit','32','--memory','128m','--cpus','0.5','--log-opt','max-size=5m','--log-opt','max-file=2',
             '--mount',f'type=volume,src={POLICY},dst=/etc/hello,readonly',
             '--mount',f'type=volume,src={CREDENTIALS},dst=/var/lib/hello/credentials',image])
    else:
        if inspect(PROVISIONER)['Image']!=image:
            raise RuntimeError('An existing provisioner uses a different image.')
        run(['docker','start',PROVISIONER])
    check_provisioner(tls)
    server['onboarding']={'fingerprint_key':tls['fingerprint_key'],'fingerprint_key_version':1,
                         'provisioner_url':URL,'ca_pem':tls['ca'],'client_certificate_pem':tls['client'],
                         'client_private_key_pem':tls['client_key'],'profiles':[{'id':PROFILE,
                         'name':'Hello staging Android smoke','platforms':[{'platform':'ANDROID',
                         'application_identifiers':['com.example.hellofcmsmoke']}]}]}
    try:
        write_policy('server',json.dumps(server).encode())
        run(['docker','restart','--time','30','hello-staging-server'],timeout=45)
        check_server()
        if state()!=current:
            raise RuntimeError('Staging dispatch or runtime state changed unexpectedly.')
    except Exception:
        write_policy('server',old)
        run(['docker','restart','--time','30','hello-staging-server'],timeout=45)
        check_server()
        raise
    return {'onboarding':'ready','profile':PROFILE,'environment':'staging','firebase_project':PROJECT}
