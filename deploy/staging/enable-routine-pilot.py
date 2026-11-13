#!/usr/bin/env python3
"""Enable the owner's standing, one-device staging pilot without submitting sends."""
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


def main():
    if socket.gethostname()!='srv-kafka' or sys.argv[1:]!=['--owner-approved']:
        raise SystemExit('Requires designated staging host and explicit routine-pilot approval.')
    os.umask(0o077)
    for path in D.glob('postman-window-*.json'):
        record=json.loads(path.read_text())
        if record.get('state') not in ('closed','aborted'):
            raise SystemExit('A timed window requires reconciliation before standing activation.')
    if any(path.read_text().strip()=='armed' for path in D.glob('pilot-watchdog-*.marker')):
        raise SystemExit('An earlier pilot watchdog is still armed.')
    snapshot=json.loads(pilot.owner("SELECT json_build_object('runtime',(SELECT row_to_json(r) FROM notification_runtime r),'policy',(SELECT row_to_json(p) FROM application_dispatch_policy p WHERE application_id=1));"))
    if not snapshot['runtime']['sealed'] or not snapshot['runtime']['migration_hold'] or snapshot['policy']['dispatch_mode']!='HELD':
        raise SystemExit('Expected closed staging state; inspect before reactivation.')
    revision=int(snapshot['policy']['dispatch_revision'])
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
    now=datetime.datetime.now(datetime.timezone.utc)
    record={'mode':'routine-one-device-pilot','state':'preparing','approved_at':now.isoformat(),
        'owner_approval':'ya, lanjut ke pilot staging rutin','application':pilot.APP,'cohort':cohort,
        'only_user_id':1,'only_binding_id':1,'automatic_expiry':None,'campaigns_submitted_by_script':0,
        'previous_dispatch_revision':revision,'restart_policy':'unless-stopped'}
    evidence={'legacy':{'owner_confirmed_exclusion':True,'source':'Owner confirmation during this staging setup'},
        'consent':record,'coverage':{'user':1,'binding':1,'owner_epoch':1,'cohort':cohort,'broadcast':False},
        'quota':{'replicas':1,'concurrency':1,'requests_per_second':1,'max_attempts_per_intent':1}}
    hashes={};identifier=str(uuid.uuid4())
    for kind,value in evidence.items():
        data=(json.dumps(value,sort_keys=True,indent=2)+'\n').encode()
        (D/f'routine-{identifier}-{kind}.json').write_bytes(data);hashes[kind]=hashlib.sha256(data).hexdigest()
    record['evidence_sha256']=hashes
    (D/'routine-pilot.json').write_text(json.dumps(record,indent=2)+'\n')
    def interrupted(signum,frame):raise RuntimeError('Routine setup interrupted; restoring holds')
    signal.signal(signal.SIGTERM,interrupted);signal.signal(signal.SIGINT,interrupted)
    opened=False
    try:
        opened=True
        pilot.owner(f"""BEGIN; SET LOCAL lock_timeout='2s';
DO $routine$ DECLARE r public.notification_runtime%ROWTYPE; BEGIN
 IF current_database()<>'hello_staging' OR session_user<>'hello_staging' THEN RAISE EXCEPTION 'Unexpected target owner'; END IF;
 SELECT * INTO STRICT r FROM public.notification_runtime WHERE singleton FOR UPDATE;
 IF NOT r.sealed OR NOT r.migration_hold OR r.origin_epoch<>'7fb10b55-50b3-4b19-8569-a5c7f3bb49ba' OR r.source_epoch<>'cdb5a8ea-b654-4a5e-aa8d-de31eceb4a9e' THEN RAISE EXCEPTION 'Lineage/holds changed'; END IF;
 PERFORM 1 FROM public.applications WHERE id=1 AND public_id='{pilot.APP}' AND status='ACTIVE' AND revision=2 FOR UPDATE;
 IF NOT FOUND OR (SELECT count(*) FROM public.applications)<>1 THEN RAISE EXCEPTION 'Application changed'; END IF;
 PERFORM 1 FROM public.application_dispatch_policy WHERE application_id=1 AND dispatch_revision={revision} AND dispatch_mode='HELD' AND hold_new AND NOT coverage_approved FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'Dispatch generation changed'; END IF;
 IF (SELECT count(*) FROM public.installation_bindings WHERE application_id=1 AND retired_at IS NULL)<>1
 OR NOT EXISTS(SELECT FROM public.current_binding_routes c JOIN public.installation_bindings b ON b.id=c.binding_id AND b.application_id=c.application_id JOIN public.mobile_installations i ON i.id=b.installation_id AND i.application_id=b.application_id JOIN public.users u ON u.id=b.owner_user_id AND u.application_id=b.application_id
  WHERE c.application_id=1 AND c.binding_id=1 AND c.revision=1 AND c.destination='FCM_ACTIVE' AND c.cohort_id='{cohort}' AND c.origin_epoch=r.origin_epoch AND c.source_epoch=r.source_epoch AND b.retired_at IS NULL AND b.owner_user_id=1 AND b.owner_epoch=1 AND i.owner_user_id=b.owner_user_id AND i.owner_epoch=b.owner_epoch AND u.status='ACTIVE')
 OR EXISTS(SELECT FROM public.notifications WHERE status IN ('QUEUED','PROCESSING','SCHEDULED'))
 OR EXISTS(SELECT FROM public.notification_delivery_intents WHERE status='PENDING')
 THEN RAISE EXCEPTION 'Device inventory or pending work differs from standing approval'; END IF;
 UPDATE public.notification_runtime SET sealed=false,migration_hold=false WHERE singleton;
END $routine$; COMMIT;""")
        grant=str(uuid.uuid4());expires=(now+datetime.timedelta(hours=1)).strftime('%Y-%m-%dT%H:%M:%SZ')
        role_sql('hello_dispatch_reviewer_login',f"INSERT INTO application_dispatch_grants(id,project_id,operator_role,application_revision,expected_revision,previous_mode,destination,cohort_id,origin_epoch,source_epoch,reason,legacy_evidence,consent_evidence,coverage_evidence,quota_evidence,expires_at) VALUES('{grant}','{pilot.APP}','hello_dispatch_executor_login'::regrole::oid,2,{revision},'HELD','PILOT','{cohort}','7fb10b55-50b3-4b19-8569-a5c7f3bb49ba','cdb5a8ea-b654-4a5e-aa8d-de31eceb4a9e','revalidation',decode('{hashes['legacy']}','hex'),decode('{hashes['consent']}','hex'),decode('{hashes['coverage']}','hex'),decode('{hashes['quota']}','hex'),'{expires}');")
        if role_sql('hello_dispatch_executor_login',f"SELECT apply_application_dispatch('{grant}','{pilot.APP}');")!=str(revision+1):raise RuntimeError('Unexpected dispatch revision')
        pilot.text(['docker','update','--restart','unless-stopped',*pilot.PROCESSES])
        pilot.text(['docker','start',*pilot.PROCESSES])
        markers={'hello-staging-scheduler':'durable scheduler started','hello-staging-admission':'worker started in admission_only mode','hello-staging-delivery':'delivery worker started'}
        for _ in range(30):
            ready=True
            for name,message in markers.items():
                state=json.loads(pilot.text(['docker','inspect','--format','{{json .State}}',name]))
                if state['Status']!='running':raise RuntimeError(name+' failed startup')
                logs=subprocess.run(['docker','logs','--since',now.isoformat(),name],capture_output=True,text=True)
                ready=ready and message in logs.stdout+logs.stderr
            if ready:break
            time.sleep(1)
        else:raise RuntimeError('Routine worker startup timeout')
        ready=pilot.owner("SELECT NOT r.sealed AND NOT r.migration_hold AND p.dispatch_mode='PILOT' AND NOT p.hold_new AND NOT p.coverage_approved FROM notification_runtime r CROSS JOIN application_dispatch_policy p WHERE p.application_id=1;")
        if ready!='t':raise RuntimeError('Routine pilot readiness differs from intended state')
        record.update(state='active',dispatch_revision=revision+1)
        (D/'routine-pilot.json').write_text(json.dumps(record,indent=2)+'\n')
        print('Routine one-device PILOT active; no automatic expiry.')
        print('All three processing roles passed startup and use restart=unless-stopped.')
        print('No campaign submitted by this script. Manual hold remains available.')
    except Exception:
        signal.signal(signal.SIGTERM,signal.SIG_IGN);signal.signal(signal.SIGINT,signal.SIG_IGN)
        if opened:pilot.restore_holds(D)
        record.update(state='activation_failed_and_held')
        (D/'routine-pilot.json').write_text(json.dumps(record,indent=2)+'\n')
        raise


if __name__=='__main__':main()
