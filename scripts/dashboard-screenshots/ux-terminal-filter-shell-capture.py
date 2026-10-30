#!/usr/bin/env python3
"""Unrun native45 source. Separate source/build/native allocation is required."""
import argparse
import errno
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import pty
import select
import signal
import struct
import subprocess
import termios
import time

REPO = Path(__file__).resolve().parents[2]
SUPPORT = REPO / 'scripts/dashboard-screenshots/ux-terminal-loop-capture.py'
if hashlib.sha256(SUPPORT.read_bytes()).hexdigest() != '980fc761382c0de2854f43decb854658c69462ac05ad5b899105585eac8231d6':
    raise RuntimeError('frozen PTY support source changed before import')
spec = importlib.util.spec_from_file_location('_arm_filter_pty_support', SUPPORT)
support = importlib.util.module_from_spec(spec)
spec.loader.exec_module(support)  # Definitions only; the historical main is not called.
sha, retained = support.sha, support.retained
LIMIT, RESERVE = 16 * 1024 * 1024, 131072
BINDING = 'docs/validation/ux-terminal-filter-shell-proposal-2026-10-30.json'
BINDING_SHA = '4e6301237a2893f4a6dfc06d8c66f50041ab6dd34e6d948598bc1f5c635d9420'
SELECTOR = '^TestUXTerminalFilterShellFixture$'
ROSTER = ['leases-scrolled', 'filter-visible', 'filter-help', 'filter-edited',
          'leases-applied', 'filter-discard-draft', 'leases-cancelled', 'filter-quit-ready']
KEYS = {'down': b'\x1b[B', '/': b'/', 'q': b'q', 'enter': b'\r', 'esc': b'\x1b'}


def interrupted(signum, _frame):
    for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(sig, signal.SIG_IGN)
    raise KeyboardInterrupt('owned capture interrupted: ' + str(signum))


def write_json(path, value):
    raw = (json.dumps(value, indent=2) + '\n').encode()
    temporary = path.with_name(path.name + '.new')
    with temporary.open('xb') as target:
        if target.write(raw) != len(raw):
            raise RuntimeError('short original metadata write')
    temporary.replace(path)
    path.chmod(0o600)


def verify_inputs(paths, expected):
    if len(paths) != 8 or len(set(paths)) != 8:
        raise RuntimeError('eight historical runtime containers required')
    result = {}
    seen = set()
    for p in paths:
        if not p.is_absolute() or p.resolve() != p or not p.is_dir() or p.stat().st_mode & 0o077 or p.is_relative_to(REPO):
            raise RuntimeError('canonical private historical runtime required')
        h = sha(p / 'artifact-manifest.json')
        if h not in expected or h in seen:
            raise RuntimeError('historical runtime identity')
        seen.add(h)
        members = json.loads((p / 'artifact-manifest.json').read_text())
        if len(members) != expected[h] or {str(f.relative_to(p)) for f in p.rglob('*') if f.is_file() or f.is_symlink()} != set(members) | {'artifact-manifest.json'}:
            raise RuntimeError('historical runtime roster')
        for name, digest in members.items():
            file = p / name
            if file.is_symlink() or not file.resolve().is_relative_to(p) or not file.is_file() or sha(file) != digest:
                raise RuntimeError('historical runtime bytes')
        result[str(p)] = h
    if any(a.is_relative_to(b) or b.is_relative_to(a) for i, a in enumerate(paths) for b in paths[i + 1:]):
        raise RuntimeError('historical runtime overlap')
    return result


def verify_build(binary, receipt):
    if sha(REPO / BINDING) != BINDING_SHA:
        raise RuntimeError('native45 binding identity')
    binding = json.loads((REPO / BINDING).read_text())
    source = binding['sourceBinding']
    if source['requiredEntries'] != 69 or len(set(source['members'])) != 69 or source['testSelector'] != SELECTOR:
        raise RuntimeError('native45 binding roster')
    if receipt.get('complete') is not True or type(receipt.get('originalCompileExitCode')) is not int or receipt['originalCompileExitCode'] != 0 or not all(receipt.get(k) is True for k in ('originalCompileHandleJoined', 'sourceUnchanged', 'preservedUnchanged', 'TMPAndGOTMPEmpty')):
        raise RuntimeError('original successful compile/source/cleanup receipt required')
    if receipt.get('testSelector') != SELECTOR or len(receipt.get('sourceCommit', '')) != 40 or set(receipt['sourceSHA256']) != set(source['members']):
        raise RuntimeError('fresh69 compile receipt required')
    if not binary.is_absolute() or binary.is_symlink() or not binary.is_file() or sha(binary) != receipt['binarySHA256']:
        raise RuntimeError('fresh native45 binary identity')
    if any((REPO / name).is_symlink() or not (REPO / name).is_file() or sha(REPO / name) != h for name, h in receipt['sourceSHA256'].items()):
        raise RuntimeError('native45 source cut drift')
    for name, h in binding['currentSource']['existingMemberSHA256'].items():
        if receipt['sourceSHA256'].get(name) != h:
            raise RuntimeError('existing reviewed source differs')
    return binding


