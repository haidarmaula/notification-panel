#!/usr/bin/env python3
"""Provision staging-only GitHub authorization and VM verification, never a runner."""
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
from cd_protocol import REPOSITORY

LOCAL=Path.home()/'.config/hello/staging/cd'
REMOTE='/home/haidar/hello-staging-ops'


def run(args,data=None):
    r=subprocess.run(args,input=data,capture_output=True,timeout=60)
    if r.returncode:raise RuntimeError(r.stderr.decode(errors='replace')[-500:] or 'CD setup failed')
    return r.stdout


def api(path,data=None):
    args=['gh','api','repos/'+REPOSITORY+'/'+path]
    if data is not None:args+=['--method','PUT','--input','-']
    return json.loads(run(args,json.dumps(data).encode() if data is not None else None))


def main():
    os.umask(0o077);LOCAL.mkdir(mode=0o700,parents=True,exist_ok=True)
    private=LOCAL/'signing-private.pem';public=LOCAL/'verification.pem'
    if private.is_symlink() or public.is_symlink():raise RuntimeError('Refusing CD key symlink.')
    if private.exists()!=public.exists():raise RuntimeError('Incomplete local CD identity; restore it without rotating trust.')
    if not private.exists():run(['openssl','genpkey','-algorithm','ED25519','-out',str(private)])
    private.chmod(0o600)
    derived=run(['openssl','pkey','-in',str(private),'-pubout'])
    if public.exists() and public.read_bytes()!=derived:raise RuntimeError('Local CD key pair differs; refuse rotation.')
    public.write_bytes(derived)
    check="from pathlib import Path; import hashlib,json; p=Path('/home/haidar/hello-staging-ops/cd'); print(json.dumps({'key':hashlib.sha256((p/'verification.pem').read_bytes()).hexdigest() if (p/'verification.pem').exists() else None,'config':json.loads((p/'config.json').read_text()) if (p/'config.json').exists() else None}))"
    pinned=json.loads(run(['ssh','-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','kafka-vps',shlex.join(['python3','-c',check])]))
    if pinned['key'] not in [None,hashlib.sha256(derived).hexdigest()] or pinned['config'] not in [None,{'repository':REPOSITORY,'schema':41,'enabled':True}]:raise RuntimeError('Existing VM trust or scope differs; refuse GitHub secret mutation.')
    result=subprocess.run(['gh','api','repos/'+REPOSITORY+'/environments/staging'],capture_output=True)
    policy={'protected_branches':False,'custom_branch_policies':True}
    if result.returncode==0:
        environment=json.loads(result.stdout)
        if environment.get('deployment_branch_policy')!=policy or any(p.get('type')=='required_reviewers' for p in environment.get('protection_rules',[])):raise RuntimeError('Existing staging environment protection differs; refuse to weaken it.')
    elif b'404' in result.stderr:
        api('environments/staging',{'deployment_branch_policy':policy})
    else:raise RuntimeError('Cannot inspect staging environment.')

    policies=api('environments/staging/deployment-branch-policies')['branch_policies']
    if any(p['name']!='main' or p['type']!='branch' for p in policies):raise RuntimeError('Existing staging branch rules require review.')
    if not policies:
        run(['gh','api','repos/'+REPOSITORY+'/environments/staging/deployment-branch-policies','--method','POST','--input','-'],b'{"name":"main","type":"branch"}')
    variables=api('environments/staging/variables')['variables']
    existing=next((v for v in variables if v['name']=='STAGING_SCHEMA_VERSION'),None)
    if existing and existing['value']!='41':raise RuntimeError('Existing schema compatibility differs; inspect first.')
    if not existing:run(['gh','api','repos/'+REPOSITORY+'/environments/staging/variables','--method','POST','--input','-'],b'{"name":"STAGING_SCHEMA_VERSION","value":"41"}')
    run(['gh','secret','set','STAGING_CD_SIGNING_KEY','--repo',REPOSITORY,'--env','staging'],private.read_bytes())
    script="""from pathlib import Path
import hashlib,json,os,sys
root=Path('/home/haidar/hello-staging-ops/cd');root.mkdir(mode=0o700,exist_ok=True);os.umask(0o077)
pub=root/'verification.pem';data=sys.stdin.buffer.read()
if pub.exists() and pub.read_bytes()!=data:raise RuntimeError('Existing VM CD trust key differs; refuse rotation')
pub.write_bytes(data)
config=root/'config.json'
expected={'repository':'haidarmaula/notification-panel','schema':41,'enabled':True}
if config.exists() and json.loads(config.read_text())!=expected:raise RuntimeError('Existing CD scope differs')
config.write_text(json.dumps(expected)+'\\n')
print('Staging verification key pinned; no GitHub credential installed on VM.')
"""
    output=run(['ssh','-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','kafka-vps',shlex.join(['python3','-c',script])],public.read_bytes())
    print(output.decode().strip());print('Staging environment limited to main; signing secret installed only in GitHub staging.')


if __name__=='__main__':main()
