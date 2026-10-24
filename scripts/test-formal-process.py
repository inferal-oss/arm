#!/usr/bin/env python3
"""Proposed component controls; run pure then native only after source review.

Pure controls never call a native backend/Popen/signal/Wait. Native controls are
harmless fixed fixtures and require an independent trusted-tool invocation.
They do not start TLC, TLAPM, Go, ARM, authenticated clients or remote services.
"""
from contextlib import ExitStack
import hashlib
import stat
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import types
import unittest
from unittest.mock import Mock, patch

sys.dont_write_bytecode = True
source = Path(__file__).with_name('formal_process.py')
owner = types.ModuleType('formal_owner_component')
owner.__file__ = str(source)
exec(compile(source.read_bytes(), str(source), 'exec'), owner.__dict__)


def fake_state():
    return types.SimpleNamespace(active=None, quarantine=[None] * 64)


def fake_owned():
    state = fake_state()
    latch = owner.CancellationLatch(state)
    backend = types.SimpleNamespace(observe=lambda pid: 0)
    value = owner.Owned(backend, latch, state)
    process = Mock(spec=subprocess.Popen)
    process.pid, process.returncode = 3210, None
    with latch.deferred():
        value.start_may_begin = True
        value.bind_fresh(process)
    return value


class Pure(unittest.TestCase):
    def test_import_does_not_construct_native_backend(self):
        self.assertIsNone(owner._STATE)

    def test_universal_newline_text(self):
        self.assertEqual(owner._text(b'one\r\ntwo\rthree\n'), 'one\ntwo\nthree\n')

    def test_text_decode_stays_strict(self):
        with patch.object(owner.locale, 'getpreferredencoding', return_value='utf-8'):
            with self.assertRaises(UnicodeDecodeError):
                owner._text(b'\xff')

    def test_invalid_timeout_refuses_before_admission(self):
        command = owner.Command.__new__(owner.Command)
        for value in (True, None, -1, 0, float('nan'), float('inf'), 3601):
            with self.subTest(value=value), self.assertRaises(ValueError):
                command.command(['never-started'], 'refusal', timeout=value)

    def test_raw_pid_cannot_be_bound(self):
        state = fake_state(); latch = owner.CancellationLatch(state)
        value = owner.Owned(None, latch, state)
        with latch.deferred(), self.assertRaises(RuntimeError):
            value.bind_fresh(3210)
        self.assertFalse(value.authority)
        self.assertIsNotNone(value.unknown)
        # Entirely fake local state; no native object or global slot exists.

    def test_observe_does_not_reap(self):
        value = fake_owned()
        with patch.object(owner.os, 'waitpid', side_effect=AssertionError('no Wait during observation')):
            self.assertEqual(value.observe(), 0)
            self.assertEqual(value.observe(), 0)
        self.assertEqual(value.wait_calls, 0)

    def test_native_wait_retired_before_exact_once_call(self):
        value = fake_owned(); value.observe()
        def wait(pid, flags):
            self.assertFalse(value.authority)
            self.assertTrue(value.retired)
            self.assertEqual(value.wait_calls, 1)
            return pid, 0
        with patch.object(owner.os, 'waitpid', side_effect=wait) as native:
            self.assertEqual(value.reap(), 0)
            with self.assertRaises(RuntimeError):value.reap()
        self.assertEqual(native.call_count, 1)
        self.assertEqual(value.process.returncode, 0)

    def test_uncertain_native_wait_never_retries_or_signals(self):
        value = fake_owned(); value.observe()
        with patch.object(owner.os, 'waitpid', side_effect=ChildProcessError) as wait:
            with self.assertRaises(ChildProcessError):value.reap()
            with patch.object(owner.os, 'killpg') as kill:
                with self.assertRaises(RuntimeError):value.cancel()
                kill.assert_not_called()
        self.assertEqual(wait.call_count, 1)
        self.assertIs(value.state.quarantine[value.slot], value)

    def test_observation_error_revokes_authority(self):
        value = fake_owned()
        value.backend.observe = Mock(side_effect=OSError('unavailable'))
        with self.assertRaises(OSError):value.observe()
        self.assertFalse(value.authority)
        self.assertIsNotNone(value.unknown)
        with patch.object(owner.os, 'killpg') as kill:
            with self.assertRaises(RuntimeError):value.cancel()
            kill.assert_not_called()

    def test_cancel_kills_group_before_wait_even_after_terminal_term(self):
        value = fake_owned(); value.observe(); events = []
        clock = [0.0]
        def monotonic():clock[0] += 0.1;return clock[0]
        def killpg(pid, number):
            if number == 0:raise ProcessLookupError
            self.assertTrue(value.authority)
            self.assertFalse(value.reaped)
            events.append(('signal', number))
        def wait(pid, flags):events.append(('wait', pid));return pid, 0
        with patch.object(owner.time, 'monotonic', side_effect=monotonic), patch.object(owner.time, 'sleep'), \
                patch.object(owner.os, 'killpg', side_effect=killpg), \
                patch.object(owner.os, 'kill', side_effect=ProcessLookupError), \
                patch.object(owner.os, 'waitpid', side_effect=wait):
            self.assertEqual(value.cancel(), 0)
        self.assertEqual(events, [('signal', signal.SIGTERM), ('signal', signal.SIGKILL), ('wait', 3210)])

    def test_normal_wait_has_no_signals(self):
        value = fake_owned();value.observe()
        with patch.object(owner.os, 'waitpid', return_value=(3210, 0)), patch.object(owner.os, 'killpg') as kill:
            value.reap()
        kill.assert_not_called()

    def test_quarantine_overflow_refuses(self):
        state = fake_state();state.quarantine[:] = [object()] * 64
        with self.assertRaises(RuntimeError):owner.Owned(None, None, state)

    def test_unused_owner_can_discard_only_before_start(self):
        state = fake_state();value = owner.Owned(None, None, state)
        value.release_unstarted();self.assertIsNone(value.slot)
        value = owner.Owned(None, None, state);value.start_may_begin = True
        with self.assertRaises(RuntimeError):value.release_unstarted()

    def test_unknown_atexit_is_failure_not_implicit_reap(self):
        saved = owner._STATE
        owner._STATE = types.SimpleNamespace(quarantine=[object()])
        try:
            with patch.object(owner.os, '_exit') as exit_process:
                owner._refuse_unknown_finalizer_reap()
            exit_process.assert_called_once_with(1)
        finally:owner._STATE = saved

    def test_active_latch_cannot_be_replaced(self):
        state = fake_state();state.active = object()
        with patch.object(owner.signal, 'signal') as change:
            with self.assertRaises(RuntimeError):owner.CancellationLatch(state).install()
            change.assert_not_called()

    def test_repeated_term_and_int_defer_first_exception(self):
        state = fake_state();latch = owner.CancellationLatch(state)
        with patch.object(owner.signal, 'signal'):
            with self.assertRaises(SystemExit) as error:
                with latch.deferred():
                    latch.receive(signal.SIGTERM, None)
                    latch.receive(signal.SIGINT, None)
                    self.assertEqual(latch.depth, 1)
            self.assertEqual(error.exception.code, 143)
        self.assertEqual(latch.received, signal.SIGTERM)

    def test_int_is_keyboardinterrupt_after_deferred_cut(self):
        latch = owner.CancellationLatch(fake_state())
        with patch.object(owner.signal, 'signal'), self.assertRaises(KeyboardInterrupt):
            with latch.deferred():latch.receive(signal.SIGINT, None)

    def test_linux_builtin_native_result_domain(self):
        backend = owner.LinuxBackend.__new__(owner.LinuxBackend)
        constants = {'P_PID':1,'WEXITED':4,'WNOHANG':1,'WNOWAIT':32,
                     'CLD_EXITED':1,'CLD_KILLED':2,'CLD_DUMPED':3}
        with ExitStack() as stack:
            for name,value in constants.items():stack.enter_context(patch.object(owner.os,name,value,create=True))
            native = stack.enter_context(patch.object(owner.os,'waitid',create=True))
            native.return_value=None;self.assertIsNone(backend.observe(3210))
            native.return_value=types.SimpleNamespace(si_pid=3210,si_code=1,si_status=7)
            self.assertEqual(backend.observe(3210),7)
            native.return_value=types.SimpleNamespace(si_pid=3210,si_code=2,si_status=15)
            self.assertEqual(backend.observe(3210),-15)
            native.return_value=types.SimpleNamespace(si_pid=3211,si_code=1,si_status=0)
            with self.assertRaises(RuntimeError):backend.observe(3210)


    def capture_replacement(self, kind, diagnostics=False):
        with tempfile.TemporaryDirectory(prefix='formal-capture-pure-') as directory:
            path = Path(directory) / 'capture.log'
            capture = owner.Capture(path)
            try:
                os.pwrite(capture.fileno(), b'original', 0)
                if kind == 'multilink':
                    other = Path(directory) / 'linked'
                    os.link(path, other)
                else:
                    path.rename(Path(directory) / 'original')
                    other = Path(directory) / 'other'
                    other.write_bytes(b'decoy')
                    if kind == 'symlink':
                        path.symlink_to(other)
                    else:
                        other.rename(path)
                        other = path
                before = other.read_bytes()
                with self.assertRaises(RuntimeError):
                    if diagnostics:
                        capture.append_diagnostics('must never overwrite')
                    else:
                        capture.read()
                self.assertEqual(other.read_bytes(), before)
                self.assertEqual(os.pread(capture.fileno(), 32, 0), b'original')
            finally:
                capture.close()

    def test_capture_regular_swap_refuses_original_descriptor_read(self):
        self.capture_replacement('regular')

    def test_capture_symlink_swap_refuses_and_never_overwrites(self):
        self.capture_replacement('symlink', diagnostics=True)

    def test_capture_multilink_refuses_and_never_overwrites(self):
        self.capture_replacement('multilink', diagnostics=True)

    def test_capture_regular_swap_diagnostics_never_overwrite_decoy(self):
        self.capture_replacement('regular', diagnostics=True)

    def test_fixture_output_uses_retained_descriptor(self):
        with tempfile.TemporaryDirectory(prefix='formal-fixture-pure-') as directory:
            path = Path(directory) / 'fixture.log'
            fixture = owner.Fixture.__new__(owner.Fixture)
            fixture.log = path
            fixture.capture = owner.Capture(path)
            try:
                os.pwrite(fixture.capture.fileno(), b'original', 0)
                self.assertEqual(fixture.output(), 'original')
                path.rename(Path(directory) / 'original')
                path.write_bytes(b'decoy')
                with self.assertRaises(RuntimeError):
                    fixture.output()
                self.assertEqual(path.read_bytes(), b'decoy')
            finally:
                fixture.capture.close()

    def test_capture_diagnostics_append_same_open_object(self):
        with tempfile.TemporaryDirectory(prefix='formal-capture-pure-') as directory:
            capture = owner.Capture(Path(directory) / 'log')
            try:
                os.pwrite(capture.fileno(), b'original', 0)
                capture.append_diagnostics(' diagnostic')
                self.assertEqual(capture.read(), b'original diagnostic')
                self.assertTrue(capture.receipt()['path_matches_open_descriptor'])
            finally:
                capture.close()

    def metadata_cancellation(self, incoming):
        # Exercise the actual Command finalization path with fake retained native
        # objects only: no CDLL, child, signal, real wait or runtime module import.
        state = fake_state()
        latch = owner.CancellationLatch(state)
        latch.verify = lambda: None
        latch.received = None
        class FakePopen(subprocess.Popen):
            def __init__(self, *args, **kwargs):
                self.pid, self.returncode = 3210, None
            def __del__(self):
                pass  # No native child exists in this pure control.
        def cancel(value):
            value.authority = False
            value.retired = value.reaped = True
            value.process.returncode = -15
            value.state.quarantine[value.slot] = None
            value.slot = None
        def metadata(path, record):
            self.assertEqual(record['error_type'], 'TimeoutExpired')
            raise incoming
        with tempfile.TemporaryDirectory(prefix='formal-metadata-pure-') as directory:
            command = owner.Command.__new__(owner.Command)
            command.state, command.latch = state, latch
            command.backend = types.SimpleNamespace(identity={'fake': True})
            command.run = command.cwd = Path(directory)
            ticks = iter([0.0, 2.0, 3.0])
            with patch.object(owner, 'process_state', return_value=state), \
                    patch.object(owner.subprocess, 'Popen', FakePopen), \
                    patch.object(owner.Owned, 'cancel', cancel), \
                    patch.object(owner.time, 'monotonic', side_effect=lambda: next(ticks)), \
                    patch.object(owner, '_receipt', side_effect=metadata), \
                    patch.object(owner.os, 'waitpid', side_effect=AssertionError('no native Wait')), \
                    patch.object(owner.os, 'killpg', side_effect=AssertionError('no native signal')):
                with self.assertRaises(type(incoming)) as raised:
                    command.command(['fake-only'], 'metadata', timeout=1)
            self.assertIs(raised.exception, incoming)
            self.assertIsInstance(incoming.formal_prior_failure, subprocess.TimeoutExpired)
            self.assertEqual(incoming.formal_prior_failure.timeout, 1)
            self.assertIs(incoming.formal_metadata_failure, incoming)
            self.assertIsNone(state.active)
            self.assertFalse(any(state.quarantine))

    def test_metadata_first_int_outranks_timeout_retains_original(self):
        self.metadata_cancellation(KeyboardInterrupt())

    def test_metadata_first_term_outranks_timeout_retains_original(self):
        self.metadata_cancellation(SystemExit(143))

    def test_first_cancellation_is_not_replaced_by_second(self):
        first, second = KeyboardInterrupt(), SystemExit(143)
        self.assertIs(owner._first_failure(first, second), first)
        self.assertIs(owner._first_failure(second, first), second)


    def fixture_close_failure(self, primary, secondary, cut):
        # Actual Fixture.close in a real exception/finally context; every native
        # object/operation below is fake and no child or library is constructed.
        state = fake_state()
        fixture = owner.Fixture.__new__(owner.Fixture)
        fixture.state = state
        state.active = fixture
        fixture.log = Path('/unopened-pure-fixture.log')
        fixture.capture = Mock()
        native = types.SimpleNamespace(authority=True, start_may_begin=True,
            process=None, reaped=False, absence=None, unknown=None)
        fixture.owner = native
        def cancel():
            native.authority = False
            if cut == 'cleanup':
                native.unknown = 'fake cleanup error, not native evidence'
                raise secondary
            native.reaped = True
            native.absence = {'pid':'absent', 'group':'absent'}
        native.cancel = cancel
        native.receipt = lambda: {'fake_only':True, 'authority':native.authority}
        if cut == 'capture':
            fixture.capture.close.side_effect = secondary
        with patch.object(owner, '_receipt', side_effect=secondary if cut == 'metadata' else None) as metadata, \
                patch.object(owner.os, 'waitpid', side_effect=AssertionError('no native Wait')), \
                patch.object(owner.os, 'killpg', side_effect=AssertionError('no native signal')):
            try:
                try:
                    raise primary
                finally:
                    fixture.close()
            except BaseException as error:
                observed = error
            else:
                self.fail('original caller exception disappeared')
        expected = secondary if (isinstance(secondary, (KeyboardInterrupt, SystemExit)) and
            not isinstance(primary, (KeyboardInterrupt, SystemExit))) else primary
        self.assertIs(observed, expected)
        if expected is secondary:
            self.assertIs(observed.formal_prior_failure, primary)
        self.assertEqual(metadata.call_count, 1)
        self.assertEqual(metadata.call_args[0][1]['caller_failure_type'], type(primary).__name__)
        self.assertIsNone(state.active)
        self.assertFalse(native.authority)  # No cleanup error regains authority.
        fixture.capture.close.assert_called_once_with()

    def test_fixture_close_first_int_outranks_ordinary_caller(self):
        for cut in ('cleanup','capture','metadata'):
            with self.subTest(cut=cut):
                self.fixture_close_failure(RuntimeError('caller'), KeyboardInterrupt(), cut)

    def test_fixture_close_first_term_outranks_ordinary_caller(self):
        for cut in ('cleanup','capture','metadata'):
            with self.subTest(cut=cut):
                self.fixture_close_failure(RuntimeError('caller'), SystemExit(143), cut)

    def test_fixture_close_second_cancellation_keeps_first(self):
        for cut in ('cleanup','capture','metadata'):
            for first_int in (False,True):
                with self.subTest(cut=cut, first_int=first_int):
                    primary = KeyboardInterrupt() if first_int else SystemExit(143)
                    secondary = SystemExit(143) if first_int else KeyboardInterrupt()
                    self.fixture_close_failure(primary, secondary, cut)

    def test_fixture_close_ordinary_cleanup_keeps_caller(self):
        for cut in ('cleanup','capture','metadata'):
            with self.subTest(cut=cut):
                self.fixture_close_failure(RuntimeError('caller'), ValueError('cleanup'), cut)

    def fixture_constructor_failure(self, primary, secondary, cut):
        # Invoke actual Fixture.__init__, including its except/finally code;
        # native Start/binding/backend/capture are explicit local fakes only.
        state = fake_state()
        latch = owner.CancellationLatch(state)
        capture = Mock()
        native = types.SimpleNamespace(authority=False, start_may_begin=False,
            process=None, unknown=None)
        def bind(value):
            native.process = value
            native.authority = True
            raise primary
        def cancel():
            native.authority = False
            if cut == 'cleanup':
                native.unknown = 'fake cleanup error, not native evidence'
                raise secondary
        native.bind_fresh, native.cancel = bind, cancel
        if cut == 'capture':
            capture.close.side_effect = secondary
        child = object()  # Never a real Popen/native authority.
        with patch.object(owner, 'process_state', return_value=state), \
                patch.object(owner, 'cancellation_latch', return_value=latch), \
                patch.object(owner, 'native_backend', return_value=object()), \
                patch.object(owner, 'Owned', return_value=native), \
                patch.object(owner, 'Capture', return_value=capture), \
                patch.object(owner.subprocess, 'Popen', return_value=child) as start, \
                patch.object(owner.os, 'waitpid', side_effect=AssertionError('no native Wait')), \
                patch.object(owner.os, 'killpg', side_effect=AssertionError('no native signal')):
            try:
                owner.Fixture(['fake-only'], '/unopened-constructor.log', '/unopened')
            except BaseException as error:
                observed = error
            else:
                self.fail('constructor error disappeared')
        expected = secondary if (isinstance(secondary, (KeyboardInterrupt, SystemExit)) and
            not isinstance(primary, (KeyboardInterrupt, SystemExit))) else primary
        self.assertIs(observed, expected)
        if expected is secondary:
            self.assertIs(observed.formal_prior_failure, primary)
        self.assertIsNone(state.active)
        self.assertFalse(native.authority)
        self.assertIs(native.process, child)
        self.assertTrue(native.start_may_begin)
        self.assertEqual(start.call_count, 1)
        capture.close.assert_called_once_with()

    def test_fixture_constructor_first_int_outranks_ordinary_error(self):
        for cut in ('cleanup','capture'):
            with self.subTest(cut=cut):
                self.fixture_constructor_failure(RuntimeError('constructor'), KeyboardInterrupt(), cut)

    def test_fixture_constructor_first_term_outranks_ordinary_error(self):
        for cut in ('cleanup','capture'):
            with self.subTest(cut=cut):
                self.fixture_constructor_failure(RuntimeError('constructor'), SystemExit(143), cut)

    def test_fixture_constructor_second_cancellation_keeps_first(self):
        for cut in ('cleanup','capture'):
            for first_int in (False,True):
                with self.subTest(cut=cut, first_int=first_int):
                    primary = KeyboardInterrupt() if first_int else SystemExit(143)
                    secondary = SystemExit(143) if first_int else KeyboardInterrupt()
                    self.fixture_constructor_failure(primary, secondary, cut)

    def test_fixture_constructor_ordinary_cleanup_keeps_original(self):
        for cut in ('cleanup','capture'):
            with self.subTest(cut=cut):
                self.fixture_constructor_failure(RuntimeError('constructor'), ValueError('cleanup'), cut)

    def darwin_result(self, *, pid=3210, signo=20, error=0, code=1, status=0,
                      result=0, native_errno=0):
        # Bypass __init__: real ctypes storage, but no CDLL/native call.
        backend = owner.DarwinBackend.__new__(owner.DarwinBackend)
        observations = []
        def native(kind, selected, pointer, flags):
            self.assertEqual((kind, selected, flags), (1, 3210, 37))
            value = pointer._obj
            value.pid, value.signo, value.error = pid, signo, error
            value.code, value.status = code, status
            raw = bytes(value)
            observations.append((value, raw))
            owner.ctypes.set_errno(native_errno)
            return result
        backend.function = Mock(side_effect=native)
        return backend, observations

    @patch.object(owner.signal,'NSIG',32)
    @patch.object(owner.signal,'SIGCHLD',20)
    def test_darwin_exit_signed_int_normalizes_without_changing_raw(self):
        for raw in (0, 42, 127, 255, 256, 257, 65536, 0xffffff,
                    0x7fffffff, -1, -256, -0x80000000):
            with self.subTest(raw=raw):
                backend, captured = self.darwin_result(status=raw)
                self.assertEqual(backend.observe(3210), raw & 255)
                self.assertEqual(len(captured), 1)
                value, before = captured[0]
                self.assertEqual(value.status, raw)
                self.assertEqual(bytes(value), before)
                backend.function.assert_called_once()

    @patch.object(owner.signal,'NSIG',32)
    @patch.object(owner.signal,'SIGCHLD',20)
    def test_darwin_exit_normalization_keeps_exact_identity_and_code_guards(self):
        changes = ({'pid':3211}, {'signo':19}, {'error':1},
                   {'code':0}, {'code':4}, {'code':5}, {'code':6},
                   {'code':2,'status':0}, {'code':2,'status':32},
                   {'code':3,'status':256}, {'code':3,'status':-1})
        for change in changes:
            with self.subTest(change=change):
                backend, _ = self.darwin_result(**change)
                with self.assertRaises(RuntimeError):backend.observe(3210)
        backend, _ = self.darwin_result(pid=0)
        self.assertIsNone(backend.observe(3210))
        for code in (2, 3):
            for status in (1, 15, 31):
                with self.subTest(code=code,status=status):
                    backend, _ = self.darwin_result(code=code,status=status)
                    self.assertEqual(backend.observe(3210), -status)

    @patch.object(owner.signal,'NSIG',32)
    @patch.object(owner.signal,'SIGCHLD',20)
    def test_darwin_native_error_and_invalid_pid_never_normalize(self):
        backend, _ = self.darwin_result(result=-1,native_errno=10)
        with self.assertRaises(OSError) as error:backend.observe(3210)
        self.assertEqual(error.exception.errno,10)
        for pid in (True,None,0,-1,0x80000000):
            with self.subTest(pid=pid):
                backend, _ = self.darwin_result()
                with self.assertRaises(ValueError):backend.observe(pid)
                backend.function.assert_not_called()

    @patch.object(owner.signal,'NSIG',32)
    @patch.object(owner.signal,'SIGCHLD',20)
    def test_darwin_normalized_exit_still_requires_exact_sole_wait(self):
        for raw, waited in ((0xffffff,255), (256,0), (256,7)):
            with self.subTest(raw=raw,waited=waited):
                value = fake_owned()
                value.backend, _ = self.darwin_result(status=raw)
                self.assertEqual(value.observe(),raw & 255)
                with patch.object(owner.os,'waitpid',return_value=(3210,waited << 8)) as wait, \
                        patch.object(owner.os,'killpg') as send:
                    if waited == (raw & 255):
                        self.assertEqual(value.reap(),waited)
                    else:
                        with self.assertRaisesRegex(RuntimeError,'disagrees'):value.reap()
                        self.assertIsNotNone(value.unknown)
                    with self.assertRaises(RuntimeError):value.reap()
                    with self.assertRaises(RuntimeError):value.cancel()
                    self.assertEqual(wait.call_count,1)
                    send.assert_not_called()
                self.assertFalse(value.authority)
                self.assertTrue(value.retired)
                self.assertTrue(value.reaped)
                self.assertEqual(value.process.returncode,waited)


    def test_linux_exited_domain_remains_eight_bit(self):
        backend = owner.LinuxBackend.__new__(owner.LinuxBackend)
        constants = {'P_PID':1,'WEXITED':4,'WNOHANG':1,'WNOWAIT':32,
                     'CLD_EXITED':1,'CLD_KILLED':2,'CLD_DUMPED':3}
        with ExitStack() as stack:
            for name,value in constants.items():
                stack.enter_context(patch.object(owner.os,name,value,create=True))
            native = stack.enter_context(patch.object(owner.os,'waitid',create=True))
            for status in (-1,256,0xffffff):
                with self.subTest(status=status):
                    native.return_value=types.SimpleNamespace(si_pid=3210,si_code=1,si_status=status)
                    with self.assertRaises(RuntimeError):backend.observe(3210)


