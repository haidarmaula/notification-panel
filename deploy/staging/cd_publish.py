#!/usr/bin/env python3
"""Hosted-only publisher: verify completed CI, then create a signed readiness tag."""
import json
from datetime import datetime, timezone
import os
from pathlib import Path
import re
import subprocess
import tempfile
import urllib.error
import urllib.request
from cd_protocol import REPOSITORY, checked_run, signed_message, verify_message, checked_schema


def main():
    if os.environ.get('GITHUB_REPOSITORY')!=REPOSITORY or os.environ.get('GITHUB_EVENT_NAME')!='workflow_run' or os.environ.get('GITHUB_REF')!='refs/heads/main':raise RuntimeError('Unexpected publication workflow scope.')
    event=json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text())
    ci=event['workflow_run'];run_id=ci['id'];sha=ci['head_sha']
    if type(run_id) is not int or not re.fullmatch('[0-9a-f]{40}',sha):raise RuntimeError('Invalid CI identity.')
    token=os.environ['GH_TOKEN']
    def api(path,data=None):
        req=urllib.request.Request('https://api.github.com/repos/'+REPOSITORY+'/'+path,
            data=json.dumps(data).encode() if data is not None else None,
            headers={'Authorization':'Bearer '+token,'Accept':'application/vnd.github+json','X-GitHub-Api-Version':'2022-11-28','User-Agent':'hello-staging-cd'})
        with urllib.request.urlopen(req,timeout=20) as r:return json.load(r)
    run=api('actions/runs/'+str(run_id))
    jobs=api(f'actions/runs/{run_id}/attempts/{run["run_attempt"]}/jobs?per_page=100')['jobs']
    checked_run(run,jobs,sha)
    if api('git/ref/heads/main')['object']['sha']!=sha:
        print('Newer main commit exists; skipping stale CI completion.');return
    schema=int(os.environ['STAGING_SCHEMA_VERSION'])
    if schema<1:raise RuntimeError('Explicit staging schema compatibility is required.')
    checked_schema(Path('db/migrations').glob('*.up.sql'),schema)
    payload={'version':1,'repository':REPOSITORY,'sha':sha,'schema':schema,'ci_run_id':run_id,'ci_run_attempt':run['run_attempt']}
    tag='staging-ready/'+sha
    with tempfile.TemporaryDirectory() as directory:
        key=Path(directory)/'signing.pem';key.write_text(os.environ['STAGING_CD_SIGNING_KEY']);key.chmod(0o600)
        public=Path(directory)/'verification.pem'
        subprocess.run(['openssl','pkey','-in',str(key),'-pubout','-out',str(public)],capture_output=True,check=True)
        message=signed_message(payload,key)
        try:
            existing=api('git/ref/tags/'+tag)
        except urllib.error.HTTPError as error:
            if error.code!=404:raise
        else:
            if existing['object']['type']!='tag':raise RuntimeError('Existing readiness ref is not signed metadata.')
            prior=api('git/tags/'+existing['object']['sha'])
            if prior['object'].get('sha')!=sha or prior['object'].get('type')!='commit':raise RuntimeError('Existing tag target mismatch.')
            verify_message(prior['message'],public,sha,schema)
            print('Valid readiness tag already exists; preserving original authorization.');return
        obj=api('git/tags',{'tag':tag,'message':message,'object':sha,'type':'commit','tagger':{'name':'github-actions[bot]','email':'41898282+github-actions[bot]@users.noreply.github.com','date':datetime.now(timezone.utc).isoformat()}})
        api('git/refs',{'ref':'refs/tags/'+tag,'sha':obj['sha']})
    print('Published signed staging authorization for '+sha+' from CI run '+str(run_id)+'.')


if __name__=='__main__':main()
