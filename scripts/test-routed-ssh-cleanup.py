#!/usr/bin/env python3
"""Fast acceptance-harness cleanup and environment-label regressions."""
import contextlib
import hashlib
import importlib.util
import io
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('routed_ssh_acceptance', Path(__file__).with_name('test-routed-ssh.py'))
acceptance = importlib.util.module_from_spec(spec)
spec.loader.exec_module(acceptance)


class FakeNode:
    def __init__(self, name, stopped=True):
        self.name = name
        self.stopped = stopped
        self.calls = 0

    def stop(self):
        self.calls += 1
        return self.stopped


class FakeRelay:
    def __init__(self):
        self.calls = 0

    def close(self):
        self.calls += 1


class FixtureCleanupTest(unittest.TestCase):
    def test_success_requires_every_stop_before_removal(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / 'fixture'
            root.mkdir()
            first, second = FakeNode('first', False), FakeNode('second')
            relay = FakeRelay()
            with contextlib.redirect_stderr(io.StringIO()):
                with self.assertRaisesRegex(RuntimeError, 'cleanup unconfirmed'):
                    acceptance.finish_fixture(root, [first, second], relay, True, False)
            self.assertTrue(root.is_dir())
            self.assertEqual((first.calls, second.calls, relay.calls), (1, 1, 1))

    def test_original_error_is_preserved_and_fixture_retained(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / 'fixture'
            root.mkdir()
            node = FakeNode('failed-stop', False)
            with contextlib.redirect_stderr(io.StringIO()):
                with self.assertRaisesRegex(ValueError, 'original test failure'):
                    try:
                        raise ValueError('original test failure')
                    finally:
                        acceptance.finish_fixture(root, [node], FakeRelay(), False,
                                                  sys.exc_info()[0] is not None)
            self.assertTrue(root.is_dir())
            self.assertEqual(node.calls, 1)

    def test_removal_error_refuses_success(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / 'fixture'
            root.mkdir()
            with mock.patch.object(acceptance.shutil, 'rmtree', side_effect=OSError('synthetic removal failure')):
                with contextlib.redirect_stderr(io.StringIO()):
                    with self.assertRaisesRegex(RuntimeError, 'cleanup unconfirmed'):
                        acceptance.finish_fixture(root, [FakeNode('good')], FakeRelay(), True, False)
            self.assertTrue(root.is_dir())

    def test_environment_is_observed_not_a_fixed_guest_label(self):
        with mock.patch.object(acceptance.os, 'sched_getaffinity', return_value={0, 1}, create=True), \
             mock.patch.object(acceptance.platform, 'system', return_value='ExampleOS'), \
             mock.patch.object(acceptance.platform, 'machine', return_value='example-arch'), \
             mock.patch.object(acceptance.platform, 'release', return_value='example-kernel'):
            environment = acceptance.benchmark_environment()
        self.assertEqual(environment['os'], 'ExampleOS')
        self.assertEqual(environment['architecture'], 'example-arch')
        self.assertEqual(environment['cpu_scope'], {'kind': 'process_affinity', 'count': 2})
        self.assertIn('loopback', environment['topology'])
        self.assertNotIn('QEMU', str(environment))

    def test_future_benchmark_hashes_exact_binary_bytes(self):
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / 'arm'
            binary.write_bytes(b'exact private test binary bytes')
            self.assertEqual(acceptance.file_sha256(binary), hashlib.sha256(binary.read_bytes()).hexdigest())


if __name__ == '__main__':
    unittest.main()
