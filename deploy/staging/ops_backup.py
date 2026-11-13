"""Encrypted DB/config backups and network-isolated sealed restore validation."""
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import time
import uuid
from ops_common import ROOT, ROLES, SEED, atomic, db, inspect, read_container, run, save


def table_summary_sql():
    # Values are hashes/counts only; row contents never leave this SQL statement.
    return """DO $summary$ DECLARE t record; n bigint; h text; result jsonb='{}'; BEGIN
FOR t IN SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename LOOP
EXECUTE format('SELECT count(*),md5(coalesce(string_agg(md5(to_jsonb(x)::text),'''' ORDER BY md5(to_jsonb(x)::text)),'''')) FROM public.%I x',t.tablename) INTO n,h;
result=result||jsonb_build_object(t.tablename,jsonb_build_object('count',n,'digest',h));
END LOOP; PERFORM set_config('hello.backup_summary',result::text,false); END $summary$;
SELECT current_setting('hello.backup_summary');"""


def bundle(files):
    out=io.BytesIO()
    with tarfile.open(fileobj=out,mode='w:gz') as tar:
        for name,data in files.items():
            entry=tarfile.TarInfo(name);entry.mode=0o600;entry.size=len(data);tar.addfile(entry,io.BytesIO(data))
    return out.getvalue()


