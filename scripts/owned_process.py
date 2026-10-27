"""Bounded POSIX command groups with exclusive, nonreaping ownership.

Build helpers use this single-threaded controller instead of Popen.communicate:
waitid(WNOWAIT) retains the original session leader until signalling authority
is closed. Only then may waitpid reap it. An external reaper, unsupported ABI,
or unjoined detached pipe holder is an explicit failure, never exit-zero proof.
"""
from contextlib import contextmanager
import ctypes
import errno
import os
import platform
import selectors
import shutil
import signal
import subprocess
import sys
import threading
import time


class OwnershipError(RuntimeError):
    pass


MAX_OUTPUT_BYTES = 32 * 1024 * 1024
WAITID_RETRIES = 8

MANAGED_SIGNALS = {signal.SIGINT, signal.SIGTERM, signal.SIGALRM, signal.SIGCHLD}
CATCHABLE_SIGNALS = signal.valid_signals() - {signal.SIGKILL, signal.SIGSTOP}


def preflight():
    # Refuse closed stdio before selectors/pipes can occupy descriptors 0-2.
    # Otherwise a later DUP2 followed by CLOSE could silently discard output.
    for descriptor in (0, 1, 2):
        try:
            os.fstat(descriptor)
        except OSError as error:
            if error.errno == errno.EBADF:
                raise OwnershipError('owned_command_stdio_unavailable') from None
            raise
    if (threading.current_thread() is not threading.main_thread() or threading.active_count() != 1 or
            signal.getsignal(signal.SIGCHLD) != signal.SIG_DFL or
            not hasattr(os, 'posix_spawn') or not hasattr(signal, 'pthread_sigmask')):
        raise OwnershipError('owned_command_controller_unsupported')


@contextmanager
def deferred_signals():
    # Freeze callbacks while taking the handler snapshot too: a callback must
    # not install another reaper between handler discovery and mask update.
    previous = signal.pthread_sigmask(signal.SIG_BLOCK, CATCHABLE_SIGNALS)
    try:
        callbacks = {sig for sig in CATCHABLE_SIGNALS if callable(signal.getsignal(sig))}
        signal.pthread_sigmask(signal.SIG_SETMASK, previous | MANAGED_SIGNALS | callbacks)
        yield previous
    finally:
        signal.pthread_sigmask(signal.SIG_SETMASK, previous)


class ExitObserver:
    def __init__(self):
        # Gated native layouts: XNU bsd/sys/signal.h and Linux UAPI
        # asm-generic/siginfo.h. The aligned opaque storage exceeds both ABIs;
        # no guessed nonzero bytes or Popen returncode stand in for identity.
        if (sys.platform not in ('darwin', 'linux') or ctypes.sizeof(ctypes.c_void_p) != 8 or
                ctypes.sizeof(ctypes.c_int) != 4 or platform.machine().lower() not in
                ('x86_64', 'aarch64', 'arm64')):
            raise OwnershipError('owned_command_waitid_abi_unsupported')
        required = ('P_PID', 'WEXITED', 'WNOHANG', 'WNOWAIT', 'CLD_EXITED', 'CLD_KILLED', 'CLD_DUMPED')
        if not all(hasattr(os, name) for name in required):
            raise OwnershipError('owned_command_waitid_unsupported')
        self.pid_offset = 12 if sys.platform == 'darwin' else 16
        self.libc = ctypes.CDLL(None, use_errno=True)
        try:
            self.waitid = self.libc.waitid
        except AttributeError:
            raise OwnershipError('owned_command_waitid_unsupported') from None
        self.waitid.argtypes = [ctypes.c_int, ctypes.c_uint, ctypes.c_void_p, ctypes.c_int]
        self.waitid.restype = ctypes.c_int
        probe = (ctypes.c_longlong * 32)()
        result = self.waitid(os.P_PID, os.getpid(), ctypes.byref(probe), os.WEXITED | os.WNOHANG | os.WNOWAIT)
        if result != -1 or ctypes.get_errno() != errno.ECHILD:
            raise OwnershipError('owned_command_waitid_kernel_unsupported')

    def exited(self, pid):
        for _attempt in range(WAITID_RETRIES):
            storage = (ctypes.c_longlong * 32)()  # Fresh even after EINTR.
            if self.waitid(os.P_PID, pid, ctypes.byref(storage), os.WEXITED | os.WNOHANG | os.WNOWAIT) == 0:
                break
            code = ctypes.get_errno()
            if code == errno.EINTR:
                continue
            if code == errno.ECHILD:
                raise OwnershipError('owned_command_leader_no_longer_waitable')
            raise OwnershipError('owned_command_waitid_failed')
        else:
            raise OwnershipError('owned_command_waitid_interrupted')
        signo = ctypes.c_int.from_buffer(storage, 0).value
        observed_pid = ctypes.c_int.from_buffer(storage, self.pid_offset).value
        if signo == 0 and observed_pid == 0:
            return False
        code = ctypes.c_int.from_buffer(storage, 8).value
        if signo != signal.SIGCHLD or observed_pid != pid or code not in (os.CLD_EXITED, os.CLD_KILLED, os.CLD_DUMPED):
            raise OwnershipError('owned_command_waitid_identity_mismatch')
        return True


