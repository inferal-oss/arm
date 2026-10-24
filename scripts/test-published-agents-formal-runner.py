#!/usr/bin/env python3
"""Closed-schema and source/proof roster guards for the publication suite."""
import copy
import os
import signal
import subprocess
import sys
import time
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace

path=Path(__file__).with_name('test-published-agents-formal.py')
spec=importlib.util.spec_from_file_location('publication_suite',path)
suite=importlib.util.module_from_spec(spec)
spec.loader.exec_module(suite)

class PublicationRunnerGuards(unittest.TestCase):

    def test_changed_helper_after_constructor_refuses_before_child(self):
        with tempfile.TemporaryDirectory(prefix='formal-initial-binding-') as directory:
            captured = Path(directory)
            fake_runner = captured/'test-formal.py'
            fake_helper = captured/'formal_process.py'
            fake_runner.write_bytes(b'captured runner')
            fake_helper.write_bytes(b'captured helper')
            work = suite.base.ROOT/'.tla'/('binding-'+captured.name)
            args = SimpleNamespace(mode='check', targets=[], work_dir=str(work))
            try:
                with patch.object(suite.base, '__file__', str(fake_runner)):
                    instance = suite.PublishedGate(args)
                    self.assertIn(str(fake_helper.resolve()), instance.runner_sources)
                    fake_helper.write_bytes(b'changed helper before first child')
                    with patch.object(suite.base, 'process_runtime', side_effect=AssertionError('no runtime/child')):
                        with self.assertRaisesRegex(RuntimeError, 'source changed'):
                            instance.command(['never-started'], 'binding')
            finally:
                import shutil
                if work.exists():
                    shutil.rmtree(work)

    def state(self):
        row={name:False if kind is bool else 0 if kind is int else '' for name,kind in suite.FIELDS.items()}
        row['schema']='arm.published-agents/v1'
        return row
    def test_closed_scalar_observations(self):
        row=self.state()
        suite.validate_states([row,row])
        for name,mutate in {
            'missing':lambda r:r.pop('budget_ok'),
            'unknown':lambda r:r.update(extra=0),
            'alias':lambda r:r.update(Schema=r.pop('schema')),
            'schema':lambda r:r.update(schema='arm.unknown/v1'),
            'integer_bool':lambda r:r.update(native_calls=True),
            'negative':lambda r:r.update(dispatches=-1),
            'unicode':lambda r:r.update(action='é'),
            'oversize':lambda r:r.update(action='x'*129),
        }.items():
            with self.subTest(name=name):
                changed=copy.deepcopy(row);mutate(changed)
                with self.assertRaises(ValueError):suite.validate_states([row,changed])
        for rows in ([],[row],[row]*129):
            with self.assertRaises(ValueError):suite.validate_states(rows)
    def test_exact_complete_model_proof_roster(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp)
            for name in suite.MODEL_FILES:(root/name).write_text('fixture')
            self.assertEqual(set(suite.model_roster(root)),suite.MODEL_FILES)
            (root/'Unproved.tla').write_text('new local proof dependency')
            with self.assertRaises(ValueError):suite.model_roster(root)
            (root/'Unproved.tla').unlink();(root/'PublishedAgentsProof.tla').unlink()
            with self.assertRaises(ValueError):suite.model_roster(root)
            (root/'PublishedAgentsProof.tla').symlink_to(root/'PublishedAgents.tla')
            with self.assertRaises(ValueError):suite.model_roster(root)
    def test_complete_proof_scope_before_prover_invocation(self):
        source=(suite.MODEL/'PublishedAgentsProof.tla').read_text()
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp)
            for name in suite.MODEL_FILES:(root/name).write_bytes((suite.MODEL/name).read_bytes())
            proof=root/'PublishedAgentsProof.tla'
            gate=object.__new__(suite.PublishedGate);gate.run=root;gate.summary={}
            called=[]
            def prefix_success(*args,**kwargs):
                called.append(args);return 'All 9 obligations proved.'
            gate.command=prefix_success
            for name,text in {
                'early_end':source.replace('BoundedCapacity(a,b,p,j,l1,l2,budget) ==','=========\nBoundedCapacity(a,b,p,j,l1,l2,budget) ==',1),
                'missing_safety':source.replace('THEOREM Safety ==','THEOREM OtherSafety =='),
                'missing_preservation':source.replace('THEOREM PreserveAccept1 ==','THEOREM OtherPreserveAccept1 =='),
                'trailing_source':source+'THEOREM Trailing == TRUE BY SMT\n',
            }.items():
                with self.subTest(name=name):
                    proof.write_text(text)
                    gate.summary['sources']=suite.proof_source_hashes(root)
                    with self.assertRaises(ValueError):gate.prove()
                    self.assertEqual(called,[],'invalid scope reached a prefix-success prover')
            proof.write_text(source)
            parsed=suite.base.validate_module_source(proof,suite.PROOF_THEOREMS)
            self.assertIn('Safety',parsed)
            self.assertEqual(len([n for n in parsed if n.startswith('Preserve')]),24)
    def test_positive_prefix_count_is_not_complete_proof(self):
        from types import SimpleNamespace
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp)
            for name in suite.MODEL_FILES:(root/name).write_bytes((suite.MODEL/name).read_bytes())
            gate=object.__new__(suite.PublishedGate);gate.run=root;gate.summary={'sources':suite.proof_source_hashes(root)}
            gate.args=SimpleNamespace(tlapm='offline-fixture')
            calls=[]
            def command(argv,label,**kwargs):
                calls.append(label)
                return ('---- summary of module "PublishedAgentsProof" ----\n  obligations_count = 1531\n====\n'
                        if label=='tlapm-scope' else 'All 9 obligations proved.')
            gate.command=command
            with self.assertRaises(RuntimeError):gate.prove()
            self.assertEqual(calls,['tlapm-scope','tlapm'])
    def test_ambiguous_success_counts_and_changed_sources_refused(self):
        from types import SimpleNamespace
        for case in ('duplicate_same','duplicate_different','before_prove_change','summary_source_change','proof_source_change','valid'):
            with self.subTest(case=case),tempfile.TemporaryDirectory() as tmp:
                root=Path(tmp)
                for name in suite.MODEL_FILES:(root/name).write_bytes((suite.MODEL/name).read_bytes())
                gate=object.__new__(suite.PublishedGate);gate.run=root;gate.summary={'gates':[],'sources':suite.proof_source_hashes(root)}
                if case=='before_prove_change':
                    model=root/'PublishedAgents.tla';model.write_text(model.read_text()+'\n(* changed before prove *)\n')
                gate.args=SimpleNamespace(tlapm='offline-fixture')
                calls=[]
                def command(argv,label,**kwargs):
                    calls.append(label)
                    if (case=='summary_source_change' and label=='tlapm-scope'
                            or case=='proof_source_change' and label=='tlapm'):
                        model=root/'PublishedAgents.tla';model.write_text(model.read_text()+'\n(* changed during command *)\n')
                    if label=='tlapm-scope':return '---- summary of module "PublishedAgentsProof" ----\n  obligations_count = 1531\n====\n'
                    if case=='duplicate_same':return 'All 1531 obligations proved.\nAll 1531 obligations proved.'
                    if case=='duplicate_different':return 'All 1531 obligations proved.\nAll 9 obligations proved.'
                    return 'All 1531 obligations proved.'
                gate.command=command
                if case=='valid':
                    gate.prove();self.assertEqual(len(gate.summary['gates']),1)
                    self.assertEqual(set(gate.summary['gates'][0]['source_sha256']),suite.MODEL_FILES)
                else:
                    with self.assertRaises(RuntimeError):gate.prove()
                    self.assertEqual(gate.summary['gates'],[])
                if case=='summary_source_change':self.assertEqual(calls,['tlapm-scope'])
                if case=='before_prove_change':self.assertEqual(calls,[])
    def test_duplicate_json_and_edges(self):
        with self.assertRaises(ValueError):suite.base.strict_object([('action','one'),('action','two')])
        row=self.state();other=copy.deepcopy(row);other['action']='discover'
        doc=suite.base.envelope([row,other])
        self.assertEqual(doc['counterexample']['action'][0][0],doc['counterexample']['state'][0])
        self.assertEqual(doc['counterexample']['action'][0][2],doc['counterexample']['state'][1])
    def test_negative_inventory_cannot_silently_be_empty(self):
        initial=self.state();initial['action']='init'
        def trace(*actions):
            rows=[copy.deepcopy(initial)]
            for action in actions:
                row=copy.deepcopy(rows[-1]);row['action']=action
                if action=='accept1':row.update(dispatches=1,generation1=1)
                if action=='reply2':row.update(reply_to=1)
                rows.append(row)
            return rows
        actual={'accepted':trace('publish1','prepare','begin','accept1'),
                'replacement_reply':trace('publish1','change','publish2','accept2','reply2'),
                'lost_ack_restart':trace('publish1','prepare','begin','accept1','unknown','crash','restart'),
                'acknowledged':trace('publish1','prepare','begin','accept1','ack1')}
        negatives=suite.synthetic_negatives(actual)
        self.assertEqual(len(negatives),8)
        self.assertTrue(all(rows[0]==initial for rows in negatives.values()))
        actual['lost_ack_restart']=[r for r in actual['lost_ack_restart'] if r['action']!='unknown']
        with self.assertRaises(ValueError):suite.synthetic_negatives(actual)

