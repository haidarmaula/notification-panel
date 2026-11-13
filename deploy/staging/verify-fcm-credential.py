#!/usr/bin/env python3
"""Validate Google OAuth credentials without sending a notification or logging tokens."""
import base64
import json
import os
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request

SEED = 'postgres@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d'


def encode(value):
    return base64.urlsafe_b64encode(value).rstrip(b'=')


def main():
    result = subprocess.run(['docker','run','--rm','--network','none','--read-only','--user','0:0',
        '--cap-drop','ALL','--security-opt','no-new-privileges',
        '--mount','type=bind,src=/etc/hello/fcm/service-account.json,dst=/input,readonly',
        '--entrypoint','cat',SEED,'/input'],capture_output=True,timeout=30)
    if result.returncode:
        raise SystemExit('Credential read failed; no secret values printed.')
    account=json.loads(result.stdout)
    if account['project_id']!='push-notifications-stagi-a6a41' or account['token_uri']!='https://oauth2.googleapis.com/token':
        raise SystemExit('Unexpected credential project or OAuth endpoint.')
    now=int(time.time())
    header=encode(json.dumps({'alg':'RS256','typ':'JWT','kid':account['private_key_id']},separators=(',',':')).encode())
    claims=encode(json.dumps({'iss':account['client_email'],'scope':'https://www.googleapis.com/auth/firebase.messaging',
        'aud':account['token_uri'],'iat':now,'exp':now+3600},separators=(',',':')).encode())
    unsigned=header+b'.'+claims
    fd=os.memfd_create('hello-google-signing-key',os.MFD_CLOEXEC)
    try:
        os.write(fd,account['private_key'].encode());os.lseek(fd,0,os.SEEK_SET)
        signature=subprocess.run(['openssl','dgst','-sha256','-sign',f'/proc/self/fd/{fd}'],
            input=unsigned,capture_output=True,pass_fds=(fd,),timeout=10)
    finally:
        os.close(fd)
    if signature.returncode:
        raise SystemExit('Credential signature probe failed; no secret values printed.')
    assertion=(unsigned+b'.'+encode(signature.stdout)).decode()
    request=urllib.request.Request(account['token_uri'],data=urllib.parse.urlencode({
        'grant_type':'urn:ietf:params:oauth:grant-type:jwt-bearer','assertion':assertion}).encode())
    client=urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with client.open(request,timeout=15) as response:
            token=json.loads(response.read(65536))
    except urllib.error.HTTPError as error:
        raise SystemExit('Google OAuth credential probe rejected: HTTP '+str(error.code))
    except OSError:
        raise SystemExit('Google OAuth network probe failed; no secret values printed.')
    if not token.get('access_token') or token.get('token_type','').lower()!='bearer' or token.get('expires_in',0)<300:
        raise SystemExit('Unexpected OAuth credential probe response.')
    print('PASS Google OAuth authentication for Firebase Messaging scope; no notification sent.')
    print('Tokens/private keys remained in memory and were not printed or written to disk.')


if __name__=='__main__':
    main()
