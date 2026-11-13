"""Public-only documentation artifacts, atomic activation and rollback pointers."""
import hashlib
import io
import json
from pathlib import PurePosixPath
import re
import subprocess
import tarfile
import urllib.error
import urllib.request
import ssl
import uuid
from ops_common import ROOT, SEED, run, read_container

VOLUME='hello-staging-api-docs'


def helper(*args,data=None,write=False):
    return run(['docker','run','--rm','-i','--network','none','--read-only','--user','0:0','--cap-drop','ALL',
        *(['--cap-add','CHOWN'] if write else []),'--security-opt','no-new-privileges',
        '--mount',f'type=volume,src={VOLUME},dst=/docs'+('' if write else ',readonly'),
        '--entrypoint',args[0],SEED,*args[1:]],data)


def exists():return subprocess.run(['docker','volume','inspect',VOLUME],capture_output=True).returncode==0


def current():
    if not (ROOT/'docs-enabled.json').exists():return None
    if not exists():raise RuntimeError('Documentation volume missing.')
    data=helper('sh','-c','if [ -L /docs/current ]; then readlink /docs/current; elif [ -e /docs/current ]; then exit 1; fi').decode().strip()
    if not data:return None
    if not re.fullmatch(r'releases/[a-z0-9][a-z0-9-]{0,63}',data):raise RuntimeError('Unexpected docs pointer; refuse activation.')
    return data.split('/')[1]


def validate_bundle(data,digest,release):
    if not re.fullmatch('[a-z0-9][a-z0-9-]{0,63}',release) or hashlib.sha256(data).hexdigest()!=digest:raise ValueError('Documentation artifact identity mismatch.')
    if len(data)>32*1024**2:raise ValueError('Documentation artifact too large.')
    with tarfile.open(fileobj=io.BytesIO(data)) as tar:
        seen=set();files={}
        for member in tar.getmembers():
            name=PurePosixPath(member.name)
            if not member.isfile() or name.is_absolute() or '..' in name.parts or str(name)!=member.name or member.name in seen or member.size>16*1024**2:raise ValueError('Unsafe documentation archive member.')
            seen.add(member.name);files[member.name]=tar.extractfile(member).read()
        if sum(map(len,files.values()))>32*1024**2:raise ValueError('Expanded documentation exceeds limit.')
    manifest=json.loads(files['manifest.json'])
    if manifest['release']!=release or set(files)!={*manifest['sha256'],'manifest.json'}:raise ValueError('Documentation file inventory mismatch.')
    for name,digest in manifest['sha256'].items():
        if hashlib.sha256(files[name]).hexdigest()!=digest:raise ValueError('Documentation file digest mismatch.')
    if f'/docs/releases/{release}/app.js'.encode() not in files['index.html']:raise ValueError('Documentation assets must be release-specific.')
    return files


def publish(release,digest):
    if not (ROOT/'docs-enabled.json').exists():raise RuntimeError('Bootstrap the private /docs gateway before deploying documentation.')
    data=(ROOT/'releases'/release/'api-docs.tar').read_bytes();files=validate_bundle(data,digest,release)
    present=helper('sh','-c','if [ -e /docs/releases/$1 ]; then test -f /docs/releases/$1/manifest.json && cat /docs/releases/$1/manifest.json; fi','sh',release)
    if present:
        if present!=files['manifest.json']:raise RuntimeError('Immutable documentation release conflict.')
        return release
    token=uuid.uuid4().hex
    out=io.BytesIO()
    with tarfile.open(fileobj=out,mode='w') as tar:
        for name,content in files.items():
            info=tarfile.TarInfo('incoming/'+token+'/'+name);info.size=len(content);info.mode=0o644;info.uid=0;info.gid=65532;tar.addfile(info,io.BytesIO(content))
    helper('tar','-xpf','-','-C','/docs',data=out.getvalue(),write=True)
    helper('sh','-c','mkdir -p /docs/releases && test ! -e /docs/releases/$2 && mv /docs/incoming/$1 /docs/releases/$2','sh',token,release,write=True)
    return release


def switch(release):
    if release is None and not (ROOT/'docs-enabled.json').exists():return
    if not exists():
        if release is None:return
        raise RuntimeError('Documentation volume is missing.')
    if release is None:
        helper('sh','-c','rm -f /docs/current',write=True);return
    if not re.fullmatch('[a-z0-9][a-z0-9-]{0,63}',release):raise RuntimeError('Unsafe documentation pointer.')
    helper('sh','-c','test -f /docs/releases/$1/manifest.json && rm -f /docs/current.next && ln -s releases/$1 /docs/current.next && mv -Tf /docs/current.next /docs/current','sh',release,write=True)


def verify(release):
    if current()!=release:raise RuntimeError('Documentation pointer differs from release.')
    if release is None:return
    ca=read_container('hello-staging-gateway','/gateway/server-ca.crt').decode()
    client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=ssl.create_default_context(cadata=ca)))
    def get(path):
        with client.open('https://10.1.16.109:8443'+path,timeout=5) as response:
            if response.status!=200:raise RuntimeError('Documentation gateway probe failed.')
            return response.read(16*1024**2)
    html=get('/docs/')
    if f'data-release="{release}"'.encode() not in html:raise RuntimeError('Gateway serves a different documentation index.')
    base='/docs/releases/'+release+'/'
    manifest=json.loads(get(base+'manifest.json'))
    if manifest['release']!=release:raise RuntimeError('Gateway documentation manifest differs.')
    for name,digest in manifest['sha256'].items():
        if hashlib.sha256(get(base+name)).hexdigest()!=digest:raise RuntimeError('Gateway documentation asset digest differs.')
