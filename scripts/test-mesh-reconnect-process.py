#!/usr/bin/env python3
"""Real bounded developer subprocess controls; no ARM backend or external SSH."""
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import types
import unittest

ROOT=Path(__file__).resolve().parent

def load(name,path):
    spec=importlib.util.spec_from_file_location(name,path)
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module

process=load('bounded_fixture_process',Path(os.environ.get('TEST_RECONNECT_PROCESS',str(ROOT/'mesh_reconnect_fixture_process.py'))))
harness=load('reconnect_acceptance',Path(os.environ.get('TEST_RECONNECT_HARNESS',str(ROOT/'test-mesh-reconnect.py'))))


class OwnedProcessControls(unittest.TestCase):
    def test_actual_argv_stdin_outputs_and_exit(self):
        code='import sys; data=sys.stdin.buffer.read(); print(repr(sys.argv[1:])); sys.stdout.buffer.write(data); sys.stderr.write("stderr"); sys.exit(7)'
        result,receipt=process.bounded_command([sys.executable,'-c',code,'space arg',';literal'],stdin='payload',timeout=5)
        self.assertEqual(result.returncode,7)
        self.assertIn('payload',result.stdout)
        self.assertIn('space arg',result.stdout)
        self.assertEqual(result.stderr,'stderr')
        self.assertTrue(receipt['wait_joined'] and receipt['output_joined'] and receipt['group_absence_observed'])
        self.assertEqual(receipt['signals'],[])

    def test_actual_timeout_joins_cooperative_parent_and_child(self):
        with tempfile.TemporaryDirectory() as name:
            root=Path(name);ready=root/'ready';joined=root/'joined'
            code='''import subprocess,sys,signal,time,pathlib
child=subprocess.Popen([sys.executable,'-c','import time; time.sleep(30)'])
def stop(*args):
 child.wait(timeout=2)
 pathlib.Path(sys.argv[2]).write_text(str(child.returncode))
 sys.exit(0)
signal.signal(signal.SIGTERM,stop)
pathlib.Path(sys.argv[1]).write_text(str(child.pid))
while True: time.sleep(.01)
'''
            with self.assertRaises(process.CommandFailure) as captured:
                process.bounded_command([sys.executable,'-c',code,ready,joined],timeout=.4)
            error=captured.exception
            self.assertIn('timed out',str(error))
            self.assertTrue(error.receipt['wait_joined'])
            self.assertTrue(error.receipt['output_joined'])
            self.assertTrue(error.receipt['group_absence_observed'])
            self.assertTrue(joined.is_file(),'parent must positively wait for actual child')
            self.assertLess(int(joined.read_text()),0)
            child=int(ready.read_text())
            with self.assertRaises(ProcessLookupError):os.kill(child,0)

    def test_actual_output_overflow_retains_bounded_prefix_and_failure(self):
        code='import os; os.write(2,b"marker");\nwhile True: os.write(1,b"x"*65536)'
        with self.assertRaises(process.CommandFailure) as captured:
            process.bounded_command([sys.executable,'-c',code],timeout=5,output_limit=1024)
        error=captured.exception
        self.assertIn('output exceeded',str(error))
        self.assertEqual(len(error.stdout.encode()),1024)
        self.assertGreater(error.receipt['bytes_observed']['stdout'],1024)
        self.assertEqual(error.receipt['bytes_retained']['stdout'],1024)
        self.assertTrue(error.receipt['wait_joined'])

    def test_input_refusal_happens_before_launch(self):
        with tempfile.TemporaryDirectory() as name:
            marker=Path(name)/'launched'
            with self.assertRaises(ValueError):
                process.bounded_command([sys.executable,'-c','import pathlib,sys;pathlib.Path(sys.argv[1]).touch()',marker],stdin=b'x'*((1<<20)+1),timeout=1)
            self.assertFalse(marker.exists())

    def test_actual_fixture_shutdown_join_precedes_both_backend_stops(self):
        with tempfile.TemporaryDirectory() as name:
            root=Path(name);ready=root/'ready';joined=root/'joined'
            code='''import signal,time,pathlib,sys
def stop(*args):
 time.sleep(.1)
 pathlib.Path(sys.argv[2]).write_text('joined')
 sys.exit(0)
signal.signal(signal.SIGTERM,stop)
pathlib.Path(sys.argv[1]).write_text('ready')
while True: time.sleep(.01)
'''
            server=subprocess.Popen([sys.executable,'-c',code,ready,joined],start_new_session=True)
            try:
                deadline=time.monotonic()+3
                while not ready.exists():
                    self.assertIsNone(server.poll());self.assertLess(time.monotonic(),deadline);time.sleep(.01)
                fixture=harness.Fixture.__new__(harness.Fixture)
                fixture.server=server;fixture.server_log=None;fixture.homes=[root/'source',root/'destination'];fixture.image='unused'
                for index,home in enumerate(fixture.homes):
                    home.mkdir(mode=0o700)
                    endpoint=home/'endpoint.json'
                    endpoint.write_text(json.dumps({'url':'http://127.0.0.1:12345','pid':2147483000+index,'token':'private-test-generation'}))
                    endpoint.chmod(0o600)
                calls=[]
                def observed_stop(argv,**kwargs):
                    self.assertTrue(joined.exists(),'backend stop ran before server helper/session join')
                    self.assertIsNotNone(server.poll())
                    calls.append(argv)
                    (Path(argv[2])/'endpoint.json').unlink()
                    return subprocess.CompletedProcess(argv,0,json.dumps({'stopped':True,'phase':'stopped','id':'a'*32}),'')
                fixture.command=observed_stop
                self.assertEqual(fixture.cleanup(),[])
                self.assertEqual(len(calls),2)
                self.assertIsNotNone(server.returncode)
            finally:
                if server.poll() is None:server.kill()
                server.wait(timeout=2)

    def test_unknown_server_join_defers_backend_cleanup(self):
        fixture=harness.Fixture.__new__(harness.Fixture)
        class Unknown:
            def poll(self):return None
            def terminate(self):pass
            def wait(self,timeout):raise subprocess.TimeoutExpired('owned fixture',timeout)
        fixture.server=Unknown();fixture.server_log=None;fixture.homes=['source','destination'];fixture.image='unused'
        def forbidden(*args,**kwargs):self.fail('backend stop while fixture closure unknown')
        fixture.command=forbidden
        errors=fixture.cleanup()
        self.assertEqual(len(errors),2)
        self.assertIn('unconfirmed',errors[0]);self.assertIn('deferred',errors[1])

    def test_cleanup_exception_still_persists_failed_report(self):
        with tempfile.TemporaryDirectory() as name:
            args=types.SimpleNamespace(arm=Path(sys.executable),fixture_sshd=Path(sys.executable),report=Path(name)/'report.json',keep=False,interactive='none')
            original=harness.Fixture;owned=[]
            class Broken(original):
                def setup(self):owned.append(self.root);raise RuntimeError('setup diagnostic')
                def cleanup(self):raise RuntimeError('cleanup diagnostic')
            harness.Fixture=Broken
            try:
                self.assertEqual(harness.execute(args),1)
                report=json.loads(args.report.read_text())
                self.assertFalse(report['passed']);self.assertEqual(report['status'],'failed')
                self.assertIn('setup diagnostic',report['failure'])
                self.assertIn('cleanup diagnostic',report['cleanup_errors'][0])
                self.assertTrue(owned[0].exists())
                harness.remove_exact(owned[0],report['final_roster'])
            finally:harness.Fixture=original


    def test_operation_deadline_retains_failure_and_clears_before_cleanup(self):
        with tempfile.TemporaryDirectory() as name:
            args=types.SimpleNamespace(arm=Path(sys.executable),fixture_sshd=Path(sys.executable),report=Path(name)/'report.json',keep=False,interactive='none',operation_timeout=1)
            original=harness.Fixture;owned=[]
            class Expiring(original):
                def setup(self):owned.append(self.root);time.sleep(5)
                def cleanup(self):
                    self_timer=signal.getitimer(signal.ITIMER_REAL)[0]
                    if self_timer:raise AssertionError('operation timer leaked into cleanup')
                    time.sleep(.05)
                    return []
            harness.Fixture=Expiring
            try:
                self.assertEqual(harness.execute(args),1)
                report=json.loads(args.report.read_text())
                self.assertIn('operation deadline',report['failure'])
                self.assertEqual(report['cleanup_errors'],[])
                self.assertEqual(report['status'],'failed')
                harness.remove_exact(owned[0],report['final_roster'])
            finally:harness.Fixture=original

    def test_live_saved_pid_is_unknown_and_never_signalled(self):
        with tempfile.TemporaryDirectory() as name:
            child=subprocess.Popen([sys.executable,'-c','import time;time.sleep(30)'])
            try:
                fixture=harness.Fixture.__new__(harness.Fixture)
                fixture.server=None;fixture.server_log=None;fixture.homes=[Path(name)/'source',Path(name)/'destination'];fixture.image='unused';fixture.backend_pids={child.pid}
                fixture.command=lambda argv,**kwargs:subprocess.CompletedProcess(argv,0,'','')
                errors=fixture.cleanup()
                self.assertTrue(any('PID absence unconfirmed' in error for error in errors))
                self.assertIsNone(child.poll(),'cleanup must not signal a saved PID')
            finally:
                child.terminate();child.wait(timeout=2)


    def actual_exited_leader_control(self, interrupt):
        # Same real fork/early-exit/live-pipe scenario as the independent root
        # probe. Instrumentation observes native reap and real group signals;
        # it does not replace process lifetime or signalling results.
        with tempfile.TemporaryDirectory() as name:
            root=Path(name);image=root/'child.py'
            image.write_text("import os,time\npid=os.fork()\nif pid:\n os._exit(0)\ntime.sleep(10)\nos._exit(0)\n")
            observations=[];reaped={};native_popen=subprocess.Popen;native_signal=os.killpg
            class Tracked(native_popen):
                def record(self):
                    if self.returncode is not None and self.pid not in reaped:
                        try:os.waitpid(self.pid,os.WNOHANG)
                        except ChildProcessError:
                            reaped[self.pid]=True
                            observations.append({'leader_reaped':self.pid,'actual_second_wait':'ECHILD'})
                def poll(self):
                    value=super().poll();self.record();return value
                def wait(self,*args,**kwargs):
                    try:return super().wait(*args,**kwargs)
                    finally:self.record()
            def observe_signal(pgid,sig):
                if sig:
                    with native_popen(['/bin/ps','-p',str(pgid),'-o','stat='],stdout=subprocess.PIPE,stderr=subprocess.PIPE) as ps:
                        status,_=ps.communicate(timeout=2)
                    observations.append({'signal':int(sig),'pgid':pgid,'after_actual_reap':reaped.get(pgid,False),'native_leader_status':status.decode().strip()})
                return native_signal(pgid,sig)
            original_subprocess=process.subprocess
            process.subprocess=types.SimpleNamespace(Popen=Tracked,PIPE=subprocess.PIPE,TimeoutExpired=subprocess.TimeoutExpired,CompletedProcess=subprocess.CompletedProcess)
            process.os.killpg=observe_signal
            old_alarm=signal.getsignal(signal.SIGALRM)
            if interrupt:
                def interrupted(*_):raise KeyboardInterrupt('fixture-only interrupted loop')
                signal.signal(signal.SIGALRM,interrupted);signal.setitimer(signal.ITIMER_REAL,.3)
            try:
                with self.assertRaises(process.CommandFailure) as captured:
                    process.bounded_command([sys.executable,str(image)],timeout=5 if interrupt else .3)
                receipt=captured.exception.receipt
            finally:
                signal.setitimer(signal.ITIMER_REAL,0);signal.signal(signal.SIGALRM,old_alarm)
                process.os.killpg=native_signal;process.subprocess=original_subprocess
            self.assertTrue(receipt['wait_joined'])
            actual_signals=[row for row in observations if row.get('signal')]
            self.assertTrue(actual_signals,observations)
            self.assertFalse(any(row['after_actual_reap'] for row in actual_signals),observations)
            self.assertTrue(all(row['native_leader_status'].startswith('Z') for row in actual_signals),observations)
            self.assertTrue(any(row.get('actual_second_wait')=='ECHILD' for row in observations),observations)
            self.assertEqual(receipt.get('signal_refusals_after_wait',[]),[])
            # Read-only positive cleanup observation; never send another signal
            # after actual Wait consumed the anchor.
            deadline=time.monotonic()+2
            while True:
                try:os.killpg(receipt['pid'],0)
                except ProcessLookupError:break
                self.assertLess(time.monotonic(),deadline,'owned group disappearance unconfirmed')
                time.sleep(.02)

    def test_actual_exited_leader_pipe_timeout_never_signals_after_reap(self):
        self.actual_exited_leader_control(False)

    def test_actual_exited_leader_pipe_interruption_never_signals_after_reap(self):
        self.actual_exited_leader_control(True)

    def test_reaped_leader_remaining_group_is_unknown_without_signals(self):
        with tempfile.TemporaryDirectory() as name:
            image=Path(name)/'closed-pipes.py'
            image.write_text("import os,time\npid=os.fork()\nif pid: os._exit(0)\nos.close(1)\nos.close(2)\ntime.sleep(2)\nos._exit(0)\n")
            with self.assertRaises(process.CommandFailure) as captured:
                process.bounded_command([sys.executable,str(image)],timeout=5)
            receipt=captured.exception.receipt
            self.assertTrue(receipt['wait_joined'] and receipt['output_joined'])
            self.assertFalse(receipt['group_absence_observed'])
            self.assertEqual(receipt['signals'],[])
            self.assertIn('no signalling after reap',str(captured.exception))
            deadline=time.monotonic()+3
            while True:
                try:os.killpg(receipt['pid'],0)
                except ProcessLookupError:break
                self.assertLess(time.monotonic(),deadline,'bounded self-exit group still present')
                time.sleep(.02)

    def actual_nongraceful_server(self, signalled):
        with tempfile.TemporaryDirectory() as name:
            code='import os,signal;os.kill(os.getpid(),signal.SIGTERM)' if signalled else 'import sys;sys.exit(7)'
            server=subprocess.Popen([sys.executable,'-c',code],start_new_session=True)
            try:
                self.assertEqual(server.wait(timeout=2),-signal.SIGTERM if signalled else 7)
                fixture=harness.Fixture.__new__(harness.Fixture)
                fixture.server=server;fixture.server_log=None;fixture.homes=[Path(name)/'source',Path(name)/'destination'];fixture.image='unused';fixture.backend_pids=set()
                calls=[]
                fixture.command=lambda argv,**kwargs:(calls.append(argv) or subprocess.CompletedProcess(argv,0,'',''))
                errors=fixture.cleanup()
                self.assertFalse(calls,'backend teardown despite non-graceful SSH fixture exit')
                self.assertTrue(any('graceful shutdown unconfirmed' in error for error in errors),errors)
                self.assertTrue(any('deferred' in error for error in errors),errors)
            finally:
                if server.poll() is None:server.kill()
                server.wait(timeout=2)

    def test_actual_nonzero_fixture_exit_defers_backends(self):
        self.actual_nongraceful_server(False)

    def test_actual_signalled_fixture_exit_defers_backends(self):
        self.actual_nongraceful_server(True)


    def test_interruption_after_successful_wait_cannot_revive_group_authority(self):
        native_popen=subprocess.Popen;native_signal=os.killpg;events=[]
        class InterruptedWait(native_popen):
            def wait(self,*args,**kwargs):
                result=super().wait(*args,**kwargs)
                try:os.waitpid(self.pid,os.WNOHANG)
                except ChildProcessError:events.append('actual-reaped-ECHILD')
                raise KeyboardInterrupt('after actual native reap')
        def observed(pgid,sig):
            if sig:events.append(('nonzero-signal',int(sig)))
            return native_signal(pgid,sig)
        original_subprocess=process.subprocess
        process.subprocess=types.SimpleNamespace(Popen=InterruptedWait,PIPE=subprocess.PIPE,TimeoutExpired=subprocess.TimeoutExpired,CompletedProcess=subprocess.CompletedProcess)
        process.os.killpg=observed
        try:
            with self.assertRaises(process.CommandFailure) as captured:
                process.bounded_command([sys.executable,'-c','print("actual exit")'],timeout=5)
            self.assertTrue(captured.exception.receipt['wait_joined'])
            self.assertIn('actual-reaped-ECHILD',events)
            self.assertFalse(any(isinstance(event,tuple) for event in events),events)
            self.assertEqual(captured.exception.receipt['signals'],[])
        finally:
            process.os.killpg=native_signal;process.subprocess=original_subprocess


    def test_native_reap_before_returncode_assignment_never_revives_signal_authority(self):
        with tempfile.TemporaryDirectory() as name:
            image=Path(name)/'native-wait-gap.py'
            image.write_text("import os,time\npid=os.fork()\nif pid: os._exit(0)\nos.close(1)\nos.close(2)\ntime.sleep(2)\nos._exit(0)\n")
            native_popen=subprocess.Popen;native_signal=os.killpg;events=[];reaped={}
            class ReapedBeforeAssignment(native_popen):
                def wait(self,*args,**kwargs):
                    if self.pid not in reaped:
                        waited,status=os.waitpid(self.pid,0)
                        if waited!=self.pid:raise AssertionError('actual native leader reap required')
                        if self.returncode is not None:raise AssertionError('control requires pre-assignment gap')
                        try:os.waitpid(self.pid,os.WNOHANG)
                        except ChildProcessError:
                            reaped[self.pid]=True
                            events.append({'actual_reaped':self.pid,'second_wait':'ECHILD','cached_returncode':self.returncode})
                        raise KeyboardInterrupt('actual native reap before returncode assignment')
                    raise ChildProcessError('native leader already reaped in controlled gap')
            def observed(pgid,sig):
                if sig:events.append({'signal':int(sig),'after_actual_reap':reaped.get(pgid,False)})
                return native_signal(pgid,sig)
            original_subprocess=process.subprocess
            process.subprocess=types.SimpleNamespace(Popen=ReapedBeforeAssignment,PIPE=subprocess.PIPE,TimeoutExpired=subprocess.TimeoutExpired,CompletedProcess=subprocess.CompletedProcess)
            process.os.killpg=observed
            try:
                with self.assertRaises(process.CommandFailure) as captured:
                    process.bounded_command([sys.executable,str(image)],timeout=5)
                receipt=captured.exception.receipt
            finally:
                process.os.killpg=native_signal;process.subprocess=original_subprocess
            # Observe eventual bounded self-exit before assertions/TempDir removal;
            # the runner must not signal this group after losing wait ownership.
            deadline=time.monotonic()+3
            while True:
                try:os.killpg(receipt['pid'],0)
                except ProcessLookupError:break
                self.assertLess(time.monotonic(),deadline,'self-exiting private group still present')
                time.sleep(.02)
            self.assertTrue(any(row.get('second_wait')=='ECHILD' and row['cached_returncode'] is None for row in events),events)
            self.assertFalse(any(row.get('signal') for row in events),events)
            self.assertFalse(receipt['wait_joined'],'unknown native wait must not be fabricated as joined')
            self.assertTrue(receipt['wait_ownership_ambiguous'])
            self.assertFalse(receipt['signal_authority_retained'])


    def test_pipe_setup_failure_after_start_joins_owned_child(self):
        native_selector=process.selectors.DefaultSelector
        class BrokenSelector:
            def __init__(self):self.inner=native_selector()
            def register(self,*args):raise OSError('fixture-only registration failure')
            def get_map(self):return self.inner.get_map()
            def close(self):return self.inner.close()
            def select(self,*args):return self.inner.select(*args)
        process.selectors.DefaultSelector=BrokenSelector
        try:
            with self.assertRaises(process.CommandFailure) as captured:
                process.bounded_command([sys.executable,'-c','import time;time.sleep(30)'],timeout=5)
            receipt=captured.exception.receipt
            self.assertTrue(receipt['wait_joined'])
            self.assertTrue(receipt['group_absence_observed'])
            self.assertFalse(receipt['output_joined'],'forced close is not observed EOF')
            self.assertTrue(receipt['signals'])
        finally:process.selectors.DefaultSelector=native_selector


if __name__=='__main__':unittest.main()
