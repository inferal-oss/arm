#!/usr/bin/env python3
"""Fixture evidence controls; one owned non-ARM child tests delayed exit.

No ARM backend, SSH, listener, credentials or public operation is launched.
"""
from pathlib import Path
import copy
import importlib.util
import json
import os
import itertools
import select
import sys
import subprocess
import tempfile
import unittest
from unittest import mock

ROOT=Path(__file__).resolve().parent

def load(name,path):
    spec=importlib.util.spec_from_file_location(name,path);module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module

module=load('private_fixture_evidence',ROOT/'mesh_reconnect_fixture_evidence.py')
harness=load('private_fixture_harness',ROOT/'test-mesh-reconnect.py')


def event(sequence,action,**fields):
    return dict(sequence=sequence,action=action,input_sha256='a'*64,child_started=True,
        child_wait_failed=False,child_exit=0,stdout_sha256='b'*64,stderr_sha256='c'*64,**fields)


def mixed():
    return [event(1,'read_only_probe'),event(2,'lifecycle_inspect'),
        event(3,'pair',pair='d'*64),event(4,'lifecycle_revoke',key='other'),
        event(5,'lifecycle_revoke',key='expected',fault='after')]


def encoded(rows):return b''.join(json.dumps(r,separators=(',',':')).encode()+b'\n' for r in rows)

def endpoint(pid=2147483000,token='private-generation'):
    return json.dumps({'url':'http://127.0.0.1:12345','pid':pid,'token':token}).encode()


def stopped(**fields):return json.dumps(dict(stopped=True,phase='stopped',id='e'*32,**fields))


class EventEvidence(unittest.TestCase):
    def test_actual_mixed_omitempty_shapes_select_one_committed_revoke(self):
        rows=module.decode_fixture_events(encoded(mixed()))
        selected=module.committed_revoke_fault(rows,'expected')
        self.assertEqual(selected['sequence'],5)
        self.assertEqual(selected['action'],'lifecycle_revoke')
        self.assertNotIn('key',rows[0]);self.assertNotIn('key',rows[2])
        with tempfile.TemporaryDirectory() as name:
            path=Path(name)/'events';path.write_bytes(encoded(mixed()))
            self.assertEqual(module.read_fixture_events(path),rows)

    def test_wrong_missing_or_duplicate_fault_identity_never_proves_commit(self):
        for change in ('missing-key','wrong-key','missing-fault','before-fault','wrong-fault',
            'missing-action','wrong-action','not-started','wait-failed','bad-exit','duplicate-key'):
            with self.subTest(change=change):
                rows=mixed();row=rows[-1]
                if change=='missing-key':row.pop('key')
                elif change=='wrong-key':row['key']='another'
                elif change=='missing-fault':row.pop('fault')
                elif change=='before-fault':row['fault']='before'
                elif change=='wrong-fault':row['fault']='unknown'
                elif change=='missing-action':row.pop('action')
                elif change=='wrong-action':row['action']='pair'
                elif change=='not-started':row['child_started']=False
                elif change=='wait-failed':row['child_wait_failed']=True
                elif change=='bad-exit':row['child_exit']=-1
                else:rows.append(event(6,'pair',key='expected',pair='d'*64))
                with self.assertRaises((ValueError,AssertionError)):
                    module.committed_revoke_fault(module.decode_fixture_events(encoded(rows)),'expected')

    def test_closedness_types_digests_order_eof_and_byte_bounds(self):
        mutations=[('unknown','x'),('stage','after'),('child_started',1),('child_wait_failed',None),
            ('child_exit',False),('sequence',True),('sequence',0),('sequence',65),
            ('input_sha256','g'*64),('pair',None),('key',''),('action','')]
        for field,value in mutations:
            with self.subTest(field=field,value=value):
                rows=mixed();rows[-1][field]=value
                with self.assertRaises((ValueError,AssertionError)):
                    module.committed_revoke_fault(rows,'expected')
                with self.assertRaises(ValueError):module.decode_fixture_events(encoded(rows))
        raw=encoded(mixed())
        for bad in (raw[:-1],raw+b'\n',raw+b'{}\n',b'null\n',b'\xff\n',
            raw.replace(b'"key":"expected"',b'"key":"expected","k\\u0065y":"expected"'),
            b'x'*(module.EVENT_FILE_LIMIT+1),b' '*16385+b'\n',encoded([event(2,'x'),event(1,'x')]),encoded([event(i+1,'x') for i in range(65)])):
            with self.subTest(raw_prefix=bad[:40]):
                with self.assertRaises((ValueError,UnicodeError)):module.decode_fixture_events(bad)
        self.assertEqual(module.decode_fixture_events(b''),[])


