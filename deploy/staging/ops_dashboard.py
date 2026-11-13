"""Journaled first dashboard activation from a signed, deployed staging artifact."""
import io
import json
from pathlib import Path
import secrets
import ssl
import subprocess
import tarfile
import tempfile
import time
import urllib.request
import urllib.error
from ops_common import ROOT, SEED, ROLES, inspect, read_container, run, save, atomic, state, lock
from ops_gateway_docs import write_config, api_ready
from ops_health import client

VOLUME='hello-staging-dashboard-policy'
ASSETS=('dashboard-client.crt','dashboard-client.key','dashboard-server-ca.crt')
MARKER='HELLO_DASHBOARD'


def container_exists(name):return subprocess.run(['docker','inspect',name],capture_output=True).returncode==0

def archive(files):
    out=io.BytesIO()
    with tarfile.open(fileobj=out,mode='w') as tar:
        for name,data in files.items():
            entry=tarfile.TarInfo(name);entry.uid=0;entry.gid=65532;entry.mode=0o640;entry.size=len(data);tar.addfile(entry,io.BytesIO(data))
    return out.getvalue()


def host_tls(name):
    if name not in ['server-ca.crt','gateway-client-ca.crt','gateway-client-ca.key']:raise RuntimeError('Unexpected TLS source.')
    return run(['docker','run','--rm','--network','none','--read-only','--user','0:0','--cap-drop','ALL','--security-opt','no-new-privileges',
        '--mount','type=bind,src=/etc/hello/staging-tls,dst=/tls,readonly','--entrypoint','cat',SEED,'/tls/'+name])


def write_assets(files):
    run(['docker','run','--rm','-i','--network','none','--read-only','--user','0:0','--cap-drop','ALL','--cap-add','CHOWN','--security-opt','no-new-privileges',
        '--mount','type=bind,src=/etc/hello/gateway-assets,dst=/assets','--entrypoint','tar',SEED,'-xpf','-','-C','/assets'],archive(files))


def make_material(server_policy):
    with tempfile.TemporaryDirectory(prefix='dashboard-tls-',dir=ROOT) as directory:
        d=Path(directory)
        def openssl(*args,data=None):return run(['openssl',*map(str,args)],data)
        def ca(name):
            openssl('req','-x509','-newkey','rsa:3072','-nodes','-days','365','-subj','/CN=hello-staging-'+name,'-addext','basicConstraints=critical,CA:TRUE','-addext','keyUsage=critical,keyCertSign,cRLSign',
                '-keyout',d/(name+'.key'),'-out',d/(name+'.crt'))
        def certificate(name,usage,authority,signing_key=None):
            openssl('req','-new','-newkey','rsa:3072','-nodes','-subj','/CN=hello-staging-'+name,'-keyout',d/(name+'.key'),'-out',d/(name+'.csr'))
            ext=d/(name+'.ext');ext.write_text('basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage='+usage+'\nsubjectAltName=DNS:localhost,IP:127.0.0.1\n')
            openssl('x509','-req','-in',d/(name+'.csr'),'-CA',d/(authority+'.crt'),'-CAkey','/dev/stdin' if signing_key else d/(authority+'.key'),
                '-set_serial',str(secrets.randbits(120)+1),'-days','365','-extfile',ext,'-out',d/(name+'.crt'),data=signing_key)
        ca('dashboard-server-ca');ca('dashboard-gateway-ca')
        certificate('dashboard-server','serverAuth','dashboard-server-ca')
        certificate('dashboard-client','clientAuth','dashboard-gateway-ca')
        (d/'api-client-ca.crt').write_bytes(host_tls('gateway-client-ca.crt'))
        certificate('dashboard-api-client','clientAuth','api-client-ca',host_tls('gateway-client-ca.key'))
        policy={'origin':'https://10.1.16.109:8443','upstream':'https://127.0.0.1:8080','upstream_server_name':'localhost',
            'api_key':server_policy['api_key'],'upstream_ca_pem':host_tls('server-ca.crt').decode(),
            'upstream_client_certificate_pem':(d/'dashboard-api-client.crt').read_text(),'upstream_client_key_pem':(d/'dashboard-api-client.key').read_text(),
            'max_sessions':1000,
            'ingress':{'endpoint':{'listen':'127.0.0.1:18443','server_certificate_pem':(d/'dashboard-server.crt').read_text(),
                'server_key_pem':(d/'dashboard-server.key').read_text(),'client_ca_pem':(d/'dashboard-gateway-ca.crt').read_text()},
                'gateway_ips':['127.0.0.1'],'ip_per_minute':240,'auth_ip_per_minute':8,'auth_total_per_minute':60,'concurrent_requests':64},
            'monitoring':{**server_policy['monitoring'],'listen':'127.0.0.1:19105'}}
        assets={name:(d/name).read_bytes() for name in ASSETS}
        return json.dumps(policy).encode(),assets


