#!/usr/bin/env python3
"""Open the owner's bounded one-device Postman pilot; never submit a campaign."""
import datetime
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import time
import uuid

D=Path.home()/'hello-staging-provision'
spec=importlib.util.spec_from_file_location('pilot_runtime',D/'run-one-pilot.py')
pilot=importlib.util.module_from_spec(spec);spec.loader.exec_module(pilot)


def write_record(path,record):
    temporary=path.with_suffix('.tmp')
    temporary.write_text(json.dumps(record,indent=2)+'\n')
    temporary.replace(path)


def close_at_deadline(path):
    path=Path(path)
    if path.parent.resolve()!=D.resolve() or not path.name.startswith('postman-window-'):
        raise SystemExit('Unexpected window record.')
    signal.signal(signal.SIGHUP,signal.SIG_IGN)
    record=json.loads(path.read_text())
    record.update(watchdog_ready=True,watchdog_pid=os.getpid())
    write_record(path,record)
    while True:
        record=json.loads(path.read_text())
        if record['state'] in ('closed','aborted'):return
        if time.time()>=record['deadline_epoch']:break
        time.sleep(2)
    try:
        for attempt in range(3):
            try:
                pilot.restore_holds(D)
                break
            except Exception:
                if attempt==2:raise
                time.sleep(5)
        record.update(state='closed',closed_at=datetime.datetime.now(datetime.timezone.utc).isoformat())
    except Exception as error:
        record.update(state='closure_failed',error=str(error))
        raise
    finally:
        write_record(path,record)
        current=D/'postman-window-current.json'
        if current.exists() and json.loads(current.read_text()).get('window_id')==record.get('window_id'):
            write_record(current,record)