class ShutdownEvidence(unittest.TestCase):
    def tracker(self):
        tracker=module.BackendShutdownEvidence();before=tracker.observe('/private/source',endpoint());return tracker,before

    def test_positive_result_is_bound_and_reused_only_after_independent_absence(self):
        tracker,before=self.tracker();record=tracker.record_stop('/private/source',before,0,stopped(),None)
        seen=[];reused=tracker.reuse('/private/source',None,lambda pid:seen.append(pid) or True)
        self.assertEqual(seen,[before['pid']]);self.assertTrue(reused['independent_pid_absence_observed'])
        self.assertEqual(reused['endpoint_sha256'],before['endpoint_sha256'])
        record['result']['stopped']=False;reused['result']['phase']='fake'
        self.assertIs(tracker.reuse('/private/source',None,lambda _:True)['result']['stopped'],True)

    def test_absence_missing_receipt_or_failed_result_is_unknown(self):
        tracker,before=self.tracker()
        with self.assertRaises(ValueError):tracker.reuse('/private/source',None,lambda _:True)
        bads=[(1,stopped()),(0,'{}'),(0,'{"stopped":1,"phase":"stopped","id":"'+'e'*32+'"}'),
            (0,'{"stopped":true,"phase":"ready","id":"'+'e'*32+'"}'),
            (0,'{"stopped":true,"phase":"stopped","id":"bad"}'),
            (0,stopped()+stopped()),(0,'{"stopped":true,"stopped":false,"phase":"stopped","id":"'+'e'*32+'"}')]
        for code,raw in bads:
            with self.subTest(code=code,result=raw):
                with self.assertRaises(ValueError):tracker.record_stop('/private/source',before,code,raw,None)
                with self.assertRaises(ValueError):tracker.reuse('/private/source',None,lambda _:True)

    def test_pending_reuse_is_not_native_absence_evidence(self):
        tracker,before=self.tracker();tracker.record_stop('/private/source',before,0,stopped(),None)
        candidate=tracker.pending_reuse('/private/source',None)
        self.assertNotIn('independent_pid_absence_observed',candidate)
        with self.assertRaises(ValueError):tracker.reuse('/private/source',None,lambda _:False)

    def test_failed_new_stop_cannot_leave_an_older_receipt_eligible(self):
        tracker,before=self.tracker();tracker.record_stop('/private/source',before,0,stopped(),None)
        with self.assertRaises(ValueError):tracker.record_stop('/private/source',before,1,stopped(),None)
        with self.assertRaises(ValueError):tracker.reuse('/private/source',None,lambda _:True)

    def test_mismatched_replaced_late_or_reappeared_endpoint_retires_old_receipt(self):
        for change in ('new-token','new-pid','same-reappeared'):
            with self.subTest(change=change):
                tracker,before=self.tracker();tracker.record_stop('/private/source',before,0,stopped(),None)
                raw=endpoint(token='replacement') if change=='new-token' else endpoint(pid=2147483001) if change=='new-pid' else endpoint()
                self.assertIsNone(tracker.reuse('/private/source',raw,lambda _:True))
                with self.assertRaises(ValueError):tracker.reuse('/private/source',None,lambda _:True)
        tracker,before=self.tracker()
        for field,value in [('home','/private/destination'),('pid',2147483001),('endpoint_sha256','f'*64)]:
            with self.subTest(field=field):
                wrong=dict(before,**{field:value})
                with self.assertRaises(ValueError):tracker.record_stop('/private/source',wrong,0,stopped(),None)
        with self.assertRaises(ValueError):tracker.record_stop('/private/source',before,0,stopped(),endpoint(token='late-start'))
        with self.assertRaises(ValueError):tracker.reuse('/private/source',None,lambda _:True)

    def test_live_reused_pid_and_permission_unknown_never_allow_duplicate_skip(self):
        tracker,before=self.tracker();tracker.record_stop('/private/source',before,0,stopped(),None)
        for value in (False,None,1):
            with self.subTest(pid_observation=value):
                with self.assertRaises(ValueError):tracker.reuse('/private/source',None,lambda _:value)
        def denied(_):raise PermissionError('native observation denied')
        with self.assertRaises(PermissionError):tracker.reuse('/private/source',None,denied)

    def test_endpoint_closed_schema_privacy_and_bounds(self):
        for raw in (b'{}',b'null',endpoint().replace(b'"pid": 2147483000',b'"pid": true'),
            endpoint().replace(b'"token":',b'"extra":0,"token":'),b'x'*16385):
            with self.subTest(raw=raw[:30]):
                with self.assertRaises(ValueError):module.BackendShutdownEvidence().observe('/private/source',raw)
        with tempfile.TemporaryDirectory() as name:
            home=Path(name);self.assertIsNone(module.read_fixture_endpoint(home))
            path=home/'endpoint.json';path.write_bytes(endpoint());path.chmod(0o600)
            self.assertEqual(module.read_fixture_endpoint(home),endpoint())
            path.chmod(0o644)
            with self.assertRaises(ValueError):module.read_fixture_endpoint(home)
            path.unlink();target=home/'other';target.write_bytes(endpoint());target.chmod(0o600);path.symlink_to(target)
            with self.assertRaises(ValueError):module.read_fixture_endpoint(home)


