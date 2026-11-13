#!/usr/bin/env python3
"""Execute the explicitly approved campaign once and always restore staging holds."""
import datetime
import hashlib
import io
import json
import os
import signal
from pathlib import Path
import socket
import ssl
import subprocess
import sys
import tarfile
import time
import urllib.error
import urllib.request
import uuid

SEED='postgres@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d'
REV='4695a2c9730740a9fb88e009fd2c00599edc9ba6'
APP='63677e77-2661-43b8-8253-35c1134bcc60'
PROCESSES=['hello-staging-scheduler','hello-staging-admission','hello-staging-delivery']


def raw(args,data=None,env=None):
    r=subprocess.run(args,input=data,capture_output=True,env=env,timeout=45)
    if r.returncode:
        raise RuntimeError('Command failed: '+r.stderr.decode(errors='replace')[-600:])
    return r.stdout


def text(args,data=None,env=None):
    return raw(args,data.encode() if data is not None else None,env).decode().strip()


def owner(sql):
    return text(['docker','exec','-i','hello-staging-postgres','psql','-X','-U','hello_staging',
                 '-d','hello_staging','-v','ON_ERROR_STOP=1','-qAt'],sql)


def server_file(path):
    archive=raw(['docker','cp','hello-staging-server:'+path,'-'])
    with tarfile.open(fileobj=io.BytesIO(archive)) as bundle:
        return bundle.extractfile(path.rsplit('/',1)[-1]).read()


def restore_holds(directory):
    close_error=None
    try:owner((directory/'close-pilot.sql').read_text())
    except Exception as error:close_error=error
    for name in PROCESSES:
        if subprocess.run(['docker','container','inspect',name],capture_output=True).returncode==0:
            try:text(['docker','stop','--time','30',name])
            except Exception as error:close_error=error
    if close_error:owner((directory/'close-pilot.sql').read_text())
    held=owner("SELECT r.sealed AND r.migration_hold AND p.hold_new AND NOT p.coverage_approved AND p.dispatch_mode='HELD' FROM notification_runtime r CROSS JOIN application_dispatch_policy p WHERE p.application_id=1;")
    if held!='t':raise RuntimeError('CRITICAL: original send holds were not verified')
    for name in PROCESSES:
        inspected=subprocess.run(['docker','inspect','--format','{{.State.Running}}',name],capture_output=True,text=True)
        if inspected.returncode==0 and inspected.stdout.strip()!='false':raise RuntimeError('CRITICAL: processing container still running')
    print('Verified application HELD, runtime sealed/migration-held and all processing roles stopped.',flush=True)


def watchdog(marker):
    directory=Path.home()/'hello-staging-provision'
    marker=Path(marker)
    if marker.parent.resolve()!=directory.resolve() or not marker.name.startswith('pilot-watchdog-'):
        raise SystemExit('Unexpected watchdog marker.')
    signal.signal(signal.SIGHUP,signal.SIG_IGN)
    for _ in range(180):
        if marker.read_text().strip()=='disarmed':return
        time.sleep(1)
    if marker.read_text().strip()!='disarmed':
        print('Independent three-minute pilot deadline reached; restoring holds.',flush=True)
        restore_holds(directory)


