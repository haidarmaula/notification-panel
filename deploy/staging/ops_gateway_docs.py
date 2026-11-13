"""One-time private gateway extension; no application/TLS credential changes."""
import json
import subprocess
import ssl
import time
import urllib.error
import urllib.request
from ops_common import ROOT, SEED, inspect, read_container, run, save, atomic
from ops_docs import VOLUME

MARKER='HELLO_API_DOCS'
BLOCK='''    # HELLO_API_DOCS: public reference files only, in a separate readonly volume.
    location = /docs { return 308 /docs/; }
    location = /docs/ {
      rewrite ^ /docs/index.html last;
    }
    location = /docs/index.html {
      alias /api-docs/current/index.html;
      default_type text/html;
      add_header Cache-Control "no-store" always;
      add_header X-Content-Type-Options "nosniff" always;
      add_header Referrer-Policy "no-referrer" always;
      add_header Content-Security-Policy "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'" always;
    }
    location ^~ /docs/releases/ {
      alias /api-docs/releases/;
      autoindex off;
      add_header Cache-Control "public, max-age=31536000, immutable" always;
      add_header X-Content-Type-Options "nosniff" always;
      add_header Referrer-Policy "no-referrer" always;
      add_header Content-Security-Policy "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'" always;
    }
    location ^~ /docs/ { return 404; }
'''


def write_config(data):
    script="""set -eu
umask 027
test ! -L /assets/nginx.conf
config_temp=$(mktemp /assets/.nginx-docs.XXXXXX)
trap 'rm -f "$config_temp"' EXIT
cat > "$config_temp"
chown 0:65532 "$config_temp"
chmod 640 "$config_temp"
mv -f "$config_temp" /assets/nginx.conf
"""
    run(['docker','run','--rm','-i','--network','none','--read-only','--user','0:0','--cap-drop','ALL','--cap-add','CHOWN',
        '--security-opt','no-new-privileges','--mount','type=bind,src=/etc/hello/gateway-assets,dst=/assets',
        '--entrypoint','sh',SEED,'-c',script],data)


def gateway_args(image):
    return ['--network','host','--user','65532:65532','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges',
        '--pids-limit','128','--memory','128m','--cpus','.5','--tmpfs','/tmp:rw,noexec,nosuid,size=32m,uid=65532,gid=65532',
        '--mount','type=bind,src=/etc/hello/gateway-assets,dst=/gateway,readonly','--mount',f'type=volume,src={VOLUME},dst=/api-docs,readonly',
        '--entrypoint','nginx',image]


def alive(name):return subprocess.run(['docker','inspect',name],capture_output=True).returncode==0


def api_ready():
    ca=read_container('hello-staging-gateway','/gateway/server-ca.crt').decode()
    client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=ssl.create_default_context(cadata=ca)))
    for _ in range(10):
        try:client.open('https://10.1.16.109:8443/api/v1/apps',timeout=3)
        except urllib.error.HTTPError as error:
            if error.code==401:return
        except (OSError,urllib.error.URLError):pass
        time.sleep(.5)
    raise RuntimeError('TLS gateway/API authentication probe failed.')


def recover():
    path=ROOT/'docs-gateway-journal.json'
    if not path.exists():return
    journal=json.loads(path.read_text())
    if journal['phase']=='committed':return
    old=ROOT/'gateway-before-docs.conf'
    if not old.exists():raise RuntimeError('Original gateway configuration is missing.')
    write_config(old.read_bytes())
    backup='hello-staging-gateway-before-docs'
    if alive(backup):
        if alive('hello-staging-gateway'):run(['docker','rm','-f','hello-staging-gateway'])
        run(['docker','rename',backup,'hello-staging-gateway'])
        run(['docker','start','hello-staging-gateway'])
    else:
        if not inspect('hello-staging-gateway')['State']['Running']:
            run(['docker','start','hello-staging-gateway'])
        else:run(['docker','exec','hello-staging-gateway','nginx','-c','/gateway/nginx.conf','-s','reload'])
    api_ready();(ROOT/'docs-enabled.json').unlink(missing_ok=True);path.unlink()


def configure():
    recover()
    name='hello-staging-gateway';old=inspect(name)
    config=read_container(name,'/gateway/nginx.conf')
    mounted=any(m.get('Name')==VOLUME and m['Destination']=='/api-docs' and not m['RW'] for m in old['Mounts'])
    if MARKER.encode() in config and mounted:
        api_ready();save(ROOT/'docs-enabled.json',{'volume':VOLUME,'route':'/docs/'})
        return {'docs_gateway':'already enabled'}
    if MARKER.encode() in config or mounted:raise RuntimeError('Partial/foreign documentation gateway state requires inspection.')
    h=old['HostConfig']
    if h['NetworkMode']!='host' or not h['ReadonlyRootfs'] or h.get('CapDrop')!=['ALL'] or h.get('CapAdd') or old['Config']['User']!='65532:65532' or len(old['Mounts'])!=1 or old['Mounts'][0]['Destination']!='/gateway' or old['Mounts'][0].get('Source')!='/etc/hello/gateway-assets' or old['Mounts'][0]['RW']:raise RuntimeError('Unexpected gateway runtime; refuse replacement.')
    text=config.decode()
    if text.count('    location / {')!=1 or text.count('http {')!=1:raise RuntimeError('Unexpected gateway configuration layout.')
    text=text.replace('http {','http {\n  include /etc/nginx/mime.types;',1).replace('    location / {',BLOCK+'    location / {',1)
    backup='hello-staging-gateway-before-docs'
    if alive(backup):raise RuntimeError('Existing gateway backup requires inspection.')
    atomic(ROOT/'gateway-before-docs.conf',config)
    save(ROOT/'docs-gateway-journal.json',{'phase':'prepared'})
    try:
        run(['docker','volume','create',VOLUME]);write_config(text.encode())
        run(['docker','run','--rm',*gateway_args(old['Image']),'-c','/gateway/nginx.conf','-t'])
        run(['docker','stop','--time','15',name]);run(['docker','rename',name,backup])
        run(['docker','run','-d','--name',name,'--restart','unless-stopped','--log-opt','max-size=10m','--log-opt','max-file=3',*gateway_args(old['Image']),'-c','/gateway/nginx.conf','-g','daemon off;'])
        api_ready()
        save(ROOT/'docs-gateway-journal.json',{'phase':'committed'})
        save(ROOT/'docs-enabled.json',{'volume':VOLUME,'route':'/docs/'})
    except BaseException:
        recover();raise
    return {'docs_gateway':'enabled','path':'https://10.1.16.109:8443/docs/','network_access':'existing private gateway/firewall policy preserved'}