class CleanupPositiveRecords(unittest.TestCase):
    def fixture(self,root,events):
        fixture=harness.Fixture.__new__(harness.Fixture)
        class Server:
            def poll(self):return None
            def terminate(self):events.append('fixture-terminate')
            def wait(self,timeout):events.append('fixture-positive-wait');return 0
        fixture.server=Server();fixture.server_log=None;fixture.homes=[root/'source',root/'destination'];fixture.image='trusted-private-arm';fixture.calls=[];fixture.evidence={};fixture.backend_pids=set();fixture.shutdown_evidence=module.BackendShutdownEvidence()
        for index,home in enumerate(fixture.homes):
            home.mkdir(mode=0o700);path=home/'endpoint.json';path.write_bytes(endpoint(pid=2147483000+index));path.chmod(0o600)
        def stop(argv,**kwargs):
            events.append('stop-'+str(argv[2]));self.assertIn('fixture-positive-wait',events)
            (Path(argv[2])/'endpoint.json').unlink()
            return subprocess.CompletedProcess(argv,0,stopped(),'')
        fixture.command=stop
        return fixture

    @staticmethod
    def absent(pid,sig):
        if sig!=0:raise AssertionError('saved PID must never be signalled')
        raise ProcessLookupError()

    def test_cleanup_orders_current_stop_after_fixture_join_and_records_positive_result(self):
        with tempfile.TemporaryDirectory() as name:
            events=[];fixture=self.fixture(Path(name),events)
            with mock.patch.object(harness.os,'kill',side_effect=self.absent):self.assertEqual(fixture.cleanup(),[])
            self.assertEqual(len([x for x in events if x.startswith('stop-')]),2)
            self.assertEqual(len(fixture.evidence['positive-backend-shutdowns']),2)

    def test_final_already_stopped_generation_is_reused_without_another_stop(self):
        with tempfile.TemporaryDirectory() as name:
            events=[];fixture=self.fixture(Path(name),events);fixture.observe_backend_endpoints()
            for home in fixture.homes:
                before=fixture.shutdown_evidence.selected(home);(home/'endpoint.json').unlink();fixture.shutdown_evidence.record_stop(home,before,0,stopped(),None)
            fixture.command=lambda *a,**k:self.fail('already positively stopped backend was stopped twice')
            with mock.patch.object(harness.os,'kill',side_effect=self.absent):self.assertEqual(fixture.cleanup(),[])
            self.assertEqual(len(fixture.evidence['verified-shutdown-reuse']),2)
            self.assertEqual(events,['fixture-terminate','fixture-positive-wait'])

    def test_unrecorded_absence_is_retained_and_replacement_requires_current_stop(self):
        with tempfile.TemporaryDirectory() as name:
            events=[];fixture=self.fixture(Path(name),events);fixture.observe_backend_endpoints()
            first,second=fixture.homes
            before=fixture.shutdown_evidence.selected(first);(first/'endpoint.json').unlink();fixture.shutdown_evidence.record_stop(first,before,0,stopped(),None)
            (first/'endpoint.json').write_bytes(endpoint(pid=2147483002,token='replacement'));(first/'endpoint.json').chmod(0o600)
            (second/'endpoint.json').unlink()
            with mock.patch.object(harness.os,'kill',side_effect=self.absent):errors=fixture.cleanup()
            self.assertEqual(len([x for x in events if x.startswith('stop-')]),1)
            self.assertTrue(any('without positive exact-generation' in e for e in errors))
            self.assertEqual(len(fixture.evidence['positive-backend-shutdowns']),1)


    def prepare_already_stopped(self,fixture):
        fixture.observe_backend_endpoints()
        for home in fixture.homes:
            before=fixture.shutdown_evidence.selected(home)
            (home/'endpoint.json').unlink()
            fixture.shutdown_evidence.record_stop(home,before,0,stopped(),None)
        fixture.command=lambda *a,**k:self.fail('absent endpoint stop was reissued')

    def test_actual_first_alive_then_later_esrch_reuses_only_after_shared_observation(self):
        # Model CLI's already positive shutdown/lock-release result while its
        # one owned non-ARM process has not finished exiting. The first native
        # probe must actually see it alive; only a later ESRCH closes reuse.
        with tempfile.TemporaryDirectory() as name:
            root=Path(name);events=[];fixture=self.fixture(root,events)
            child=subprocess.Popen([sys.executable,'-c',
                'import sys;sys.stdout.write("r");sys.stdout.flush();sys.stdin.buffer.read();sys.exit(0)'],
                stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,start_new_session=True)
            native_kill=os.kill;reads=[]
            try:
                ready,_,_=select.select([child.stdout],[],[],2)
                self.assertTrue(ready,'bounded owned child readiness')
                self.assertEqual(child.stdout.read(1),b'r')
                path=fixture.homes[0]/'endpoint.json';path.write_bytes(endpoint(pid=child.pid));path.chmod(0o600)
                self.prepare_already_stopped(fixture)
                def observe(pid,sig):
                    self.assertEqual(sig,0,'saved PID must never receive a nonzero signal')
                    if pid==child.pid and not reads:
                        native_kill(pid,0);reads.append('actual-alive')
                        child.stdin.close()
                        self.assertEqual(child.wait(timeout=1),0,'sole owned Wait must positively join')
                        return None  # Preserve the first actual alive observation.
                    try:return native_kill(pid,0)
                    except ProcessLookupError:
                        if pid==child.pid:reads.append('actual-later-ESRCH')
                        raise
                with mock.patch.object(harness.os,'kill',side_effect=observe):errors=fixture.cleanup()
                print('actual-delayed-exit-observations '+json.dumps({'pid':child.pid,'reads':reads,'owned_wait_exit':child.returncode,'cleanup_errors':errors}),flush=True)
                self.assertEqual(errors,[])
                self.assertEqual(reads,['actual-alive','actual-later-ESRCH'])
                proof=next(row for row in fixture.evidence['verified-shutdown-reuse'] if row['pid']==child.pid)
                self.assertTrue(proof['independent_pid_absence_observed'])
                self.assertEqual(events,['fixture-terminate','fixture-positive-wait'])
            finally:
                if not child.stdin.closed:child.stdin.close()
                self.assertEqual(child.wait(timeout=2),0)
                child.stdout.close()

    def test_live_at_shared_deadline_and_permission_unknown_do_not_publish_reuse(self):
        for mode in ('live-at-deadline','permission-unknown'):
            with self.subTest(mode=mode),tempfile.TemporaryDirectory() as name:
                events=[];fixture=self.fixture(Path(name),events);self.prepare_already_stopped(fixture)
                def observe(pid,sig):
                    self.assertEqual(sig,0)
                    if mode=='permission-unknown':raise PermissionError(1,'native observation denied')
                clock=itertools.count(0,3)
                with mock.patch.object(harness.os,'kill',side_effect=observe),mock.patch.object(harness.time,'monotonic',side_effect=lambda:next(clock)):
                    errors=fixture.cleanup()
                self.assertTrue(errors)
                self.assertNotIn('verified-shutdown-reuse',fixture.evidence)
                self.assertTrue(any('final shutdown reuse unknown' in error for error in errors))

    def test_endpoint_reappearance_during_shared_observation_invalidates_old_receipt(self):
        with tempfile.TemporaryDirectory() as name:
            events=[];fixture=self.fixture(Path(name),events);self.prepare_already_stopped(fixture)
            first=fixture.homes[0];oldpid=fixture.shutdown_evidence.selected(first)['pid'];created=[]
            def observe(pid,sig):
                self.assertEqual(sig,0)
                if pid==oldpid and not created:
                    path=first/'endpoint.json';path.write_bytes(endpoint(pid=2147483002,token='late-start'));path.chmod(0o600);created.append(True)
                raise ProcessLookupError()
            with mock.patch.object(harness.os,'kill',side_effect=observe):errors=fixture.cleanup()
            self.assertTrue(any('endpoint appeared during shutdown observation' in error for error in errors))
            self.assertNotIn(str(first),fixture.shutdown_evidence.stops)
            self.assertFalse(any(row['home']==str(first) for row in fixture.evidence.get('verified-shutdown-reuse',[])))


if __name__=='__main__':unittest.main(verbosity=2)