class FilterSession(support.Session):
    def __init__(self, root, budget_root, binary, deadline, inputs):
        self.root, self.budget_root, self.deadline = root, budget_root, deadline
        self.retained_inputs = inputs
        self.size, self.grid = (45, 14), '45x14'
        self.progress = root / self.grid / 'progress.json'
        self.master = self.slave = self.child = None
        self.bytes, self.resizes, self.actions = bytearray(), [], []
        self.state = self.last_drain_observation = None
        self.initial_barrier = None
        self.quit_press = None
        self.cleanup = {'childWaited': False, 'termiosRestored': None, 'masterClosed': False,
                        'slaveClosed': False, 'ownedChildTerminationRequested': False,
                        'finalMasterEOFObserved': False, 'finalMasterEIOObserved': False,
                        'finalDrainQuietPolls': 0, 'finalDrainTimedOut': False}
        self.stream_path = self.grid + '.pty'
        self.stream = (root / self.stream_path).open('xb')
        try:
            self.master, self.slave = pty.openpty()
            self.mode = termios.tcgetattr(self.slave)
            fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack('HHHH', 14, 45, 0, 0))
        except BaseException:
            self.finish()
            raise

    def start(self, binary):
        env = {'TERM': 'xterm-256color', 'COLORTERM': 'truecolor', 'GOMAXPROCS': '2',
               'GOMEMLIMIT': '1536MiB', 'GOENV': 'off', 'GOFLAGS': '', 'GOTOOLCHAIN': 'local',
               'GOPROXY': 'off', 'GOSUMDB': 'off', 'TMPDIR': str(self.root / 'tmp'),
               'ARM_TUI_FILTER_SHELL_ROOT': str(self.root), 'ARM_TUI_FILTER_SHELL_GRID': self.grid}
        # main already retains this session inside its cleanup finally before
        # original Popen is called. No detached session or group/PID lookup.
        self.child = subprocess.Popen([str(binary), '-test.run=' + SELECTOR,
                                       '-test.timeout=35s', '-test.count=1'],
                                      cwd=REPO / 'internal/tui', env=env,
                                      stdin=self.slave, stdout=self.slave, stderr=self.slave)

    def total(self):
        return retained(self.budget_root) + sum(retained(p) for p in self.retained_inputs)

    def bound(self):
        if time.monotonic() >= self.deadline or len(self.bytes) > 4 * 1024 * 1024 or self.total() + RESERVE > LIMIT:
            raise RuntimeError('native45 deadline/whole-root budget')

    def drain(self, wait=0.03, cleanup=False):
        if not cleanup:
            self.bound()
        self.last_drain_observation = 'quiet'
        if not select.select([self.master], [], [], wait)[0]:
            return False
        try:
            data = os.read(self.master, 65536)
        except OSError as error:
            if error.errno == errno.EIO:
                self.last_drain_observation = 'eio'
                return False
            raise
        if not data:
            self.last_drain_observation = 'eof'
            return False
        self.last_drain_observation = 'data'
        if len(self.bytes) + len(data) > 4 * 1024 * 1024 or self.total() + len(data) + RESERVE > LIMIT:
            if cleanup:
                self.cleanup['cleanupOutputTruncated'] = True
                return True
            raise RuntimeError('next original PTY block exceeds whole-root budget')
        self.bytes.extend(data)
        if self.stream.write(data) != len(data):
            raise RuntimeError('short original stream retention')
        self.stream.flush()
        if not cleanup and b'\x1b]52;' in self.bytes:
            raise RuntimeError('clipboard OSC in original output')
        return True

    def write_input(self, data, kind):
        self.bound()
        record = {'kind': kind, 'requested': len(data), 'returned': None, 'hex': data.hex(),
                  'bytesThrough': len(self.bytes), 'prefixSHA256': hashlib.sha256(self.bytes).hexdigest()}
        self.actions.append(record)
        if len(self.actions) > 24:
            raise RuntimeError('parent input action bound')
        self.save_actions()
        try:
            record['returned'] = os.write(self.master, data)  # Exactly once; never retry.
        except OSError as error:
            record.update(errorName=type(error).__name__, errno=error.errno)
            try:
                self.save_actions()
            except Exception:
                pass  # The original input error still escapes unchanged.
            raise
        try:
            self.save_actions()
        except Exception:
            if record['returned'] != len(data):
                raise RuntimeError('short original input; recording also failed')
            raise
        if record['returned'] != len(data):
            raise RuntimeError('short original input; no retry')
        return record

    def save_actions(self):
        if len((json.dumps(self.actions) + '\n').encode()) > 8192:
            raise RuntimeError('parent action byte bound')
        self.bound()
        write_json(self.root / 'input-actions.json', self.actions)

    def load(self):
        state = super().load()
        if state and (state.get('diagnostics', {}).get('unknown') or state.get('diagnostics', {}).get('metadataFailed')):
            raise RuntimeError('original passive observation failed or unknown')
        return state

    def wait(self, predicate, label):
        end = min(self.deadline, time.monotonic() + 5)
        while time.monotonic() < end:
            self.drain()
            if self.child.poll() is not None:
                raise RuntimeError('original child ended before ' + label)
            state = self.load()
            if state and predicate(state):
                return state
        raise RuntimeError('original state deadline: ' + label)

    def settle(self):
        if self.child.poll() is not None:
            raise RuntimeError('child ended before original prefix')
        super().settle()
        if self.child.poll() is not None:
            raise RuntimeError('child ended during original prefix')

    def key(self, key, predicate=lambda s: True):
        state = self.load()
        before = state['inputs']
        expected_downs = state['downs'] + 1 if key == 'down' else None
        self.write_input(KEYS[key], key)
        return self.wait(lambda s: s['inputs'] > before and predicate(s) and (expected_downs is None or s['downs'] == expected_downs), key)

    def paste(self, text, count):
        self.write_input(b'\x1b[200~' + text.encode('ascii') + b'\x1b[201~', 'paste')
        return self.wait(lambda s: s['pastes'] == count, 'original bracketed paste')

    def wheel(self, count):
        self.write_input(b'\x1b[<65;11;7M', 'wheel-down')
        return self.wait(lambda s: s['mouseDelivered'] == count and s['wheelInputs'] == count, 'original wheel delivery')

    def startup_output(self):
        end = min(self.deadline, time.monotonic() + 5)
        while time.monotonic() < end:
            self.drain()
            state = self.load()
            path = self.root / self.grid / 'readiness.json'
            if self.child.poll() is not None:
                raise RuntimeError('child ended before initial output barrier')
            if not state or not path.exists():
                continue
            if path.stat().st_size > 16384:
                raise RuntimeError('readiness byte bound')
            info = json.loads(path.read_text())
            if info['unknown'] or info['startup']['drift']:
                raise RuntimeError('initial output observation unknown')
            frame = info.get('frame')
            if not frame or state['nativeSizeMessages'] < 1:
                continue
            start, stop = frame['start'], frame['end']
            if not frame['full'] or frame['requested'] != frame['returned'] or stop - start != frame['returned'] or not 0 <= start < stop <= 4 * 1024 * 1024:
                raise RuntimeError('original initial output range')
            if stop > len(self.bytes):
                continue
            original = bytes(self.bytes[start:stop])
            if hashlib.sha256(original).hexdigest() != frame['sha256'] or not all(token in original for token in (b'\x1b[?1049h', b'\x1b[?1002h', b'\x1b[?1006h', b'[q] Exit', b'Inspect')):
                raise RuntimeError('query-only or invalid original renderer write')
            descriptor_keys = {'inputUnwrapped', 'inputTerminal', 'outputTerminal', 'sameOutputFD', 'sameOutputName', 'termFilePreserved', 'sizeMatches'}
            if set(info['descriptor']) != descriptor_keys or any(value is not True for value in info['descriptor'].values()) or not info['startup']['valid'] or info['startup']['sealed'] or info['startup']['fingerprint'] != frame['fingerprint'] or state['grid'] != [45, 14] or state['inputMode'] != '' or state['selected'] != 0 or len(state['visibleLeaseIDs']) != 8:
                raise RuntimeError('initial descriptor/model fingerprint')
            self.initial_barrier = {'frame': frame, 'descriptor': info['descriptor'],
                                    'fingerprint': frame['fingerprint'], 'bytesThrough': len(self.bytes)}
            return
        raise RuntimeError('initial original renderer output deadline')

    def checkpoint(self, name, checkpoints):
        if len(checkpoints) >= 8:
            raise RuntimeError('native45 checkpoint bound')
        super().checkpoint(name, checkpoints)

    def footer_quit(self):
        state = self.load()
        r = next(r for r in state['regions'] if r['key'] == 'ctrl+c' and r['y'] == 13 and r['height'] == 1 and r['x'] >= 0 and r['x'] + r['width'] <= 45)
        if state['inputValue'] != 'probe-draftq' or not state['inputFocused'] or not state['draftVisibleInViewport']:
            raise RuntimeError('footer quit original state')
        self.quit_press = {'region': r, 'revision': state['revision']}
        self.quit_press['write'] = self.write_input(f"\x1b[<0;{r['x'] + r['width']//2 + 1};14M".encode(), 'footer-quit')
        end = min(self.deadline, time.monotonic() + 5)
        while self.child.poll() is None and time.monotonic() < end:
            self.drain()
        if self.child.poll() is None:
            raise RuntimeError('original footer Quit did not return child')

    def finish(self):
        # Adapt the reviewed original-handle cleanup, with NO q/Ctrl+C fallback.
        errors = []
        try:
            if self.child is not None:
                end = time.monotonic() + 1
                while self.child.poll() is None and time.monotonic() < end:
                    try:
                        self.drain(cleanup=True)
                    except Exception:
                        errors.append('cleanup-drain')
                        break
                if self.child.poll() is None:
                    self.cleanup['ownedChildTerminationRequested'] = True
                    try:
                        self.child.terminate()
                        self.child.wait(timeout=2)
                    except subprocess.TimeoutExpired:
                        # A failed kill request cannot skip the final original
                        # wait below. Preserve request errors as failed cleanup.
                        try:
                            self.child.kill()
                        except Exception:
                            errors.append('original-child-kill-unknown')
                        try:
                            self.child.wait(timeout=2)
                        except Exception:
                            errors.append('original-child-wait-unknown')
                    except Exception:
                        errors.append('original-child-termination-unknown')
                try:
                    self.cleanup.update(childExitCode=self.child.wait(timeout=1), childWaited=True)
                except Exception:
                    errors.append('original-child-wait-unknown')
            if self.slave is not None:
                try:
                    restored = termios.tcgetattr(self.slave)
                    restored[3] &= ~getattr(termios, 'PENDIN', 0)
                    self.mode[3] &= ~getattr(termios, 'PENDIN', 0)
                    self.cleanup['termiosRestored'] = restored == self.mode
                except Exception:
                    errors.append('termios-unknown')
                os.close(self.slave)
                self.slave = None
                self.cleanup['slaveClosed'] = True
            if self.master is not None:
                self.cleanup['finalDrainAfterChildJoin'] = self.cleanup['childWaited']
                end = time.monotonic() + 1
                while time.monotonic() < end:
                    self.drain(0.05, cleanup=True)
                    outcome = self.last_drain_observation
                    if outcome in ('eof', 'eio'):
                        self.cleanup['finalMasterEOFObserved' if outcome == 'eof' else 'finalMasterEIOObserved'] = True
                        break
                    if outcome == 'quiet':
                        self.cleanup['finalDrainQuietPolls'] += 1
                else:
                    self.cleanup['finalDrainTimedOut'] = True
                    errors.append('final-master-no-eof')
        except Exception:
            errors.append('cleanup-unknown')
        finally:
            try:
                self.stream.close()
            except Exception:
                errors.append('stream-close-unknown')
            for name in ('master', 'slave'):
                fd = getattr(self, name)
                if fd is not None:
                    try:
                        os.close(fd)
                        self.cleanup[name + 'Closed'] = True
                    except Exception:
                        errors.append(name + '-close-unknown')
                    setattr(self, name, None)
        self.cleanup['cleanupErrors'] = errors
        return self.cleanup


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', required=True, type=Path)
    parser.add_argument('--build-receipt', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--budget-root', required=True, type=Path)
    parser.add_argument('--retained-input-root', required=True, action='append', type=Path)
    args = parser.parse_args()
    os.umask(0o077)
    root, budget = args.output, args.budget_root
    if not budget.is_absolute() or budget.resolve() != budget or not budget.is_dir() or budget.stat().st_mode & 0o077 or budget.is_relative_to(REPO) or not root.is_absolute() or root.parent.resolve() != root.parent or not root.is_relative_to(budget) or root == budget:
        raise RuntimeError('canonical private whole-budget/fresh capture paths')
    build = json.loads(args.build_receipt.read_text())
    binding = verify_build(args.binary, build)
    expected = {c['manifestSHA256']: c['entries'] for c in binding['retention']['historicalRuntimeContainers']}
    preserved = verify_inputs(args.retained_input_root, expected)
    if any(budget.is_relative_to(p) or p.is_relative_to(budget) for p in args.retained_input_root):
        raise RuntimeError('whole budget and historical roots overlap')
    root.mkdir(mode=0o700)
    (root / 'tmp').mkdir(mode=0o700)
    write_json(root / 'build-receipt.json', build)
    failure, session = None, None
    frames, receipts = [], []
    deadline = time.monotonic() + 70
    for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(sig, interrupted)
    try:
        if retained(budget) + sum(retained(p) for p in args.retained_input_root) + RESERVE > LIMIT:
            raise RuntimeError('combined budget before original launch')
        session = FilterSession(root, budget, args.binary, deadline, args.retained_input_root)
        session.start(args.binary)
        session.wait(lambda s: s['initSuppressed'] and s['revision'] >= 1 and s['nativeSizeMessages'] >= 1, 'native45 ready')
        session.startup_output()
        for _ in range(12):
            if session.load()['viewport']['offset'] >= 3:
                break
            session.key('down')
        if session.load()['viewport']['offset'] < 3:
            raise RuntimeError('original list scroll precondition')
        session.checkpoint(ROSTER[0], frames)
        session.key('/', lambda s: s['inputMode'] == 'filter')
        session.paste('probe-draft', 1)
        session.wait(lambda s: s['inputFocused'] and s['draftVisibleInViewport'] and s['viewport']['offset'] == 0, 'visible filter')
        session.checkpoint(ROSTER[1], frames)
        session.wheel(1)
        session.wheel(2)
        session.wait(lambda s: s['inputFocused'] and s['viewport']['offset'] > 0 and '[Enter] Apply filter' in s['viewport']['rows'] and '[Esc] Back' in s['viewport']['rows'], 'readable help')
        session.checkpoint(ROSTER[2], frames)
        session.key('q', lambda s: s['inputValue'] == 'probe-draftq' and s['draftVisibleInViewport'])
        session.checkpoint(ROSTER[3], frames)
        session.key('enter', lambda s: s['filter'] == 'probe-draftq' and s['inputMode'] == '' and s['visibleLeaseIDs'] == [])
        session.checkpoint(ROSTER[4], frames)
        session.key('/', lambda s: s['inputMode'] == 'filter')
        session.paste('-discard', 2)
        session.wait(lambda s: s['inputValue'] == 'probe-draftq-discard' and s['draftVisibleInViewport'], 'discard draft')
        session.checkpoint(ROSTER[5], frames)
        session.key('esc', lambda s: s['inputMode'] == '' and s['filter'] == 'probe-draftq')
        session.checkpoint(ROSTER[6], frames)
        session.key('/', lambda s: s['inputMode'] == 'filter' and s['inputValue'] == 'probe-draftq' and s['draftVisibleInViewport'])
        session.checkpoint(ROSTER[7], frames)
        # Coverage is actual original offset/height/total, not an assumed8-row form.
        coverage = set()
        total = frames[1]['state']['viewport']['total']
        for index in (1, 2):
            v = frames[index]['state']['viewport']
            if v['total'] != total:
                raise RuntimeError('form changed during help scroll')
            coverage.update(range(v['offset'], min(total, v['offset'] + v['height'])))
        if coverage != set(range(total)):
            raise RuntimeError('actual form/help row coverage incomplete')
        session.footer_quit()
    except BaseException as error:
        failure = type(error).__name__ + ': ' + str(error)
    finally:
        for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            signal.signal(sig, signal.SIG_IGN)
        if session is not None:
            cleanup = session.finish()
            child_path = root / '45x14' / 'child-final.json'
            diagnostic_path = root / '45x14' / 'readiness.json'
            try:
                child = json.loads(child_path.read_text()) if child_path.exists() else None
                diagnostic = json.loads(diagnostic_path.read_text()) if diagnostic_path.exists() else None
            except Exception as error:
                child = diagnostic = None
                failure = failure or type(error).__name__ + ': original child metadata unreadable'
            positive = ['programStarted', 'programReturned', 'sameWrapperReturned', 'sameModel', 'quitRequested', 'quitMsgObserved', 'initSuppressed', 'fakeClosed', 'contextCanceled', 'inputFocused']
            try:
                good = bool(child and not child['testFailed'] and all(child.get(k) is True for k in positive) and child['nativeSizeMessages'] >= 1 and child['pastes'] == 2 and child['wheelInputs'] == 2 and child['footerInputs'] == 1 and child['mouseProduced'] == child['mouseDelivered'] == 3 and child['mouseActive'] == child['reads'] == child['activeFakeCalls'] == 0 and not child['calls'] and not child['forbidden'] and child['fakeCloses'] == 1 and child['inputMode'] == 'filter' and child['inputValue'] == child['filter'] == 'probe-draftq')
                good = good and bool(session.initial_barrier and diagnostic and not diagnostic['unknown'] and diagnostic['startup']['sealed'] and not diagnostic['startup']['drift'] and diagnostic['programResult'] == 'nil' and diagnostic['frame'] == session.initial_barrier['frame'])
                if good:
                    counts = diagnostic['counters']
                    good = (len(diagnostic['records']) <= 48 and all(counts.get(k) == 3 for k in ('callback-entry', 'cmd-created', 'cmd-start', 'cmd-produced', 'typed-delivery', 'typed-frame')) and counts.get('parsed-wheel') == 2 and counts.get('parsed-click') == 1 and counts.get('cmd-nil', 0) == 0)
                good = good and cleanup.get('childExitCode') == 0 and all(cleanup.get(k) for k in ['childWaited', 'finalDrainAfterChildJoin', 'termiosRestored', 'slaveClosed', 'masterClosed']) and bool(cleanup['finalMasterEOFObserved'] or cleanup['finalMasterEIOObserved']) and not any(cleanup.get(k) for k in ['ownedChildTerminationRequested', 'finalDrainTimedOut', 'cleanupOutputTruncated', 'cleanupErrors'])
            except Exception:
                good = False
            if not good:
                failure = failure or 'original child/callback/cleanup outcome failed or unknown'
            receipts.append({'grid': '45x14', 'cleanup': cleanup, 'originalChild': child,
                             'diagnosticSHA256': sha(diagnostic_path) if diagnostic_path.exists() else None,
                             'actionsSHA256': sha(root / 'input-actions.json') if (root / 'input-actions.json').exists() else None,
                             'initialBarrier': session.initial_barrier, 'footerPress': session.quit_press, 'originalLastInputOutcome': session.actions[-1] if session.actions else None})
        postcheck_failures = []
        def final_roster():
            if failure is None and ([f['name'] for f in frames] != ROSTER or len(frames) != 8):
                raise RuntimeError('original checkpoint roster')
        def final_inputs():
            if verify_inputs(args.retained_input_root, expected) != preserved:
                raise RuntimeError('historical inputs changed')
        def final_budget():
            if retained(budget) + sum(retained(p) for p in args.retained_input_root) + RESERVE > LIMIT:
                raise RuntimeError('final native budget')
        # Independent guards always run, even after failed work or another guard.
        for label, check in [('roster', final_roster), ('source', lambda: verify_build(args.binary, build)), ('preserved', final_inputs), ('budget', final_budget)]:
            try:
                check()
            except BaseException as error:
                postcheck_failures.append(label + ': ' + type(error).__name__)
                failure = failure or postcheck_failures[-1]
        write_json(root / 'capture-receipt.json', {'complete': failure is None, 'failure': failure,
                   'receipts': receipts, 'checkpoints': len(frames), 'postcheckFailures': postcheck_failures, 'budgetScope': 'whole fresh runtime directory plus all eight immutable historical runtime inputs',
                   'qualification': 'Original fixed45 Program/PTY/model/fake lifecycle only; no live backend or universal terminal/reap claim'})
    if failure:
        raise RuntimeError('original native45 capture failed; preserve receipts, no retry')


if __name__ == '__main__':
    main()