def routing(config):
    text=config.decode()
    if MARKER in text or text.count('    location / {')!=1 or 'proxy_pass https://127.0.0.1:8080;' not in text:raise RuntimeError('Unexpected gateway routing layout.')
    text=text.replace('    location / {','    location /api/v1/ {',1)
    position=text.rfind('\n  }\n}')
    if position<0:raise RuntimeError('Unexpected gateway server boundary.')
    block='''
    # HELLO_DASHBOARD: authenticated BFF; preserve API and documentation locations.
    location / {
      proxy_pass https://127.0.0.1:18443;
      proxy_ssl_certificate /gateway/dashboard-client.crt;
      proxy_ssl_certificate_key /gateway/dashboard-client.key;
      proxy_ssl_trusted_certificate /gateway/dashboard-server-ca.crt;
      proxy_ssl_verify on;
      proxy_ssl_server_name on;
      proxy_ssl_name localhost;
      proxy_set_header Host $host;
      proxy_set_header X-Forwarded-Proto https;
      proxy_set_header X-Forwarded-For $remote_addr;
      proxy_connect_timeout 5s;
      proxy_read_timeout 30s;
    }'''
    return (text[:position]+block+text[position:]).encode()


def gateway_reload():
    run(['docker','exec','hello-staging-gateway','nginx','-c','/gateway/nginx.conf','-t'])
    run(['docker','exec','hello-staging-gateway','nginx','-c','/gateway/nginx.conf','-s','reload'])
    api_ready()


def verify():
    with client().open('https://127.0.0.1:19105/readyz',timeout=5) as response:
        if response.status!=200:raise RuntimeError('Dashboard BFF is unready.')
    ca=read_container('hello-staging-gateway','/gateway/server-ca.crt').decode()
    browser=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=ssl.create_default_context(cadata=ca)))
    with browser.open('https://10.1.16.109:8443/login',timeout=5) as response:
        body=response.read(2*1024*1024)
        if response.status!=200 or b'<div id="root">' not in body or b'/assets/' not in body:raise RuntimeError('Dashboard HTML probe failed.')
    with browser.open('https://10.1.16.109:8443/bff/session',timeout=5) as response:
        data=json.load(response);cookie=response.headers.get('Set-Cookie','')
        if data.get('authenticated') is not False or not data.get('csrfToken') or 'Secure' not in cookie or 'HttpOnly' not in cookie or '__Host-hello_session=' not in cookie:raise RuntimeError('Dashboard secure session probe failed.')


def recover():
    path=ROOT/'dashboard-bootstrap.json'
    if not path.exists():return
    journal=json.loads(path.read_text())
    if journal['phase']=='committed':return
    write_config((ROOT/'gateway-before-dashboard.conf').read_bytes());gateway_reload()
    if container_exists('hello-staging-dashboard'):run(['docker','rm','-f','hello-staging-dashboard'])
    if subprocess.run(['docker','volume','inspect',VOLUME],capture_output=True).returncode==0:run(['docker','volume','rm',VOLUME])
    run(['docker','run','--rm','--network','none','--read-only','--user','0:0','--cap-drop','ALL','--security-opt','no-new-privileges',
        '--mount','type=bind,src=/etc/hello/gateway-assets,dst=/assets','--entrypoint','rm',SEED,'-f',*('/assets/'+name for name in ASSETS)])
    atomic(ROOT/'deployment.json',(ROOT/'deployment-before-dashboard.json').read_bytes())
    (ROOT/'dashboard-enabled.json').unlink(missing_ok=True);path.unlink()


