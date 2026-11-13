"""Install loopback-only mTLS diagnostics and persistent user cron jobs."""
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
from ops_common import ROOT, ROLES, SEED, enter_maintenance, leave_maintenance, read_container, run, save, write_policy, state, maintenance_matches
from ops_release import ready, stop_roles


def diagnostics():
    tls=ROOT/'tls';tls.mkdir(mode=0o700,exist_ok=True)
    if (ROOT/'diagnostics-installed.json').exists():
        ready();return {'diagnostics':'already configured'}
    recover_diagnostics()
    if (ROOT/'diagnostics-installed.json').exists():
        ready();return {'diagnostics':'recovered configured state'}
    old={role:read_container('hello-staging-'+role,'/etc/hello/'+v[1]) for role,v in ROLES.items()}
    if any(json.loads(data).get('monitoring') for data in old.values()):raise RuntimeError('Existing diagnostics require review.')
    with tempfile.TemporaryDirectory(dir=ROOT) as temp:
        d=Path(temp)
        def openssl(*args):run(['openssl',*map(str,args)])
        openssl('req','-x509','-newkey','rsa:3072','-nodes','-days','365','-subj','/CN=hello-staging-management-ca','-keyout',d/'ca.key','-out',tls/'ca.crt')
        for name,usage in [('server','serverAuth'),('client','clientAuth')]:
            key=d/(name+'.key') if name=='server' else tls/(name+'.key')
            openssl('req','-new','-newkey','rsa:3072','-nodes','-subj','/CN=hello-staging-management-'+name,'-keyout',key,'-out',d/(name+'.csr'))
            (d/'ext').write_text('basicConstraints=critical,CA:FALSE\nextendedKeyUsage='+usage+'\nsubjectAltName=IP:127.0.0.1\n')
            cert=d/(name+'.crt') if name=='server' else tls/(name+'.crt')
            openssl('x509','-req','-in',d/(name+'.csr'),'-CA',tls/'ca.crt','-CAkey',d/'ca.key','-CAcreateserial','-days','365','-extfile',d/'ext','-out',cert)
        # Preserve the original secret policies in a protected Docker volume.
        out=io.BytesIO()
        with tarfile.open(fileobj=out,mode='w') as tar:
            for role,data in old.items():
                entry=tarfile.TarInfo(role+'.json');entry.uid=0;entry.gid=65532;entry.mode=0o640;entry.size=len(data);tar.addfile(entry,io.BytesIO(data))
        volume='hello-staging-pre-monitoring-policy'
        present=subprocess.run(['docker','volume','inspect',volume],capture_output=True).returncode==0
        if present:raise RuntimeError('Prior diagnostics preparation exists; inspect before retrying.')
        before=state();save(ROOT/'diagnostics-setup.json',{'phase':'prepared','before':before})
        run(['docker','volume','create',volume])
        run(['docker','run','--rm','-i','--network','none','--read-only','--user','0:0','--cap-drop','ALL','--cap-add','CHOWN',
             '--mount',f'type=volume,src={volume},dst=/backup','--entrypoint','tar',SEED,'-xpf','-','-C','/backup'],out.getvalue())
        save(ROOT/'diagnostics-setup.json',{'phase':'backed-up','before':before})
        if enter_maintenance()!=before:raise RuntimeError('State changed before diagnostics maintenance.')
        save(ROOT/'diagnostics-setup.json',{'phase':'maintenance','before':before})
        try:
            stop_roles()
            for role,data in old.items():
                policy=json.loads(data)
                policy['monitoring']={'listen':f'127.0.0.1:{ROLES[role][2]}','server_certificate_pem':(d/'server.crt').read_text(),
                    'server_key_pem':(d/'server.key').read_text(),'client_ca_pem':(tls/'ca.crt').read_text()}
                write_policy(role,json.dumps(policy).encode())
            for role in reversed(list(ROLES)):run(['docker','start','hello-staging-'+role])
            ready()
            save(ROOT/'diagnostics-setup.json',{'phase':'reopening','before':before})
            leave_maintenance(before)
            save(ROOT/'diagnostics-installed.json',{'ports':{r:v[2] for r,v in ROLES.items()},'scope':'loopback mTLS'})
        except BaseException:
            recover_diagnostics()
            raise
    return {'diagnostics':'configured','scope':'loopback mTLS'}


