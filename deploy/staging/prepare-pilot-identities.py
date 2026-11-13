#!/usr/bin/env python3
"""Provision separate binding and dispatch control identities, without releases."""
import io
import json
import secrets
import socket
import subprocess
import tarfile

SEED='postgres@sha256:a426e44bac0b759c95894d68e1a0ac03ecc20b619f498a91aae373bf06d8508d'
VOLUME='hello-staging-pilot-credentials'
ROLES={'hello_binding_reviewer_login':'hello_binding_issuer',
       'hello_binding_executor_login':'hello_binding_operator',
       'hello_dispatch_reviewer_login':'hello_dispatch_issuer',
       'hello_dispatch_executor_login':'hello_dispatch_operator'}


def command(args,data=None):
    result=subprocess.run(args,input=data,capture_output=True,timeout=30)
    if result.returncode:
        raise SystemExit('Pilot identity preparation failed; no credentials printed.')
    return result.stdout


def main():
    if socket.gethostname()!='srv-kafka':raise SystemExit('Run on srv-kafka.')
    owner=['docker','exec','-i','hello-staging-postgres','psql','-X','-U','hello_staging',
           '-d','hello_staging','-v','ON_ERROR_STOP=1','-At']
    names=','.join("'"+name+"'" for name in list(ROLES)+list(ROLES.values()))
    if command(owner,('SELECT count(*) FROM pg_roles WHERE rolname IN ('+names+');').encode()).strip()!=b'0':
        raise SystemExit('Pilot roles already exist; inspect before provisioning.')
    if subprocess.run(['docker','volume','inspect',VOLUME],capture_output=True).returncode==0:
        raise SystemExit('Pilot credential volume already exists; inspect before replacing.')
    passwords={name:secrets.token_hex(32) for name in ROLES}
    output=io.BytesIO()
    with tarfile.open(fileobj=output,mode='w') as bundle:
        data=json.dumps(passwords).encode();entry=tarfile.TarInfo('credentials.json')
        entry.mode,entry.size=0o600,len(data);bundle.addfile(entry,io.BytesIO(data))
    command(['docker','volume','create','--label','hello.purpose=restricted-pilot-credentials',VOLUME])
    command(['docker','run','--rm','-i','--network','none','--read-only','--user','0:0',
             '--cap-drop','ALL','--security-opt','no-new-privileges','--mount',
             f'type=volume,src={VOLUME},dst=/policy','--entrypoint','tar',SEED,
             '-xpf','-','-C','/policy'],output.getvalue())
    sql=['BEGIN;']
    for login,group in ROLES.items():
        sql.extend([f'CREATE ROLE {group} NOLOGIN;',
            f"CREATE ROLE {login} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '{passwords[login]}';",
            f'GRANT {group} TO {login};'])
    sql.extend(['GRANT USAGE ON SCHEMA public TO hello_binding_issuer,hello_binding_operator;',
        'GRANT INSERT(id,application_id,binding_id,operator_role,origin_epoch,source_epoch,expected_revision,destination,cohort_id,evidence_id,reason,resume_from,expires_at), SELECT(id), UPDATE(revoked_at) ON public.binding_route_grants TO hello_binding_issuer;',
        'GRANT EXECUTE ON FUNCTION public.apply_binding_route(uuid,uuid) TO hello_binding_operator;',
        'COMMIT;'])
    command(owner,'\n'.join(sql).encode())
    print('Prepared distinct restricted binding/dispatch reviewer and executor identities.')
    print('Credentials are root-only; no route or dispatch grant issued.')


if __name__=='__main__':main()
