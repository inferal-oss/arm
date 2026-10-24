#!/usr/bin/env python3
"""Offline rejection and proof-roster guards for the publication gate."""
import copy
import importlib.util
import json
import os
import signal
import subprocess
import sys
import time
import shutil
import tempfile
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest import mock

ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('publication_gate',ROOT/'scripts/test-formal-publication.py')
gate=importlib.util.module_from_spec(spec);spec.loader.exec_module(gate)

def rows():
    row={k:('none' if kind is str else False if kind is bool else 0) for k,kind in gate.SCALARS.items()}
    row['schema']='arm.share-publication/v1'
    row['native']={s:{'status':'never','count':0,'joined':False,'skipped':False} for s in ('preparation','verification')}
    return [row,copy.deepcopy(row)]

class PublicationGuards(unittest.TestCase):

    def test_changed_helper_after_constructor_refuses_before_child(self):
        with tempfile.TemporaryDirectory(prefix='formal-initial-binding-') as directory:
            captured = Path(directory)
            fake_runner = captured/'test-formal.py'
            fake_helper = captured/'formal_process.py'
            fake_runner.write_bytes(b'captured runner')
            fake_helper.write_bytes(b'captured helper')
            work = gate.base.ROOT/'.tla'/('binding-'+captured.name)
            args = SimpleNamespace(mode='check', targets=[], work_dir=str(work))
            try:
                with mock.patch.object(gate.base, '__file__', str(fake_runner)):
                    instance = gate.PublicationGate(args)
                    self.assertIn(str(fake_helper.resolve()), instance.runner_sources)
                    fake_helper.write_bytes(b'changed helper before first child')
                    with mock.patch.object(gate.base, 'process_runtime', side_effect=AssertionError('no runtime/child')):
                        with self.assertRaisesRegex(RuntimeError, 'source changed'):
                            instance.command(['never-started'], 'binding')
            finally:
                import shutil
                if work.exists():
                    shutil.rmtree(work)

    def test_closed_native_scopes_and_scalar_types(self):
        valid=rows();gate.states(valid)
        for mutate in (lambda r:r[1]['native'].pop('verification'),
                       lambda r:r[1]['native']['preparation'].__setitem__('count',True),
                       lambda r:r[1]['native']['preparation'].__setitem__('joined',1),
                       lambda r:r[1].__setitem__('revision',True),
                       lambda r:r[1].__setitem__('extra','ignored')):
            bad=copy.deepcopy(valid);mutate(bad)
            with self.assertRaises(ValueError):gate.states(bad)
    def test_negative_cannot_start_from_invented_state(self):
        g=object.__new__(gate.PublicationGate);g.initial_state=rows()[0]
        g.tlc=lambda *a,**k:self.fail('must reject before invoking TLC')
        bad=rows();bad[0]['charged']=True
        with self.assertRaises(ValueError):g.replay(bad,'forged-init',accepted=False)
    def test_capacity_requires_exact_ordered_controls_before_tlc(self):
        cases=[]
        for rows,byte_count,reserved,event,old,new in gate.capacity_input_roster():
            before={'rows':rows,'bytes':byte_count,'reserved':reserved}
            cases.append({'before':before,'event':event,'old':old,'new':new,
                          'accepted':False,'after':copy.deepcopy(before)})
        self.assertEqual(len(cases),288)
        self.assertIs(gate.capacity_cases(cases),cases)
        # Repeated equal controls remain part of the reviewed producer roster.
        self.assertLess(len(set(gate.capacity_input_roster())),288)
        g=object.__new__(gate.PublicationGate)
        g.tlc=lambda *a,**k:self.fail('must reject before invoking TLC')
        substituted=copy.deepcopy(cases);substituted[-1]['event']=2
        reordered=copy.deepcopy(cases);reordered[0],reordered[-1]=reordered[-1],reordered[0]
        for bad in ([copy.deepcopy(cases[0]) for _ in cases],substituted,reordered):
            with self.assertRaisesRegex(ValueError,'capacity input roster differs'):
                g.capacity(bad)
        # Structure validation does not invent an oracle for Go's result.
        changed_result=copy.deepcopy(cases);changed_result[0]['accepted']=True
        changed_result[0]['after']['rows']=9
        self.assertIs(gate.capacity_cases(changed_result),changed_result)
    def proof_gate(self):
        (ROOT/'.tla').mkdir(exist_ok=True)
        temp=tempfile.TemporaryDirectory(prefix='proof-roster-',dir=ROOT/'.tla')
        self.addCleanup(temp.cleanup)
        root=Path(temp.name)
        (root/'scripts').mkdir()
        for name in gate.CODE_FILES:shutil.copyfile(ROOT/name,root/name)
        model=root/'formal/share-publication';model.mkdir(parents=True)
        for name in gate.FORMAL_FILES:shutil.copyfile(ROOT/'formal/share-publication'/name,model/name)
        patch=mock.patch.object(gate,'ROOT',root);patch.start();self.addCleanup(patch.stop)
        # Small real files stand in for tool provenance; these tests never claim
        # that their synthetic success text is a mathematical proof.
        self.tools=root/'tools';self.tools.mkdir()
        for name in ('z3','zenon','isabelle'):
            path=self.tools/name;path.write_text('#!/bin/sh\necho synthetic-tool\n');path.chmod(0o700)
        ml=self.tools/'poly';ml.mkdir();(ml/'poly').write_text('actual poly fixture bytes')
        isa=self.tools/'isabelle-home';(isa/'heaps').mkdir(parents=True)
        for name in ('Pure','TLA+','Options'):(isa/'heaps'/name).write_text('actual heap fixture '+name)
        lib=self.tools/'stdlib';lib.mkdir();(lib/'TLAPS.tla').write_text('actual TLAPS fixture')
        self.config=('library_path == "'+str(lib)+'"\nsearch_path == << "'+str(lib)+'" >>\n'
            +"PATH='"+str(self.tools)+"'\nexport ISABELLE_HOME='"+str(isa)+"'\nexport ML_HOME='"+str(ml)+"'\n")
        exe=self.tools/'tlapm';exe.write_text('synthetic prover bytes')
        g=gate.PublicationGate(SimpleNamespace(mode='prove',targets=list(gate.TARGETS),
            work_dir=str(root/'.tla/work'),tlapm=str(exe)))
        g.test_root=root
        return g
    def summary(self,module,count=19):
        return '---- summary of module "'+module+'" ----\nobligations_count = '+str(count)+'\n'
    def fake_command(self,g,observe=None):
        calls=[]
        def command(argv,name,**kwargs):
            calls.append((list(map(str,argv)),name,kwargs))
            if observe:observe(argv,name)
            if '--config' in argv:return self.config
            if '--summary' in argv:return self.summary(Path(argv[-1]).stem)
            return 'All 19 obligations proved.\n'
        g.command=command
        return calls
    def test_every_proof_module_checked_with_actual_logic(self):
        g=self.proof_gate();calls=self.fake_command(g);g.prove()
        active=[c for c in calls if '--config' not in c[0]]
        self.assertEqual([Path(c[0][-1]).stem for c in active],
            [m for m in gate.PROOF_MODULES for _ in range(2)])
        proof_calls=[c for c in active if '--summary' not in c[0]]
        self.assertEqual(len(proof_calls),27)
        self.assertEqual(len(g.summary['qualified_theorems']),242)
        for argv,_,kwargs in proof_calls:
            self.assertIn('--strict',argv);self.assertIn('--nofp',argv)
            self.assertEqual(argv[argv.index('--stretch')+1],'10')
            self.assertEqual(kwargs['timeout'],3600)
        self.assertEqual(proof_calls[0][0][proof_calls[0][0].index('--smt-logic')+1],'AUFNIA')
        self.assertTrue(all('--smt-logic' not in c[0] for c in proof_calls[1:]))
        caches=[c[0][c[0].index('--cache-dir')+1] for c in active]
        self.assertEqual(len(set(caches)),54)
        self.assertEqual(g.summary['proof_status'],'passed')
        self.assertEqual(len(g.summary['closure_checks']),109)
    def test_zero_partial_error_or_wrong_count_proof_never_passes(self):
        for text in ('All 0 obligations proved.','18/19 obligations proved.',
                     'All 19 obligations proved.\n[ERROR]: failed','All 18 obligations proved.',
                     'All 19 obligations proved.\nAll 18 obligations proved.',
                     'prefix All 19 obligations proved. suffix'):
            g=self.proof_gate()
            g.command=lambda argv,*a,**k:self.config if '--config' in argv else self.summary(Path(argv[-1]).stem) if '--summary' in argv else text
            with self.assertRaises(RuntimeError):g.prove()
            self.assertEqual(g.summary['gates'],[])
    def test_invalid_parser_scope_never_starts_proof(self):
        for parsed in (self.summary('Other'),self.summary('JournalCapacityProof',0),
                       self.summary('JournalCapacityProof')+'obligations_count = 19\n'):
            g=self.proof_gate();calls=[]
            def command(argv,*a,**k):
                if '--config' in argv:return self.config
                self.assertIn('--summary',argv);calls.append(argv);return parsed
            g.command=command
            with self.assertRaises(RuntimeError):g.prove()
            self.assertEqual(len(calls),1)
    def test_module_omission_substitution_extra_import_and_prefix_refused(self):
        for mutation in ('missing','substitution','import','prefix','extra','missing-invocation','axiom'):
            g=self.proof_gate();model=g.test_root/'formal/share-publication'
            path=model/'SharePublicationNativeProof.tla'
            if mutation=='missing':path.unlink()
            elif mutation=='substitution':path.write_text(path.read_text().replace('THEOREM NativeEqualityProjects','THEOREM ForeignNativeEqualityProjects'))
            elif mutation=='import':path.write_text(path.read_text().replace('EXTENDS SharePublicationDefinitions','EXTENDS SharePublicationDefinitions, Sequences'))
            elif mutation=='prefix':path.write_text(path.read_text().replace('THEOREM NativeEqualityProjects','====\nTHEOREM NativeEqualityProjects'))
            elif mutation=='extra':(model/'UncheckedProof.tla').write_text('---- MODULE UncheckedProof ----\n====\n')
            elif mutation=='axiom':path.write_text(path.read_text().replace('EXTENDS SharePublicationDefinitions','EXTENDS SharePublicationDefinitions\n  ASSUME TRUE'))
            else:
                reduced=dict(gate.PROOF_THEOREMS);del reduced['SharePublicationNativeProof']
                patch=mock.patch.object(gate,'PROOF_THEOREMS',reduced);patch.start();self.addCleanup(patch.stop)
            g.command=lambda *a,**k:self.fail('must reject before invoking any tool')
            with self.assertRaises(ValueError):g.prove()
            if mutation=='missing-invocation':patch.stop()
    def test_missing_or_changed_initial_capture_refused(self):
        for mutation in ('missing','capture','original','ambient'):
            g=self.proof_gate()
            if mutation=='missing':g.summary.pop('sources')
            elif mutation=='ambient':(g.test_root/'TLAPS.tla').write_text('shadow')
            else:
                path=(g.run if mutation=='capture' else g.test_root/'formal/share-publication')/'SharePublication.tla'
                path.write_text(path.read_text()+'\n(* mutation *)\n')
            g.command=lambda *a,**k:self.fail('must reject before tools')
            with self.assertRaises(RuntimeError):g.prove()
    def test_source_backend_and_runner_mutation_at_each_boundary_refused(self):
        for boundary in ('parser','proof'):
            for mutation in ('source','original','backend','library','runner'):
                g=self.proof_gate();calls=[]
                def command(argv,name,**kwargs):
                    if '--config' in argv:return self.config
                    parsing='--summary' in argv;calls.append(argv)
                    if parsing==(boundary=='parser'):
                        if mutation=='source':p=g.run/'proof-source/SharePublication.tla'
                        elif mutation=='original':p=g.test_root/'formal/share-publication/SharePublication.tla'
                        elif mutation=='backend':p=self.tools/'poly/poly'
                        elif mutation=='library':p=self.tools/'stdlib/TLAPS.tla'
                        else:p=g.test_root/'scripts/test-formal.py'
                        p.write_text(p.read_text()+'\n(* actual mutation *)\n')
                    return self.summary(Path(argv[-1]).stem) if parsing else 'All 19 obligations proved.\n'
                g.command=command
                with self.assertRaisesRegex(RuntimeError,'changed'):g.prove()
                self.assertEqual(len(calls),1 if boundary=='parser' else 2)
                self.assertEqual(g.summary['gates'],[])
    def test_late_failed_helper_cannot_be_replaced_by_imported_facts(self):
        g=self.proof_gate()
        def command(argv,name,**kwargs):
            if '--config' in argv:return self.config
            if '--summary' in argv:return self.summary(Path(argv[-1]).stem)
            if Path(argv[-1]).stem=='SharePublicationCommitUnavailableProof':
                return 'All 18 obligations proved.\n'
            return 'All 19 obligations proved.\n'
        g.command=command
        with self.assertRaises(RuntimeError):g.prove()
        self.assertEqual(len(g.summary['proof_progress']),25)
        self.assertEqual(g.summary['proof_status'],'incomplete')
        self.assertEqual(g.summary['gates'],[])
    def test_missing_actual_polyml_or_changed_config_cannot_pass(self):
        for mutation in ('missing-poly','config'):
            g=self.proof_gate()
            if mutation=='missing-poly':(self.tools/'poly/poly').unlink()
            def command(argv,name,**kwargs):
                if '--config' in argv:return self.config
                if '--summary' in argv:
                    self.config+='\nchanged actual configured command\n'
                    return self.summary(Path(argv[-1]).stem)
                self.fail('must reject before backend proof')
            g.command=command
            with self.assertRaises((ValueError,RuntimeError)):g.prove()
            self.assertEqual(g.summary['gates'],[])
    def test_differing_per_backend_paths_refused_before_proof(self):
        g=self.proof_gate();self.config+="\nPATH='/different-unreviewed-backend'\n"
        def command(argv,name,**kwargs):
            if '--config' in argv:return self.config
            self.fail('inconsistent tool search paths must be refused before parsing/proving')
        g.command=command
        with self.assertRaisesRegex(ValueError,'consistent configured backend PATH'):g.prove()
        self.assertEqual(g.summary['gates'],[])
    def test_real_signal_cancellation_retains_output_and_reaps_owned_children(self):
        for sig,code in ((signal.SIGINT,130),(signal.SIGTERM,143)):
            g=self.proof_gate();ready=g.test_root/'ready.json'
            exe=Path(g.args.tlapm)
            program="""#!/usr/bin/env python3
import json,os,signal,subprocess,sys,time
if '--version' in sys.argv:print('synthetic cancellation fixture');sys.exit(0)
if '--config' in sys.argv:print(CONFIG,end='');sys.exit(0)
child=subprocess.Popen([sys.executable,'-c','import signal,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); time.sleep(20)'])
print('owned fixture child '+str(child.pid),flush=True)
with open(READY+'.tmp','w') as f:json.dump({'child':child.pid,'group':os.getpgrp()},f)
os.replace(READY+'.tmp',READY)
while True:time.sleep(1)
""".replace('CONFIG',repr(self.config)).replace('READY',repr(str(ready)))
            exe.write_text(program);exe.chmod(0o700)
            process=gate.base.process_runtime().Fixture([sys.executable,str(g.test_root/'scripts/test-formal-publication.py'),
                '--mode','prove','--tlapm',str(exe),'--work-dir',str(g.test_root/'.tla/cli')],
                g.test_root/'outer.log',g.test_root,env=dict(os.environ,PYTHONDONTWRITEBYTECODE='1'))
            owned=None
            try:
                deadline=time.monotonic()+15
                while not ready.exists() and time.monotonic()<deadline and process.terminal() is None:time.sleep(0.02)
                if not ready.exists():
                    _,output=process.wait(timeout=2)
                    self.fail('fixture did not reach parser child: '+output)
                owned=json.loads(ready.read_text())
                process.send(sig)
                actual_exit,output=process.wait(timeout=10)
                self.assertEqual(actual_exit,code,output)
                summaries=list((g.test_root/'.tla/cli').glob('run-*/summary.json'))
                self.assertEqual(len(summaries),1)
                doc=json.loads(summaries[0].read_text());self.assertEqual(doc['status'],'incomplete')
                self.assertFalse(any(row.get('proofs')=='passed' for row in doc['gates']))
                logs=list(summaries[0].parent.glob('*-scope.log'))
                self.assertEqual(len(logs),1)
                self.assertIn('owned fixture child '+str(owned['child']),logs[0].read_text())
                # A Linux orphan can briefly remain as an exited zombie. It is not
                # live owned work and must not make cleanup look like a failure.
                deadline=time.monotonic()+3
                while time.monotonic()<deadline:
                    try:os.kill(owned['child'],0)
                    except ProcessLookupError:
                        state='';break
                    probe=subprocess.run(['ps','-o','stat=','-p',str(owned['child'])],capture_output=True,text=True)
                    if probe.returncode:
                        # A race with process exit is fine; a failed observation of
                        # a still-existing PID cannot establish child cleanup.
                        try:os.kill(owned['child'],0)
                        except ProcessLookupError:
                            state='';break
                        self.fail('cannot observe owned child: '+probe.stderr)
                    state=probe.stdout.strip()
                    self.assertTrue(state,'empty state for existing owned child')
                    if state.startswith('Z'):break
                    time.sleep(0.02)
                self.assertTrue(not state or state.startswith('Z'),'owned child still live: '+state)
            finally:
                process.close()  # Fresh outer owner only; no historical group.
                # Inner harmless fixture children independently expire at20s.

if __name__=='__main__':unittest.main()
