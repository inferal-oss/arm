"""Retained native ownership for development-tool commands.

Import reads/hashes this source and registers a failure-only atexit guard.
It installs no native library, signal handler or process.
Only a main-thread Command acquires the process-wide cancellation latch. Fresh
Popen ownership is retained through non-reaping waitid observation and one
irreversible native waitpid. Unknown owners stay strongly quarantined; neither
saved PIDs nor Python finalizers acquire a second signal/reap authority.

Platform and caller validation is recorded separately in docs/VALIDATION.md.
"""
from contextlib import contextmanager
import atexit
import ctypes
import errno
import hashlib
import io
import json
import locale
import math
import os
from pathlib import Path
import signal
import stat
import subprocess
import sys
import threading
import time
import types

SOURCE = Path(__file__).resolve()
SOURCE_SHA256 = hashlib.sha256(SOURCE.read_bytes()).hexdigest()
MAX_OUTPUT_BYTES = 16 << 20
MAX_TIMEOUT_SECONDS = 3600
_STATE_KEY = '_arm_formal_process_state_v1'
_STATE = None


def process_state():
    """Same source bytes across captured clones share one process-wide owner."""
    global _STATE
    if threading.current_thread() is not threading.main_thread():
        raise RuntimeError('formal process ownership requires the main thread')
    state = getattr(sys, _STATE_KEY, None)
    if state is None:
        state = types.SimpleNamespace(schema=1, source_sha256=SOURCE_SHA256,
                                      latch=None, active=None,
                                      quarantine=[None] * 64)
        setattr(sys, _STATE_KEY, state)
    if state.schema != 1 or state.source_sha256 != SOURCE_SHA256:
        raise RuntimeError('process-wide formal owner source changed')
    _STATE = state
    return state


def _refuse_unknown_finalizer_reap():
    if _STATE is not None and any(x is not None for x in _STATE.quarantine):
        # Receipts flush before ordinary teardown. This is a failing exit only,
        # not child cleanup or known-no-start/known-no-commit evidence.
        os._exit(1)


atexit.register(_refuse_unknown_finalizer_reap)


class CancellationLatch:
    """Defer the first TERM/INT through launch and sole-Wait critical cuts.

    INT compatibility is deliberate: KeyboardInterrupt is delivered after the
    current retained cleanup, while TERM retains the existing SystemExit143.
    Further TERM/INT are ignored after the first cancellation, including inside
    finalization. No generic immunity to other signals is claimed.
    """
    def __init__(self, state):
        self.state = state
        self.depth = 0
        self.pending = None
        self.received = None
        self.installed = False

    def install(self):
        if self.state.active is not None:
            raise RuntimeError('cannot replace an active formal cancellation owner')
        if self.received is not None:
            raise RuntimeError('formal cancellation already received; no new command')
        for number in (signal.SIGTERM, signal.SIGINT):
            signal.signal(number, self.receive)
        self.installed = True

    def verify(self):
        if (not self.installed or
                any(signal.getsignal(n) != self.receive
                    for n in (signal.SIGTERM, signal.SIGINT))):
            raise RuntimeError('formal cancellation handler was replaced')

    def receive(self, number, _frame):
        for item in (signal.SIGTERM, signal.SIGINT):
            signal.signal(item, signal.SIG_IGN)
        if self.received is None:
            self.received = number
            self.pending = number
        if self.depth == 0:
            self.deliver()

    def deliver(self):
        number = self.pending
        if number is not None:
            self.pending = None
            if number == signal.SIGINT:
                raise KeyboardInterrupt
            raise SystemExit(128 + number)

    @contextmanager
    def deferred(self):
        self.depth += 1
        try:
            yield
        except BaseException:
            self.depth -= 1
            # Do not replace an already-thrown native/OS exception. Any pending
            # cancellation is retained explicitly in command metadata.
            raise
        else:
            self.depth -= 1
            if self.depth == 0:
                self.deliver()


def cancellation_latch(state):
    if any(x is not None for x in state.quarantine):
        raise RuntimeError('an unknown retained formal owner blocks new admission')
    if state.latch is None:
        state.latch = CancellationLatch(state)
        state.latch.install()
    elif state.latch.received is not None:
        raise RuntimeError('formal cancellation already received; no new command')
    else:
        # Reinstalling while inactive supports imported Gate callers whose
        # entrypoint explicitly installed its legacy TERM handler beforehand.
        state.latch.install()
    return state.latch