class OwnedCommand:
    def __init__(self, arguments, *, env=None, cwd=None, capture_stderr=True, stop_timeout=5):
        preflight()
        self.observer = ExitObserver()  # Refuse unsupported hosts before spawn.
        self.arguments, self.env, self.cwd = arguments, env, cwd
        self.capture_stderr, self.stop_timeout = capture_stderr, stop_timeout
        self.pid = None
        self.authority = False
        self.lost = False
        self.loss_reason = None
        self.returncode = None
        self.discard_output = False
        self.signal_incomplete = False
        self.selector_closed = False
        self.selector = selectors.DefaultSelector()
        self.buffers = {'stdout': bytearray(), 'stderr': bytearray()}
        self.read_fds = []

    def start(self):
        # posix_spawn restores the child's original mask atomically with exec.
        # Registration finishes before a pending managed signal can run here.
        with deferred_signals() as previous:
            preflight()  # Recheck if a caller changed stdio after construction.
            actions, write_fds = [], []
            try:
                for name, target in [('stdout', 1)] + ([('stderr', 2)] if self.capture_stderr else []):
                    reader, writer = os.pipe()
                    self.read_fds.append(reader)
                    write_fds.append(writer)
                    self.selector.register(reader, selectors.EVENT_READ, name)
                    actions.extend([(os.POSIX_SPAWN_DUP2, writer, target),
                                    (os.POSIX_SPAWN_CLOSE, reader), (os.POSIX_SPAWN_CLOSE, writer)])
                directory = os.open('.', os.O_RDONLY) if self.cwd else None
                try:
                    if self.cwd:
                        os.chdir(self.cwd)
                    environment = os.environ if self.env is None else self.env
                    executable = shutil.which(self.arguments[0], path=os.pathsep.join(os.get_exec_path(environment)))
                    if executable is None:
                        raise FileNotFoundError(errno.ENOENT, 'owned command executable not found', self.arguments[0])
                    self.pid = os.posix_spawn(executable, self.arguments, environment,
                                              file_actions=actions, setsid=True, setsigmask=previous,
                                              setsigdef=tuple(getattr(signal, name) for name in ('SIGPIPE', 'SIGXFZ', 'SIGXFSZ') if hasattr(signal, name)))
                    self.authority = True
                finally:
                    if directory is not None:
                        try:
                            os.fchdir(directory)
                        finally:
                            os.close(directory)
            finally:
                for descriptor in write_fds:
                    os.close(descriptor)

    def _exited(self):
        if self.lost:
            raise OwnershipError(self.loss_reason)
        try:
            if signal.getsignal(signal.SIGCHLD) != signal.SIG_DFL:
                raise OwnershipError('owned_command_sigchld_policy_changed')
            return self.observer.exited(self.pid)
        except OwnershipError as error:
            self.authority = False
            self.lost = True
            self.loss_reason = str(error)
            raise

    def _drain(self, timeout):
        deadline = time.monotonic() + timeout
        while True:
            exited = self._exited()
            if exited and not self.selector.get_map():
                return
            left = deadline - time.monotonic()
            if left <= 0:
                raise subprocess.TimeoutExpired(self.arguments, timeout)
            for key, _ in self.selector.select(min(left, .05)):
                chunk = os.read(key.fd, 65536)
                if chunk:
                    if not self.discard_output:
                        if sum(map(len, self.buffers.values())) + len(chunk) > MAX_OUTPUT_BYTES:
                            raise OwnershipError('owned_command_output_limit')
                        self.buffers[key.data].extend(chunk)
                else:
                    self._close_fd(key.fd)

    def _signal(self, sig):
        # Check exact unreaped child ownership and signal in one deferred region.
        # Exit between the check and killpg is safe: no reap can reuse its ID.
        with deferred_signals():
            if not self.authority:
                return
            self._exited()
            try:
                os.killpg(self.pid, sig)
            except ProcessLookupError:
                pass
            except PermissionError:
                # Darwin may deny a signal to an all-zombie group. Do not
                # infer its descendants' closure from that denial.
                self.signal_incomplete = True

    def _reap(self):
        with deferred_signals():
            if self.lost:
                raise OwnershipError(self.loss_reason)
            if not self._exited():
                self.authority = False
                raise OwnershipError('owned_command_original_wait_incomplete')
            # This is the one-way boundary, before the FIRST kernel reap.
            self.authority = False
            returned, status = os.waitpid(self.pid, os.WNOHANG)
            if returned != self.pid:
                raise OwnershipError('owned_command_original_wait_mismatch')
            self.returncode = os.waitstatus_to_exitcode(status)

    def _close_fd(self, descriptor):
        with deferred_signals():
            if descriptor not in self.read_fds:
                return
            # Retire descriptor ownership before close; no interrupt can leave
            # a stale fd number to close after a later allocation reuses it.
            self.read_fds.remove(descriptor)
            try:
                try:
                    self.selector.unregister(descriptor)
                except (KeyError, ValueError):
                    pass
            finally:
                try:
                    os.close(descriptor)
                except OSError as error:
                    if error.errno != errno.EBADF:
                        raise

    def close_pipes(self):
        for descriptor in list(self.read_fds):
            self._close_fd(descriptor)
        with deferred_signals():
            if not self.selector_closed:
                self.selector_closed = True
                self.selector.close()

    def cancel(self):
        # A second managed interrupt is deferred throughout bounded cleanup.
        with deferred_signals():
            try:
                if not self.authority:
                    if self.lost:
                        raise OwnershipError(self.loss_reason)
                    if self.pid is not None and self.returncode is None:
                        # An exception may land after authority retirement but
                        # before waitpid. Retry only a proven nonblocking reap;
                        # ECHILD never becomes an invented exit-zero status.
                        try:
                            self._reap()
                        except OwnershipError:
                            raise OwnershipError('owned_command_original_wait_unconfirmed') from None
                    return
                self.discard_output = True
                self._signal(signal.SIGTERM)
                joined = False
                try:
                    self._drain(self.stop_timeout)
                    joined = True
                except subprocess.TimeoutExpired:
                    self._signal(signal.SIGKILL)
                    try:
                        self._drain(self.stop_timeout)
                        joined = True
                    except subprocess.TimeoutExpired:
                        pass
                self._reap()
                if not joined:
                    raise OwnershipError('owned_command_pipe_join_incomplete')
                if self.signal_incomplete:
                    raise OwnershipError('owned_command_group_signal_incomplete')
            finally:
                self.close_pipes()

    def result(self):
        return subprocess.CompletedProcess(self.arguments, self.returncode, bytes(self.buffers['stdout']),
                                           bytes(self.buffers['stderr']) if self.capture_stderr else None)


def run_owned_command(arguments, *, env=None, cwd=None, capture_stderr=True, timeout=20, stop_timeout=5):
    owner = OwnedCommand(arguments, env=env, cwd=cwd, capture_stderr=capture_stderr, stop_timeout=stop_timeout)
    try:
        owner.start()
        owner._drain(timeout)
        owner._reap()
        return owner.result()
    except BaseException:
        owner.cancel()
        raise
    finally:
        owner.close_pipes()
