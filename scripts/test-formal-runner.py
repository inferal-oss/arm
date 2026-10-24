#!/usr/bin/env python3
"""Offline regressions for snapshot provenance and hostile trace inputs."""
import argparse
import errno
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import signal
import shlex
import subprocess
import tempfile
import time
import unittest
import sys
from unittest.mock import patch

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('formal_runner', Path(__file__).with_name('test-formal.py'))
formal = importlib.util.module_from_spec(spec)
spec.loader.exec_module(formal)

class RunnerTests(unittest.TestCase):

    def test_changed_helper_after_constructor_refuses_before_child(self):
        with tempfile.TemporaryDirectory(prefix='formal-initial-binding-') as directory:
            captured = Path(directory)
            fake_runner = captured/'test-formal.py'
            fake_helper = captured/'formal_process.py'
            fake_runner.write_bytes(b'captured runner')
            fake_helper.write_bytes(b'captured helper')
            work = formal.ROOT/'.tla'/('binding-'+captured.name)
            args = argparse.Namespace(mode='check', targets=[], work_dir=str(work))
            try:
                with patch.object(formal, '__file__', str(fake_runner)):
                    instance = formal.Gate(args)
                    self.assertIn(str(fake_helper.resolve()), instance.runner_sources)
                    fake_helper.write_bytes(b'changed helper before first child')
                    with patch.object(formal, 'process_runtime', side_effect=AssertionError('no runtime/child')):
                        with self.assertRaisesRegex(RuntimeError, 'source changed'):
                            instance.command(['never-started'], 'binding')
            finally:
                import shutil
                if work.exists():
                    shutil.rmtree(work)

    def setUp(self):
        (formal.ROOT / '.tla').mkdir(exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(prefix='runner-test-', dir=formal.ROOT / '.tla')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def process_is_running(self, pid):
        if sys.platform.startswith('linux'):
            try:
                state = Path('/proc/' + str(pid) + '/stat').read_text().rsplit(')', 1)[1].split()[0]
            except (FileNotFoundError, ProcessLookupError):
                return False
            return state not in ('Z', 'X')
        result = subprocess.run(['ps', '-o', 'stat=', '-p', str(pid)], text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=5)
        self.assertFalse(result.stderr, result.stderr)
        self.assertIn(result.returncode, (0, 1))
        return bool(result.stdout.strip()) and not result.stdout.strip().startswith(('Z', 'X'))

    def test_bounded_duplicate_and_trailing_input(self):
        path = self.root / 'trace.json'
        for raw in (b'{"state":1,"state":2}', b'{} {}', b' ' * ((1 << 20) + 1)):
            path.write_bytes(raw)
            with self.assertRaises(ValueError):
                formal.read_json(path)

    def test_linux_process_disappearing_during_read_is_absent(self):
        # Linux procfs can return ESRCH after open but before read when cleanup
        # wins the race. Permission failures remain unavailable evidence.
        with patch.object(sys, 'platform', 'linux'):
            for error in (FileNotFoundError(), ProcessLookupError()):
                with self.subTest(error=type(error).__name__):
                    with patch.object(Path, 'read_text', side_effect=error):
                        self.assertFalse(self.process_is_running(123))
            with patch.object(Path, 'read_text', side_effect=PermissionError()):
                with self.assertRaises(PermissionError):
                    self.process_is_running(123)

    def test_snapshot_hashes_and_all_gate_paths_use_copied_sources(self):
        source = self.root / 'source'
        source.mkdir()
        expected = {}
        for name in ('SharePreparation.tla', 'SharePreparationProof.tla', 'Witness.tla', 'Safety.cfg'):
            expected[name] = ((formal.MODEL / name).read_text() if name.endswith('Proof.tla') else 'old:' + name)
            (source / name).write_text(expected[name])
        args = argparse.Namespace(work_dir=str(self.root / 'runs'), mode='all', targets=['prepared'],
                                  java='java', tlc_jar='jar', tlapm='tlapm')
        with patch.object(formal, 'MODEL', source):
            gate = formal.Gate(args)
            for item in source.iterdir():
                item.write_text('changed after snapshot')
            for name, digest in gate.summary['sources'].items():
                self.assertEqual((gate.run / name).read_text(), expected[name])
                self.assertEqual(digest, hashlib.sha256((gate.run / name).read_bytes()).hexdigest())
            commands = []
            def command(argv, name, **kwargs):
                commands.append(list(map(str, argv)))
                if name == 'tlapm':
                    return 'All 19 obligations proved.'
                return 'Model checking completed. No error has been found.\n950 distinct states found, 0 states left on queue.'
            gate.command = command
            gate.check()
            gate.prove()
            self.assertEqual((gate.run / 'Safety.cfg').read_text(), 'old:Safety.cfg')
            self.assertIn(str(gate.run / 'Safety.cfg'), commands[0])
            self.assertIn('-Djava.io.tmpdir=' + str(gate.run / 'java-tmp'), commands[0])
            self.assertIn(str(gate.run / 'SharePreparation.tla'), commands[0])
            self.assertIn(str(gate.run / 'SharePreparationProof.tla'), commands[1])
            self.assertIn(str(gate.run), commands[1])
            self.assertNotIn(str(source), commands[1])
            self.assertIn('--strict', commands[1])
            self.assertIn('--nofp', commands[1])
            self.assertEqual(commands[1][commands[1].index('--stretch') + 1], '10')

    def test_module_scope_masks_comments_strings_and_requires_final_end(self):
        path = self.root / 'Scope.tla'
        valid = ('(* ==== (* nested ---- MODULE Wrong ---- *) *)\n'
                 '---- MODULE Scope ----\n'
                 'Text == "==== THEOREM Fake ---- MODULE Wrong ----"\n'
                 '\\* ==== THEOREM Hidden\n'
                 'THEOREM Safety == TRUE OBVIOUS\n'
                 '==== (* final comment *)\n')
        path.write_text(valid)
        self.assertEqual(formal.validate_module_source(path, ('Safety',)), ['Safety'])
        bad = [valid.replace('THEOREM Safety', '====\nTHEOREM Safety'),
               valid + '\nTHEOREM Outside == TRUE OBVIOUS\n',
               valid.replace('THEOREM Safety', '(* THEOREM Safety *) THEOREM Other'),
               valid.replace('MODULE Scope', 'MODULE Wrong'),
               valid.replace('THEOREM Safety', '---- MODULE Inner ----\nTHEOREM Safety'),
               valid.replace('==== (* final comment *)', '(* unclosed'),
               valid.replace('Text == "====', 'Text == "unclosed\n====')]
        for text in bad:
            with self.subTest(text=text):
                path.write_text(text)
                with self.assertRaises(ValueError):
                    formal.validate_module_source(path, ('Safety',))

    def test_prefix_proof_success_cannot_bypass_safety_scope(self):
        args = argparse.Namespace(work_dir=str(self.root / 'runs'), mode='prove',
                                  targets=[], tlapm='tlapm')
        gate = formal.Gate(args)
        path = gate.run / 'SharePreparationProof.tla'
        original = path.read_text()
        header, rest = original.split('\n', 1)
        path.write_text(header + '\nTHEOREM Prefix1 == TRUE OBVIOUS\nTHEOREM Prefix2 == TRUE OBVIOUS\n====\n' + rest)
        # Model an invalid source already present when the snapshot was made,
        # so source stability alone cannot stand in for the scope guard.
        gate.summary['sources'][path.name] = hashlib.sha256(path.read_bytes()).hexdigest()
        calls = []
        gate.command = lambda *a, **kw: calls.append(a) or 'All 2 obligations proved.'
        with self.assertRaises(ValueError):
            gate.prove()
        self.assertEqual(calls, [], 'prover must not run on an early-terminated source')
        self.assertEqual(gate.summary['gates'], [])
        path.write_text(original.replace('THEOREM Safety ==', 'THEOREM RemovedSafety =='))
        gate.summary['sources'][path.name] = hashlib.sha256(path.read_bytes()).hexdigest()
        with self.assertRaises(ValueError):
            gate.prove()
        self.assertEqual(calls, [])

    def test_before_or_during_proof_source_mutation_cannot_pass(self):
        args = argparse.Namespace(work_dir=str(self.root / 'runs'), mode='prove',
                                  targets=[], tlapm='tlapm')
        for when in ('before', 'during'):
            with self.subTest(when=when):
                gate = formal.Gate(args)
                path = gate.run / 'SharePreparation.tla'
                calls = []
                def change():
                    path.write_text(path.read_text() + '\n(* actual file mutation *)\n')
                def command(*a, **kw):
                    calls.append(a)
                    if when == 'during':
                        change()
                    return 'All 509 obligations proved.'
                gate.command = command
                if when == 'before':
                    change()
                with self.assertRaisesRegex(RuntimeError, 'captured source changed'):
                    gate.prove()
                self.assertEqual(len(calls), 0 if when == 'before' else 1)
                self.assertEqual(gate.summary['gates'], [])

    def test_timeout_preserves_output_and_stops_owned_descendants(self):
        args = argparse.Namespace(work_dir=str(self.root / 'runs'), mode='prove', targets=[])
        gate = formal.Gate(args)
        pidfile = self.root / 'child.pid'
        child = ('import os, signal, sys, time; from pathlib import Path; '
                 'signal.signal(signal.SIGTERM, signal.SIG_IGN); '
                 'Path(sys.argv[1]).write_text(str(os.getpid())); '
                 'print("child ready", flush=True); time.sleep(20)')
        parent = ('import subprocess, sys, time; '
                  'subprocess.Popen([sys.executable, "-u", "-c", sys.argv[1], sys.argv[2]]); '
                  'print("parent ready", flush=True); time.sleep(20)')

        for closed_output in (False, True):
            with self.subTest(closed_output=closed_output):
                gate = formal.Gate(args)
                pidfile = self.root / ('child-closed.pid' if closed_output else 'child-open.pid')
                command = parent
                if closed_output:
                    command = parent.replace('sys.argv[2]]);',
                        'sys.argv[2]], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL);')
                try:
                    with self.assertRaises(subprocess.TimeoutExpired) as failure:
                        gate.command([sys.executable, '-u', '-c', command, child, pidfile], 'owned-timeout', timeout=2)
                    self.assertIn('parent ready', failure.exception.output)
                    log = (gate.run / 'owned-timeout.log').read_text()
                    self.assertIn('parent ready', log)
                    if not closed_output:
                        self.assertIn('child ready', failure.exception.output)
                        self.assertIn('child ready', log)
                    pid = int(pidfile.read_text())
                    deadline = time.monotonic() + 5
                    while self.process_is_running(pid) and time.monotonic() < deadline:
                        time.sleep(0.02)
                    self.assertFalse(self.process_is_running(pid), 'owned descendant is still executing after timeout')
                finally:
                    # No historical PID signal: this fixed harmless tool and its
                    # descendant have independent20s lifetime bounds.
                    pass

    def test_signal_failure_keeps_timeout_and_drained_output(self):
        # Inject only the signal refusal; the owned tool and its timeout/drain
        # are real. A denied cleanup attempt never establishes process absence.
        actual_killpg = os.killpg
        for denied in (signal.SIGTERM, signal.SIGKILL):
            with self.subTest(denied=denied):
                gate = formal.Gate(argparse.Namespace(work_dir=str(self.root / 'runs'),
                                                      mode='prove', targets=[]))
                pidfile = self.root / ('denied-' + str(denied) + '.pid')
                command = ('import os, sys, time; from pathlib import Path; '
                           'Path(sys.argv[1]).write_text(str(os.getpid())); '
                           'print("owned tool ready", flush=True); time.sleep(20)')
                attempts = []
                def killpg(pid, sig):
                    attempts.append(sig)
                    if sig == denied:
                        raise PermissionError(errno.EPERM, 'injected signal denial')
                    return actual_killpg(pid, sig)
                try:
                    with patch.object(formal.os, 'killpg', side_effect=killpg):
                        with self.assertRaises(subprocess.TimeoutExpired) as failure:
                            gate.command([sys.executable, '-u', '-c', command, pidfile],
                                         'denied-signal', timeout=2)
                    self.assertIn('owned tool ready', failure.exception.output)
                    log = (gate.run / 'denied-signal.log').read_text()
                    self.assertIn('owned tool ready', log)
                    self.assertIn(signal.Signals(denied).name, log)
                    self.assertIn('errno=' + str(errno.EPERM), log)
                    self.assertIn('cleanup unconfirmed', log)
                    self.assertEqual(attempts.count(signal.SIGKILL), 1)
                    self.assertEqual(attempts.count(signal.SIGTERM), 1)
                    self.assertFalse(self.process_is_running(int(pidfile.read_text())))
                    self.assertEqual(gate.summary['gates'], [])
                finally:
                    # No historical PID signal: this fixed harmless tool and its
                    # descendant have independent20s lifetime bounds.
                    pass

    def test_cli_signals_preserve_output_and_stop_owned_work(self):
        for sig in (signal.SIGINT, signal.SIGTERM):
            with self.subTest(signal=sig):
                case = self.root / ('signal-' + str(sig))
                case.mkdir()
                leader_file, child_file, ready_file = case / 'leader.pid', case / 'child.pid', case / 'ready'
                helper = case / 'java.py'
                helper.write_text('''import os, signal, subprocess, sys, time
from pathlib import Path
if '-version' in sys.argv:
    print('offline Java fixture')
    raise SystemExit(0)
if '-help' in sys.argv:
    print('TLC - offline fixture Version 1')
    raise SystemExit(1)
signal.signal(signal.SIGTERM, signal.SIG_IGN)
Path(os.environ['FORMAL_TEST_LEADER']).write_text(str(os.getpid()))
code = "import os, signal, sys, time; from pathlib import Path; signal.signal(signal.SIGTERM, signal.SIG_IGN); print('child ready', flush=True); Path(sys.argv[1]).write_text(str(os.getpid())); time.sleep(20)"
subprocess.Popen([sys.executable, '-u', '-c', code, os.environ['FORMAL_TEST_CHILD']])
print('tool ready', flush=True)
Path(os.environ['FORMAL_TEST_READY']).write_text('ready')
time.sleep(20)
''')
                java = case / 'java'
                java.write_text('#!/bin/sh\nexec ' + shlex.quote(sys.executable) + ' -u ' + shlex.quote(str(helper)) + ' "$@"\n')
                java.chmod(0o700)
                jar = case / 'fixture.jar'
                jar.write_text('offline fixture')
                work = case / 'runs'
                env = dict(os.environ, FORMAL_TEST_LEADER=str(leader_file), FORMAL_TEST_CHILD=str(child_file),
                           FORMAL_TEST_READY=str(ready_file))
                command = [sys.executable, str(formal.ROOT / 'scripts/test-formal.py'), '--mode', 'check',
                           '--java', str(java), '--tlc-jar', str(jar), '--work-dir', str(work)]
                runner = formal.process_runtime().Fixture(command, case/'outer.log', formal.ROOT, env=env)
                try:
                    deadline = time.monotonic() + 10
                    while (not child_file.exists() or child_file.stat().st_size == 0 or not ready_file.exists()) and time.monotonic() < deadline:
                        if runner.terminal() is not None:
                            self.fail('runner exited before signal fixture started: ' + runner.wait()[1])
                        time.sleep(0.02)
                    self.assertTrue(child_file.exists(), 'tool descendant did not start')
                    self.assertTrue(ready_file.exists(), 'tool did not publish output readiness')
                    runner.send(sig)
                    actual_exit, output = runner.wait(timeout=10)
                    self.assertEqual(actual_exit, 128 + sig, output)
                    logs = list(work.glob('run-*/safety.log'))
                    self.assertEqual(len(logs), 1)
                    self.assertIn('tool ready', logs[0].read_text())
                    self.assertIn('child ready', logs[0].read_text())
                    for path in (leader_file, child_file):
                        pid = int(path.read_text())
                        deadline = time.monotonic() + 5
                        while self.process_is_running(pid) and time.monotonic() < deadline:
                            time.sleep(0.02)
                        self.assertFalse(self.process_is_running(pid), 'owned work is live after runner signal')
                finally:
                    runner.close()  # Only this still-retained fresh outer child.
                    # Inner tools are independently20s bounded; saved PID files
                    # never authorize a signal after native Wait or unknown.

    def test_closed_tlc_edges_and_scalar_types(self):
        states = [{k: False if t is bool else 0 if t is int else 'x' for k, t in formal.FIELDS.items()} for _ in range(2)]
        for s in states:
            s['schema'] = 'arm.share-preparation/v1'
        doc = formal.envelope(states)
        path = self.root / 'trace.json'
        path.write_text(json.dumps(doc))
        self.assertEqual(formal.tlc_states(path), states)
        doc['counterexample']['action'][0][2][0] = 99
        path.write_text(json.dumps(doc))
        with self.assertRaises(ValueError):
            formal.tlc_states(path)
        states[0]['launches'] = True
        with self.assertRaises(ValueError):
            formal.validate_states(states)

    def test_interrupt_during_timeout_grace_stops_owned_work(self):
        case = self.root / 'timeout-interrupt'
        case.mkdir()
        leader_file, child_file, term_file = case / 'leader.pid', case / 'child.pid', case / 'term'
        helper = case / 'tool.py'
        helper.write_text('''import os, signal, subprocess, sys, time
from pathlib import Path
signal.signal(signal.SIGTERM, lambda *_: Path(sys.argv[3]).write_text('TERM observed'))
Path(sys.argv[1]).write_text(str(os.getpid()))
code = "import os, signal, sys, time; from pathlib import Path; signal.signal(signal.SIGTERM, signal.SIG_IGN); print('child ready', flush=True); Path(sys.argv[1]).write_text(str(os.getpid())); time.sleep(20)"
subprocess.Popen([sys.executable, '-u', '-c', code, sys.argv[2]])
print('tool ready', flush=True)
time.sleep(20)
''')
        work = case / 'runs'
        harness = '''import argparse, importlib.util, sys
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('runner', sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
gate = module.Gate(argparse.Namespace(work_dir=sys.argv[2], mode='check', targets=[]))
try:
    gate.command([sys.executable, '-u', sys.argv[3], *sys.argv[4:]], 'timeout-interrupt', timeout=2)
except KeyboardInterrupt:
    raise SystemExit(130)
'''
        command = [sys.executable, '-u', '-c', harness, str(formal.ROOT / 'scripts/test-formal.py'),
                   str(work), str(helper), str(leader_file), str(child_file), str(term_file)]
        runner = formal.process_runtime().Fixture(command, case/'outer.log', formal.ROOT)
        try:
            deadline = time.monotonic() + 10
            while not term_file.exists() and time.monotonic() < deadline:
                if runner.terminal() is not None:
                    self.fail('timeout fixture exited before TERM grace: ' + runner.wait()[1])
                time.sleep(0.01)
            self.assertTrue(term_file.exists(), 'tool did not reach timeout TERM grace')
            runner.send(signal.SIGINT)
            actual_exit, output = runner.wait(timeout=10)
            self.assertEqual(actual_exit, 130, output)
            logs = list(work.glob('run-*/timeout-interrupt.log'))
            self.assertEqual(len(logs), 1)
            self.assertIn('tool ready', logs[0].read_text())
            self.assertIn('child ready', logs[0].read_text())
            for path in (leader_file, child_file):
                pid = int(path.read_text())
                deadline = time.monotonic() + 5
                while self.process_is_running(pid) and time.monotonic() < deadline:
                    time.sleep(0.02)
                self.assertFalse(self.process_is_running(pid), 'owned work is live after timeout interruption')
        finally:
            runner.close()  # No historical leader/child PID cleanup.

    def test_empty_or_incomplete_proof_is_not_acceptance(self):
        args = argparse.Namespace(work_dir=str(self.root / 'runs'), mode='prove',
                                  targets=[], tlapm='tlapm')
        for output in ('All 0 obligations proved.', 'All 2 obligations proved.\n[ERROR]: incomplete',
                       '1/2 obligations failed.'):
            gate = formal.Gate(args)
            gate.command = lambda *a, **kw: output
            with self.assertRaises(RuntimeError):
                gate.prove()

    def test_safety_requires_nonempty_complete_exploration(self):
        args = argparse.Namespace(work_dir=str(self.root / 'runs'), mode='check', targets=[])
        gate = formal.Gate(args)
        completed = 'Model checking completed. No error has been found.\n'
        statistics = '950 distinct states found, 0 states left on queue.'
        for output in (completed + '0 distinct states found, 0 states left on queue.',
                       completed + '950 distinct states found, 1 states left on queue.',
                       statistics, completed):
            gate.tlc = lambda *a, **kw: output
            with self.assertRaises(RuntimeError):
                gate.check()
        self.assertEqual(gate.summary['gates'], [])
        gate.tlc = lambda *a, **kw: completed + statistics
        gate.check()
        self.assertEqual(gate.summary['gates'][0]['safety'], 'passed')

    def test_negative_requires_valid_initial_and_actual_exploration(self):
        args = argparse.Namespace(work_dir=str(self.root / 'runs'), mode='traces', targets=[])
        gate = formal.Gate(args)
        initial = {k: False if t is bool else 0 if t is int else 'x' for k, t in formal.FIELDS.items()}
        initial['schema'] = 'arm.share-preparation/v1'
        states = [dict(initial), dict(initial)]
        with self.assertRaises(ValueError):
            gate.replay(states, 'no-validated-init', accepted=False)
        gate.initial_state = dict(initial)
        states[0]['action'] = 'wrong-init'
        with self.assertRaises(ValueError):
            gate.replay(states, 'corrupt-init', accepted=False)
        states[0] = dict(initial)
        gate.tlc = lambda *a, **kw: '0 distinct states found'
        with self.assertRaises(RuntimeError):
            gate.replay(states, 'empty-exploration', accepted=False)
        source = (gate.run / 'Replayemptyexploration.tla').read_text()
        self.assertIn('/\\ Next', source)
        self.assertNotIn('[Next]_vars', source)

if __name__ == '__main__':
    unittest.main()
