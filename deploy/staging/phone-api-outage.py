#!/usr/bin/env python3
"""Temporarily drop only the known test phone's inbound staging HTTPS traffic."""
import json
from pathlib import Path
import socket
import subprocess
import sys
import time

HERE=Path(__file__).resolve().parent
IMAGE='postgres@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d'
RULE=['INPUT','-s','10.1.19.199/32','-d','10.1.16.109/32','-p','tcp','--dport','8443','-m','comment','--comment','hello-phone-receipt-outage','-j','DROP']
RECORD=HERE/'phone-api-outage.json'


def firewall(action,check=True):
    args=['docker','run','--rm','--network','host','--read-only','--user','0:0','--cap-drop','ALL',
          '--cap-add','NET_ADMIN','--security-opt','no-new-privileges','--mount','type=bind,src=/usr,dst=/usr,readonly',
          '--entrypoint','/usr/sbin/xtables-nft-multi',IMAGE,'iptables','-w','5',action,*RULE]
    r=subprocess.run(args,capture_output=True,timeout=20)
    if check and r.returncode:raise RuntimeError('Phone API isolation rule operation failed.')
    return r.returncode


def stop():
    if firewall('-C',False)==0:firewall('-D')
    if firewall('-C',False)==0:raise RuntimeError('Phone API isolation is still active.')
    RECORD.write_text(json.dumps({'status':'restored','source':'10.1.19.199','port':8443,'at':time.time()})+'\n')
    print('Verified temporary phone-only API rule absent.',flush=True)


def main():
    if socket.gethostname()!='srv-kafka':raise SystemExit('Run on srv-kafka.')
    if sys.argv[1:]==['--watchdog']:
        time.sleep(45)
        stop()
    elif sys.argv[1:]==['--stop']:stop()
    elif sys.argv[1:]==['--start']:
        if firewall('-C',False)==0:raise RuntimeError('Existing isolation must be restored first.')
        with (HERE/'phone-api-outage-watchdog.log').open('ab') as log:
            subprocess.Popen([sys.executable,str(Path(__file__).resolve()),'--watchdog'],stdin=subprocess.DEVNULL,stdout=log,stderr=log,start_new_session=True)
        firewall('-I')
        RECORD.write_text(json.dumps({'status':'active','source':'10.1.19.199','port':8443,'expires':time.time()+45})+'\n')
        print('Phone-to-staging HTTPS dropped for at most 45 seconds; independent cleanup armed.',flush=True)
    else:raise SystemExit('Use --start or --stop.')


if __name__=='__main__':main()
