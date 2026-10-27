"""Native, bounded child-ownership cases; no ARM binary or backend is used."""
import ctypes
import errno
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import Mock, patch

import owned_process as owned


def load_wrapper(path, name):
    sys.path.insert(0, str(path.parent))
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class OwnershipTests(unittest.TestCase):
    def test_native_wnowait_retains_exact_child_until_retirement(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            child = ('from pathlib import Path;import sys,time\n'
                     'root=Path(sys.argv[1]);(root/"ready").touch();end=time.monotonic()+5\n'
                     'while not (root/"release").exists():\n'
                     ' if time.monotonic()>end:raise SystemExit(3)\n'
                     ' time.sleep(.005)\n')
            owner = owned.OwnedCommand([sys.executable, '-c', child, directory])
            try:
                owner.start()
                deadline = time.monotonic() + 2
                while not (root / 'ready').exists() and time.monotonic() < deadline:
                    time.sleep(.005)
                self.assertTrue((root / 'ready').exists(), 'original child did not reach its live barrier')
                self.assertFalse(owner._exited())
                (root / 'release').touch()
                deadline = time.monotonic() + 2
                while not owner._exited() and time.monotonic() < deadline:
                    time.sleep(.005)
                self.assertTrue(owner._exited(), 'released original child did not exit')
                self.assertTrue(owner._exited())  # The first native observation did not reap.
                owner._drain(1)
                owner._reap()
                self.assertFalse(owner.authority)
                self.assertEqual(owner.returncode, 0)
                with patch.object(owned.os, 'killpg', side_effect=AssertionError('retired signal')):
                    owner.cancel()
            finally:
                try:
                    owner.cancel()
                finally:
                    owner.close_pipes()

    def test_callback_reaper_cannot_run_between_exit_observation_and_group_signal(self):
        owner = owned.OwnedCommand([sys.executable, '-c', 'pass'])
        events = []
        original_handler = signal.getsignal(signal.SIGUSR1)
        try:
            owner.start()
            original_observer = owner.observer.exited
            deadline = time.monotonic() + 2
            while not original_observer(owner.pid) and time.monotonic() < deadline:
                time.sleep(.005)
            self.assertTrue(original_observer(owner.pid))
            def reaper(*_args):
                returned, _status = os.waitpid(owner.pid, os.WNOHANG)
                self.assertEqual(returned, owner.pid)
                events.append('callback reaped original')
            signal.signal(signal.SIGUSR1, reaper)
            def observe_and_deliver(pid):
                exited = original_observer(pid)
                os.kill(os.getpid(), signal.SIGUSR1)
                return exited
            def verify_original_before_signal(pid, _sig):
                # A real WNOWAIT observation must still succeed at the exact
                # signal boundary. No foreign/stale numeric group is signalled.
                self.assertTrue(original_observer(pid))
                events.append('original still waitable at group signal')
            owner.observer.exited = observe_and_deliver
            with patch.object(owned.os, 'killpg', side_effect=verify_original_before_signal):
                owner._signal(signal.SIGTERM)
            self.assertEqual(events, ['original still waitable at group signal', 'callback reaped original'])
            with patch.object(owned.os, 'killpg', side_effect=AssertionError('post-callback stale signal')):
                with self.assertRaisesRegex(owned.OwnershipError, 'no_longer_waitable'):
                    owner.cancel()
        finally:
            signal.signal(signal.SIGUSR1, original_handler)
            owner.close_pipes()

    def test_changed_sigchld_policy_closes_group_authority_before_signalling(self):
        owner = owned.OwnedCommand([sys.executable, '-c', 'pass'])
        original_handler = signal.getsignal(signal.SIGCHLD)
        try:
            owner.start()
            os.waitpid(owner.pid, 0)
            signal.signal(signal.SIGCHLD, signal.SIG_IGN)
            with patch.object(owned.os, 'killpg', side_effect=AssertionError('unverified group signal')):
                with self.assertRaisesRegex(owned.OwnershipError, 'sigchld_policy_changed'):
                    owner.cancel()
            self.assertFalse(owner.authority)
        finally:
            signal.signal(signal.SIGCHLD, original_handler)
            owner.close_pipes()

    def test_startup_interrupt_registers_original_child_before_unmask(self):
        spawn = owned.os.posix_spawn
        pids = []
        def spawn_then_interrupt(*args, **kwargs):
            pid = spawn(*args, **kwargs)
            pids.append(pid)
            os.kill(os.getpid(), signal.SIGTERM)
            return pid
        original_handler = signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(RuntimeError('startup interruption')))
        try:
            with patch.object(owned.os, 'posix_spawn', side_effect=spawn_then_interrupt):
                with self.assertRaisesRegex(RuntimeError, 'startup interruption'):
                    owned.run_owned_command([sys.executable, '-c', 'import time;time.sleep(2)'], stop_timeout=.1)
            self.assertEqual(len(pids), 1)
            with self.assertRaises(ChildProcessError):
                os.waitpid(pids[0], os.WNOHANG)
        finally:
            signal.signal(signal.SIGTERM, original_handler)

    def test_exec_child_restores_prior_mask_and_default_pipe_disposition(self):
        code = 'import signal,json;print(json.dumps(sorted(map(int,signal.pthread_sigmask(signal.SIG_BLOCK,set())))))'
        result = owned.run_owned_command([sys.executable, '-c', code])
        self.assertEqual(json.loads(result.stdout), sorted(map(int, signal.pthread_sigmask(signal.SIG_BLOCK, set()))))
        result = owned.run_owned_command(['/bin/sh', '-c', 'kill -PIPE $$; exit 42'])
        self.assertEqual(result.returncode, -signal.SIGPIPE)

    def test_closed_stdio_is_refused_without_launch_in_an_isolated_driver(self):
        with tempfile.TemporaryDirectory(prefix='arm-owned-stdio-test-') as directory:
            launched = Path(directory) / 'launched'
            child = f'from pathlib import Path;Path({str(launched)!r}).write_text("launched");print("owned stdout")'
            driver = (
                'import os,sys;'
                f'sys.path.insert(0,{str(Path(__file__).resolve().parent)!r});'
                'import owned_process;os.close(0);os.close(1)\n'
                'try:owned_process.run_owned_command([sys.executable,"-c",' + repr(child) + '])\n'
                'except owned_process.OwnershipError as error:os.write(2,str(error).encode())\n'
                'else:raise AssertionError("closed stdio accepted")'
            )
            result = subprocess.run([sys.executable, '-c', driver], capture_output=True, timeout=3)
            self.assertEqual(result.returncode, 0)
            self.assertEqual(result.stderr, b'owned_command_stdio_unavailable')
            self.assertFalse(launched.exists())

    def test_explicit_path_selects_the_requested_executable(self):
        with tempfile.TemporaryDirectory(prefix='arm-owned-path-test-') as directory:
            tool = Path(directory) / 'owned-environment-tool'
            tool.write_text('#!/bin/sh\nprintf "explicit path\\n"\n')
            tool.chmod(0o700)
            result = owned.run_owned_command([tool.name], env={'PATH': directory})
            self.assertEqual(result.stdout, b'explicit path\n')

    def test_kernel_without_wnowait_is_refused_before_spawn(self):
        def unsupported(*_args):
            ctypes.set_errno(errno.EINVAL)
            return -1
        library = Mock(waitid=unsupported)
        with patch.object(owned.ctypes, 'CDLL', return_value=library), \
                patch.object(owned.os, 'posix_spawn', side_effect=AssertionError('unsupported launch')):
            with self.assertRaisesRegex(owned.OwnershipError, 'kernel_unsupported'):
                owned.run_owned_command([sys.executable, '-c', 'pass'])

    def test_waitid_retry_uses_fresh_zeroed_siginfo(self):
        observer, calls = owned.ExitObserver(), []
        def interrupted(_kind, _pid, pointer, _flags):
            calls.append(None)
            if len(calls) == 1:
                ctypes.c_int.from_address(ctypes.addressof(pointer._obj)).value = signal.SIGCHLD
                ctypes.set_errno(errno.EINTR)
                return -1
            self.assertEqual(ctypes.string_at(pointer, 256), b'\0' * 256)
            return 0
        observer.waitid = interrupted
        self.assertFalse(observer.exited(os.getpid()))
        self.assertEqual(len(calls), 2)

    def test_persistent_waitid_interrupts_have_a_finite_observation_bound(self):
        observer, calls = owned.ExitObserver(), []
        def interrupted(*_args):
            calls.append(None)
            ctypes.set_errno(errno.EINTR)
            return -1
        observer.waitid = interrupted
        with self.assertRaisesRegex(owned.OwnershipError, 'waitid_interrupted'):
            observer.exited(os.getpid())
        self.assertEqual(len(calls), owned.WAITID_RETRIES)

    def test_fd_retirement_does_not_repeat_close_after_kernel_close_interrupt(self):
        owner = owned.OwnedCommand([sys.executable, '-c', 'pass'])
        reader, writer = os.pipe()
        owner.read_fds.append(reader)
        owner.selector.register(reader, owned.selectors.EVENT_READ, 'stdout')
        close, calls = owned.os.close, []
        def close_then_interrupt(descriptor):
            close(descriptor)
            calls.append(descriptor)
            if descriptor == reader:
                raise KeyboardInterrupt('after descriptor close')
        try:
            with patch.object(owned.os, 'close', side_effect=close_then_interrupt):
                with self.assertRaises(KeyboardInterrupt):
                    owner._close_fd(reader)
                owner.close_pipes()
            self.assertEqual(calls.count(reader), 1)
        finally:
            close(writer)
            owner.close_pipes()

    def test_explicit_empty_environment_does_not_inherit_ambient_values(self):
        with patch.dict(os.environ, {'ARM_OWNED_AMBIENT_SENTINEL': 'must-not-inherit'}):
            result = owned.run_owned_command([sys.executable, '-c', 'import os;print(os.environ.get("ARM_OWNED_AMBIENT_SENTINEL","absent"))'], env={})
        self.assertEqual(result.stdout, b'absent\n')

    def test_pending_reap_after_interrupt_retries_without_signalling(self):
        real_wait = owned.os.waitpid
        calls = []
        def interrupt_first(*args):
            calls.append(args)
            if len(calls) == 1:
                raise KeyboardInterrupt('before actual reap')
            return real_wait(*args)
        with patch.object(owned.os, 'waitpid', side_effect=interrupt_first), \
                patch.object(owned.os, 'killpg', side_effect=AssertionError('closed-authority signal')):
            with self.assertRaises(KeyboardInterrupt):
                owned.run_owned_command([sys.executable, '-c', 'pass'])
        self.assertEqual(len(calls), 2)
        with self.assertRaises(ChildProcessError):
            real_wait(calls[0][0], os.WNOHANG)

    def test_output_limit_stops_and_joins_original_command(self):
        with patch.object(owned, 'MAX_OUTPUT_BYTES', 1024):
            with self.assertRaisesRegex(owned.OwnershipError, 'output_limit'):
                owned.run_owned_command([sys.executable, '-c', 'import os,time;os.write(1,b"x"*4096);time.sleep(1)'], stop_timeout=.1)

    def test_registration_failure_closes_unregistered_pipe_descriptors(self):
        pipe, descriptors = owned.os.pipe, []
        def tracked_pipe():
            pair = pipe()
            descriptors.extend(pair)
            return pair
        with patch.object(owned.os, 'pipe', side_effect=tracked_pipe), \
                patch.object(owned.selectors.KqueueSelector, 'register', side_effect=RuntimeError('registration refused')) if sys.platform == 'darwin' else patch.object(owned.selectors.EpollSelector, 'register', side_effect=RuntimeError('registration refused')):
            with self.assertRaisesRegex(RuntimeError, 'registration refused'):
                owned.run_owned_command([sys.executable, '-c', 'pass'])
        for descriptor in descriptors:
            with self.assertRaises(OSError):
                fcntl.fcntl(descriptor, fcntl.F_GETFD)

    def test_spawn_failure_closes_original_launch_pipes(self):
        pipe, descriptors = owned.os.pipe, []
        def tracked_pipe():
            pair = pipe()
            descriptors.extend(pair)
            return pair
        with patch.object(owned.os, 'pipe', side_effect=tracked_pipe):
            with self.assertRaises(FileNotFoundError):
                owned.run_owned_command(['/arm-command-that-does-not-exist'])
        for descriptor in descriptors:
            with self.assertRaises(OSError):
                fcntl.fcntl(descriptor, fcntl.F_GETFD)

    def test_second_interrupt_waits_for_bounded_cleanup_and_original_reap(self):
        original_signal, original_drain = owned.OwnedCommand._signal, owned.OwnedCommand._drain
        pids, phases = [], []
        with tempfile.TemporaryDirectory(prefix='arm-owned-interrupt-test-') as directory:
            ready = Path(directory) / 'ready'
            child = ('from pathlib import Path;import signal,time;'
                     'signal.signal(signal.SIGTERM,signal.SIG_IGN);'
                     f'Path({str(ready)!r}).write_text("ready");time.sleep(2)')
            armed = False
            def expire_only_after_child_is_ready(owner, timeout):
                nonlocal armed
                if not armed:
                    armed = True
                    deadline = time.monotonic() + 2
                    while not ready.exists() and time.monotonic() < deadline:
                        time.sleep(.005)
                    self.assertTrue(ready.exists())
                    raise subprocess.TimeoutExpired(owner.arguments, timeout)
                return original_drain(owner, timeout)
            def signal_and_interrupt(owner, sig):
                pids.append(owner.pid)
                phases.append(sig)
                result = original_signal(owner, sig)
                if sig == signal.SIGTERM:
                    os.kill(os.getpid(), signal.SIGTERM)
                return result
            original_handler = signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(RuntimeError('second interruption')))
            try:
                with patch.object(owned.OwnedCommand, '_signal', signal_and_interrupt), \
                        patch.object(owned.OwnedCommand, '_drain', expire_only_after_child_is_ready):
                    with self.assertRaisesRegex(RuntimeError, 'second interruption'):
                        owned.run_owned_command([sys.executable, '-c', child], timeout=1, stop_timeout=.1)
                self.assertEqual(phases, [signal.SIGTERM, signal.SIGKILL])
                with self.assertRaises(ChildProcessError):
                    os.waitpid(pids[0], os.WNOHANG)
            finally:
                signal.signal(signal.SIGTERM, original_handler)

    def test_external_pipe_owner_is_not_signalled_and_join_failure_is_explicit_in_both_helpers(self):
        root = Path(__file__).resolve().parent
        wrappers = [load_wrapper(root / 'dashboard-screenshots/capture.py', 'capture_owned_test'),
                    load_wrapper(root / 'release/build.py', 'release_owned_test')]
        for wrapper in wrappers:
            with self.subTest(wrapper=wrapper.__name__), tempfile.TemporaryDirectory(prefix='arm-owned-pipe-test-') as directory:
                ready, finished = [Path(directory) / name for name in ('ready', 'finished')]
                child = ('from pathlib import Path;import time;'
                         f'Path({str(ready)!r}).write_text("ready");'
                         f'time.sleep(.7);Path({str(finished)!r}).write_text("finished")')
                parent = ('from pathlib import Path;import subprocess,sys,time;'
                          f'subprocess.Popen([sys.executable,"-c",{child!r}],start_new_session=True);'
                          f'path=Path({str(ready)!r})\nwhile not path.exists():time.sleep(.005)')
                calls, kill = [], owned.os.killpg
                def observe_kill(pid, sig):
                    calls.append(pid)
                    return kill(pid, sig)
                started = time.monotonic()
                error = RuntimeError if wrapper.__name__.startswith('release') else wrapper.CaptureError
                with patch.object(wrapper, 'PROCESS_STOP_TIMEOUT', .05), patch.object(owned.os, 'killpg', side_effect=observe_kill):
                    with self.assertRaisesRegex(error, 'pipe_join_incomplete'):
                        if wrapper.__name__.startswith('release'):
                            wrapper.run([sys.executable, '-c', parent], timeout=.1)
                        else:
                            wrapper.run([sys.executable, '-c', parent], env=dict(os.environ), timeout=.1)
                self.assertLess(time.monotonic() - started, .6)
                self.assertEqual(len(set(calls)), 1)  # Only the retained original group.
                deadline = time.monotonic() + 2
                while not finished.exists() and time.monotonic() < deadline:
                    time.sleep(.01)
                self.assertTrue(finished.exists(), 'detached original fixture was signalled')


if __name__ == '__main__':
    unittest.main()