class _Sigval(ctypes.Union):
    _fields_ = [('integer', ctypes.c_int32), ('pointer', ctypes.c_void_p)]


class _Siginfo(ctypes.Structure):
    _fields_ = [('signo', ctypes.c_int32), ('error', ctypes.c_int32),
                ('code', ctypes.c_int32), ('pid', ctypes.c_int32),
                ('uid', ctypes.c_uint32), ('status', ctypes.c_int32),
                ('address', ctypes.c_void_p), ('value', _Sigval),
                ('band', ctypes.c_long), ('pad', ctypes.c_ulong * 7)]


class DarwinBackend:
    """Exact Darwin arm64 LP64 ABI from the separately accepted native C probe."""
    def __init__(self):
        if sys.platform != 'darwin' or os.uname().machine != 'arm64':
            raise RuntimeError('only the probed Darwin arm64 waitid ABI is supported')
        expected = {'signo': 0, 'error': 4, 'code': 8, 'pid': 12, 'uid': 16,
                    'status': 20, 'address': 24, 'value': 32, 'band': 40, 'pad': 48}
        if (ctypes.sizeof(_Siginfo), ctypes.alignment(_Siginfo),
                ctypes.sizeof(ctypes.c_int), ctypes.sizeof(ctypes.c_void_p),
                ctypes.sizeof(ctypes.c_long), ctypes.sizeof(_Sigval)) != (104, 8, 4, 8, 8, 8):
            raise RuntimeError('Darwin waitid ABI layout mismatch')
        if {name: getattr(_Siginfo, name).offset for name in expected} != expected:
            raise RuntimeError('Darwin waitid ABI offsets mismatch')
        if signal.SIGCHLD != 20 or signal.NSIG != 32:
            raise RuntimeError('Darwin signal constants mismatch')
        self.library = ctypes.CDLL('/usr/lib/libSystem.B.dylib', use_errno=True)
        self.function = self.library.waitid
        self.function.argtypes = [ctypes.c_int, ctypes.c_uint32,
                                  ctypes.POINTER(_Siginfo), ctypes.c_int]
        self.function.restype = ctypes.c_int
        self.identity = {'backend': 'Darwin arm64 LP64', 'symbol': 'waitid',
                         'library': '/usr/lib/libSystem.B.dylib',
                         'abi_receipt_sha256': '8a363a95079cb189632e6262cf587f108696e5970a2f7c0c68f2dc85f8a1cc59',
                         'system_implementation_hash_claimed': False}

    def observe(self, pid):
        if type(pid) is not int or not 0 < pid <= 0x7fffffff:
            raise ValueError('exact positive owned PID required')
        value = _Siginfo()
        ctypes.set_errno(0)
        result = self.function(1, pid, ctypes.byref(value), 4 | 1 | 32)
        number = ctypes.get_errno()
        if result != 0:
            raise OSError(number or errno.EIO, 'native non-reaping waitid failed')
        if value.pid == 0:
            return None
        if value.pid != pid or value.signo != signal.SIGCHLD or value.error != 0:
            raise RuntimeError('crossed or malformed Darwin waitid result')
        if value.code == 1:
            # Darwin may expose a wider signed C-int exit value here. Leave
            # raw storage untouched (not saved in receipts); normalize only
            # CLD_EXITED to waitpid's low8.
            # Owned.reap still requires the exact native Wait to agree.
            return value.status & 0xff
        if value.code in (2, 3) and 1 <= value.status < signal.NSIG:
            return -value.status
        raise RuntimeError('unsupported Darwin waitid result')


class LinuxBackend:
    def __init__(self):
        names = ('waitid', 'P_PID', 'WEXITED', 'WNOHANG', 'WNOWAIT',
                 'CLD_EXITED', 'CLD_KILLED', 'CLD_DUMPED')
        if not sys.platform.startswith('linux') or not all(hasattr(os, n) for n in names):
            raise RuntimeError('Linux native non-reaping waitid is unavailable')
        self.identity = {'backend': 'Linux os.waitid', 'system_implementation_hash_claimed': False}

    def observe(self, pid):
        if type(pid) is not int or not 0 < pid <= 0x7fffffff:
            raise ValueError('exact positive owned PID required')
        value = os.waitid(os.P_PID, pid, os.WEXITED | os.WNOHANG | os.WNOWAIT)
        if value is None:
            return None
        if value.si_pid != pid:
            raise RuntimeError('crossed Linux waitid result')
        if value.si_code == os.CLD_EXITED and 0 <= value.si_status <= 255:
            return value.si_status
        if value.si_code in (os.CLD_KILLED, os.CLD_DUMPED) and 1 <= value.si_status < signal.NSIG:
            return -value.si_status
        raise RuntimeError('unsupported Linux waitid result')


