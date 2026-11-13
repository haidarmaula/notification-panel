"""Focused operational safety regressions; no VM or provider access."""
import copy
import unittest
from ops_common import clone_args, maintenance_matches
from ops_health import evaluate


class OperationsTests(unittest.TestCase):
    def spec(self):
        return {'Name':'/hello-staging-server','Config':{'Cmd':None},'HostConfig':{
            'NetworkMode':'host','ReadonlyRootfs':True,'CapDrop':['ALL'],'CapAdd':None,
            'SecurityOpt':['no-new-privileges'],'Privileged':False,'Memory':536870912,
            'NanoCpus':1000000000,'PidsLimit':128,'RestartPolicy':{'Name':'unless-stopped'}},
            'Mounts':[{'Type':'volume','Name':'hello-staging-server-mobile-policy','Destination':'/etc/hello','RW':False}]}

    def test_replacement_keeps_active_policy_and_hardening(self):
        args=clone_args('server','sha256:'+'a'*64,self.spec())
        self.assertIn('type=volume,src=hello-staging-server-mobile-policy,dst=/etc/hello,readonly',args)
        self.assertIn('--read-only',args)
        self.assertIn('--cap-drop',args)

    def test_refuses_changed_mount_or_privileged_runtime(self):
        for kind in ['mount','privileged','network']:
            s=copy.deepcopy(self.spec())
            if kind=='mount':s['Mounts'][0]['RW']=True
            if kind=='privileged':s['HostConfig']['Privileged']=True
            if kind=='network':s['HostConfig']['NetworkMode']='bridge'
            with self.assertRaises(RuntimeError):clone_args('server','sha256:'+'a'*64,s)

    def test_never_clears_an_unrelated_incident_hold(self):
        before={'sealed':False,'migration_hold':False,'origin_epoch':'a','source_epoch':'b','policies':[{'revision':6,'hold':False}]}
        expected={**before,'migration_hold':True}
        self.assertTrue(maintenance_matches(before,expected))
        for change in [{'sealed':True},{'source_epoch':'c'},{'policies':[{'revision':7,'hold':True}]}]:
            self.assertFalse(maintenance_matches(before,{**expected,**change}))

    def test_stuck_work_and_new_provider_failure_raise_alerts(self):
        metrics={'hello_work_oldest_seconds{kind="send_outbox"}':700,
                 'hello_provider_results_total{disposition="permanent",reason="auth"}':2}
        old={**metrics,'hello_provider_results_total{disposition="permanent",reason="auth"}':1}
        alerts=evaluate(metrics,old)
        self.assertTrue(any('backlog' in a for a in alerts))
        self.assertTrue(any('provider' in a for a in alerts))

    def test_old_provider_error_does_not_alert_forever(self):
        metrics={'hello_provider_results_total{disposition="permanent",reason="auth"}':2}
        self.assertEqual(evaluate(metrics,metrics),[])