def main():
    if socket.gethostname()!='srv-kafka' or sys.argv[1:]!=['--owner-approved']:
        raise SystemExit('Requires designated staging host and explicit owner approval.')
    os.umask(0o077)
    now=datetime.datetime.now(datetime.timezone.utc);deadline=now+datetime.timedelta(minutes=30)
    state=json.loads(pilot.owner("SELECT json_build_object('runtime',(SELECT row_to_json(r) FROM notification_runtime r),'policy',(SELECT row_to_json(p) FROM application_dispatch_policy p WHERE application_id=1));"))
    if not state['runtime']['sealed'] or not state['runtime']['migration_hold'] or state['policy']['dispatch_mode']!='HELD':
        raise SystemExit('Expected original held state; inspect an existing window before reopening.')
    revision=int(state['policy']['dispatch_revision'])
    cohort='baba1602-b527-4b25-a2b3-7fa5b8f4250d'
    credentials=json.loads(pilot.text(['docker','run','--rm','--network','none','--read-only','--user','0:0',
        '--cap-drop','ALL','--security-opt','no-new-privileges','--mount',
        'type=volume,src=hello-staging-pilot-credentials,dst=/policy,readonly','--entrypoint','cat',pilot.SEED,'/policy/credentials.json']))

    def role_sql(login,sql):
        env=os.environ.copy();env['PGPASSWORD']=credentials[login]
        return pilot.text(['docker','run','--rm','-i','--network','host','--read-only','--env','PGPASSWORD',
            '--cap-drop','ALL','--security-opt','no-new-privileges','--mount',
            'type=bind,src=/etc/hello/postgres-ca.crt,dst=/ca.crt,readonly','--entrypoint','psql',pilot.SEED,
            '-X',f'postgres://{login}@127.0.0.1:5432/hello_staging?sslmode=verify-full&sslrootcert=/ca.crt',
            '-v','ON_ERROR_STOP=1','-qAt'],sql,env)

    identifier=str(uuid.uuid4());path=D/f'postman-window-{identifier}.json'
    record={'window_id':identifier,'state':'opening','deadline_epoch':deadline.timestamp(),'expires_at':deadline.isoformat(),
        'owner_approval':'ya sudah aktifkan lagi — reopening the same one-device staging pilot for manual Postman requests',
        'application':pilot.APP,'cohort':cohort,'only_user_id':1,'only_binding_id':1,
        'previous_dispatch_revision':revision,'campaigns_submitted_by_this_script':0}
    write_record(path,record)
    with (D/'postman-window-watchdog.log').open('ab') as log:
        watcher=subprocess.Popen([sys.executable,str(D/'open-postman-window.py'),'--watchdog',str(path)],
            stdin=subprocess.DEVNULL,stdout=log,stderr=log,start_new_session=True)
    for _ in range(25):
        if watcher.poll() is not None:raise SystemExit('Automatic closure process failed before opening.')
        acknowledged=json.loads(path.read_text())
        if acknowledged.get('watchdog_ready'):
            record=acknowledged
            break
        time.sleep(.2)
    else:
        record.update(state='aborted');write_record(path,record)
        raise SystemExit('Automatic closure was not acknowledged; runtime remains held.')
    opened=False
    def interrupted(signum,frame):raise RuntimeError('Postman window setup interrupted')
    signal.signal(signal.SIGTERM,interrupted)
    signal.signal(signal.SIGINT,interrupted)
    try:
        opened=True
        pilot.owner(f"""BEGIN; SET LOCAL lock_timeout='2s';
DO $window$ DECLARE r public.notification_runtime%ROWTYPE; BEGIN
 IF current_database()<>'hello_staging' OR session_user<>'hello_staging' THEN RAISE EXCEPTION 'Unexpected staging owner'; END IF;
 SELECT * INTO STRICT r FROM public.notification_runtime WHERE singleton FOR UPDATE;
 IF NOT r.sealed OR NOT r.migration_hold OR r.origin_epoch<>'7fb10b55-50b3-4b19-8569-a5c7f3bb49ba' OR r.source_epoch<>'cdb5a8ea-b654-4a5e-aa8d-de31eceb4a9e' THEN RAISE EXCEPTION 'Reviewed lineage/holds changed'; END IF;
 PERFORM 1 FROM public.applications WHERE id=1 AND public_id='{pilot.APP}' AND status='ACTIVE' AND revision=2 FOR UPDATE;
 IF NOT FOUND OR (SELECT count(*) FROM public.applications)<>1 THEN RAISE EXCEPTION 'Application scope changed'; END IF;
 PERFORM 1 FROM public.application_dispatch_policy WHERE application_id=1 AND dispatch_revision={revision} AND dispatch_mode='HELD' AND hold_new AND NOT coverage_approved FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'Dispatch generation changed'; END IF;
 IF (SELECT count(*) FROM public.installation_bindings WHERE application_id=1 AND retired_at IS NULL)<>1
 OR NOT EXISTS(SELECT FROM public.current_binding_routes c JOIN public.installation_bindings b ON b.id=c.binding_id AND b.application_id=c.application_id JOIN public.mobile_installations i ON i.id=b.installation_id AND i.application_id=b.application_id JOIN public.users u ON u.id=b.owner_user_id AND u.application_id=b.application_id
  WHERE c.application_id=1 AND c.binding_id=1 AND c.revision=1 AND c.destination='FCM_ACTIVE' AND c.cohort_id='{cohort}' AND c.origin_epoch=r.origin_epoch AND c.source_epoch=r.source_epoch AND b.retired_at IS NULL AND b.owner_user_id=1 AND b.owner_epoch=1 AND i.owner_user_id=b.owner_user_id AND i.owner_epoch=b.owner_epoch AND u.status='ACTIVE')
 OR EXISTS(SELECT FROM public.notifications WHERE status IN ('QUEUED','PROCESSING','SCHEDULED'))
 OR EXISTS(SELECT FROM public.notification_delivery_intents WHERE status='PENDING')
 OR EXISTS(SELECT FROM public.notification_targets WHERE application_id=1 AND (target_type<>'INDIVIDUAL' OR user_id IS DISTINCT FROM 1))
 THEN RAISE EXCEPTION 'One-device inventory or pending work changed'; END IF;
 UPDATE public.notification_runtime SET sealed=false,migration_hold=false WHERE singleton;
END $window$; COMMIT;""")
        evidence={'legacy':{'owner_confirmed_exclusion':True,'reference':'Owner confirmed yes in this staging chat on 2026-11-12'},
            'consent':record,'coverage':{'only_user':1,'only_binding':1,'cohort':cohort,'broadcast':False},
            'quota':{'replicas':1,'concurrency':1,'requests_per_second':1,'max_attempts_per_intent':1}}
        hashes={}
        for kind,value in evidence.items():
            data=(json.dumps(value,sort_keys=True,indent=2)+'\n').encode()
            (D/f'postman-{identifier}-{kind}.json').write_bytes(data);hashes[kind]=hashlib.sha256(data).hexdigest()
        grant=str(uuid.uuid4());expires=(deadline+datetime.timedelta(minutes=1)).strftime('%Y-%m-%dT%H:%M:%SZ')
        role_sql('hello_dispatch_reviewer_login',f"INSERT INTO application_dispatch_grants(id,project_id,operator_role,application_revision,expected_revision,previous_mode,destination,cohort_id,origin_epoch,source_epoch,reason,legacy_evidence,consent_evidence,coverage_evidence,quota_evidence,expires_at) VALUES('{grant}','{pilot.APP}','hello_dispatch_executor_login'::regrole::oid,2,{revision},'HELD','PILOT','{cohort}','7fb10b55-50b3-4b19-8569-a5c7f3bb49ba','cdb5a8ea-b654-4a5e-aa8d-de31eceb4a9e','revalidation',decode('{hashes['legacy']}','hex'),decode('{hashes['consent']}','hex'),decode('{hashes['coverage']}','hex'),decode('{hashes['quota']}','hex'),'{expires}');")
        if role_sql('hello_dispatch_executor_login',f"SELECT apply_application_dispatch('{grant}','{pilot.APP}');")!=str(revision+1):raise RuntimeError('Unexpected dispatch generation')
        pilot.text(['docker','start',*pilot.PROCESSES])
        markers={'hello-staging-scheduler':'durable scheduler started','hello-staging-admission':'worker started in admission_only mode','hello-staging-delivery':'delivery worker started'}
        for _ in range(30):
            ready=True
            for name,message in markers.items():
                status=json.loads(pilot.text(['docker','inspect','--format','{{json .State}}',name]))
                if status['Status']!='running':raise RuntimeError(name+' failed startup')
                logs=subprocess.run(['docker','logs','--since',now.isoformat(),name],capture_output=True,text=True)
                ready=ready and message in logs.stdout+logs.stderr
            if ready:break
            time.sleep(1)
        else:raise RuntimeError('Processing startup timeout')
        record.update(state='open',dispatch_revision=revision+1)
        if watcher.poll() is not None:raise RuntimeError('Automatic closure process exited during startup')
        write_record(path,record)
        write_record(D/'postman-window-current.json',record)
        jakarta=deadline.astimezone(datetime.timezone(datetime.timedelta(hours=7)))
        print('Postman one-device PILOT active. All three processing roles passed startup.')
        print('Automatic hold restoration scheduled for '+jakarta.strftime('%Y-%m-%d %H:%M:%S WIB'))
        print('No campaign was submitted by this script.')
    except Exception:
        signal.signal(signal.SIGTERM,signal.SIG_IGN)
        signal.signal(signal.SIGINT,signal.SIG_IGN)
        if opened:pilot.restore_holds(D)
        record.update(state='aborted');write_record(path,record)
        raise


if __name__=='__main__':
    if len(sys.argv)==3 and sys.argv[1]=='--watchdog':close_at_deadline(sys.argv[2])
    else:main()
