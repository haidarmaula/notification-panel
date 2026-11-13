"""Read-only staging diagnostics and bounded local alerts."""
import json
import hashlib
import subprocess
import math
import re
import shutil
import ssl
import time
import urllib.request
from ops_common import ROOT, ROLES, inspect, run, save


def parse_metrics(text):
    result={}
    for line in text.splitlines():
        if not line or line.startswith('#'):continue
        parts=line.rsplit(' ',1)
        if len(parts)!=2:continue
        value=float(parts[1])
        if not math.isfinite(value):raise RuntimeError('Nonfinite diagnostic metric.')
        result[parts[0]]=value
    return result


def evaluate(metrics,previous):
    alerts=[]
    for key,value in metrics.items():
        if key.startswith('hello_work_oldest_seconds') and value>600:alerts.append('backlog older than 600s: '+key)
        if key.startswith('hello_provider_results_total') and 'disposition="accepted"' not in key and value>previous.get(key,value):alerts.append('new provider failure: '+key)
        if key.startswith('hello_provider_requests_total') and ('status_class="4xx"' in key or 'status_class="5xx"' in key) and value>previous.get(key,value):alerts.append('new provider HTTP error: '+key)
        if key.startswith('hello_provider_breaker_open') and value:alerts.append('provider breaker open')
    return alerts


def client():
    context=ssl.create_default_context(cafile=str(ROOT/'tls/ca.crt'))
    context.minimum_version=ssl.TLSVersion.TLSv1_3
    context.load_cert_chain(str(ROOT/'tls/client.crt'),str(ROOT/'tls/client.key'))
    return urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=context))


def probe(role,metrics=False):
    path='/metrics' if metrics else '/readyz'
    with client().open(f'https://127.0.0.1:{ROLES[role][2]}'+path,timeout=5) as response:
        data=response.read(2*1024*1024).decode()
        if response.status!=200:raise RuntimeError('Unready role: '+role)
        return parse_metrics(data) if metrics else data


def health(persist=True):
    previous=json.loads((ROOT/'status.json').read_text()) if (ROOT/'status.json').exists() else {}
    report={'at':time.time(),'alerts':[],'roles':{},'metrics':{}}
    for name in ['postgres','kafka','gateway']:
        try:
            obj=inspect('hello-staging-'+name)
            if not obj['State']['Running']:report['alerts'].append(name+' container stopped')
        except RuntimeError:report['alerts'].append(name+' container unavailable')
    for role in ROLES:
        try:
            obj=inspect('hello-staging-'+role)
            if not obj['State']['Running']:raise RuntimeError('stopped')
            probe(role);metrics=probe(role,True)
            report['roles'][role]='ready'
            report['metrics'][role]=metrics
            report['alerts']+=evaluate(metrics,previous.get('metrics',{}).get(role,{}))
        except Exception:report['roles'][role]='unready';report['alerts'].append(role+' diagnostics unavailable or unready')
    if shutil.disk_usage(ROOT).free<3*1024**3:report['alerts'].append('VM free disk below 3 GiB')
    backups=list((ROOT/'backups').glob('*.cms'))
    if not backups or time.time()-max(f.stat().st_mtime for f in backups)>7200:report['alerts'].append('encrypted backup missing or older than 2h')
    if backups:
        newest=max(backups,key=lambda f:f.stat().st_mtime)
        try:
            manifest=json.loads(newest.with_suffix('.json').read_text())
            if hashlib.sha256(newest.read_bytes()).hexdigest()!=manifest['sha256']:report['alerts'].append('latest encrypted backup checksum mismatch')
        except (OSError,ValueError,KeyError):report['alerts'].append('latest backup manifest unavailable')
    for name in ['ca.crt','client.crt']:
        check=subprocess.run(['openssl','x509','-checkend','2592000','-noout','-in',str(ROOT/'tls'/name)],capture_output=True)
        if check.returncode:report['alerts'].append('management certificate expires within 30 days: '+name)
    cd_status=ROOT/'cd/status.json'
    if cd_status.exists() and json.loads(cd_status.read_text()).get('phase') in ['failed','rollback-blocked']:report['alerts'].append('staging CD deployment failed; inspect cd/status.json')
    if (ROOT/'cd/last-error.json').exists():report['alerts'].append('staging CD polling or authorization error')
    if (ROOT/'docs-enabled.json').exists():
        try:
            import ops_docs
            docs_release=ops_docs.current()
            journal=json.loads((ROOT/'deployment.json').read_text()) if (ROOT/'deployment.json').exists() else {}
            expected=journal.get('new_docs') if journal.get('phase')=='committed' else journal.get('old_docs') if journal.get('phase')=='rolled-back' else docs_release
            if docs_release!=expected:raise RuntimeError('Documentation pointer drifted from deployed backend.')
            ops_docs.verify(docs_release)
            report['docs_release']=docs_release
        except Exception:report['alerts'].append('API documentation unavailable or integrity check failed')
    if persist:
        save(ROOT/'status.json',report)
        alerts=report['alerts'];old=previous.get('alerts',[])
        if alerts!=old:
            message='hello-staging: '+('; '.join(alerts) if alerts else 'all local checks recovered')
            run(['logger','-t','hello-staging',message])
            with (ROOT/'alerts.jsonl').open('a') as log:log.write(json.dumps({'at':report['at'],'alerts':alerts})+'\n')
    return report