def native_backend():
    if sys.platform == 'darwin':
        return DarwinBackend()
    if sys.platform.startswith('linux'):
        return LinuxBackend()
    raise RuntimeError('formal retained ownership requires supported macOS or Linux waitid')


class Owned:
    def __init__(self, backend, latch, state):
        self.backend, self.latch, self.state = backend, latch, state
        self.process = self.pid = None
        self.authority = self.retired = self.reaped = False
        self.terminal = self.unknown = self.wait_status = self.absence = None
        self.signals = []
        self.wait_calls = self.observations = 0
        self.start_may_begin = False
        self.slot = None
        for index, value in enumerate(state.quarantine):
            if value is None:
                self.slot = index
                state.quarantine[index] = self
                break
        if self.slot is None:
            raise RuntimeError('retained formal owner quarantine exhausted')

    def release_unstarted(self):
        if self.start_may_begin or self.process is not None or self.authority:
            raise RuntimeError('cannot discard a possible native start')
        if self.slot is not None and self.state.quarantine[self.slot] is self:
            self.state.quarantine[self.slot] = None
            self.slot = None

    def bind_fresh(self, process):
        if self.process is not None:
            raise RuntimeError('cannot replace a retained process object')
        # Retain a returned object before validating its scalar binding, so an
        # exceptional/refused binding cannot hand its destructor a hidden reap.
        self.process = process
        if (self.latch.depth <= 0 or self.retired or
                not isinstance(process, subprocess.Popen) or process.returncode is not None or
                type(process.pid) is not int or process.pid <= 0):
            self.unknown = 'fresh native owner binding refused'
            raise RuntimeError('exact fresh retained Popen required')
        self.process, self.pid = process, process.pid
        self.authority = True

    def lose(self, reason):
        self.authority = False
        self.unknown = reason
        raise RuntimeError('owned native outcome unknown: ' + reason)

    def check(self):
        if (not self.authority or self.retired or self.process is None or
                self.process.pid != self.pid or self.process.returncode is not None):
            self.lose('identity/authority unavailable')

    def observe(self):
        with self.latch.deferred():
            self.check()
            try:
                result = self.backend.observe(self.pid)
            except BaseException:
                self.authority = False
                self.unknown = 'non-reaping observation failed'
                raise
            self.observations += 1
            if result is not None:
                if type(result) is not int or not -(signal.NSIG - 1) <= result <= 255:
                    self.lose('malformed terminal observation')
                if self.terminal is not None and self.terminal != result:
                    self.lose('terminal observation changed')
                self.terminal = result
            elif self.terminal is not None:
                self.lose('terminal observation disappeared')
            return result

    def signal_group(self, number):
        self.check()
        if self.latch.depth <= 0 or number not in (signal.SIGTERM, signal.SIGKILL) or len(self.signals) >= 2:
            self.lose('unguarded or repeated signal attempt')
        item = {'signal': int(number), 'retained_before_attempt': True}
        self.signals.append(item)
        try:
            os.killpg(self.pid, number)
            item['result'] = 'sent'
        except ProcessLookupError:
            item['result'] = 'no_group'
        except OSError as error:
            item.update(result='denied_or_unknown', errno=error.errno)

    def reap(self):
        with self.latch.deferred():
            self.check()
            if self.terminal is None:
                self.lose('native Wait requires positive terminal observation')
            self.authority = False
            self.retired = True
            self.wait_calls += 1
            try:
                pid, status = os.waitpid(self.pid, os.WNOHANG)
            except BaseException:
                self.unknown = 'sole native Wait failed; no retry authority'
                raise
            if pid != self.pid or not (os.WIFEXITED(status) or os.WIFSIGNALED(status)):
                self.unknown = 'sole native Wait did not return exact terminal child'
                raise RuntimeError(self.unknown)
            actual = os.waitstatus_to_exitcode(status)
            self.process.returncode = actual
            self.wait_status, self.reaped = status, True
            if self.slot is None or self.state.quarantine[self.slot] is not self:
                self.unknown = 'quarantine identity changed after native Wait'
                raise RuntimeError(self.unknown)
            self.state.quarantine[self.slot] = None
            self.slot = None
            if actual != self.terminal:
                self.unknown = 'native Wait disagrees with WNOWAIT observation'
                raise RuntimeError(self.unknown)
            return actual

    def require_absence(self, timeout=2.0):
        if not self.retired or not self.reaped:
            raise RuntimeError('absence cannot replace native Wait')
        until = time.monotonic() + timeout
        while True:
            value = {}
            for label, function in (('pid', os.kill), ('group', os.killpg)):
                try:
                    function(self.pid, 0)  # Read-only; never restored authority.
                except ProcessLookupError:
                    value[label] = 'absent'
                except OSError as error:
                    value[label] = 'unknown_errno_' + str(error.errno)
                else:
                    value[label] = 'still_present'
            self.absence = value
            if value == {'pid': 'absent', 'group': 'absent'}:
                return
            if time.monotonic() >= until:
                raise RuntimeError('post-Wait PID/group cleanup unconfirmed: ' + repr(value))
            time.sleep(0.02)

    def cancel(self, grace=0.5, terminal_timeout=5.0):
        with self.latch.deferred():
            self.check()
            # Even a terminal leader remains an unreaped identity anchor. A
            # descendant may ignore TERM after its parent exits; KILL must occur
            # BEFORE our sole Wait, rather than after communicate/Popen.wait.
            self.signal_group(signal.SIGTERM)
            until = time.monotonic() + grace
            while time.monotonic() < until:
                self.observe()
                time.sleep(0.01)
            self.signal_group(signal.SIGKILL)
            until = time.monotonic() + terminal_timeout
            while self.observe() is None:
                if time.monotonic() >= until:
                    self.lose('bounded cancellation did not observe terminal child')
                time.sleep(0.01)
            actual = self.reap()
            self.require_absence()
            if any(x['result'] == 'denied_or_unknown' for x in self.signals):
                raise RuntimeError('signal denial remains unconfirmed despite later exit')
            return actual

    def receipt(self):
        return {'pid': self.pid, 'start_may_begin': self.start_may_begin,
                'authority': self.authority, 'retired': self.retired,
                'reaped': self.reaped, 'observed_exit': self.terminal,
                'native_wait_calls': self.wait_calls, 'native_wait_status': self.wait_status,
                'signals': list(self.signals), 'observation_calls': self.observations,
                'post_wait_absence': self.absence, 'unknown': self.unknown,
                'strongly_quarantined_until_native_reap': self.slot is not None}


