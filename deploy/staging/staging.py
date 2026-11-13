#!/usr/bin/env python3
"""Workstation controller for the single, explicitly configured staging VM."""
import argparse
import hashlib
import io
import tarfile
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import time

HERE=Path(__file__).resolve().parent
REPO=HERE.parent.parent
LOCAL=Path.home()/'.config/hello/staging/operations'
REMOTE='/home/haidar/hello-staging-ops'
SSH=['ssh','-o','BatchMode=yes','-o','ConnectTimeout=10','-o','StrictHostKeyChecking=yes','kafka-vps']


def call(args,data=None,timeout=120):
    r=subprocess.run(args,input=data,capture_output=True,timeout=timeout)
    if r.returncode:raise RuntimeError((r.stderr.decode(errors='replace')[-1000:] or 'Operation failed; inspect protected log.'))
    return r.stdout


def remote(command,*args,data=None,timeout=120):
    cmd=['/usr/bin/python3',REMOTE+'/run.py',command,*map(str,args)]
    return call(SSH+[shlex.join(cmd)],data,timeout)


def install():
    LOCAL.mkdir(mode=0o700,parents=True,exist_ok=True);os.chmod(LOCAL,0o700)
    key=LOCAL/'backup-private.pem';cert=LOCAL/'backup-recipient.crt'
    if key.exists()!=cert.exists():raise RuntimeError('Incomplete local backup identity; inspect without overwriting keys.')
    if not key.exists():
        call(['openssl','req','-x509','-newkey','rsa:3072','-nodes','-days','3650','-subj','/CN=hello-staging-backup-recipient',
              '-keyout',str(key),'-out',str(cert)])
        os.chmod(key,0o600);os.chmod(cert,0o600)
    if key.is_symlink() or key.stat().st_mode&0o077:raise RuntimeError('Backup private key must be a private regular file.')
    call(SSH+['umask 077; mkdir -p '+REMOTE])
    files={name:(HERE/name).read_bytes() for name in ['ops.py','ops_common.py','ops_backup.py','ops_health.py','ops_release.py','ops_setup.py','ops_cd.py','cd_protocol.py']}
    version=hashlib.sha256(b''.join(name.encode()+files[name] for name in sorted(files))).hexdigest()[:20]
    payload=io.BytesIO()
    with tarfile.open(fileobj=payload,mode='w') as tar:
        for name,data in files.items():
            entry=tarfile.TarInfo(name);entry.mode=0o600;entry.size=len(data);tar.addfile(entry,io.BytesIO(data))
    installer="""import fcntl,io,os,sys,tarfile
from pathlib import Path
root=Path('/home/haidar/hello-staging-ops');os.umask(0o077)
with (root/'operations.lock').open('a') as lock:
 fcntl.flock(lock,fcntl.LOCK_EX)
 version=root/'code'/sys.argv[1];version.mkdir(parents=True,exist_ok=True)
 with tarfile.open(fileobj=io.BytesIO(sys.stdin.buffer.read()),mode='r:') as tar:
  for m in tar.getmembers():
   if not m.isfile() or '/' in m.name or not m.name.endswith('.py'):raise RuntimeError('Unsafe operations archive')
   target=version/m.name
   data=tar.extractfile(m).read()
   if target.exists() and target.read_bytes()!=data:raise RuntimeError('Immutable version conflict')
   if not target.exists():target.write_bytes(data)
 launcher=bytes.fromhex(sys.argv[2]).decode()
 (root/'run.py.new').write_text(launcher);os.replace(root/'run.py.new',root/'run.py')
 (root/'active.new').unlink(missing_ok=True);os.symlink(version,root/'active.new');os.replace(root/'active.new',root/'active')
"""
    launcher="import os,sys\nfrom pathlib import Path\np=(Path('/home/haidar/hello-staging-ops/active').resolve()/'ops.py')\nos.execv('/usr/bin/python3',['python3',str(p),*sys.argv[1:]])\n"
    call(SSH+[shlex.join(['python3','-c',installer,version,launcher.encode().hex()])],payload.getvalue())
    fingerprint=hashlib.sha256(cert.read_bytes()).hexdigest()
    check=call(SSH+[shlex.join(['python3','-c',"from pathlib import Path; import hashlib; p=Path('"+REMOTE+"/backup-recipient.crt'); print(hashlib.sha256(p.read_bytes()).hexdigest() if p.exists() else 'missing')"])]).decode().strip()
    if check not in ['missing',fingerprint]:raise RuntimeError('Remote backup recipient differs; do not rotate without recovery review.')
    call(['scp','-q',str(cert),'kafka-vps:'+REMOTE+'/backup-recipient.crt'])
    print('Operations installed. Backup private key stays only on this workstation.')


