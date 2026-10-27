#!/usr/bin/env python3
"""Small, controlled bound checks for relay-loss fixture diagnostics."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('routed_forward_loss', Path(__file__).with_name('test-routed-forward-loss.py'))
loss = importlib.util.module_from_spec(spec)
spec.loader.exec_module(loss)


class DiagnosticBounds(unittest.TestCase):
    def test_tail_reads_only_its_fixed_window(self):
        with tempfile.TemporaryDirectory(prefix='arm-forward-tail-') as root:
            path = Path(root) / 'stderr'
            path.write_bytes(b'a' * (loss.MAX_CLIENT_OUTPUT - 4) + b'tail')
            original_open = Path.open
            reads = []

            class CheckedStream:
                def __init__(self, stream):
                    self.stream = stream

                def __enter__(self):
                    return self

                def __exit__(self, *_):
                    self.stream.close()

                def fileno(self):
                    return self.stream.fileno()

                def seek(self, offset):
                    return self.stream.seek(offset)

                def read(self, length=-1):
                    self.assert_bounded(length)
                    reads.append(length)
                    return self.stream.read(length)

                @staticmethod
                def assert_bounded(length):
                    assert 0 <= length <= loss.DIAGNOSTIC_TAIL, length

            def checked_open(path, *args, **kwargs):
                return CheckedStream(original_open(path, *args, **kwargs))

            with patch.object(Path, 'open', checked_open), \
                    patch.object(Path, 'read_bytes', side_effect=AssertionError('unbounded read')):
                self.assertEqual(loss.bounded_client_tail(path), b'a' * (loss.DIAGNOSTIC_TAIL - 4) + b'tail')
            self.assertEqual(reads, [loss.DIAGNOSTIC_TAIL])

    def test_live_output_and_tail_refuse_oversize(self):
        with tempfile.TemporaryDirectory(prefix='arm-forward-tail-') as root:
            stdout, stderr = Path(root) / 'stdout', Path(root) / 'stderr'
            stdout.write_bytes(b'x' * (loss.MAX_CLIENT_OUTPUT + 1))
            stderr.write_bytes(b'')
            with patch.object(Path, 'read_bytes', side_effect=AssertionError('unbounded read')):
                with self.assertRaisesRegex(AssertionError, 'exceeded fixture bound'):
                    loss.check_client_output_bounds(stdout, stderr)
                with self.assertRaisesRegex(AssertionError, 'exceeded fixture bound'):
                    loss.bounded_client_tail(stdout)


if __name__ == '__main__':
    unittest.main()