def _text(data):
    # Same locale/default strict decoding and universal-newline behavior as the
    # previous Popen(text=True), rather than silently replacing invalid bytes.
    encoding = 'utf-8' if sys.flags.utf8_mode else locale.getpreferredencoding(False)
    with io.TextIOWrapper(io.BytesIO(data), encoding=encoding, errors='strict', newline=None) as stream:
        return stream.read()


def _receipt(path, value):
    data = (json.dumps(value, indent=2) + '\n').encode()
    if len(data) > 65536:
        raise RuntimeError('formal owner metadata exceeds64KiB')
    temporary = Path(str(path) + '.tmp')
    with temporary.open('xb') as stream:
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)


class Capture:
    """One retained regular descriptor; a pathname never supplies output bytes.

    Path identity is checked at each evidence/diagnostic cut. This is not a
    universal same-UID adversary/TOCTOU guarantee: the run directory is private.
    A swapped path or extra link refuses rather than reading/writing its target.
    """
    def __init__(self, path, exclusive=False):
        self.path = Path(path)
        flags = os.O_RDWR | os.O_CREAT | os.O_NONBLOCK | os.O_NOFOLLOW
        if exclusive:
            flags |= os.O_EXCL
        fd = os.open(self.path, flags, 0o600)
        self.stream = os.fdopen(fd, 'r+b', buffering=0)
        try:
            self.identity = os.fstat(fd)
            self.check()
            os.ftruncate(fd, 0)  # Only the checked opened object is truncated.
        except BaseException:
            self.stream.close()
            raise

    def fileno(self):
        return self.stream.fileno()

    def check(self):
        opened = os.fstat(self.fileno())
        current = self.path.lstat()  # Do not follow a replacement symlink.
        for value in (opened, current):
            if (not stat.S_ISREG(value.st_mode) or value.st_uid != os.geteuid() or
                    value.st_nlink != 1):
                raise RuntimeError('formal capture must remain owned single-link regular file')
            if (value.st_dev, value.st_ino) != (self.identity.st_dev, self.identity.st_ino):
                raise RuntimeError('formal capture pathname/descriptor identity changed')
        return opened

    def read(self):
        if self.check().st_size > MAX_OUTPUT_BYTES:
            raise RuntimeError('formal capture exceeds16MiB acceptance cap')
        chunks, offset = [], 0
        while offset <= MAX_OUTPUT_BYTES:
            block = os.pread(self.fileno(), min(65536, MAX_OUTPUT_BYTES + 1 - offset), offset)
            if not block:
                break
            chunks.append(block)
            offset += len(block)
        if offset > MAX_OUTPUT_BYTES:
            raise RuntimeError('formal capture exceeds16MiB acceptance cap')
        self.check()
        return b''.join(chunks)

    def append_diagnostics(self, text):
        data = text.encode('utf-8')
        size = self.check().st_size
        if size + len(data) > MAX_OUTPUT_BYTES:
            raise RuntimeError('formal capture diagnostics exceed16MiB acceptance cap')
        offset = 0
        while offset < len(data):
            written = os.pwrite(self.fileno(), data[offset:], size + offset)
            if written <= 0:
                raise RuntimeError('formal capture diagnostic write made no progress')
            offset += written
        self.check()

    def receipt(self):
        value = self.check()
        return {'device': value.st_dev, 'inode': value.st_ino,
                'uid': value.st_uid, 'links': value.st_nlink,
                'bytes': value.st_size, 'path_matches_open_descriptor': True}

    def close(self):
        self.stream.close()


