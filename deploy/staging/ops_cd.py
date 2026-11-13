"""Outbound-only staging CD; no Actions runner, inbound webhook or GitHub token."""
import json
import os
from pathlib import Path
import re
import subprocess
import time
from cd_protocol import REPOSITORY, verify_message, checked_schema
from ops_common import ROOT, atomic, lock, run, save, state, inspect, db
from ops_backup import backup
from ops_health import health
from ops_release import deploy, rollback


def git(*args):
    env=os.environ.copy();env.update(GIT_TERMINAL_PROMPT='0',GIT_CONFIG_NOSYSTEM='1',GIT_CONFIG_GLOBAL='/dev/null')
    command=['git','-c','http.sslVerify=true','-c','core.hooksPath=/dev/null','--git-dir',str(ROOT/'cd/repository.git'),*args]
    r=subprocess.run(command,capture_output=True,timeout=120,env=env)
    if r.returncode:raise RuntimeError('Read-only GitHub fetch/inspection failed.')
    return r.stdout


def current_remote_sha():
    data=git('ls-remote','origin','refs/heads/main').decode().strip().split()
    if len(data)!=2 or not re.fullmatch('[0-9a-f]{40}',data[0]):raise RuntimeError('Invalid remote main identity.')
    return data[0]


def poll():
    folder=ROOT/'cd';config=json.loads((folder/'config.json').read_text())
    if set(config)!={'repository','schema','enabled'} or config['repository']!=REPOSITORY or config['schema']!=41 or type(config['enabled']) is not bool:raise RuntimeError('Unreviewed staging CD scope/schema; inspect config.')
    if not config['enabled']:return {'phase':'disabled'}
    repository=folder/'repository.git'
    if not repository.exists():
        run(['git','init','--bare',str(repository)])
        git('remote','add','origin','https://github.com/'+REPOSITORY+'.git')
    git('fetch','--quiet','--no-tags','--prune','origin','+refs/heads/main:refs/remotes/origin/main','+refs/tags/staging-ready/*:refs/tags/staging-ready/*')
    sha=git('rev-parse','refs/remotes/origin/main').decode().strip()
    status_file=folder/'status.json'
    old=json.loads(status_file.read_text()) if status_file.exists() else {}
    if old.get('sha')==sha and old.get('phase')=='deployed':return {'phase':'unchanged','sha':sha}
    ref='refs/tags/staging-ready/'+sha
    try:tag=git('cat-file','tag',ref).decode()
    except RuntimeError:return {'phase':'waiting-for-successful-ci','sha':sha}
    header,separator,message=tag.partition('\n\n')
    if not separator or not header.startswith('object '+sha+'\ntype commit\n'):raise RuntimeError('Readiness tag does not reference current main commit.')
    authorization=verify_message(message.strip(),folder/'verification.pem',sha,config['schema'])
    checked_schema(git('ls-tree','-r','--name-only',sha,'db/migrations').decode().splitlines(),config['schema'])
    deployed=json.loads((folder/'last-deployed.json').read_text()) if (folder/'last-deployed.json').exists() else {}
    if authorization['ci_run_id']<deployed.get('ci_run_id',0):raise RuntimeError('Older CI authorization cannot replace a newer deployment.')
    attempts=old.get('attempts',0) if old.get('sha')==sha else 0
    if old.get('sha')==sha and old.get('phase') in ['failed','rollback-blocked'] and (old.get('phase')=='rollback-blocked' or attempts>=3 or time.time()<old.get('retry_after',0)):
        return {'phase':'retry-paused','sha':sha,'attempts':attempts}
    (folder/'last-error.json').unlink(missing_ok=True)
    record={**authorization,'phase':'preparing','attempts':attempts+1,'started':time.time()}
    save(status_file,record)
    release=f'cd-{sha[:12]}-{authorization["ci_run_id"]}-a{authorization["ci_run_attempt"]}'
    try:
        manifest=ROOT/'releases'/release/'release.json'
        if not manifest.exists():
            directory=manifest.parent
            if directory.exists():
                # Preserve the failed gate log for diagnosis before a bounded retry.
                os.rename(directory,directory.with_name(directory.name+'-failed-'+str(time.time_ns())))
            source=git('archive','--format=tar',sha)
            from ops import prepare
            with lock(name='build.lock'):prepare(release,sha,config['schema'],archive=source)
        checked=json.loads(manifest.read_text())
        if checked.get('revision')!=sha or checked.get('schema')!={'version':config['schema'],'dirty':False}:raise RuntimeError('Prepared image manifest scope mismatch.')
        # A new push may arrive during the image gate. Never deploy a stale completion.
        with lock():
            latest_config=json.loads((folder/'config.json').read_text())
            if latest_config=={**config,'enabled':False}:
                record.update(phase='disabled',completed=time.time());save(status_file,record)
                return {'phase':'disabled','sha':sha}
            if latest_config!=config:raise RuntimeError('CD scope changed during preparation; refuse activation.')
            if current_remote_sha()!=sha:
                record.update(phase='superseded',completed=time.time());save(status_file,record)
                return {'phase':'superseded','sha':sha}
            if state()['schema']!={'version':config['schema'],'dirty':False}:raise RuntimeError('Schema drift; no automatic migrations.')
            record['phase']='deploying';save(status_file,record)
            journal=json.loads((ROOT/'deployment.json').read_text()) if (ROOT/'deployment.json').exists() else {}
            already_committed=journal.get('phase')=='committed' and journal.get('release')==release and all(inspect('hello-staging-'+role)['Image']==image for role,image in checked['images'].items())
            if not already_committed:
                backup()
                deploy(release)
            report=health()
            if any(value!='ready' for value in report['roles'].values()):
                try:rollback()
                except Exception:
                    record['phase']='rollback-blocked'
                    try:
                        db('UPDATE notification_runtime SET migration_hold=true;')
                        record['incident_hold_applied']=True
                    except Exception:record['incident_hold_applied']=False
                    save(status_file,record)
                    raise RuntimeError('Post-deploy role unready; rollback blocked. Inspect incident hold and pending deliveries before recovery.')
                raise RuntimeError('Post-deploy role failed readiness; restored prior deployment.')
            record['operational_alerts']=report['alerts']
            record.update(phase='deployed',release=release,completed=time.time());save(status_file,record)
            save(folder/'last-deployed.json',record)
        run(['logger','-t','hello-staging','CD deployed checked main commit '+sha])
        return {'phase':'deployed','sha':sha,'release':release,'ci_run_id':authorization['ci_run_id']}
    except Exception:
        record.update(phase='rollback-blocked' if record['phase']=='rollback-blocked' else 'failed',completed=time.time(),retry_after=time.time()+300)
        save(status_file,record)
        raise
