"""Signed staging-CD protocol safety tests; no GitHub, Docker or provider access."""
import base64
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from cd_protocol import REPOSITORY, REQUIRED_JOBS, checked_run, signed_message, verify_message

SHA='a'*40

class CDProtocolTests(unittest.TestCase):
    def run_info(self):return {'id':123,'run_attempt':1,'event':'push','status':'completed','conclusion':'success','head_sha':SHA,'head_branch':'main','path':'.github/workflows/ci.yml','head_repository':{'full_name':REPOSITORY}}
    def jobs(self):return [{'name':n,'conclusion':'success'} for n in REQUIRED_JOBS]
    def test_only_successful_same_repo_main_push_is_eligible(self):
        checked_run(self.run_info(),self.jobs(),SHA)
        for change in [{'event':'pull_request'},{'head_branch':'feature'},{'conclusion':'failure'},{'status':'in_progress'},{'head_sha':'b'*40},{'head_repository':{'full_name':'attacker/fork'}}]:
            with self.assertRaises(ValueError):checked_run({**self.run_info(),**change},self.jobs(),SHA)
    def test_every_gate_must_have_exactly_one_success(self):
        for jobs in [self.jobs()[:-1],[*self.jobs(),self.jobs()[0]],[{**j,'conclusion':'skipped'} if i==0 else j for i,j in enumerate(self.jobs())]]:
            with self.assertRaises(ValueError):checked_run(self.run_info(),jobs,SHA)
    def test_signed_marker_rejects_tampering_wrong_scope_and_wrong_key(self):
        with tempfile.TemporaryDirectory() as d:
            d=Path(d)
            for name in ['trusted','wrong']:
                subprocess.run(['openssl','genpkey','-algorithm','ED25519','-out',str(d/(name+'.key'))],check=True,capture_output=True)
                subprocess.run(['openssl','pkey','-in',str(d/(name+'.key')),'-pubout','-out',str(d/(name+'.pub'))],check=True,capture_output=True)
            payload={'version':1,'repository':REPOSITORY,'sha':SHA,'schema':41,'ci_run_id':123,'ci_run_attempt':1}
            message=signed_message(payload,d/'trusted.key')
            self.assertEqual(verify_message(message,d/'trusted.pub',SHA,41),payload)
            with self.assertRaises(ValueError):verify_message(message,d/'wrong.pub',SHA,41)
            with self.assertRaises(ValueError):verify_message(message,d/'trusted.pub','b'*40,41)
            with self.assertRaises(ValueError):verify_message(message,d/'trusted.pub',SHA,42)
            obj=json.loads(message);raw=json.loads(base64.b64decode(obj['payload']));raw['schema']=42
            obj['payload']=base64.b64encode(json.dumps(raw).encode()).decode()
            with self.assertRaises(ValueError):verify_message(json.dumps(obj),d/'trusted.pub',SHA,42)




class CDPollTests(unittest.TestCase):
    def exercise(self,stale=False,tamper=False,paused=False,blocked=False):
        import contextlib
        from unittest.mock import patch,Mock
        import ops_cd as cd
        import ops
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);folder=root/'cd';(folder/'repository.git').mkdir(parents=True)
            (folder/'config.json').write_text(json.dumps({'repository':REPOSITORY,'schema':41,'enabled':True}))
            private=folder/'private'
            subprocess.run(['openssl','genpkey','-algorithm','ED25519','-out',str(private)],check=True,capture_output=True)
            subprocess.run(['openssl','pkey','-in',str(private),'-pubout','-out',str(folder/'verification.pem')],check=True,capture_output=True)
            payload={'version':1,'repository':REPOSITORY,'sha':SHA,'schema':41,'ci_run_id':123,'ci_run_attempt':1}
            message=signed_message(payload,private)
            if tamper:message=message.replace('signature','changed')
            tag='object '+SHA+'\ntype commit\ntag staging-ready/'+SHA+'\n\n'+message+'\n'
            def git(*args):
                if args[0]=='fetch':return b''
                if args[0]=='rev-parse':return SHA.encode()
                if args[0]=='cat-file':return tag.encode()
                if args[0]=='archive':return b'archive'
                raise AssertionError(args)
            def prepare(release,sha,schema,archive=None):
                p=root/'releases'/release;p.mkdir(parents=True)
                (p/'release.json').write_text(json.dumps({'revision':sha,'schema':{'version':schema,'dirty':False},'images':{}}))
            deployed=Mock()
            incident=Mock()
            @contextlib.contextmanager
            def lock(**kwargs):
                if paused and not kwargs:(folder/'config.json').write_text(json.dumps({'repository':REPOSITORY,'schema':41,'enabled':False}))
                yield
            with patch.multiple(cd,ROOT=root,git=git,current_remote_sha=lambda:'b'*40 if stale else SHA,lock=lock,
                state=lambda:{'schema':{'version':41,'dirty':False}},backup=Mock(),deploy=deployed,run=Mock(),db=incident,rollback=Mock(side_effect=RuntimeError('pending deliveries')),health=lambda:{'roles':{'server':'unready' if blocked else 'ready'},'alerts':[]}),patch.object(ops,'prepare',prepare):
                if tamper:
                    with self.assertRaises(ValueError):cd.poll()
                    deployed.assert_not_called();return
                if blocked:
                    with self.assertRaises(RuntimeError):cd.poll()
                    status=json.loads((folder/'status.json').read_text());self.assertEqual(status['phase'],'rollback-blocked');self.assertTrue(status['incident_hold_applied']);incident.assert_called_once()
                    self.assertEqual(cd.poll()['phase'],'retry-paused');return
                result=cd.poll()
                if paused:self.assertEqual(result['phase'],'disabled');deployed.assert_not_called();return
                if stale:self.assertEqual(result['phase'],'superseded');deployed.assert_not_called()
                else:
                    self.assertEqual(result['phase'],'deployed');deployed.assert_called_once()
                    self.assertEqual(cd.poll()['phase'],'unchanged');deployed.assert_called_once()

    def test_current_signed_commit_deploys_once(self):self.exercise()
    def test_newer_main_suppresses_old_ci_completion(self):self.exercise(stale=True)
    def test_invalid_signature_never_builds_or_deploys(self):self.exercise(tamper=True)
    def test_pause_during_build_prevents_activation(self):self.exercise(paused=True)
    def test_blocked_rollback_holds_dispatch_and_stops_automatic_retries(self):self.exercise(blocked=True)


class ScheduledBackupLockTests(unittest.TestCase):
    def test_backup_waits_for_a_short_health_check_lock(self):
        import fcntl,os,threading,time
        from unittest.mock import patch
        import ops_common as common
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);fd=os.open(root/'operations.lock',os.O_CREAT|os.O_RDWR,0o600)
            fcntl.flock(fd,fcntl.LOCK_EX)
            release=threading.Timer(.2,lambda:fcntl.flock(fd,fcntl.LOCK_UN));release.start()
            started=time.monotonic()
            try:
                with patch.object(common,'ROOT',root),patch.object(common.socket,'gethostname',return_value='srv-kafka'):
                    with common.lock(timeout=2):self.assertGreaterEqual(time.monotonic()-started,.15)
            finally:release.join();os.close(fd)


if __name__=='__main__':unittest.main()