def schedule():
    existing=subprocess.run(['crontab','-l'],capture_output=True,text=True)
    if existing.returncode not in [0,1]:raise RuntimeError('Cannot inspect current crontab.')
    lines=existing.stdout.splitlines();kept=[];inside=False
    for line in lines:
        if line=='# BEGIN HELLO STAGING OPS':inside=True;continue
        if line=='# END HELLO STAGING OPS':inside=False;continue
        if not inside:kept.append(line)
    if inside:raise RuntimeError('Unterminated existing cron block.')
    script=ROOT/'run.py'
    # VM cron runs in UTC. Hourly encrypted backups; local checks every minute.
    entries=['# BEGIN HELLO STAGING OPS',
        f'7 * * * * /usr/bin/python3 {script} backup --cron >> {ROOT}/backup-cron.log 2>&1',
        f'* * * * * /usr/bin/python3 {script} health --cron >> {ROOT}/health-cron.log 2>&1',
        '# END HELLO STAGING OPS']
    if (ROOT/'cd/config.json').exists() and json.loads((ROOT/'cd/config.json').read_text()).get('enabled'):
        entries.insert(-1,f'* * * * * /usr/bin/python3 {script} cd --cron >> {ROOT}/cd/poll.log 2>&1')
    run(['crontab','-'],('\n'.join(kept+entries)+'\n').encode())
    return {'backup':'hourly at minute 7 UTC','health':'every minute','alerts':'local status and syslog'}


def recover_diagnostics():
    record=ROOT/'diagnostics-setup.json'
    if not record.exists():return
    journal=json.loads(record.read_text());before=journal['before'];current=state()
    if journal['phase']=='reopening' and current==before:
        ready()
        save(ROOT/'diagnostics-installed.json',{'ports':{r:v[2] for r,v in ROLES.items()},'scope':'loopback mTLS'})
        return
    volume='hello-staging-pre-monitoring-policy'
    if journal['phase']=='prepared' and current==before:
        present=run(['docker','volume','ls','-q','--filter','name=^'+volume+'$']).strip()
        if present:run(['docker','volume','rm',volume])
        record.unlink();return
    old={r:run(['docker','run','--rm','--network','none','--read-only','--user','0:0','--cap-drop','ALL',
        '--mount',f'type=volume,src={volume},dst=/backup,readonly','--entrypoint','cat',SEED,'/backup/'+r+'.json']) for r in ROLES}
    if current==before:
        if enter_maintenance()!=before:raise RuntimeError('Diagnostics recovery state changed.')
    elif not maintenance_matches(before,current):raise RuntimeError('Unrelated incident; refuse diagnostics recovery.')
    stop_roles()
    for role,data in old.items():write_policy(role,data)
    for role in ROLES:run(['docker','start','hello-staging-'+role])
    baseline_ready()
    leave_maintenance(before)
    run(['docker','volume','rm',volume]);record.unlink()


def baseline_ready():
    # Original roles have no management listeners; never reopen on a transient restart.
    import ssl
    import time
    import urllib.error
    import urllib.request
    from ops_common import inspect
    baseline={r:inspect('hello-staging-'+r) for r in ROLES}
    for _ in range(5):
        time.sleep(1)
        for role,old in baseline.items():
            current=inspect('hello-staging-'+role)
            if not current['State']['Running'] or current['RestartCount']!=old['RestartCount'] or current['State']['Pid']!=old['State']['Pid']:
                raise RuntimeError('Original process unstable; remain maintenance held.')
    ca=read_container('hello-staging-gateway','/gateway/server-ca.crt').decode()
    opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=ssl.create_default_context(cadata=ca)))
    try:
        opener.open('https://10.1.16.109:8443/api/v1/apps',timeout=5)
    except urllib.error.HTTPError as error:
        if error.code==401:return
    raise RuntimeError('Original TLS/API authentication probe failed; remain held.')