def main():
    if socket.gethostname()!='srv-kafka' or sys.argv[1:]!=['--owner-approved']:
        raise SystemExit('Requires explicit owner approval and the designated staging host.')
    os.umask(0o077)
    directory=Path.home()/'hello-staging-provision'
    review=json.loads((directory/'pilot-send-review.json').read_text())
    campaign=review['campaign'];target=review['target'];binding=target['binding']
    if review['review_status']!='pending' or campaign['campaign_id']!='1' or campaign['application_id']!=APP:
        raise SystemExit('This pilot is not a new pending reviewed campaign; inspect before retrying.')
    for name,digest in review['procedure_sha256'].items():
        if hashlib.sha256((directory/name).read_bytes()).hexdigest()!=digest:
            raise SystemExit('Reviewed procedure changed.')
    for value in (APP,review['pilot_cohort_id'],review['binding_evidence_id'],
                  target['runtime']['origin_epoch'],target['runtime']['source_epoch']):
        uuid.UUID(value)
    if binding['id']!=1 or binding['user_id']!=1 or binding['owner_epoch']!=1:
        raise SystemExit('Unexpected reviewed target binding.')
    config='security.protocol=SSL\nssl.keystore.type=PEM\nssl.keystore.location=/etc/kafka/tls/admin.pem\nssl.truststore.type=PEM\nssl.truststore.location=/etc/kafka/tls/ca.crt\ndefault.api.timeout.ms=10000\nrequest.timeout.ms=10000\n'

    def kafka(tool,*args,cluster=False):
        prefix=['cluster-id'] if cluster else []
        script='config=$1; shift; tool=$1; shift; timeout 20 /opt/kafka/bin/"$tool" '
        script+=('cluster-id ' if cluster else '')+'--bootstrap-server localhost:9095 --command-config <(printf "%b" "$config") "$@"'
        return text(['docker','exec','hello-staging-kafka','bash','-c',script,'bash',config,tool,*args])

    if 'Cluster ID: eGTxoZosTWKe7DWll1Teug' not in kafka('kafka-cluster.sh',cluster=True):
        raise SystemExit('Kafka cluster identity changed.')
    metadata=kafka('kafka-topics.sh','--describe','--topic',r'notification\.(send\.requested|delivery\.updated)')
    for topic,incarnation in [('notification.send.requested','7DWU8nW_Sku7FufiVO568Q'),('notification.delivery.updated','Kll9XLH2R2eZUHpILxg2lg')]:
        lines=[line for line in metadata.splitlines() if line.startswith('Topic: '+topic+'\tTopicId:')]
        if len(lines)!=1 or ('TopicId: '+incarnation) not in lines[0] or 'PartitionCount: 3' not in lines[0]:
            raise SystemExit('Kafka topic identity changed.')
    empty={f'notification.send.requested:{p}:0' for p in range(3)}
    for bound in ('earliest','latest'):
        if set(kafka('kafka-get-offsets.sh','--topic','notification.send.requested','--time',bound).splitlines())!=empty:
            raise SystemExit('Unexpected retained send records; refusing pilot.')
    checkpoints=kafka('kafka-consumer-groups.sh','--group','hello-staging-admission-v1','--describe')
    rows=[line.split() for line in checkpoints.splitlines() if line.startswith('hello-staging-admission-v1 ')]
    if len(rows)!=3 or {tuple(row[2:6]) for row in rows}!={(str(p),'0','0','0') for p in range(3)}:
        raise SystemExit('Admission checkpoints differ from reviewed zero backlog.')
    print('Verified exact Kafka identities and zero backlog.',flush=True)
    credentials=json.loads(text(['docker','run','--rm','--network','none','--read-only','--user','0:0',
        '--cap-drop','ALL','--security-opt','no-new-privileges','--mount',
        'type=volume,src=hello-staging-pilot-credentials,dst=/policy,readonly','--entrypoint','cat',SEED,'/policy/credentials.json']))

    def role_sql(login,sql):
        env=os.environ.copy();env['PGPASSWORD']=credentials[login]
        return text(['docker','run','--rm','-i','--network','host','--read-only','--env','PGPASSWORD',
            '--cap-drop','ALL','--security-opt','no-new-privileges','--mount',
            'type=bind,src=/etc/hello/postgres-ca.crt,dst=/ca.crt,readonly','--entrypoint','psql',SEED,
            '-X',f'postgres://{login}@127.0.0.1:5432/hello_staging?sslmode=verify-full&sslrootcert=/ca.crt',
            '-v','ON_ERROR_STOP=1','-qAt'],sql,env)

    now=datetime.datetime.now(datetime.timezone.utc)
    expires=(now+datetime.timedelta(minutes=30)).strftime('%Y-%m-%dT%H:%M:%SZ')
    origin=target['runtime']['origin_epoch'];source=target['runtime']['source_epoch'];cohort=review['pilot_cohort_id']
    evidence={
        'legacy':review['owner_confirmed_legacy_exclusion'],
        'consent':{'owner':'haidar','approval':'continue after explicit one-device send proposal','approved_at':now.isoformat(),'campaign':campaign,'target':binding},
        'coverage':{'application':APP,'cohort':cohort,'only_binding':binding,'target_count':1,'broadcast':False},
        'quota':review['delivery_limits']}
    hashes={}
    for kind,value in evidence.items():
        data=(json.dumps(value,sort_keys=True,indent=2)+'\n').encode()
        (directory/f'pilot-{kind}-evidence.json').write_bytes(data);hashes[kind]=hashlib.sha256(data).hexdigest()
    review.update(review_status='owner-approved',notification_send_approved=True,approval_reference=evidence['consent']['approval'],evidence_sha256=hashes)
    (directory/'pilot-send-review.json').write_text(json.dumps(review,indent=2)+'\n')
    marker=directory/('pilot-watchdog-'+str(uuid.uuid4())+'.marker')
    marker.write_text('armed\n')
    with (directory/'pilot-watchdog.log').open('ab') as log:
        subprocess.Popen([sys.executable,str(directory/'run-one-pilot.py'),'--watchdog',str(marker)],
                         stdin=subprocess.DEVNULL,stdout=log,stderr=log,start_new_session=True)
    def interrupted(signum,frame):raise RuntimeError('Pilot interrupted; restoring holds')
    signal.signal(signal.SIGTERM,interrupted)
    signal.signal(signal.SIGINT,interrupted)
    result={};opened=False;refresh=None;request=None;failure=None
    try:
        opened=True
        owner((directory/'open-approved-pilot.sql').read_text())
        grant=str(uuid.uuid4())
        role_sql('hello_binding_reviewer_login',f"INSERT INTO binding_route_grants(id,application_id,binding_id,operator_role,origin_epoch,source_epoch,expected_revision,destination,cohort_id,evidence_id,reason,expires_at) VALUES('{grant}',1,1,'hello_binding_executor_login'::regrole::oid,'{origin}','{source}',0,'FCM_ACTIVE','{cohort}','{review['binding_evidence_id']}','pilot','{expires}');")
        if role_sql('hello_binding_executor_login',f"SELECT apply_binding_route('{grant}','{APP}');")!='1':raise RuntimeError('Unexpected binding revision')
        grant=str(uuid.uuid4())
        role_sql('hello_dispatch_reviewer_login',f"INSERT INTO application_dispatch_grants(id,project_id,operator_role,application_revision,expected_revision,previous_mode,destination,cohort_id,origin_epoch,source_epoch,reason,legacy_evidence,consent_evidence,coverage_evidence,quota_evidence,expires_at) VALUES('{grant}','{APP}','hello_dispatch_executor_login'::regrole::oid,2,1,'HELD','PILOT','{cohort}','{origin}','{source}','pilot',decode('{hashes['legacy']}','hex'),decode('{hashes['consent']}','hex'),decode('{hashes['coverage']}','hex'),decode('{hashes['quota']}','hex'),'{expires}');")
        if role_sql('hello_dispatch_executor_login',f"SELECT apply_application_dispatch('{grant}','{APP}');")!='2':raise RuntimeError('Unexpected dispatch revision')
        print('Applied exact binding and one-device PILOT grants.',flush=True)
        if subprocess.run(['docker','container','inspect','hello-staging-delivery'],capture_output=True).returncode==0:
            raise RuntimeError('Existing delivery container requires inspection')
        text(['docker','start','hello-staging-scheduler','hello-staging-admission'])
        text(['docker','run','-d','--name','hello-staging-delivery','--restart','no','--network','host',
            '--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--pids-limit','128',
            '--memory','512m','--cpus','1','--log-opt','max-size=10m','--log-opt','max-file=3',
            '--mount','type=volume,src=hello-staging-delivery-policy,dst=/etc/hello,readonly','hello-worker:'+REV])
        markers={'hello-staging-scheduler':'durable scheduler started','hello-staging-admission':'worker started in admission_only mode','hello-staging-delivery':'delivery worker started'}
        for attempt in range(30):
            ready=True
            for name,startup_message in markers.items():
                state=json.loads(text(['docker','inspect','--format','{{json .State}}',name]))
                if state['Status']!='running':raise RuntimeError(name+' failed startup')
                logs=subprocess.run(['docker','logs','--since',now.isoformat(),name],capture_output=True,text=True)
                ready=ready and startup_message in logs.stdout+logs.stderr
            if ready:break
            time.sleep(1)
        else:raise RuntimeError('Daemon readiness timeout')
        print('Scheduler, admission and delivery roles passed startup.',flush=True)
        policy=json.loads(server_file('/etc/hello/server.json'))
        admin=json.loads(text(['docker','run','--rm','--network','none','--read-only','--user','0:0',
            '--cap-drop','ALL','--security-opt','no-new-privileges','--mount',
            'type=bind,src=/etc/hello/staging-admin.json,dst=/input,readonly','--entrypoint','cat',SEED,'/input']))
        ca=text(['docker','exec','hello-staging-gateway','cat','/gateway/server-ca.crt'])
        client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=ssl.create_default_context(cadata=ca)))

        def request(path,payload=None,token=None,key=None,empty=False):
            headers={'X-API-Key':policy['api_key'],'Content-Type':'application/json'}
            if token:headers['Authorization']='Bearer '+token
            if key:headers['Idempotency-Key']=key
            data=b'' if empty else json.dumps(payload).encode()
            req=urllib.request.Request('https://10.1.16.109:8443/api/v1/'+path,data=data,headers=headers)
            try:
                with client.open(req,timeout=15) as response:return response.status,json.loads(response.read(131072))
            except urllib.error.HTTPError as error:
                raise RuntimeError('Pilot API request returned HTTP '+str(error.code))
        _,login=request('auth/login',{'email':admin['email'],'password':admin['password']})
        access=login['data']['access_token'];refresh=login['data']['refresh_token']
        status,accepted=request(f'apps/{APP}/campaigns/1/send',token=access,key=campaign['idempotency_key'],empty=True)
        if status!=202:raise RuntimeError('Unexpected campaign acceptance status')
        print('Accepted campaign 1 using the stored idempotency key; monitoring its single intent.',flush=True)
        for attempt in range(90):
            result=json.loads(owner("SELECT json_build_object('campaign_status',(SELECT status FROM notifications WHERE id=1),'intents',(SELECT coalesce(json_agg(json_build_object('status',status,'attempt_count',attempt_count)),'[]'::json) FROM notification_delivery_intents WHERE application_id=1 AND notification_id=1));"))
            intents=result['intents']
            if len(intents)>1 or any(item['attempt_count']>1 for item in intents):raise RuntimeError('Pilot attempt/recipient boundary exceeded')
            if len(intents)==1 and intents[0]['status'] in ('SENT','DELIVERED','OPENED','FAILED'):
                print('Durable provider outcome: '+intents[0]['status']+'; attempts='+str(intents[0]['attempt_count']),flush=True)
                if intents[0]['status']=='FAILED':failure='Provider outcome FAILED; do not submit a new identity automatically'
                break
            time.sleep(1)
        else:raise RuntimeError('Pilot outcome timeout; do not submit a new campaign')
    except Exception as error:
        failure=str(error)
    finally:
        signal.signal(signal.SIGTERM,signal.SIG_IGN)
        signal.signal(signal.SIGINT,signal.SIG_IGN)
        if refresh and request:
            try:request('auth/logout',{'refresh_token':refresh})
            except Exception:pass
        if opened:
            restore_holds(directory)
        marker.write_text('disarmed\n')
        result.update(error=failure,send_holds_restored=opened,finished_at=datetime.datetime.now(datetime.timezone.utc).isoformat())
        (directory/'pilot-result.json').write_text(json.dumps(result,indent=2)+'\n')
        review.update(review_status='executed',result_file='pilot-result.json')
        (directory/'pilot-send-review.json').write_text(json.dumps(review,indent=2)+'\n')
    if failure:raise SystemExit(failure)


if __name__=='__main__':
    if len(sys.argv)==3 and sys.argv[1]=='--watchdog':watchdog(sys.argv[2])
    else:main()
