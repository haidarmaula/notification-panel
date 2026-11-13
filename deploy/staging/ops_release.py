"""Immutable-image staging replacement with guarded maintenance and rollback."""
import json
from pathlib import Path
import re
import subprocess
import time
from ops_common import ROOT, ROLES, ORDER, clone_args, db, enter_maintenance, inspect, leave_maintenance, run, save, state, maintenance_matches
from ops_health import probe
import ops_docs as docs


def exists(name):return subprocess.run(['docker','inspect',name],capture_output=True).returncode==0


def ready():
    for _ in range(40):
        try:
            for role in ROLES:probe(role)
            return
        except Exception:time.sleep(1)
    raise RuntimeError('Role readiness deadline exceeded.')


def stop_roles():
    for role in ORDER:
        name='hello-staging-'+role
        if exists(name):run(['docker','stop','--time','30',name],timeout=45)


def capture(release):
    if not re.fullmatch('[a-z0-9][a-z0-9-]{0,63}',release):raise RuntimeError('Invalid release name.')
    target=ROOT/'releases'/release
    if target.exists():raise RuntimeError('Release already exists.')
    current=state()
    if current['schema']['dirty']:raise RuntimeError('Database migration is dirty.')
    manifest={'release':release,'schema':current['schema'],'images':{},'created':time.time(),'kind':'captured-current'}
    manifest['docs_release']=docs.current()
    docs.verify(manifest['docs_release'])
    for role in ROLES:manifest['images'][role]=inspect('hello-staging-'+role)['Image']
    save(target/'release.json',manifest)
    return manifest


def image_check(role,image):
    obj=json.loads(run(['docker','image','inspect',image]))[0]
    target='worker' if role in ['admission','delivery'] else role
    expected=['/'+target]+(['--deployment'] if role in ['server','dashboard'] else [])
    if obj['Config']['User']!='65532:65532' or obj['Config']['Entrypoint']!=expected:raise RuntimeError('Unexpected candidate image identity.')
    if obj['Os']!='linux' or obj['Architecture']!='amd64':raise RuntimeError('Candidate image platform mismatch.')


def ensure_fence(journal):
    current=state()
    if current==journal['before']:
        if enter_maintenance()!=journal['before']:raise RuntimeError('State changed while fencing.')
    elif not maintenance_matches(journal['before'],current):
        raise RuntimeError('Unrelated incident state changed; leave held and inspect.')


def rollback_journal(journal):
    ensure_fence(journal)
    stop_roles()
    for role in reversed(ORDER):
        name='hello-staging-'+role;old=journal['old_names'][role]
        if exists(old):
            if exists(name):run(['docker','rm',name])
            run(['docker','rename',old,name])
        elif not exists(name):raise RuntimeError('Both old and current containers missing: '+role)
        if inspect(name)['Image']!=journal['old_images'][role]:raise RuntimeError('Rollback image identity mismatch.')
        run(['docker','start',name])
    ready()
    docs.switch(journal.get('old_docs'));docs.verify(journal.get('old_docs'))
    journal['phase']='rollback-reopening';save(ROOT/'deployment.json',journal)
    leave_maintenance(journal['before'])
    journal['phase']='rolled-back';journal['completed']=time.time();save(ROOT/'deployment.json',journal)


def deploy(release):
    manifest=json.loads((ROOT/'releases'/release/'release.json').read_text())
    if manifest['release']!=release or manifest['schema']!=state()['schema']:raise RuntimeError('Release/schema mismatch; migrations require separate review.')
    if set(manifest['images'])!=set(ROLES):raise RuntimeError('Incomplete release role inventory.')
    current={role:inspect('hello-staging-'+role) for role in ROLES}
    for role,image in manifest['images'].items():
        image_check(role,image);clone_args(role,image,current[role])
    prior=ROOT/'deployment.json'
    if prior.exists() and json.loads(prior.read_text())['phase'] not in ['committed','rolled-back','baseline']:
        raise RuntimeError('Incomplete deployment requires recover first.')
    next_docs=docs.publish(release,manifest['docs_sha256']) if manifest.get('docs_sha256') else manifest.get('docs_release')
    suffix=str(int(time.time()))
    journal={'release':release,'phase':'prepared','before':state(),'started':time.time(),
        'old_names':{r:'hello-staging-'+r+'-rollback-'+suffix for r in ROLES},
        'old_images':{r:current[r]['Image'] for r in ROLES},'old_docs':docs.current(),'new_docs':next_docs}
    # Journal before changing the maintenance fence, then stop/join each process.
    save(prior,journal)
    try:
        before=enter_maintenance()
        if before!=journal['before']:raise RuntimeError('State changed before maintenance.')
        journal['phase']='replacing';save(prior,journal)
        stop_roles()
        for role in reversed(ORDER):
            name='hello-staging-'+role
            run(['docker','rename',name,journal['old_names'][role]])
            run(clone_args(role,manifest['images'][role],current[role]))
            run(['docker','start',name])
        ready()
        docs.switch(next_docs);docs.verify(next_docs)
        journal['phase']='reopening';save(prior,journal)
        leave_maintenance(before)
        journal['phase']='committed';journal['completed']=time.time();save(prior,journal)
    except BaseException:
        # Once reopened, never stop services without reacquiring a guarded fence.
        # Recovery can finalize the already healthy running release after an I/O crash.
        if journal['phase'] in ['reopening','committed','rollback-reopening','rolled-back'] and state()==journal['before']:
            raise RuntimeError('Services reopened; finalize the interrupted journal with recover.')
        rollback_journal(journal)
        raise
    return {'release':release,'phase':'committed','rollback_available':True}


def rollback():
    journal=json.loads((ROOT/'deployment.json').read_text())
    if journal['phase']!='committed':raise RuntimeError('No committed deployment to roll back.')
    if state()['schema']!=journal['before']['schema']:raise RuntimeError('Schema changed; refuse executable-only rollback.')
    journal['before']=state();journal['phase']='rollback-requested';save(ROOT/'deployment.json',journal)
    rollback_journal(journal)
    return {'release':journal['release'],'phase':'rolled-back'}


def recover():
    journal=json.loads((ROOT/'deployment.json').read_text())
    if journal['phase'] in ['committed','rolled-back','baseline']:raise RuntimeError('No incomplete deployment.')
    if journal['phase'] in ['reopening','rollback-reopening'] and state()==journal['before']:
        expected=(json.loads((ROOT/'releases'/journal['release']/'release.json').read_text())['images']
                  if journal['phase']=='reopening' else journal['old_images'])
        if any(inspect('hello-staging-'+r)['Image']!=image for r,image in expected.items()):raise RuntimeError('Recovered image inventory mismatch.')
        ready()
        docs.verify(journal.get('new_docs') if journal['phase']=='reopening' else journal.get('old_docs'))
        journal['phase']='committed' if journal['phase']=='reopening' else 'rolled-back'
        journal['completed']=time.time();save(ROOT/'deployment.json',journal)
        return {'phase':journal['phase']}
    rollback_journal(journal)
    return {'phase':'rolled-back'}
