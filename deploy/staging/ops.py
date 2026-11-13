#!/usr/bin/env python3
"""VM-side staging maintenance entry point; use staging.py from the workstation."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import time
from ops_common import ROOT, ROLES, SEED, atomic, lock, run, save, state, enter_maintenance, leave_maintenance
from ops_backup import backup, validate_restore
from ops_health import health
from ops_release import capture, deploy, recover, rollback, ready
from ops_setup import diagnostics, schedule


def prepare(name,revision,schema,archive=None):
    if not re.fullmatch('[a-z0-9][a-z0-9-]{0,63}',name) or not re.fullmatch('[0-9a-f]{40}',revision):raise RuntimeError('Invalid immutable source identity.')
    directory=ROOT/'releases'/name
    if directory.exists():raise RuntimeError('Release already exists.')
    if archive is None:archive=sys.stdin.buffer.read(64*1024**2+1)
    if len(archive)>64*1024**2:raise RuntimeError('Source archive too large.')
    directory.mkdir(mode=0o700,parents=True)
    source=directory/'source';source.mkdir(mode=0o700)
    with tarfile.open(fileobj=io.BytesIO(archive),mode='r:') as tar:
        for member in tar.getmembers():
            if not (member.isfile() or member.isdir()) or member.name.startswith('/') or '..' in Path(member.name).parts:raise RuntimeError('Unsafe source archive member.')
        tar.extractall(source,filter='data')
    # The existing image gate builds/checks all six targets, with isolated synthetic policies.
    with (directory/'image-check.log').open('wb') as log:
        r=subprocess.run([sys.executable,'scripts/check-images.py'],cwd=source,stdout=log,stderr=subprocess.STDOUT,timeout=1800)
    if r.returncode:raise RuntimeError('Image gate failed; inspect '+str(directory/'image-check.log'))
    current=state()
    if current['schema']!={'version':schema,'dirty':False}:raise RuntimeError('Reviewed schema compatibility does not match live database.')
    images={}
    for role in ROLES:
        target='worker' if role in ['admission','delivery'] else role
        obj=json.loads(run(['docker','image','inspect','hello-'+target+':local-verify']))[0]
        images[role]=obj['Id']
    manifest={'release':name,'revision':revision,'schema':current['schema'],'images':images,'source_sha256':hashlib.sha256(archive).hexdigest(),
        'created':time.time(),'checks':['six offline hardened image gates'],'hosted_ci':'not asserted; verify repository CI separately','scope':'staging only'}
    save(directory/'release.json',manifest)
    return manifest


def main():
    os.umask(0o077)
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command',choices=['diagnostics','schedule','health','backup','restore-check','capture','prepare','deploy','rollback','recover','status','health-drill','cd'])
    parser.add_argument('--release');parser.add_argument('--revision');parser.add_argument('--schema',type=int);parser.add_argument('--cron',action='store_true')
    args=parser.parse_args()
    if args.command in ['capture','prepare','deploy'] and (not args.release or not re.fullmatch('[a-z0-9][a-z0-9-]{0,63}',args.release)):raise SystemExit('Valid --release is required.')
    try:
        with lock(nonblocking=args.cron and args.command!='backup',name='cd.lock' if args.command=='cd' else 'build.lock' if args.command=='prepare' else 'operations.lock',timeout=300 if args.cron and args.command=='backup' else None):
            if args.command=='cd':
                from ops_cd import poll
                result=poll()
                if result['phase']!='retry-paused':(ROOT/'cd/last-error.json').unlink(missing_ok=True)
                if args.cron and result['phase'] in ['unchanged','waiting-for-successful-ci','retry-paused','disabled']:return
            elif args.command=='diagnostics':result=diagnostics()
            elif args.command=='schedule':result=schedule()
            elif args.command=='health-drill':
                before=enter_maintenance()
                try:
                    run(['docker','stop','--time','30','hello-staging-server'])
                    failed=health(persist=False)
                    if failed['roles'].get('server')!='unready':raise RuntimeError('Stopped API was not detected.')
                finally:
                    run(['docker','start','hello-staging-server']);ready();leave_maintenance(before)
                recovered=health()
                if recovered['alerts']:raise RuntimeError('Health drill did not recover cleanly.')
                result={'stopped_server_detected':True,'recovery':'all roles ready','dispatch_state_restored':True}
            elif args.command=='health':
                result=health()
                # Cron logs transitions through syslog/status, not one line every minute.
                if args.cron:
                    if result['alerts']:raise RuntimeError('Local staging health alerts active; inspect status.json.')
                    return
            elif args.command=='backup':result=backup()
            elif args.command=='restore-check':result=validate_restore(sys.stdin.buffer.read(256*1024**2+1))
            elif args.command=='capture':result=capture(args.release)
            elif args.command=='prepare':result=prepare(args.release,args.revision,args.schema)
            elif args.command=='deploy':
                backup()
                result=deploy(args.release)
            elif args.command=='rollback':result=rollback()
            elif args.command=='recover':result=recover()
            elif args.command=='status':
                result={name:json.loads((ROOT/name).read_text()) for name in ['deployment.json','latest-backup.json','last-restore-drill.json','status.json','cd/status.json','cd/last-error.json'] if (ROOT/name).exists()}
            if args.command=='health':
                print(json.dumps({k:v for k,v in result.items() if k!='metrics'},indent=2))
                if result['alerts']:raise SystemExit(1)
            else:print(json.dumps(result,indent=2))
    except BlockingIOError:
        if not args.cron:raise
    except Exception as error:
        if args.command=='cd':save(ROOT/'cd/last-error.json',{'at':time.time(),'error':str(error)})
        if args.cron:
            run(['logger','-t','hello-staging','Scheduled '+args.command+' failed; inspect protected operations logs.'])
        raise SystemExit(str(error))


if __name__=='__main__':main()