class DeploymentFailureTests(unittest.TestCase):
    def exercise(self,failure):
        import json
        import tempfile
        from pathlib import Path
        from unittest.mock import patch
        import ops_release as release
        before={'sealed':False,'migration_hold':False,'schema':{'version':41,'dirty':False},'policies':[]}
        current=dict(before);events=[];once=set()
        def enter():
            events.append('fence');current['migration_hold']=True;return dict(before)
        def leave(_):
            events.append('reopen')
            if failure=='reopen' and failure not in once:once.add(failure);raise RuntimeError('injected reopen failure')
            current['migration_hold']=False
        def run(args,**kwargs):
            if args[:2]==['docker','rename'] and failure=='rename' and failure not in once:
                once.add(failure);raise RuntimeError('injected rename failure')
            return b''
        def ready():
            if failure=='ready' and failure not in once:once.add(failure);raise RuntimeError('injected readiness failure')
        documentation={'current':None}
        def docs_verify(value):
            if failure=='docs' and value=='candidate' and failure not in once:once.add(failure);raise RuntimeError('injected docs integrity failure')
        from types import SimpleNamespace
        docs=SimpleNamespace(current=lambda:documentation['current'],publish=lambda *_:'candidate',switch=lambda value:documentation.update(current=value),verify=docs_verify)
        if failure=='docs':documentation['current']='previous'
        def stop():
            self.assertTrue(current['migration_hold'],'services must never stop unfenced');events.append('stop')
        def save(path,journal):
            if failure=='commit-write' and journal['phase']=='committed' and failure not in once:
                once.add(failure);raise OSError('injected disk failure after reopen')
            path.parent.mkdir(parents=True,exist_ok=True);path.write_text(json.dumps(journal))
        image='sha256:'+'a'*64
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);(root/'releases/r').mkdir(parents=True)
            (root/'releases/r/release.json').write_text(json.dumps({'release':'r','schema':before['schema'],'images':{r:image for r in release.ROLES},**({'docs_sha256':'digest'} if failure=='docs' else {})}))
            with patch.multiple(release,ROOT=root,state=lambda:dict(current),inspect=lambda _: {'Image':image},image_check=lambda *_:None,
                clone_args=lambda *_:['docker','create'],enter_maintenance=enter,leave_maintenance=leave,run=run,ready=ready,
                stop_roles=stop,exists=lambda _:True,save=save,docs=docs):
                with self.assertRaises((RuntimeError,OSError)):release.deploy('r')
                journal=json.loads((root/'deployment.json').read_text())
                if failure=='commit-write':
                    self.assertEqual(journal['phase'],'reopening')
                    self.assertEqual(events.count('stop'),1,'no rollback while dispatch is open')
                    self.assertEqual(release.recover()['phase'],'committed')
                else:self.assertEqual(journal['phase'],'rolled-back')
                if failure=='docs':self.assertEqual(documentation['current'],'previous')
        self.assertFalse(current['migration_hold'])

    def test_docs_failure_restores_both_backend_and_previous_docs(self):self.exercise('docs')
    def test_failure_during_rename_restores_fenced_originals(self):self.exercise('rename')
    def test_readiness_failure_rolls_back_before_reopening(self):self.exercise('ready')
    def test_reopen_failure_rechecks_fence_before_rollback(self):self.exercise('reopen')
    def test_crash_after_reopen_finalizes_without_stopping_open_services(self):self.exercise('commit-write')




class DiagnosticsRecoveryTests(unittest.TestCase):
    def test_unstable_originals_do_not_reopen_maintenance(self):
        import tempfile,json
        from pathlib import Path
        from unittest.mock import patch,Mock
        import ops_setup as setup
        before={'sealed':False,'migration_hold':False}
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);(root/'diagnostics-setup.json').write_text(json.dumps({'phase':'maintenance','before':before}))
            leave=Mock()
            with patch.multiple(setup,ROOT=root,state=lambda:{**before,'migration_hold':True},run=lambda *_:b'{}',
                stop_roles=lambda:None,write_policy=lambda *_:None,baseline_ready=Mock(side_effect=RuntimeError('restart loop')),leave_maintenance=leave):
                with self.assertRaises(RuntimeError):setup.recover_diagnostics()
                leave.assert_not_called()
                self.assertTrue((root/'diagnostics-setup.json').exists())

    def test_failed_preparation_cleanup_preserves_recovery_journal(self):
        import tempfile,json
        from pathlib import Path
        from unittest.mock import patch
        import ops_setup as setup
        before={'sealed':False,'migration_hold':False}
        def run(args):
            if args[:3]==['docker','volume','rm']:raise RuntimeError('busy volume')
            return b'hello-staging-pre-monitoring-policy'
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);(root/'diagnostics-setup.json').write_text(json.dumps({'phase':'prepared','before':before}))
            with patch.multiple(setup,ROOT=root,state=lambda:before,run=run):
                with self.assertRaises(RuntimeError):setup.recover_diagnostics()
                self.assertTrue((root/'diagnostics-setup.json').exists())


if __name__=='__main__':unittest.main()