class PublicationCLIProcessGuards(unittest.TestCase):
    def test_duplicate_targets_refused_before_tools_or_state(self):
        result=subprocess.run([sys.executable,str(path),'--mode','check','--targets','accepted','accepted'],capture_output=True,text=True)
        self.assertEqual(result.returncode,2)
        self.assertIn('duplicate targets',result.stderr)
    def test_actual_wrapper_signals_cleanup_owned_descendants_and_output(self):
        artifact_root=suite.ROOT/'.tla'
        artifact_root.mkdir(mode=0o700,exist_ok=True)
        for sig in (signal.SIGINT,signal.SIGTERM):
            with self.subTest(signal=sig), tempfile.TemporaryDirectory(prefix='publication-signal-',dir=artifact_root) as tmp:
                case=Path(tmp)
                leader,child=case/'leader.pid',case/'child.pid'
                helper=case/'java'
                child_code="import os,signal,sys,time;from pathlib import Path;signal.signal(signal.SIGTERM,signal.SIG_IGN);print('child ready',flush=True);Path(sys.argv[1]).write_text(str(os.getpid()));time.sleep(20)"
                helper.write_text('#!'+sys.executable+'\n'+
                    'import os,signal,subprocess,sys,time\nfrom pathlib import Path\n'+
                    "if '-version' in sys.argv: print('offline Java fixture');raise SystemExit(0)\n"+
                    "if '-help' in sys.argv: print('TLC - offline fixture Version 1');raise SystemExit(1)\n"+
                    'signal.signal(signal.SIGTERM,signal.SIG_IGN)\n'+
                    'Path(os.environ["REVIEW_LEADER"]).write_text(str(os.getpid()))\n'+
                    'subprocess.Popen([sys.executable,"-u","-c",'+repr(child_code)+',os.environ["REVIEW_CHILD"]])\n'+
                    'print("tool ready",flush=True)\ntime.sleep(20)\n')
                helper.chmod(0o700)
                jar=case/'fixture.jar';jar.write_text('offline fixture')
                work=case/'work'
                env=dict(os.environ,PYTHONDONTWRITEBYTECODE='1',REVIEW_LEADER=str(leader),REVIEW_CHILD=str(child))
                process=suite.base.process_runtime().Fixture([sys.executable,str(path),'--mode','check','--java',str(helper),'--tlc-jar',str(jar),'--work-dir',str(work)],case/'outer.log',suite.ROOT,env=env)
                try:
                    until=time.monotonic()+10
                    while (not child.exists() or not child.stat().st_size) and time.monotonic()<until:
                        if process.terminal() is not None:self.fail('wrapper exited before owned fixture: '+process.wait()[1])
                        time.sleep(.02)
                    self.assertTrue(child.exists() and child.stat().st_size,'owned child did not start')
                    pids=[int(p.read_text()) for p in (leader,child)]
                    process.send(sig)
                    actual_exit,output=process.wait(timeout=10)
                    self.assertEqual(actual_exit,128+sig,output)
                    logs=list(work.glob('run-*/safety.log'))
                    self.assertEqual(len(logs),1)
                    self.assertIn('tool ready',logs[0].read_text())
                    self.assertIn('child ready',logs[0].read_text())
                    for pid in pids:
                        result=subprocess.run(['ps','-o','stat=','-p',str(pid)],text=True,capture_output=True,timeout=5)
                        status=result.stdout.strip()
                        self.assertTrue(not status or status.startswith(('Z','X')),'owned tool or descendant survived signal')
                    self.assertFalse(list(work.glob('run-*/summary.json')),'interrupted run emitted success receipt')
                finally:
                    process.close()  # Never signal a PID/group from saved files.
                    # The inner harmless parent/child are independently20s bounded.

if __name__=='__main__':unittest.main()
