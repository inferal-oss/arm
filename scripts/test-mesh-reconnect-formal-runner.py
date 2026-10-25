#!/usr/bin/env python3
"""Offline reconnect runner guards; fake tools are fixtures, never real solvers."""
import argparse
import hashlib
import gc
import errno
import weakref
import importlib.util
import json
import os
from pathlib import Path
import signal
import shutil
import subprocess
import sys
import tempfile
import time
import types
import unittest
from unittest.mock import patch

sys.dont_write_bytecode=True
spec=importlib.util.spec_from_file_location('reconnect_formal_runner',Path(__file__).with_name('test-mesh-reconnect-formal.py'))
formal=importlib.util.module_from_spec(spec);spec.loader.exec_module(formal)

OWNED=[]
RETAINED=set()

def close_owned_cli_fixture(runner, observed_pids):
    # observed_pids is diagnostic data only, even after a failed assertion or
    # positive Wait. It can never adopt a process or restore signal authority.
    if runner is None:
        raise RuntimeError('retained CLI owner unavailable; saved PID metadata grants no cleanup authority')
    runner.close()

def native_cancellation_contract(native, log, expected_leader, positive_descendant=False):
    """Classify only this fixture's exact clean/known-denied cancellation frames.

    A reporting-control pass never turns a denied native cleanup into success.
    Unknown/missing/other failures remain refused; this function owns no process.
    """
    if type(positive_descendant) is not bool or type(expected_leader) is not int or expected_leader<=0:
        raise ValueError('exact fixture mode/leader required')
    inner=native['owner']
    if (any(type(native[k]) is not int for k in ('actual_exit','cancellation_received')) or
            any(type(inner[k]) is not int for k in ('pid','native_wait_calls','observed_exit','native_wait_status'))):
        raise ValueError('typed native scalar fields required')
    if (native['status']!='failed' or native['actual_exit']!=-signal.SIGTERM or
            native['error_type']!='SystemExit' or native['error']!='143' or
            native['cancellation_received']!=signal.SIGTERM or native['cancellation_pending'] is not None or
            native['source_sha256']!='221acd10f39f979c0ece89055361fcd860ab1cdd7e4ddeb816c36abbfc758b76' or
            inner['pid']!=expected_leader or inner['reaped'] is not True or inner['retired'] is not True or inner['authority'] is not False or
            inner['native_wait_calls']!=1 or inner['unknown'] is not None or
            inner['observed_exit']!=-signal.SIGTERM or
            os.waitstatus_to_exitcode(inner['native_wait_status'])!=-signal.SIGTERM or
            inner['post_wait_absence']!={'pid':'absent','group':'absent'} or inner['strongly_quarantined_until_native_reap'] is not False or native.get('output_refused') is not None):
        raise ValueError('cancellation frame/closure not established')
    rows=inner['signals']
    term={'signal':int(signal.SIGTERM),'retained_before_attempt':True,'result':'sent'}
    if (type(rows) is not list or len(rows)!=2 or any(type(row) is not dict or type(row.get('signal')) is not int or row.get('retained_before_attempt') is not True for row in rows) or rows[0]!=term):
        raise ValueError('exact original TERM then KILL attempts required')
    denied={'signal':int(signal.SIGKILL),'retained_before_attempt':True,'result':'denied_or_unknown','errno':errno.EPERM}
    if rows[1]==denied and type(rows[1].get('errno')) is int:
        diagnostic='[arm formal runner] SIGKILL process-group '+str(expected_leader)+' failed (errno='+str(errno.EPERM)+'); cleanup unconfirmed'
        if (positive_descendant or native['cleanup_error_type']!='RuntimeError' or
                native['cleanup_error']!='signal denial remains unconfirmed despite later exit' or
                log!='owned fake proof started\n\n'+diagnostic+'\n'):
            raise ValueError('denied cleanup not explicitly retained, or positive descendant cleanup failed')
        return 'FAILED_DENIAL_RETAINED'
    allowed=({'signal':int(signal.SIGKILL),'retained_before_attempt':True,'result':'sent'},)
    if not positive_descendant:
        allowed+=({'signal':int(signal.SIGKILL),'retained_before_attempt':True,'result':'no_group'},)
    if rows[1] not in allowed or native['cleanup_error_type'] is not None or native['cleanup_error'] is not None or log!='owned fake proof started\n':
        raise ValueError('unexpected or unconfirmed native cleanup')
    return 'CLEAN_CANCELLATION_RECORDED'

