#!/usr/bin/env python3
"""Build a deterministic self-hosted reference from the canonical OpenAPI files."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil

ROOT=Path(__file__).resolve().parents[1]
GUIDES=[('staff','Staff & campaigns','staff-business.openapi.json','Authentication, staff administration and project campaigns.'),
        ('applications','Applications & access','application-registry.openapi.json','Application lifecycle and staff access grants.'),
        ('mobile','Mobile & receipts','mobile-delivery.openapi.json','Device enrollment, delivery receipts and read state.')]
METHODS={'get','post','put','patch','delete','options','head'}


def build(destination,release):
    if not re.fullmatch('[a-z0-9][a-z0-9-]{0,63}',release):raise ValueError('Invalid documentation release identifier.')
    dest=Path(destination)
    if dest.exists() and any(dest.iterdir()):raise ValueError('Documentation output must be empty.')
    dest.mkdir(parents=True,exist_ok=True)
    source=ROOT/'web/api-docs';vendor=json.loads((source/'vendor/manifest.json').read_text())
    for name,digest in vendor['sha256'].items():
        if hashlib.sha256((source/'vendor'/name).read_bytes()).hexdigest()!=digest:raise ValueError('Vendored Swagger UI integrity mismatch.')
    guides=[]
    for id,title,file,description in GUIDES:
        path=ROOT/'docs/contracts'/file;raw=path.read_bytes();doc=json.loads(raw)
        if doc.get('openapi')!='3.1.0':raise ValueError('Unexpected OpenAPI version.')
        (dest/'contracts').mkdir(exist_ok=True);(dest/'contracts'/file).write_bytes(raw)
        guides.append({'id':id,'title':title,'file':file,'description':description,'operations':sum(k in METHODS for p in doc['paths'].values() for k in p)})
    html=(source/'index.html').read_text().replace('__DOCS_BASE__','/docs/releases/'+release).replace('__RELEASE__',release)
    (dest/'index.html').write_text(html)
    for name in ['app.js','style.css']:shutil.copyfile(source/name,dest/name)
    shutil.copytree(source/'vendor',dest/'vendor')
    (dest/'catalog.json').write_text(json.dumps({'release':release,'guides':guides},indent=2)+'\n')
    manifest={'release':release,'swagger_ui':vendor['version'],'sha256':{str(p.relative_to(dest)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(dest.rglob('*')) if p.is_file()}}
    (dest/'manifest.json').write_text(json.dumps(manifest,sort_keys=True,indent=2)+'\n')
    return manifest


if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--output',required=True);p.add_argument('--release',required=True);a=p.parse_args()
    result=build(a.output,a.release);print('Built API reference:',len(result['sha256']),'files; Swagger UI',result['swagger_ui'])
