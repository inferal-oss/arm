"""Low-cost ownership regressions; no ARM build, backend, or browser is started."""
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import pty
import signal
import struct
import subprocess
import sys
import tempfile
import time
import termios
import unittest
from unittest.mock import Mock, patch


spec = importlib.util.spec_from_file_location('dashboard_capture', Path(__file__).with_name('capture.py'))
capture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(capture)


class TerminalGeometryTests(unittest.TestCase):
    def test_actual_pty_size_matches_private_renderer_grid(self):
        master, slave = pty.openpty()
        try:
            grid = capture.terminal_geometry(slave)
            rows, cols, _, _ = struct.unpack('HHHH', fcntl.ioctl(slave, termios.TIOCGWINSZ, b'\0' * 8))
            renderer_grid = json.loads(json.dumps({'terminalGeometry': grid}))['terminalGeometry']
            self.assertEqual((rows, cols), (renderer_grid['rows'], renderer_grid['cols']))
        finally:
            os.close(master)
            os.close(slave)


class CommandOwnershipTests(unittest.TestCase):
    def test_normal_success_keeps_exit_and_output(self):
        result = capture.run([sys.executable, '-c', 'print("owned success")'], env=dict(os.environ))
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout, b'owned success\n')
        self.assertEqual(result.stderr, b'')

    def test_exited_parent_does_not_leave_pipe_owner_running(self):
        with tempfile.TemporaryDirectory(prefix='arm-capture-group-test-') as directory:
            ready, finished = [Path(directory) / name for name in ('ready', 'finished')]
            child = (
                'from pathlib import Path; import time; '
                f'Path({str(ready)!r}).write_text("ready"); '
                f'time.sleep(.6); Path({str(finished)!r}).write_text("continued")'
            )
            parent = (
                'from pathlib import Path; import subprocess, sys, time; '
                f'subprocess.Popen([sys.executable, "-c", {child!r}]); '
                f'path=Path({str(ready)!r})\n'
                'while not path.exists(): time.sleep(.005)\n'
                # Deliberately exit while the original group's child owns both pipes.
            )
            with patch.object(capture, 'PROCESS_STOP_TIMEOUT', .1):
                with self.assertRaises(subprocess.TimeoutExpired):
                    capture.run([sys.executable, '-c', parent], env=dict(os.environ), timeout=.2)
            self.assertTrue(ready.exists())
            # The old poll()-gated cleanup returns without stopping the child;
            # its bounded .6-second action would then create this marker.
            time.sleep(.7)
            self.assertFalse(finished.exists())

    def test_live_timeout_escalates_and_joins_original_parent(self):
        with tempfile.TemporaryDirectory(prefix='arm-capture-timeout-test-') as directory:
            pidfile = Path(directory) / 'pid'
            command = (
                'from pathlib import Path; import os, signal, time; '
                'signal.signal(signal.SIGTERM, signal.SIG_IGN); '
                f'Path({str(pidfile)!r}).write_text(str(os.getpid())); time.sleep(2)'
            )
            with patch.object(capture, 'PROCESS_STOP_TIMEOUT', .1):
                with self.assertRaises(subprocess.TimeoutExpired):
                    capture.run([sys.executable, '-c', command], env=dict(os.environ), timeout=.2)
            pid = int(pidfile.read_text())
            with self.assertRaises(ProcessLookupError):
                os.kill(pid, 0)

    def test_missing_original_group_is_tolerated(self):
        process = Mock(pid=123, stdout=None, stderr=None)
        process.communicate.return_value = (b'', b'')
        with patch.object(capture.os, 'killpg', side_effect=ProcessLookupError) as kill:
            capture.stop_command_group(process)
        kill.assert_called_once_with(process.pid, signal.SIGTERM)
        process.communicate.assert_called_once_with(timeout=capture.PROCESS_STOP_TIMEOUT)

    def test_unjoined_pipe_is_closed_with_bounded_parent_wait(self):
        process = Mock(pid=123)
        process.communicate.side_effect = subprocess.TimeoutExpired('owned fixture', .1)
        with patch.object(capture.os, 'killpg') as kill, patch.object(capture.time, 'sleep'):
            capture.stop_command_group(process)
        self.assertEqual(kill.call_args_list[0].args, (process.pid, signal.SIGTERM))
        self.assertEqual(kill.call_args_list[1].args, (process.pid, signal.SIGKILL))
        process.stdout.close.assert_called_once_with()
        process.stderr.close.assert_called_once_with()
        process.wait.assert_called_once_with(timeout=capture.PROCESS_STOP_TIMEOUT)


if __name__ == '__main__':
    unittest.main()