class RunnerTests(unittest.TestCase):
    def setUp(self):
        (formal.ROOT/'.tla').mkdir(exist_ok=True)
        self.root=Path(tempfile.mkdtemp(prefix='reconnect-runner-',dir=formal.ROOT/'.tla'))
        OWNED.append((self.root,self.root.stat().st_ino))
    def gate(self):
        return formal.ReconnectGate(argparse.Namespace(work_dir=str(self.root/'runs'),mode='prove',targets=['complete','recovered'],tlapm='fake-tlapm'))
    def test_complete_single_module_scope_before_any_prover(self):
        gate=self.gate();path=gate.run/'ReconnectProof.tla';text=path.read_text();header,body=text.split('\n',1)
        path.write_text(header+'\nTHEOREM Prefix == TRUE OBVIOUS\n====\n'+body)
        gate.summary['sources']=formal.model_hashes(gate.run)
        calls=[];gate.command=lambda *a,**kw:calls.append(a) or 'All 1 obligations proved.'
        with self.assertRaises(ValueError):gate.prove()
        self.assertEqual(calls,[])
    def test_source_change_before_prove_refused(self):
        gate=self.gate();path=gate.run/'Reconnect.tla';path.write_text(path.read_text()+'\n\\* changed after initial capture\n')
        calls=[];gate.command=lambda *a,**kw:calls.append(a)
        with self.assertRaises(RuntimeError):gate.prove()
        self.assertEqual(calls,[])
    def test_duplicate_or_mismatched_terminal_counts_refused(self):
        for terminal in ('All 42 obligations proved.\nAll 42 obligations proved.', 'All 41 obligations proved.', 'All 0 obligations proved.'):
            gate=self.gate()
            gate.command=lambda argv,name,**kw:'---- summary of module "ReconnectProof" ----\nobligations_count = 42\n' if name=='tlapm-scope' else terminal
            with self.assertRaises(RuntimeError):gate.prove()
    def test_source_changes_during_summary_or_proof_refused(self):
        for stage in ('tlapm-scope','tlapm'):
            gate=self.gate()
            def command(argv,name,**kw):
                if name==stage:
                    path=gate.run/'Reconnect.tla';path.write_text(path.read_text()+'\n\\* changed during tool\n')
                return '---- summary of module "ReconnectProof" ----\nobligations_count = 42\n' if name=='tlapm-scope' else 'All 42 obligations proved.'
            gate.command=command
            with self.assertRaises(RuntimeError):gate.prove()
    def test_duplicate_targets_rejected_before_tool_invocation(self):
        result=subprocess.run([sys.executable,str(Path(formal.__file__)),'--mode','check','--targets','complete','complete'],text=True,capture_output=True,timeout=10)
        self.assertEqual(result.returncode,2);self.assertIn('duplicate targets',result.stderr)
    def test_raw_fact_closedness_and_bounds(self):
        state={'action':'init'}
        valid={'key':'key','artifacts':{},'action':'init','source':{},'destination':{},'old_coord':{},'old_lifecycle':{},'pair_calls':0,'ensures':0,'confirmation_inputs':{}}
        for change in ({'extra':True},{'key':None},{'pair_calls':True}):
            bad=dict(valid,**change)
            with self.assertRaises(ValueError):formal.validate_facts([bad],[state])
        for encoded in (None, 'A'*(4*(((1<<20)+2)//3)+1)):
            row={'path':'/owned/file','sha256':'a'*64,'raw_base64':encoded}
            bad=dict(valid,artifacts={k:row for k in ('source_config','destination_config','old_coord','old_lifecycle')})
            with self.assertRaises(ValueError):formal.validate_facts([bad],[state])
    def test_go_typed_fixture_digest_escaping(self):
        value={'reason':'<>&\u2028\u2029rémy','count':2,'null':None}
        expected='{"reason":"\\u003c\\u003e\\u0026\\u2028\\u2029rémy","count":2,"null":null}'
        self.assertEqual(formal.go_json_digest(value),hashlib.sha256(expected.encode()).hexdigest())
    def test_child_confirmation_never_borrows_original_consent(self):
        plan={'key':'original','reason':'fresh child plan'}
        digest=formal.go_json_digest(plan)
        captured={'attempt':'child','plan':plan,'sha256':digest,'review_revision':3,'accepted':False}
        inputs={'child':captured}
        formal.validate_confirmation_inputs(inputs)
        child={'key':'child','revision':3,'approved':False,'review':plan,'review_sha256':digest,'review_revision':3}
        self.assertEqual(formal.active_consent(child,inputs),('exact','unapproved',False))
        self.assertEqual(formal.active_consent(dict(child,approved=True),dict(child=dict(captured,accepted=True))),('exact','approved',True))
        missing=dict(child);del missing['approved']
        self.assertEqual(formal.active_consent(missing,inputs)[1],'absent')
        missing=dict(child);del missing['review']
        self.assertEqual(formal.active_consent(missing,inputs)[0],'absent')
        changed=dict(child,review={'key':'original','reason':'substituted'})
        changed['review_sha256']=formal.go_json_digest(changed['review'])
        self.assertEqual(formal.active_consent(changed,inputs)[0],'changed')
        self.assertEqual(formal.active_consent(child,{'original':dict(captured,attempt='original',accepted=True)}),('changed','unapproved',False))
        self.assertEqual(formal.active_consent(child,{'child':dict(captured,attempt='original',accepted=True)}),('changed','unapproved',False))
        self.assertFalse(formal.exact_json({'number':1},{'number':True}))
    def test_confirmation_input_closed_hash_and_decision_contract(self):
        plan={'key':'original'}
        row={'attempt':'child','plan':plan,'sha256':formal.go_json_digest(plan),'review_revision':3,'accepted':False}
        for change in ({'extra':True},{'accepted':1},{'review_revision':True},{'sha256':'0'*64},{'plan':None}):
            with self.assertRaises(ValueError):formal.validate_confirmation_inputs({'child':dict(row,**change)})
    def case_fixture(self,names=('one',),facts_raw=None):
        rows=[];raws={}
        for name in names:
            row={'name':name}
            for field in ('states','facts'):
                path=self.root/(name+'-'+field+'.json')
                raw=(json.dumps([{'case':name,'kind':field}])+'\n').encode()
                if field=='facts' and facts_raw is not None:raw=facts_raw
                path.write_bytes(raw);row[field]=str(path);row[field+'_sha256']=hashlib.sha256(raw).hexdigest();raws[path]=raw
            rows.append(row)
        manifest=self.root/'case-manifest.json'
        manifest.write_text(json.dumps({'schema':'arm.mesh-reconnect-evidence/v1','cases':rows})+'\n')
        return manifest,rows,raws
    def test_case_hash_and_parse_use_one_actual_read_despite_replacement(self):
        manifest,rows,raws=self.case_fixture()
        target=Path(rows[0]['facts']);expected=json.loads(raws[target]);replacement=[{'case':'different-after-hash'}]
        original=Path.open;opens=[];swapped=[]
        class SwapAfterReadClose:
            def __init__(self,stream):self.stream=stream
            def __enter__(self):return self
            def read(self,*args,**kw):return self.stream.read(*args,**kw)
            def __exit__(self,*args):
                result=self.stream.__exit__(*args)
                target.write_text(json.dumps(replacement)+'\n');swapped.append(True);return result
        def opening(path,*args,**kw):
            mode=args[0] if args else kw.get('mode','r')
            stream=original(path,*args,**kw)
            if path in raws and mode=='rb':opens.append(path)
            if path==target and mode=='rb' and not swapped:return SwapAfterReadClose(stream)
            return stream
        with patch.object(Path,'open',opening):
            cases=formal.read_case_manifest(manifest,{'one'})
            name,states,facts=next(cases)
            self.assertEqual(name,'one');self.assertEqual(facts,expected)
            with self.assertRaises(StopIteration):next(cases)
        self.assertTrue(swapped);self.assertEqual(json.loads(target.read_bytes()),replacement)
        self.assertEqual(opens.count(target),1);self.assertEqual(opens.count(Path(rows[0]['states'])),1)
    def test_case_caps_duplicates_utf8_and_eof_remain_strict(self):
        for raw in (b'{"x":1,"x":2}',b'{"x":1,"\\u0078":2}',b'{} {}',b'\xff',b'"'+b'x'*((4<<20)-2)+b'"\n'):
            manifest,_,_=self.case_fixture(facts_raw=raw)
            with self.assertRaises(ValueError):next(formal.read_case_manifest(manifest,{'one'}))
        raw=b'"'+b'x'*((4<<20)-3)+b'"\n'
        self.assertEqual(len(raw),4<<20)
        manifest,_,_=self.case_fixture(facts_raw=raw)
        cases=formal.read_case_manifest(manifest,{'one'})
        self.assertEqual(len(next(cases)[2]),(4<<20)-3);cases.close()
    def test_case_roster_is_closed_before_any_artifact_read(self):
        manifest,rows,raws=self.case_fixture(('one','two'))
        original=Path.open
        def opening(path,*args,**kw):
            if path in raws:raise AssertionError('case read before full roster validation')
            return original(path,*args,**kw)
        for bad in (rows[:1],[rows[0],rows[0]],[dict(rows[0],name='unknown'),rows[1]],[dict(rows[0],extra=True),rows[1]]):
            manifest.write_text(json.dumps({'schema':'arm.mesh-reconnect-evidence/v1','cases':bad})+'\n')
            with patch.object(Path,'open',opening):
                with self.assertRaises(ValueError):next(formal.read_case_manifest(manifest,{'one','two'}))
    def test_cases_are_lazy_and_previous_decoded_graphs_not_retained(self):
        manifest,rows,raws=self.case_fixture(('one','two'));original=Path.open;opens=[]
        actual_loads=json.loads;references=[]
        class TrackedList(list):pass
        def loads(raw,*args,**kw):
            value=actual_loads(raw,*args,**kw)
            if raw in raws.values():
                value=TrackedList(value);references.append(weakref.ref(value))
            return value
        def opening(path,*args,**kw):
            if path in raws:opens.append(path)
            return original(path,*args,**kw)
        with patch.object(formal.json,'loads',loads),patch.object(Path,'open',opening):
            cases=formal.read_case_manifest(manifest,{'one','two'})
            self.assertEqual(opens,[])
            first=next(cases);self.assertEqual(first[0],'one');self.assertEqual(len(opens),2)
            self.assertFalse(any(path.name.startswith('two-') for path in opens))
            del first;gc.collect()
            self.assertEqual(len(references),2);self.assertTrue(all(ref() is None for ref in references))
            Path(rows[1]['facts']).write_text(json.dumps([{'different':'valid JSON before this case was read'}])+'\n')
            with self.assertRaises(ValueError):next(cases)
    def alive(self,pid):
        if sys.platform.startswith('linux'):
            try:state=Path('/proc/'+str(pid)+'/stat').read_text().rsplit(')',1)[1].split()[0]
            except (FileNotFoundError,ProcessLookupError):return False
            return state not in ('Z','X')
        result=subprocess.run(['ps','-o','stat=','-p',str(pid)],capture_output=True,text=True,timeout=5)
        self.assertIn(result.returncode,(0,1));return bool(result.stdout.strip()) and not result.stdout.strip().startswith(('Z','X'))
    def native_cli_term_case(self, resistant_child=False):
        # These fixture scripts stand in for tools; no real solver is executed.
        ready=self.root/'owned.json';tools=self.root/'tools';tools.mkdir()
        z3=tools/'z3';z3.write_text('#!'+sys.executable+'\nprint("fixture-z3")\n');z3.chmod(0o700)
        tool=tools/'tlapm'
        source='#!'+sys.executable+'\nimport json,os,subprocess,sys,time\n'
        source+='if "--version" in sys.argv:print("fixture-tlapm");sys.exit(0)\n'
        source+='if "--config" in sys.argv:print('+repr("PATH='"+str(tools)+"'")+');sys.exit(0)\n'
        source+='if "--summary" in sys.argv:print(\'---- summary of module "ReconnectProof" ----\\nobligations_count = 42\');sys.exit(0)\n'
        if resistant_child:
            child_ready=self.root/'child-ready.json'
            child_source='import json,os,signal,sys,time;from pathlib import Path;signal.signal(signal.SIGTERM,signal.SIG_IGN);p=Path(sys.argv[1]);tmp=p.with_suffix(".tmp");tmp.write_text(json.dumps({"pid":os.getpid(),"group":os.getpgrp(),"term_ignored":signal.getsignal(signal.SIGTERM)==signal.SIG_IGN}));os.replace(tmp,p);time.sleep(60)'
            source+='from pathlib import Path\n'
            source+='child=subprocess.Popen([sys.executable,"-c",'+repr(child_source)+','+repr(str(child_ready))+'],stdout=subprocess.DEVNULL)\n'
            source+='deadline=time.monotonic()+5\n'
            source+='while not Path('+repr(str(child_ready))+').exists():\n if time.monotonic()>=deadline:raise RuntimeError("child handler readiness timeout")\n time.sleep(.01)\n'
            source+='installed=json.loads(Path('+repr(str(child_ready))+').read_text())\n'
            source+='assert installed=={"pid":child.pid,"group":os.getpgrp(),"term_ignored":True}\n'
        else:
            # Preserve the original cooperative default-TERM child exactly.
            source+='child=subprocess.Popen([sys.executable,"-c","import time;time.sleep(60)",'+repr(str(ready))+'],stdout=subprocess.DEVNULL)\n'
        source+='open('+repr(str(ready))+',"w").write(json.dumps({"leader":os.getpid(),"child":child.pid}))\n'
        source+='print("owned fake proof started",flush=True)\ntime.sleep(60)\n'
        tool.write_text(source);tool.chmod(0o700)
        runtime=formal.base.process_runtime()
        runner=runtime.Fixture([sys.executable,str(Path(formal.__file__)),'--mode','prove','--tlapm',str(tool),'--work-dir',str(self.root/'cli-runs')],self.root/'wrapper.log',formal.ROOT)
        owned=None
        try:
            deadline=time.monotonic()+15
            while not ready.exists():
                if runner.terminal() is not None:
                    self.fail('runner exited before owned fake tool readiness: '+runner.wait()[1])
                self.assertLess(time.monotonic(),deadline);time.sleep(.02)
            owned=json.loads(ready.read_text());print('Owned fake tool receipt:',ready,json.dumps(owned),flush=True)
            if resistant_child:
                installed=json.loads((self.root/'child-ready.json').read_text())
                self.assertEqual(installed,{'pid':owned['child'],'group':owned['leader'],'term_ignored':True})
            runner.send(signal.SIGTERM)
            actual_exit,output=runner.wait(timeout=10)
            self.assertEqual(actual_exit,143,output)
            deadline=time.monotonic()+5
            while any(self.alive(pid) for pid in owned.values()) and time.monotonic()<deadline:time.sleep(.02)
            self.assertFalse(any(self.alive(pid) for pid in owned.values()))
            summaries=list((self.root/'cli-runs').glob('run-*/summary.json'));self.assertEqual(len(summaries),1)
            summary=json.loads(summaries[0].read_text());self.assertEqual(summary['status'],'failed')
            self.assertIn('owned fake proof started',(summaries[0].parent/'tlapm.log').read_text())
            native=json.loads((summaries[0].parent/'tlapm-owner.json').read_text())
            inner=native['owner']
            self.assertTrue(inner['reaped']);self.assertTrue(inner['retired']);self.assertFalse(inner['authority'])
            self.assertEqual(inner['native_wait_calls'],1);self.assertIsNone(inner['unknown'])
            self.assertEqual(inner['post_wait_absence'],{'pid':'absent','group':'absent'})
            log=(summaries[0].parent/'tlapm.log').read_text()
            outcome=native_cancellation_contract(native,log,owned['leader'],positive_descendant=resistant_child)
            # Keep both native cases and their exact owner/outcome receipts for
            # independent review. A reporting pass never clears a denied cleanup.
            RETAINED.add(self.root)
            (self.root/'native-case-outcome.json').write_text(json.dumps({
                'case':'term-resistant-descendant' if resistant_child else 'original-cooperative-default-term',
                'reporting_assertions':'passed','native_cleanup':outcome,
                'native_cleanup_success_claimed':outcome=='CLEAN_CANCELLATION_RECORDED',
                'native_owner_receipt_sha256':hashlib.sha256((summaries[0].parent/'tlapm-owner.json').read_bytes()).hexdigest(),
                'denied_case_preserved':outcome=='FAILED_DENIAL_RETAINED'})+'\n')
        finally:
            # Sole retained native owner handles any cancellation/Wait. Unknown
            # closure raises and preserves this private fixture; no PID fallback.
            close_owned_cli_fixture(runner,owned)

    def test_real_cli_term_cleans_owned_fake_tool_and_child(self):
        # Original fixture is unchanged. This now tests its strict cancellation
        # reporting contract; a denied native cleanup is explicitly failed/kept.
        self.native_cli_term_case()

    def test_real_cli_term_exited_leader_live_child_requires_clean_kill(self):
        # Default-TERM parent exits; installed SIG_IGN keeps this exact owned
        # child eligible for the helper's original pre-Wait KILL attempt.
        self.native_cli_term_case(resistant_child=True)

    def test_native_cancellation_denial_is_a_failed_retained_outcome(self):
        native={
            'status':'failed','actual_exit':-15,'error_type':'SystemExit','error':'143',
            'cancellation_received':15,'cancellation_pending':None,
            'source_sha256':'221acd10f39f979c0ece89055361fcd860ab1cdd7e4ddeb816c36abbfc758b76',
            'cleanup_error_type':'RuntimeError','cleanup_error':'signal denial remains unconfirmed despite later exit',
            'owner':{'pid':3210,'reaped':True,'retired':True,'authority':False,
                     'native_wait_calls':1,'unknown':None,'observed_exit':-15,'native_wait_status':15,
                     'post_wait_absence':{'pid':'absent','group':'absent'},'strongly_quarantined_until_native_reap':False,
                     'signals':[{'signal':15,'retained_before_attempt':True,'result':'sent'},
                                {'signal':9,'retained_before_attempt':True,'result':'denied_or_unknown','errno':1}]}}
        log='owned fake proof started\n\n[arm formal runner] SIGKILL process-group 3210 failed (errno=1); cleanup unconfirmed\n'
        self.assertEqual(native_cancellation_contract(native,log,3210),'FAILED_DENIAL_RETAINED')
        for change in ({'cleanup_error':None},{'cleanup_error':'another failure'},{'cancellation_received':2}):
            with self.assertRaises(ValueError):native_cancellation_contract(dict(native,**change),log,3210)
        with self.assertRaises(ValueError):native_cancellation_contract(native,'',3210)
        with self.assertRaises(ValueError):native_cancellation_contract(native,log+log,3210)
        with self.assertRaises(ValueError):native_cancellation_contract(native,log,3211)

    def test_live_descendant_positive_contract_refuses_denial(self):
        native={
            'status':'failed','actual_exit':-15,'error_type':'SystemExit','error':'143',
            'cancellation_received':15,'cancellation_pending':None,
            'source_sha256':'221acd10f39f979c0ece89055361fcd860ab1cdd7e4ddeb816c36abbfc758b76',
            'cleanup_error_type':None,'cleanup_error':None,
            'owner':{'pid':3210,'reaped':True,'retired':True,'authority':False,
                     'native_wait_calls':1,'unknown':None,'observed_exit':-15,'native_wait_status':15,
                     'post_wait_absence':{'pid':'absent','group':'absent'},'strongly_quarantined_until_native_reap':False,
                     'signals':[{'signal':15,'retained_before_attempt':True,'result':'sent'},
                                {'signal':9,'retained_before_attempt':True,'result':'sent'}]}}
        self.assertEqual(native_cancellation_contract(native,'owned fake proof started\n',3210,True),'CLEAN_CANCELLATION_RECORDED')
        for force in ({'signal':9,'retained_before_attempt':True,'result':'no_group'},
                      {'signal':9,'retained_before_attempt':True,'result':'denied_or_unknown','errno':1}):
            bad=dict(native,owner=dict(native['owner'],signals=[native['owner']['signals'][0],force]))
            with self.assertRaises(ValueError):native_cancellation_contract(bad,'owned fake proof started\n',3210,True)
        for change in ({'unknown':'wait unknown'},{'native_wait_calls':2},{'native_wait_calls':True},{'post_wait_absence':{'pid':'absent','group':'unknown_errno_1'}}):
            with self.assertRaises(ValueError):native_cancellation_contract(dict(native,owner=dict(native['owner'],**change)),'owned fake proof started\n',3210,True)

    def test_post_wait_saved_pid_diagnostics_cannot_authorize_kill(self):
        # Pure regression: exercise this caller's actual cleanup entry through
        # the unchanged shared Fixture.close with an explicitly reaped owner.
        # The fake metadata deliberately claims still-live saved PIDs. No native
        # child or native closure verdict is manufactured by this control.
        runtime=formal.base.process_runtime();closed=[];cancelled=[]
        runner=runtime.Fixture.__new__(runtime.Fixture)
        runner.owner=types.SimpleNamespace(authority=False,start_may_begin=True,
            retired=True,reaped=True,absence={'pid':'absent','group':'absent'},unknown=None,
            process=None,cancel=lambda:cancelled.append('cancel'),
            receipt=lambda:{'retired':True,'reaped':True,'authority':False})
        runner.capture=types.SimpleNamespace(close=lambda:closed.append('capture'))
        runner.state=types.SimpleNamespace(active=runner);runner.log=self.root/'post-wait.log'
        observed={'leader':12345,'child':23456}
        with patch.object(runtime.os,'kill',side_effect=AssertionError('historical PID signal')) as kill, \
             patch.object(runtime.os,'killpg',side_effect=AssertionError('historical group signal')) as killpg, \
             patch.object(runtime,'_receipt') as receipt, \
             patch.object(self,'alive',return_value=True) as alive:
            close_owned_cli_fixture(runner,observed)
            with self.assertRaises(RuntimeError):close_owned_cli_fixture(None,observed)
            kill.assert_not_called();killpg.assert_not_called();alive.assert_not_called()
            self.assertEqual(receipt.call_count,1)
        self.assertEqual(closed,['capture']);self.assertEqual(cancelled,[])
        self.assertIsNone(runner.state.active)

if __name__=='__main__':
    program=unittest.main(exit=False)
    if program.result.wasSuccessful():
        for path,inode in OWNED:
            info=path.lstat()
            if info.st_ino!=inode or info.st_uid!=os.getuid() or path.is_symlink():raise RuntimeError('owned guard fixture changed; preserved')
            if path in RETAINED:
                print('Native cancellation case and exact outcome retained:',path,flush=True)
            else:
                shutil.rmtree(path)
    else:
        for path,_ in OWNED:print('Failed guard fixture retained:',path,flush=True)
    sys.exit(0 if program.result.wasSuccessful() else 1)