class Native(unittest.TestCase):
    def setUp(self):
        self.path=NATIVE_ROOT/self._testMethodName
        self.path.mkdir(mode=0o700)
        self.command=owner.Command(self.path, self.path)

    def run_python(self,code,name,**kwargs):
        return self.command.command([sys.executable,'-u','-c',code],name,**kwargs)

    def receipt(self,name):return json.loads((self.path/(name+'-owner.json')).read_text())

    def test_normal_zero_and_newlines(self):
        self.assertEqual(self.run_python("import os;os.write(1,b'one\\r\\ntwo\\rthree\\n')",'normal'), 'one\ntwo\nthree\n')
        value=self.receipt('normal')['owner']
        self.assertEqual(value['native_wait_calls'],1);self.assertEqual(value['signals'],[])
        self.assertEqual(value['post_wait_absence'],{'pid':'absent','group':'absent'})

    def test_nonzero_expected_status(self):
        self.assertEqual(self.run_python("raise SystemExit(7)",'nonzero',expected=7),'')
        self.assertEqual(self.receipt('nonzero')['owner']['observed_exit'],7)

    def checked_exit_argument(self, requested, name):
        self.run_python('import os,signal;signal.alarm(3);os._exit(' + str(requested) + ')',
                        name,expected=requested & 255,timeout=5)
        receipt = self.receipt(name)
        value = receipt['owner']
        self.assertEqual(receipt['status'],'passed_native_command_only')
        self.assertEqual(receipt['actual_exit'],requested & 255)
        self.assertEqual(value['observed_exit'],requested & 255)
        self.assertEqual(value['native_wait_calls'],1)
        self.assertEqual(value['native_wait_status'] >> 8 & 255,requested & 255)
        self.assertTrue(value['retired']);self.assertTrue(value['reaped'])
        self.assertFalse(value['authority'])
        self.assertFalse(value['strongly_quarantined_until_native_reap'])
        self.assertIsNone(value['unknown']);self.assertEqual(value['signals'],[])
        self.assertEqual(value['post_wait_absence'],{'pid':'absent','group':'absent'})

    def test_wide_exit_arguments_match_native_wait(self):
        for index, requested in enumerate((256,257,65536,0xffffff,0x7fffffff)):
            with self.subTest(requested=requested):
                self.checked_exit_argument(requested,'wide-'+str(index))

    def test_negative_exit_arguments_match_native_wait(self):
        for index, requested in enumerate((-1,-256,-0x80000000)):
            with self.subTest(requested=requested):
                self.checked_exit_argument(requested,'negative-'+str(index))

    def test_signal_status(self):
        self.run_python('import os,signal;os.kill(os.getpid(),signal.SIGTERM)','signal',expected=-15)
        self.assertEqual(self.receipt('signal')['owner']['observed_exit'],-15)

    def test_existing3600_domain_without_waiting(self):
        self.run_python('pass','long-domain',timeout=3600)
        self.assertEqual(self.receipt('long-domain')['timeout_seconds'],3600)

    def test_timeout_parent_exits_term_child_ignores(self):
        code="""import subprocess,sys,time
child="import signal,time;signal.signal(signal.SIGTERM,signal.SIG_IGN);print('child ready',flush=True);time.sleep(20)"
subprocess.Popen([sys.executable,'-u','-c',child])
print('parent ready',flush=True)
time.sleep(20)
"""
        with self.assertRaises(subprocess.TimeoutExpired) as expired:self.run_python(code,'timeout',timeout=1)
        self.assertIn('parent ready',expired.exception.output)
        self.assertIn('child ready',expired.exception.output)
        value=self.receipt('timeout')
        self.assertEqual(value['status'],'failed')
        self.assertEqual([x['signal']for x in value['owner']['signals']],[15,9])
        self.assertEqual(value['owner']['native_wait_calls'],1)
        self.assertEqual(value['owner']['post_wait_absence'],{'pid':'absent','group':'absent'})

    def test_output_cap_is_explicit_failure(self):
        with self.assertRaisesRegex(RuntimeError,'16MiB'):
            self.run_python("import os;os.write(1,b'x'*(18<<20))",'output-cap')
        value=self.receipt('output-cap');self.assertEqual(value['status'],'failed')
        self.assertIn('output_refused',value)


