"""Shared staging operations primitives; never print captured secret-bearing output."""
import contextlib
import fcntl
import io
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import tarfile
import time

ROOT=Path.home()/'hello-staging-ops'
SEED='postgres@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d'
ROLES={'server':('hello-staging-server-mobile-policy','server.json',19101),
       'scheduler':('hello-staging-scheduler-policy','scheduler.json',19102),
       'admission':('hello-staging-admission-policy','worker.json',19103),
       'delivery':('hello-staging-delivery-policy','worker.json',19104)}
ORDER=['delivery','admission','scheduler','server']
def refresh_inventory():
    # Call only after acquiring the command lock; imports may predate bootstrap.
    if (ROOT/'dashboard-enabled.json').exists():
        ROLES['dashboard']=('hello-staging-dashboard-policy','dashboard.json',19105)
        if 'dashboard' not in ORDER:ORDER.insert(0,'dashboard')
    else:
        ROLES.pop('dashboard',None)
        if 'dashboard' in ORDER:ORDER.remove('dashboard')

refresh_inventory()


def run(args,data=None,timeout=60):
    r=subprocess.run(args,input=data,capture_output=True,timeout=timeout)
    if r.returncode:
        log=ROOT/'failures'/(str(time.time_ns())+'.log')
        atomic(log,b'STDERR\n'+r.stderr+b'\nSTDOUT\n'+r.stdout)
        raise RuntimeError('Operation failed: '+str(args[0])+'; protected diagnostics: '+str(log))
    return r.stdout


def inspect(name):return json.loads(run(['docker','inspect',name]))[0]


def db(sql):
    return run(['docker','exec','-i','hello-staging-postgres','psql','-XqAt','-U','hello_staging','-d','hello_staging','-v','ON_ERROR_STOP=1'],sql.encode()).decode().strip()


def atomic(path,data):
    path=Path(path);path.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
    if path.is_symlink():raise RuntimeError('Refusing symlink output.')
    tmp=path.with_name(path.name+'.tmp')
    fd=os.open(tmp,os.O_WRONLY|os.O_CREAT|os.O_TRUNC|os.O_NOFOLLOW,0o600)
    with os.fdopen(fd,'wb') as f:f.write(data);f.flush();os.fsync(f.fileno())
    os.replace(tmp,path)


def save(path,value):atomic(path,(json.dumps(value,sort_keys=True,indent=2)+'\n').encode())


def read_container(name,path):
    data=run(['docker','cp',name+':'+path,'-'])
    with tarfile.open(fileobj=io.BytesIO(data)) as tar:return tar.extractfile(Path(path).name).read()


def write_policy(role,data):
    volume,filename,_=ROLES[role];out=io.BytesIO()
    with tarfile.open(fileobj=out,mode='w') as tar:
        info=tarfile.TarInfo(filename);info.uid=0;info.gid=65532;info.mode=0o640;info.size=len(data)
        tar.addfile(info,io.BytesIO(data))
    run(['docker','run','--rm','-i','--network','none','--read-only','--user','0:0','--cap-drop','ALL','--cap-add','CHOWN',
         '--security-opt','no-new-privileges','--mount',f'type=volume,src={volume},dst=/policy','--entrypoint','tar',SEED,'-xpf','-','-C','/policy'],out.getvalue())


def state():
    return json.loads(db("SELECT json_build_object('sealed',r.sealed,'migration_hold',r.migration_hold,'origin_epoch',r.origin_epoch,'source_epoch',r.source_epoch,'policies',(SELECT json_agg(row_to_json(p) ORDER BY application_id) FROM application_dispatch_policy p),'schema',(SELECT json_build_object('version',version,'dirty',dirty) FROM schema_migrations)) FROM notification_runtime r;"))


def maintenance_matches(before,after):return after=={**before,'migration_hold':True}