def _first_failure(current, incoming):
    # First cancellation outranks an earlier ordinary failure; a second
    # cancellation must not replace the first INT/TERM. Preserve the prior cause.
    if current is None:
        return incoming
    if (isinstance(incoming, (KeyboardInterrupt, SystemExit)) and
            not isinstance(current, (KeyboardInterrupt, SystemExit))):
        incoming.formal_prior_failure = current
        return incoming
    return current


class Command:
    def __init__(self, run, cwd, backend=None):
        self.state = process_state()
        if self.state.active is not None:
            raise RuntimeError('formal native command is already active')
        self.latch = cancellation_latch(self.state)
        self.backend = native_backend() if backend is None else backend
        self.run, self.cwd = Path(run), Path(cwd)

    def command(self, argv, name, expected=0, env=None, timeout=180):
        if (Path(name).name != name or not name or type(expected) is not int or
                type(timeout) not in (int, float) or not math.isfinite(timeout) or
                not 0 < timeout <= MAX_TIMEOUT_SECONDS):
            raise ValueError('closed formal command name/deadline required')
        args = list(map(str, argv))
        if not args:
            raise ValueError('nonempty formal argv required')
        state = process_state()
        if state is not self.state or state.active is not None or any(x is not None for x in state.quarantine):
            raise RuntimeError('formal process ownership is active or unknown')
        self.latch.verify()
        if self.latch.received is not None:
            raise RuntimeError('formal cancellation already received')
        state.active = self
        owner = None
        log = self.run / (name + '.log')
        record = {'schema': 'arm.formal-native-owner/v1', 'argv': args,
                  'cwd': str(self.cwd), 'timeout_seconds': timeout,
                  'expected_exit': expected, 'output_cap_bytes': MAX_OUTPUT_BYTES,
                  'backend': self.backend.identity, 'status': 'incomplete',
                  'source': str(SOURCE), 'source_sha256': SOURCE_SHA256}
        failure = cleanup_failure = None
        capture = None
        output = ''
        actual = None
        start = time.monotonic()
        try:
            owner = Owned(self.backend, self.latch, state)
            # Regular capture avoids pipe EOF deciding when native Wait occurs.
            # O_NOFOLLOW prevents capture redirection through a stale symlink.
            capture = Capture(log)
            with self.latch.deferred():
                owner.start_may_begin = True
                process = subprocess.Popen(args, cwd=self.cwd, env=env,
                    stdout=capture, stderr=subprocess.STDOUT, start_new_session=True)
                owner.bind_fresh(process)
            while True:
                # Deadline precedes terminal acceptance; a late terminal
                # observation may be joined but never accepted as timely.
                if time.monotonic() - start >= timeout:
                    raise subprocess.TimeoutExpired(args, timeout)
                if os.fstat(capture.fileno()).st_size > MAX_OUTPUT_BYTES:
                    raise RuntimeError(name + ' exceeds16MiB formal output cap')
                if owner.observe() is not None:
                    actual = owner.reap()  # Normal completion sends no signals.
                    owner.require_absence()
                    break
                time.sleep(0.02)

        except BaseException as error:
            failure = error
            if owner is not None and owner.start_may_begin and owner.process is None:
                owner.unknown = 'native Start did not return; no PID authority'
            if owner is not None and owner.authority:
                try:
                    owner.cancel()
                except BaseException as cleanup:
                    cleanup_failure = cleanup
                    # Retain existing INT/TERM entrypoint behavior even when the
                    # first interrupt arrived during timeout cleanup.
                    failure = _first_failure(failure, cleanup)
            elif owner is not None and not owner.start_may_begin:
                owner.release_unstarted()
        finally:
            state.active = None
            try:
                try:
                    if capture is None:
                        raise RuntimeError('formal capture was not opened')
                    raw = capture.read()
                    record['capture'] = capture.receipt()
                except BaseException as capture_error:
                    record['output_refused'] = type(capture_error).__name__
                    failure = _first_failure(failure, capture_error)
                else:
                    output = _text(raw)
                    diagnostics = ''.join('\n[arm formal runner] ' + signal.Signals(x['signal']).name +
                        ' process-group ' + str(owner.pid) + ' failed (errno=' + str(x.get('errno')) +
                        '); cleanup unconfirmed\n' for x in (owner.signals if owner else []) if x['result'] == 'denied_or_unknown')
                    if diagnostics:
                        capture.append_diagnostics(diagnostics)
                        record['capture'] = capture.receipt()
                record.update(owner=owner.receipt() if owner else None, elapsed_seconds=time.monotonic() - start,
                              actual_exit=actual if actual is not None else (owner.process.returncode if owner is not None and isinstance(owner.process,subprocess.Popen) else None),
                              cancellation_received=self.latch.received,
                              cancellation_pending=self.latch.pending,
                              cleanup_error_type=type(cleanup_failure).__name__ if cleanup_failure else None,
                              cleanup_error=str(cleanup_failure)[:2048] if cleanup_failure else None)
                if failure is None and record['elapsed_seconds'] > timeout:
                    failure = subprocess.TimeoutExpired(args, timeout)
                if failure is None and actual != expected:
                    failure = RuntimeError(name + ' exited ' + str(actual) + '; see ' + str(log))
                record['status'] = 'passed_native_command_only' if failure is None else 'failed'
                if failure is not None:
                    record.update(error_type=type(failure).__name__, error=str(failure)[:2048])
                _receipt(self.run / (name + '-owner.json'), record)
            except BaseException as metadata_error:
                failure = _first_failure(failure, metadata_error)
                failure.formal_metadata_failure = metadata_error
            finally:
                if capture is not None:
                    try:
                        capture.close()
                    except BaseException as close_error:
                        failure = _first_failure(failure, close_error)
        if isinstance(failure, subprocess.TimeoutExpired):
            failure.output = output
        prior_failure = getattr(failure, 'formal_prior_failure', None)
        if isinstance(prior_failure, subprocess.TimeoutExpired):
            prior_failure.output = output
        if failure is not None:
            raise failure
        return output