def configure():
    recover()
    if (ROOT/'dashboard-enabled.json').exists():verify();return {'dashboard':'already enabled'}
    with lock(name='build.lock'):
        cd=json.loads((ROOT/'cd/status.json').read_text());previous=json.loads((ROOT/'deployment.json').read_text())
        if cd.get('phase')!='deployed' or previous.get('phase')!='committed' or cd.get('release')!=previous.get('release'):raise RuntimeError('Require a completed signed CD deployment before dashboard bootstrap.')
        artifact=json.loads((ROOT/'releases'/cd['release']/'release.json').read_text())
        if artifact.get('revision')!=cd['sha'] or not artifact.get('dashboard_image'):raise RuntimeError('Checked release lacks a dashboard image; deploy reviewed source first.')
        image=artifact['dashboard_image'];obj=json.loads(run(['docker','image','inspect',image]))[0]
        if obj['Config']['User']!='65532:65532' or obj['Config']['Entrypoint']!=['/dashboard','--deployment'] or obj['Os']!='linux' or obj['Architecture']!='amd64':raise RuntimeError('Unexpected dashboard image identity.')
        if container_exists('hello-staging-dashboard') or subprocess.run(['docker','volume','inspect',VOLUME],capture_output=True).returncode==0:raise RuntimeError('Existing dashboard resources require inspection.')
        config=read_container('hello-staging-gateway','/gateway/nginx.conf');candidate=routing(config)
        original_assets=run(['docker','exec','hello-staging-gateway','ls','/gateway']).decode().splitlines()
        if set(ASSETS)&set(original_assets):raise RuntimeError('Existing dashboard gateway identities require inspection.')
        policy,assets=make_material(json.loads(read_container('hello-staging-server','/etc/hello/server.json')))
        before=state();baseline='dashboard-baseline-'+cd['sha'][:12]+'-'+secrets.token_hex(4)
        atomic(ROOT/'gateway-before-dashboard.conf',config)
        atomic(ROOT/'deployment-before-dashboard.json',(ROOT/'deployment.json').read_bytes())
        journal={'phase':'prepared','before':before,'baseline':baseline,'source_release':cd['release'],'sha':cd['sha']};save(ROOT/'dashboard-bootstrap.json',journal)
        try:
            run(['docker','volume','create',VOLUME])
            run(['docker','run','--rm','-i','--network','none','--read-only','--user','0:0','--cap-drop','ALL','--cap-add','CHOWN','--security-opt','no-new-privileges',
                '--mount',f'type=volume,src={VOLUME},dst=/policy','--entrypoint','tar',SEED,'-xpf','-','-C','/policy'],archive({'dashboard.json':policy}))
            run(['docker','run','-d','--name','hello-staging-dashboard','--restart','unless-stopped','--network','host','--read-only','--user','65532:65532',
                '--cap-drop','ALL','--security-opt','no-new-privileges','--pids-limit','128','--memory','256m','--cpus','.5','--log-opt','max-size=10m','--log-opt','max-file=3',
                '--mount',f'type=volume,src={VOLUME},dst=/etc/hello,readonly',image])
            for attempt in range(20):
                try:
                    with client().open('https://127.0.0.1:19105/readyz',timeout=3) as response:
                        if response.status==200:break
                except (OSError,urllib.error.URLError):pass
                time.sleep(.5)
            else:raise RuntimeError('Dashboard readiness deadline exceeded.')
            write_assets(assets);write_config(candidate);gateway_reload();verify()
            if state()!=before:raise RuntimeError('Dispatch policy changed during bootstrap; preserve the incident state.')
            images={role:inspect('hello-staging-'+role)['Image'] for role in ROLES};images['dashboard']=image
            import ops_docs
            docs=ops_docs.current()
            save(ROOT/'releases'/baseline/'release.json',{'release':baseline,'revision':cd['sha'],'schema':before['schema'],'images':images,'docs_release':docs,'kind':'dashboard-bootstrap-baseline'})
            save(ROOT/'deployment.json',{'phase':'baseline','release':baseline,'before':before,'new_docs':docs,'old_docs':docs,'old_images':images,'completed':time.time()})
            save(ROOT/'dashboard-enabled.json',{'volume':VOLUME,'sha':cd['sha'],'baseline':baseline})
            journal['phase']='committed';save(ROOT/'dashboard-bootstrap.json',journal)
        except BaseException:
            recover();raise
    return {'dashboard':'enabled','url':'https://10.1.16.109:8443/login','source_sha':cd['sha'],'rollback':'baseline established; subsequent CD includes five roles','sending_policy':'unchanged'}