def native_artifact_catalog(root):
    artifacts = []
    for path in sorted(root.rglob('*')):
        value = path.lstat()
        if stat.S_ISDIR(value.st_mode):
            continue
        if not stat.S_ISREG(value.st_mode) or value.st_nlink != 1:
            raise RuntimeError('native evidence must remain single-link regular files')
        if value.st_size > (32 << 20):
            raise RuntimeError('native evidence exceeds32MiB hash/read bound; retained without deletion')
        digest = hashlib.sha256()
        with path.open('rb') as stream:
            for chunk in iter(lambda: stream.read(65536), b''):
                digest.update(chunk)
        artifacts.append({'path':str(path.relative_to(root)), 'bytes':value.st_size,
                          'sha256':digest.hexdigest()})
    owner._receipt(root/'artifacts.json', {'schema':'arm.formal-native-test-evidence/v1',
        'root':str(root), 'artifacts':artifacts,
        'source':str(source.resolve()), 'source_sha256':owner.SOURCE_SHA256})


if __name__=='__main__':
    if sys.argv[1:] == ['pure']:
        selected = Pure
    elif len(sys.argv) == 3 and sys.argv[1] == 'native':
        parent = Path(sys.argv[2])
        if not parent.is_absolute() or parent.is_symlink():
            raise SystemExit('native evidence requires absolute nonsymlink output parent')
        parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        NATIVE_ROOT = Path(tempfile.mkdtemp(prefix='formal-native-retained-', dir=parent)).resolve()
        print('Retained native evidence: ' + str(NATIVE_ROOT), flush=True)
        selected = Native
    else:
        raise SystemExit('choose pure OR native ABSOLUTE_EVIDENCE_PARENT')
    try:
        result=unittest.TextTestRunner(verbosity=2,failfast=True).run(unittest.defaultTestLoader.loadTestsFromTestCase(selected))
    finally:
        if selected is Native:
            native_artifact_catalog(NATIVE_ROOT)
    raise SystemExit(0 if result.wasSuccessful() else 1)