class Fixture:
    """Harmless CLI-test child with a retained fresh native owner.

    Tests may signal only this still-retained child, never PIDs read from its
    metadata after Wait. Inner fixture descendants are independently bounded
    and checked by the tested runner, not adopted through this class.
    """
    def __init__(self, argv, log, cwd, env=None):
        self.state = process_state()
        if self.state.active is not None:
            raise RuntimeError('another formal native fixture is active')
        self.latch = cancellation_latch(self.state)
        backend = native_backend()
        self.owner = None
        self.state.active = self
        self.log = Path(log)
        self.capture = None
        try:
            self.owner = Owned(backend, self.latch, self.state)
            self.capture = Capture(self.log, exclusive=True)
            with self.latch.deferred():
                self.owner.start_may_begin = True
                value = subprocess.Popen(list(map(str, argv)), cwd=cwd, env=env,
                    stdout=self.capture, stderr=subprocess.STDOUT, start_new_session=True)
                self.owner.bind_fresh(value)
        except BaseException as error:
            failure = error
            if self.owner is not None and self.owner.start_may_begin and self.owner.process is None:
                self.owner.unknown = 'native Start did not return; no PID authority'
            try:
                if self.owner is not None and self.owner.authority:
                    self.owner.cancel()
                elif self.owner is not None and not self.owner.start_may_begin:
                    self.owner.release_unstarted()
            except BaseException as cleanup:
                failure = _first_failure(failure, cleanup)
                failure.formal_fixture_cleanup_failure = cleanup
            finally:
                self.state.active = None
                if self.capture is not None:
                    try:
                        self.capture.close()
                    except BaseException as close_error:
                        failure = _first_failure(failure, close_error)
                        failure.formal_fixture_capture_failure = close_error
            if failure is error:
                raise
            raise failure

    @property
    def pid(self):
        return self.owner.pid

    def terminal(self):
        if self.owner.reaped:
            return self.owner.process.returncode
        return self.owner.observe()

    def send(self, number):
        if number not in (signal.SIGINT, signal.SIGTERM):
            raise ValueError('closed test signal required')
        with self.latch.deferred():
            self.owner.check()
            os.kill(self.owner.pid, number)  # Fresh, still-unreaped anchor only.

    def output(self):
        if self.capture is None:
            raise RuntimeError('fixture capture is not retained')
        return _text(self.capture.read())

    def wait(self, timeout=10):
        until = time.monotonic() + timeout
        while self.owner.observe() is None:
            if time.monotonic() >= until:
                raise subprocess.TimeoutExpired(['retained fixture'], timeout, output=self.output())
            time.sleep(0.01)
        result = self.owner.reap()
        self.owner.require_absence()
        output = self.output()
        self.state.active = None
        _receipt(self.log.with_suffix('.owner.json'), {'scope':'retained outer fixture only',
            'owner':self.owner.receipt(),'capture':self.capture.receipt(),
            'actual_exit':result,'native_join_confirmed':True})
        return result, output

    def close(self):
        caller_failure = sys.exc_info()[1]
        # Ordinary cleanup failures preserve a live caller failure. The first
        # INT/TERM still outranks an earlier ordinary failure at every cleanup
        # cut; a later cancellation never replaces the first cancellation.
        failure = caller_failure
        cleanup_failure = None
        try:
            if self.owner.authority:
                self.owner.cancel()
            elif not self.owner.start_may_begin:
                self.owner.release_unstarted()
        except BaseException as error:
            cleanup_failure = error
            failure = _first_failure(failure, error)
            failure.formal_fixture_cleanup_failure = error
        finally:
            if self.capture is not None:
                try:
                    self.capture.close()
                except BaseException as error:
                    failure = _first_failure(failure, error)
                    failure.formal_fixture_capture_failure = error
            self.state.active = None
            try:
                _receipt(self.log.with_suffix('.owner.json'), {'scope':'retained outer fixture only',
                    'owner':self.owner.receipt(),
                    'actual_exit':self.owner.process.returncode if isinstance(self.owner.process,subprocess.Popen) else None,
                    'native_join_confirmed':cleanup_failure is None and
                    self.owner.reaped and self.owner.absence=={'pid':'absent','group':'absent'} and
                    self.owner.unknown is None,'cleanup_error_type':type(cleanup_failure).__name__ if cleanup_failure else None,
                    'cleanup_error':str(cleanup_failure)[:2048] if cleanup_failure else None,
                    'caller_failure_type':type(caller_failure).__name__ if caller_failure else None,
                    'selected_failure_type':type(failure).__name__ if failure else None})
            except BaseException as error:
                failure = _first_failure(failure, error)
                failure.formal_fixture_metadata_failure = error
        if failure is not None and failure is not caller_failure:
            raise failure