def fetch_backup():
    result=json.loads(remote('backup',timeout=180))
    name=result['name']
    if not re.fullmatch(r'[0-9]{8}T[0-9]{6}Z-[a-f0-9]{8}',name):raise RuntimeError('Unexpected backup identifier.')
    folder=LOCAL/'backups';folder.mkdir(mode=0o700,parents=True,exist_ok=True)
    target=folder/(name+'.cms')
    call(['scp','-q','kafka-vps:'+REMOTE+'/backups/'+name+'.cms',str(target)])
    os.chmod(target,0o600)
    if hashlib.sha256(target.read_bytes()).hexdigest()!=result['sha256']:raise RuntimeError('Off-host ciphertext checksum mismatch.')
    (folder/(name+'.json')).write_text(json.dumps(result,indent=2)+'\n')
    print('Encrypted off-host copy verified:',target)
    return target,result


def main():
    os.umask(0o077)
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('command',choices=['install','diagnostics','schedule','health','backup','restore-check','capture','deploy','rollback','recover','status','health-drill'])
    p.add_argument('--release');p.add_argument('--ref');p.add_argument('--schema',type=int)
    a=p.parse_args()
    if a.command=='install':install();return
    if a.command=='backup':fetch_backup();return
    if a.command=='restore-check':
        file,manifest=fetch_backup()
        archive=call(['openssl','cms','-decrypt','-binary','-inform','DER','-in',str(file),'-recip',str(LOCAL/'backup-recipient.crt'),'-inkey',str(LOCAL/'backup-private.pem')])
        print(remote('restore-check',data=archive,timeout=180).decode());return
    if a.command=='deploy' and a.ref:
        if a.schema is None:raise RuntimeError('Explicit reviewed --schema version required; no automatic migrations.')
        revision=call(['git','-C',str(REPO),'rev-parse',a.ref+'^{commit}']).decode().strip()
        if not re.fullmatch('[0-9a-f]{40}',revision):raise RuntimeError('Invalid commit.')
        dirty=call(['git','-C',str(REPO),'status','--porcelain','--','Dockerfile','Dockerfile.dockerignore','go.mod','go.sum','cmd','internal','pkg'])
        if dirty:raise RuntimeError('Uncommitted runtime changes are not included in a commit artifact; commit them first.')
        release=a.release or 'git-'+revision[:12]+'-'+str(int(time.time()))
        source=call(['git','-C',str(REPO),'archive','--format=tar',revision])
        print(remote('prepare','--release',release,'--revision',revision,'--schema',a.schema,data=source,timeout=1900).decode())
        print(remote('deploy','--release',release,timeout=300).decode());return
    args=[]
    if a.command in ['capture','deploy']:
        if not a.release:raise RuntimeError('--release or deploy --ref required.')
        if not re.fullmatch('[a-z0-9][a-z0-9-]{0,63}',a.release):raise RuntimeError('Invalid release name.')
        args=['--release',a.release]
    print(remote(a.command,*args,timeout=300).decode())


if __name__=='__main__':
    try:main()
    except Exception as error:raise SystemExit(str(error))