def backup():
    started=time.time()
    cert=ROOT/'backup-recipient.crt'
    if not cert.is_file():raise RuntimeError('Install the off-host backup recipient certificate first.')
    run(['openssl','x509','-checkend','2592000','-noout','-in',str(cert)])
    p=subprocess.Popen(['docker','exec','-i','hello-staging-postgres','psql','-XqAt','-U','hello_staging','-d','hello_staging','-v','ON_ERROR_STOP=1'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,text=True)
    try:
        p.stdin.write('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SELECT pg_export_snapshot();\n'+table_summary_sql()+'\n');p.stdin.flush()
        snapshot=p.stdout.readline().strip();summary=p.stdout.readline().strip()
        if not re.fullmatch(r'[0-9A-F-]+',snapshot):raise RuntimeError('Snapshot export failed.')
        facts=json.loads(summary)
        dump=run(['docker','exec','hello-staging-postgres','pg_dump','-U','hello_staging','-d','hello_staging','-Fc','--snapshot='+snapshot],timeout=120)
        p.stdin.write('COMMIT;\n');p.stdin.close()
        if p.wait(timeout=10):raise RuntimeError('Snapshot transaction failed.')
    finally:
        if p.poll() is None:p.kill();p.wait()
    files={'database.dump':dump}
    for role in ROLES:
        files['policy-'+role+'.tar']=run(['docker','cp','hello-staging-'+role+':/etc/hello','-'])
    files['host-etc-hello.tar']=run(['docker','run','--rm','--network','none','--read-only','--user','0:0','--cap-drop','ALL','--security-opt','no-new-privileges',
        '--mount','type=bind,src=/etc/hello,dst=/source,readonly','--entrypoint','tar',SEED,'-cpf','-','-C','/source','.'])
    manifest={'started':started,'completed':time.time(),'database_sha256':hashlib.sha256(dump).hexdigest(),'tables':facts,
        'schema':json.loads(db('SELECT row_to_json(s) FROM schema_migrations s;')),
        'images':{role:inspect('hello-staging-'+role)['Image'] for role in ROLES}}
    if (ROOT/'tls').exists():
        files['ops-tls.tar']=bundle({f.name:f.read_bytes() for f in (ROOT/'tls').iterdir() if f.is_file() and not f.is_symlink()})
    import ops_docs
    if (ROOT/'docs-enabled.json').exists():
        files['api-docs-volume.tar']=ops_docs.helper('tar','-cpf','-','-C','/docs','.')
    manifest['format']=1
    manifest['files_sha256']={name:hashlib.sha256(data).hexdigest() for name,data in files.items()}
    files['manifest.json']=json.dumps(manifest).encode()
    encrypted=run(['openssl','cms','-encrypt','-binary','-aes-256-gcm','-outform','DER',str(cert)],bundle(files),timeout=120)
    name=time.strftime('%Y%m%dT%H%M%SZ',time.gmtime())+'-'+uuid.uuid4().hex[:8]
    folder=ROOT/'backups';folder.mkdir(mode=0o700,exist_ok=True)
    atomic(folder/(name+'.cms'),encrypted)
    public={'name':name,'sha256':hashlib.sha256(encrypted).hexdigest(),'bytes':len(encrypted),'completed':manifest['completed'],'schema':manifest['schema']}
    save(folder/(name+'.json'),public)
    save(ROOT/'latest-backup.json',public)
    # Retain the latest 48 hourly points and one point per day for fourteen days.
    backups=sorted(folder.glob('*.cms'),key=lambda f:f.stat().st_mtime,reverse=True)
    days=set()
    for i,f in enumerate(backups):
        day=f.name[:8]
        keep=i<48 or (day not in days and time.time()-f.stat().st_mtime<14*86400)
        days.add(day)
        if not keep:f.unlink();f.with_suffix('.json').unlink(missing_ok=True)
    return public


def validate_restore(archive):
    started=time.time()
    if len(archive)>256*1024**2:raise RuntimeError('Restore input exceeds staging validation limit.')
    with tarfile.open(fileobj=io.BytesIO(archive),mode='r:gz') as tar:
        members=tar.getmembers()
        if any(not m.isfile() or m.size>256*1024**2 for m in members) or sum(m.size for m in members)>256*1024**2:raise RuntimeError('Unexpected restore bundle member.')
        manifest=json.load(tar.extractfile('manifest.json'));dump=tar.extractfile('database.dump').read()
        file_hashes=manifest.get('files_sha256',{'database.dump':manifest['database_sha256']})
        for name,digest in file_hashes.items():
            if hashlib.sha256(tar.extractfile(name).read()).hexdigest()!=digest:raise RuntimeError('Backup member digest mismatch.')
    if hashlib.sha256(dump).hexdigest()!=manifest['database_sha256']:raise RuntimeError('Database backup digest mismatch.')
    name='hello-staging-restore-'+uuid.uuid4().hex[:12]
    run(['docker','run','-d','--name',name,'--network','none','--read-only','--tmpfs','/var/run/postgresql:rw,nosuid,size=16m',
         '--tmpfs','/var/lib/postgresql/data:rw,nosuid,size=768m','--memory','1g','--pids-limit','128',
         '-e','POSTGRES_HOST_AUTH_METHOD=trust',SEED],timeout=30)
    try:
        for _ in range(60):
            result=subprocess.run(['docker','exec','--user','postgres',name,'pg_isready','-q'],capture_output=True)
            if result.returncode==0:break
            time.sleep(.25)
        else:raise RuntimeError('Isolated restore PostgreSQL did not start.')
        obj=inspect(name)
        if obj['HostConfig']['NetworkMode']!='none' or obj['HostConfig'].get('PortBindings'):raise RuntimeError('Restore target isolation failed.')
        sql=run(['docker','exec','-i','--user','postgres',name,'pg_restore','--clean','--if-exists','--no-owner','--no-acl','--file','-'],dump,timeout=120)
        expected=json.dumps(manifest['tables']).replace("'","''")
        verify=table_summary_sql().replace("SELECT current_setting('hello.backup_summary');",'')
        seal="\n"+verify+"\nDO $verify$ BEGIN IF current_setting('hello.backup_summary')::jsonb <> '"+expected+"'::jsonb THEN RAISE EXCEPTION 'Backup table digest/count mismatch'; END IF; END $verify$;\nUPDATE public.notification_runtime SET sealed=true,migration_hold=true;\n"
        # Restore, compare and seal before the only commit, with no network or daemons.
        run(['docker','exec','-i','--user','postgres',name,'psql','-Xq','-1','-v','ON_ERROR_STOP=1','-d','postgres'],sql+seal.encode(),timeout=120)
        flags=run(['docker','exec','--user','postgres',name,'psql','-XqAt','-d','postgres','-c','SELECT sealed AND migration_hold FROM notification_runtime;']).strip()
        if flags!=b't':raise RuntimeError('Restored runtime not sealed.')
        result={'completed':time.time(),'seconds':round(time.time()-started,3),'tables_verified':len(manifest['tables']),'archive_members_verified':len(file_hashes),
            'database_sha256':manifest['database_sha256'],'schema':manifest['schema'],'network':'none','sealed':True,'migration_hold':True}
        save(ROOT/'last-restore-drill.json',result)
        return result
    finally:run(['docker','rm','-f','-v',name])
