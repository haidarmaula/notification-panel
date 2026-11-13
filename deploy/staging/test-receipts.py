#!/usr/bin/env python3
"""Send only the explicitly approved receipt tests to the existing single-device pilot."""
import importlib.util
import json
import os
from pathlib import Path
import socket
import ssl
import sys
import urllib.request
import uuid

HERE=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('pilot',HERE/'run-one-pilot.py')
p=importlib.util.module_from_spec(spec);spec.loader.exec_module(p)


def main():
    os.umask(0o077)
    if socket.gethostname()!='srv-kafka' or sys.argv[1:] not in (['1'],['2'],['3'],['4'],['5'],['6'],['7'],['8']):
        raise SystemExit('Run on srv-kafka with an explicitly approved test number (1–8).')
    number=sys.argv[1]
    record=HERE/f'receipt-test-{number}.json'
    if record.exists():raise SystemExit('Test already prepared; inspect existing campaign rather than sending another.')
    safe=p.owner("SELECT NOT r.sealed AND NOT r.migration_hold AND NOT d.hold_new AND NOT d.coverage_approved AND d.dispatch_mode='PILOT' AND d.dispatch_cohort='baba1602-b527-4b25-a2b3-7fa5b8f4250d'::uuid FROM notification_runtime r CROSS JOIN application_dispatch_policy d WHERE d.application_id=1;")
    if safe!='t':raise RuntimeError('Expected exact existing one-device routine pilot.')
    target=p.owner("SELECT count(*)=1 AND bool_and(b.id=1 AND b.owner_user_id=1 AND b.owner_epoch=1 AND b.retired_at IS NULL AND c.destination='FCM_ACTIVE') FROM installation_bindings b JOIN current_binding_routes c ON c.application_id=b.application_id AND c.binding_id=b.id WHERE b.application_id=1 AND c.cohort_id='baba1602-b527-4b25-a2b3-7fa5b8f4250d';")
    if target!='t':raise RuntimeError('Pilot target changed.')
    policy=json.loads(p.server_file('/etc/hello/server.json'))
    admin=json.loads(p.raw(['docker','run','--rm','--network','none','--read-only','--user','0:0','--cap-drop','ALL',
        '--security-opt','no-new-privileges','--mount','type=bind,src=/etc/hello/staging-admin.json,dst=/input,readonly',
        '--entrypoint','cat',p.SEED,'/input']))
    ca=p.text(['docker','exec','hello-staging-gateway','cat','/gateway/server-ca.crt'])
    client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=ssl.create_default_context(cadata=ca)))
    def request(path,payload=None,token=None,key=None):
        headers={'Content-Type':'application/json','X-API-Key':policy['api_key']}
        if token:headers['Authorization']='Bearer '+token
        if key:headers['Idempotency-Key']=key
        req=urllib.request.Request('https://10.1.16.109:8443/api/v1/'+path,
            data=json.dumps(payload).encode() if payload is not None else b'',headers=headers)
        with client.open(req,timeout=15) as response:return response.status,json.load(response)
    _,login=request('auth/login',{'email':admin['email'],'password':admin['password']})
    access=login['data']['access_token'];refresh=login['data']['refresh_token']
    try:
        status,result=request(f'apps/{p.APP}/campaigns',{'title':f'Receipt test {number}',
            'body':"Android staging receipt verification for Haidar.",'targets':[{'type':'INDIVIDUAL','id':'1'}]},access)
        if status!=201 or result['data']['status']!='DRAFT':raise RuntimeError('Unexpected draft result.')
        campaign=str(result['data']['id']);key=str(uuid.uuid4())
        record.write_text(json.dumps({'campaign_id':campaign,'idempotency_key':key,'approval':('Owner accepted the proposed offline and expired-session recovery tests with gas; one send per scenario to the same phone.' if number in ('6','7','8') else 'Owner accepted the proposed background and recent-apps dismissal tests with gas; one send per scenario to the same phone.' if number in ('4','5') else 'Owner separately approved exactly one additional test for final DELIVERED verification.' if number=='3' else 'Owner explicitly approved at most two tests to the same Android device and opening through ADB.'),'status':'prepared'})+'\n')
        status,_=request(f'apps/{p.APP}/campaigns/{campaign}/send',token=access,key=key)
        if status!=202:raise RuntimeError('Unexpected acceptance result.')
        print('Approved receipt test',number,'accepted; campaign',campaign)
    finally:
        request('auth/logout',{'refresh_token':refresh},access)


if __name__=='__main__':main()