def enter_maintenance():
    before=state()
    if before.get('schema',{}).get('dirty'):raise RuntimeError('Dirty migration state; refuse maintenance mutation.')
    if db("SELECT count(*) FROM notification_delivery_intents WHERE status='PENDING';")!='0':raise RuntimeError('Pending deliveries must drain before deployment.')
    db('BEGIN; SET LOCAL lock_timeout=\'3s\'; SELECT singleton FROM notification_runtime FOR UPDATE; UPDATE notification_runtime SET migration_hold=true; COMMIT;')
    if not maintenance_matches(before,state()):raise RuntimeError('Dispatch state changed; leave maintenance held and inspect.')
    return before


def leave_maintenance(before):
    if not maintenance_matches(before,state()):raise RuntimeError('Unrelated hold/policy/schema changed; refusing to reopen.')
    if not before['migration_hold']:
        # Recheck under the same lock used for the write, including all current policies.
        expected=json.dumps({**before,'migration_hold':True}).replace("'","''")
        db("BEGIN; SET LOCAL lock_timeout='3s'; SELECT singleton FROM notification_runtime FOR UPDATE; DO $guard$ BEGIN IF (SELECT json_build_object('sealed',r.sealed,'migration_hold',r.migration_hold,'origin_epoch',r.origin_epoch,'source_epoch',r.source_epoch,'policies',(SELECT json_agg(row_to_json(p) ORDER BY application_id) FROM application_dispatch_policy p),'schema',(SELECT json_build_object('version',version,'dirty',dirty) FROM schema_migrations))::jsonb FROM notification_runtime r) <> '"+expected+"'::jsonb THEN RAISE EXCEPTION 'Unrelated state changed'; END IF; END $guard$; UPDATE notification_runtime SET migration_hold=false; COMMIT;")


def clone_args(role,image,spec):
    h=spec['HostConfig'];mounts=spec['Mounts'];volume=ROLES[role][0]
    if not re.fullmatch(r'sha256:[0-9a-f]{64}',image):raise RuntimeError('Immutable image ID required.')
    if h['Privileged'] or h['NetworkMode']!='host' or not h['ReadonlyRootfs'] or h.get('CapAdd') or h.get('CapDrop')!=['ALL'] or 'no-new-privileges' not in h.get('SecurityOpt',[]) or spec['Config'].get('Cmd'):
        raise RuntimeError('Unexpected runtime security/command configuration.')
    if len(mounts)!=1 or mounts[0].get('Name')!=volume or mounts[0]['Type']!='volume' or mounts[0]['Destination']!='/etc/hello' or mounts[0]['RW']:
        raise RuntimeError('Unexpected active protected policy mount.')
    return ['docker','create','--name','hello-staging-'+role,'--restart','unless-stopped','--network','host','--read-only',
        '--cap-drop','ALL','--security-opt','no-new-privileges','--pids-limit',str(h['PidsLimit']),
        '--memory',str(h['Memory']),'--cpus',str(h['NanoCpus']/1e9),'--log-opt','max-size=10m','--log-opt','max-file=3',
        '--mount',f'type=volume,src={volume},dst=/etc/hello,readonly',image]


@contextlib.contextmanager
def lock(nonblocking=False,name="operations.lock",timeout=None):
    if socket.gethostname()!='srv-kafka':raise RuntimeError('Run remote operations only on srv-kafka.')
    ROOT.mkdir(mode=0o700,exist_ok=True);os.chmod(ROOT,0o700)
    with (ROOT/name).open('a') as f:
        if timeout is None:fcntl.flock(f,fcntl.LOCK_EX|(fcntl.LOCK_NB if nonblocking else 0))
        else:
            deadline=time.monotonic()+timeout
            while True:
                try:
                    fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB);break
                except BlockingIOError:
                    if time.monotonic()>=deadline:raise TimeoutError('Operations lock wait exceeded backup deadline.')
                    time.sleep(.1)
        yield
