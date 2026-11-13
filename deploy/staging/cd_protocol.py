"""Strict signed authorization for staging-only, CI-approved main commits."""
import base64
import json
from pathlib import Path
import re
import subprocess
import tempfile

REPOSITORY='haidarmaula/notification-panel'
REQUIRED_JOBS={
 'Skill integrity, format, build, unit race tests and vet',
 'Isolated worker, PostgreSQL, Kafka and simulated provider',
 'Full integration, migrations and sealed recovery',
 'Build and check six offline production images',
 'Monitoring rules and physical PITR','Reachable Go vulnerabilities','Release readiness gates'}


def checked_run(run,jobs,sha):
    if not re.fullmatch('[0-9a-f]{40}',sha) or run.get('head_sha')!=sha or run.get('head_branch')!='main' or run.get('event')!='push' or run.get('status')!='completed' or run.get('conclusion')!='success' or run.get('path')!='.github/workflows/ci.yml' or run.get('head_repository',{}).get('full_name')!=REPOSITORY:
        raise ValueError('Only successful original-repository main-push CI is eligible.')
    for name in REQUIRED_JOBS:
        found=[j for j in jobs if j.get('name')==name]
        if len(found)!=1 or found[0].get('conclusion')!='success':raise ValueError('Missing, duplicate or unsuccessful CI gate: '+name)


def checked_schema(paths,schema):
    versions=[]
    for path in paths:
        name=Path(path).name
        if not name.endswith('.up.sql'):continue
        match=re.fullmatch(r'([0-9]{6})_.+\.up\.sql',name)
        if not match:raise ValueError('Malformed committed migration filename.')
        versions.append(int(match[1]))
    if type(schema) is not int or not versions or max(versions)!=schema:
        raise ValueError('Committed migrations differ from reviewed staging schema; migrate/review separately.')


def canonical(payload):return json.dumps(payload,sort_keys=True,separators=(',',':')).encode()


def signed_message(payload,key):
    with tempfile.TemporaryDirectory() as directory:
        file=Path(directory)/'payload';file.write_bytes(canonical(payload))
        r=subprocess.run(['openssl','pkeyutl','-sign','-rawin','-inkey',str(key),'-in',str(file)],capture_output=True,check=True)
    return json.dumps({'payload':base64.b64encode(canonical(payload)).decode(),'signature':base64.b64encode(r.stdout).decode()},sort_keys=True,separators=(',',':'))


def verify_message(message,key,sha,schema):
    try:
        if len(message)>8192:raise ValueError('Oversized CD marker.')
        obj=json.loads(message)
        if set(obj)!={'payload','signature'}:raise ValueError('Unexpected marker fields.')
        raw=base64.b64decode(obj['payload'],validate=True);signature=base64.b64decode(obj['signature'],validate=True)
        if len(raw)>4096 or len(signature)!=64:raise ValueError('Invalid marker size.')
        payload=json.loads(raw)
        if set(payload)!={'version','repository','sha','schema','ci_run_id','ci_run_attempt'} or payload['version']!=1 or payload['repository']!=REPOSITORY or payload['sha']!=sha or payload['schema']!=schema or not re.fullmatch('[0-9a-f]{40}',sha) or type(payload['ci_run_id']) is not int or payload['ci_run_id']<1 or type(payload['ci_run_attempt']) is not int or payload['ci_run_attempt']<1 or canonical(payload)!=raw:
            raise ValueError('Invalid CD scope or noncanonical payload.')
        with tempfile.TemporaryDirectory() as directory:
            d=Path(directory);(d/'payload').write_bytes(raw);(d/'signature').write_bytes(signature)
            r=subprocess.run(['openssl','pkeyutl','-verify','-pubin','-inkey',str(key),'-rawin','-in',str(d/'payload'),'-sigfile',str(d/'signature')],capture_output=True)
        if r.returncode:raise ValueError('Staging CD signature rejected.')
        return payload
    except (KeyError,TypeError,json.JSONDecodeError,UnicodeError) as error:raise ValueError('Malformed staging CD marker.') from error
