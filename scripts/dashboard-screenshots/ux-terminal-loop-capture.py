#!/usr/bin/env python3
"""SOURCE PREPARATION ONLY. Run only after code review and a PTY allocation.

Consumes a separately built, reviewed test binary/receipt. Never builds ARM,
starts a backend, accesses default state or invokes the production CLI.
"""
import argparse
import errno
import fcntl
import hashlib
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
GRIDS = ((72, 28), (100, 35), (160, 50))
LIMIT = 16 * 1024 * 1024
KEYS = {'f': b'f', 'o': b'o', 'z': b'z', 'esc': b'\x1b',
        'up': b'\x1b[A', 'down': b'\x1b[B', 'pgup': b'\x1b[5~', 'pgdown': b'\x1b[6~',
        'end': b'\x1b[F', 'f12': b'\x1b[24~'}


def sha(path):
    value = hashlib.sha256()
    with path.open('rb') as source:
        for block in iter(lambda: source.read(1024 * 1024), b''):
            value.update(block)
    return value.hexdigest()


def write_json(path, value):
    raw = (json.dumps(value, indent=2) + '\n').encode()
    path.write_bytes(raw)
    path.chmod(0o600)


def retained(root):
    total = 0
    for p in root.rglob('*'):
        try:
            if p.is_file():
                total += p.stat().st_size
        except FileNotFoundError:
            pass  # Owned progress.json.new may be atomically renamed.
    return total


class Session:
    def __init__(self, root, binary, size, deadline):
        self.root, self.size, self.deadline = root, size, deadline
        self.grid = f'{size[0]}x{size[1]}'
        self.progress = root / self.grid / 'progress.json'
        self.master = self.slave = None
        self.child = None
        self.bytes = bytearray()
        self.resizes = []
        self.end_effects = []
        self.state = None
        self.last_drain_observation = None
        self.cleanup = {'childWaited': False, 'termiosRestored': None,
                        'masterClosed': False, 'slaveClosed': False,
                        'ownedChildTerminationRequested': False,
                        'finalMasterEOFObserved': False, 'finalMasterEIOObserved': False,
                        'finalDrainQuietPolls': 0, 'finalDrainTimedOut': False}
        self.stream = (root / (self.grid + '.pty')).open('wb')
        self.stream_path = self.grid + '.pty'
        try:
            self.master, self.slave = pty.openpty()
            self.mode = termios.tcgetattr(self.slave)
            self.resize(size, initial=True)
            env = {'TERM': 'xterm-256color', 'COLORTERM': 'truecolor',
                   'GOMAXPROCS': '2', 'GOMEMLIMIT': '1536MiB',
                   'GOENV': 'off', 'GOFLAGS': '', 'GOTOOLCHAIN': 'local',
                   'GOPROXY': 'off', 'GOSUMDB': 'off',
                   'TMPDIR': str(root / 'tmp'), 'ARM_TUI_LOOP_ROOT': str(root),
                   'ARM_TUI_LOOP_GRID': self.grid}
            # Inherit the original disposable FIFO process group. Outer queue
            # termination therefore includes this child; no setsid/setpgid or
            # detached session can escape that group. Local fallback targets
            # only this retained original Popen handle.
            self.child = subprocess.Popen(
                [str(binary), '-test.run=^TestUXTerminalLoopFixture$',
                 '-test.timeout=35s', '-test.count=1'],
                cwd=REPO / 'internal/tui', env=env,
                stdin=self.slave, stdout=self.slave, stderr=self.slave)
        except Exception:
            for fd in (self.master, self.slave):
                if fd is not None:
                    os.close(fd)
            self.stream.close()
            raise

    def bound(self):
        if time.monotonic() >= self.deadline:
            raise RuntimeError('owned capture deadline')
        if len(self.bytes) > 4 * 1024 * 1024 or retained(self.root) > LIMIT:
            raise RuntimeError('retained byte bound')

    def drain(self, wait=0.03, cleanup=False):
        if not cleanup:
            self.bound()
        self.last_drain_observation = 'quiet'
        if select.select([self.master], [], [], wait)[0]:
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
            if len(self.bytes) + len(data) > 4 * 1024 * 1024 or retained(self.root) + len(data) > LIMIT:
                if cleanup:
                    self.cleanup['cleanupOutputTruncated'] = True
                    self.cleanup['cleanupOutputDroppedBytes'] = self.cleanup.get('cleanupOutputDroppedBytes', 0) + len(data)
                    return True
                raise RuntimeError('next PTY block exceeds byte bound')
            self.bytes.extend(data)
            self.stream.write(data)
            self.stream.flush()
            if not cleanup and b'\x1b]52;' in self.bytes:
                raise RuntimeError('clipboard OSC in original output')
            return True
        return False

    def load(self):
        if self.progress.exists():
            self.state = json.loads(self.progress.read_text())
        return self.state

    def wait(self, predicate, label):
        end = min(self.deadline, time.monotonic() + 5)
        while time.monotonic() < end:
            self.drain()
            state = self.load()
            if state and predicate(state):
                return state
            if self.child.poll() is not None:
                raise RuntimeError('original child ended before ' + label)
        raise RuntimeError('original state deadline: ' + label)

    def settle(self):
        # Bounded observed output quiescence, not an assertion all renderer or
        # framework goroutines have joined. Replay later checks the real cells.
        quiet = time.monotonic()
        end = min(self.deadline, quiet + 2)
        while time.monotonic() < end:
            if self.drain():
                quiet = time.monotonic()
            if time.monotonic() - quiet >= 0.15:
                return
        raise RuntimeError('original output did not quiesce')

    def key(self, key, predicate=lambda s: True):
        before = self.load()['inputs']
        os.write(self.master, KEYS[key])
        return self.wait(lambda s: s['inputs'] > before and predicate(s), key)

    def end_tail(self):
        before = self.load()
        after = self.key('end', lambda s: s['reader']['following'])
        effect = {'readClosureDelta': after['readClosures'] - before['readClosures'],
                  'transportCallDelta': len(after['calls']) - len(before['calls']),
                  'cursorSame': after['reader']['cursor'] == before['reader']['cursor'],
                  'bufferSame': after['reader']['bufferSHA256'] == before['reader']['bufferSHA256'],
                  'sameReader': after['sameReader']}
        self.end_effects.append(effect)
        if effect != {'readClosureDelta': 1, 'transportCallDelta': 0,
                      'cursorSame': True, 'bufferSame': True, 'sameReader': True}:
            raise RuntimeError('End cached-tail command/read accounting differs')
        return after

    def mouse(self, key=None, row=None):
        state = self.load()
        region = next(r for r in state['regions']
                      if (r['key'] == key if key is not None else r['key'] == '' and r['row'] == row)
                      and not any(r[k] for k in ('feed', 'record', 'mesh', 'pool', 'recovery', 'picker')))
        before = state['mouseDelivered']
        # Actual SGR mouse press at a source-derived cell. The original Program
        # parser/renderer OnMouse callback produces the typed dashboard event.
        os.write(self.master, f"\x1b[<0;{region['x'] + 1};{region['y'] + 1}M".encode())
        state = self.wait(lambda s: s['mouseDelivered'] > before, 'native mouse')
        os.write(self.master, f"\x1b[<0;{region['x'] + 1};{region['y'] + 1}m".encode())
        return state

    def resize(self, size, initial=False):
        if size not in GRIDS:
            raise RuntimeError('grid outside exact validator')
        if not initial:
            self.settle()
            self.resizes.append({'offset': len(self.bytes), 'cols': size[0], 'rows': size[1]})
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack('HHHH', size[1], size[0], 0, 0))
        if self.child is not None:
            self.child.send_signal(signal.SIGWINCH)
            self.wait(lambda s: s['grid'] == list(size), 'native resize')

    def scroll(self, target):
        for _ in range(64):
            state = self.load()
            offset = state['viewport']['offset']
            if target == 'first' and offset == 0:
                return
            if target == 'middle' and offset >= max(1, (state['viewport']['total'] - state['viewport']['height']) // 2):
                return
            remaining = max(0, (state['viewport']['total'] - state['viewport']['height']) // 2 - offset)
            key = 'pgup' if target == 'first' else 'pgdown' if remaining > state['viewport']['height'] // 2 else 'down'
            self.key(key, lambda s: s['viewport']['offset'] != offset)
        raise RuntimeError('bounded genuine scroll did not reach target')

    def checkpoint(self, name, all_checkpoints):
        self.settle()
        state = self.load()
        if len(all_checkpoints) >= 48 or state['grid'] != list(self.size):
            raise RuntimeError('checkpoint/grid bound')
        if state['mouseActive'] or state['mouseProduced'] != state['mouseDelivered']:
            raise RuntimeError('unacknowledged original mouse closure')
        item = {'name': name, 'grid': self.grid, 'cols': self.size[0], 'rows': self.size[1],
                'stream': self.stream_path, 'bytesThrough': len(self.bytes),
                'prefixSHA256': hashlib.sha256(self.bytes).hexdigest(),
                'resizes': list(self.resizes), 'state': state}
        all_checkpoints.append(item)
        write_json(self.root / 'checkpoints.json', all_checkpoints)
        self.bound()

    def finish(self):
        errors = []

        def request_termination(kind, original_method):
            self.cleanup['ownedChildTerminationRequested'] = True
            request = {'kind': kind, 'originalAPIReturned': False}
            self.cleanup.setdefault('childTerminationRequests', []).append(request)
            try:
                original_method()
                request['originalAPIReturned'] = True
            except Exception as error:
                request['errorName'] = type(error).__name__
                errors.append('original-child-' + kind + '-unknown')

        try:
            if self.child is not None and self.child.poll() is None:
                try:
                    os.write(self.master, b'q')
                except OSError:
                    errors.append('quit-write')
                end = time.monotonic() + 2.5
                while self.child.poll() is None and time.monotonic() < end:
                    try:
                        self.drain(cleanup=True)
                    except Exception:
                        errors.append('cleanup-drain')
                        time.sleep(0.02)
                if self.child.poll() is None:
                    request_termination('terminate', self.child.terminate)
                    try:
                        self.child.wait(timeout=2)
                    except subprocess.TimeoutExpired:
                        request_termination('kill', self.child.kill)
                        try:
                            self.child.wait(timeout=2)
                        except Exception:
                            errors.append('original-child-wait-unknown')
                    except Exception:
                        errors.append('original-child-wait-unknown')
            if self.child is not None:
                try:
                    code = self.child.wait(timeout=1)
                    self.cleanup.update(childWaited=True, childExitCode=code)
                except Exception:
                    errors.append('original-child-wait-unknown')
            # Observe saved terminal mode before closing the parent's original
            # slave. Quiet select while a slave is held is not an EOF proof.
            try:
                restored = termios.tcgetattr(self.slave)
                restored[3] &= ~getattr(termios, 'PENDIN', 0)
                self.mode[3] &= ~getattr(termios, 'PENDIN', 0)
                self.cleanup['termiosRestored'] = restored == self.mode
            except OSError:
                errors.append('terminal-mode-unknown')
            try:
                os.close(self.slave)
                self.cleanup['slaveClosed'] = True
            except OSError:
                errors.append('slave-close-unknown')
            finally:
                self.slave = None
            # Only now, after the original child wait and parent slave close,
            # distinguish actual EOF/EIO from quiet polls. Preserve truncation.
            self.cleanup['finalDrainAfterChildJoin'] = self.cleanup['childWaited']
            drain_end = time.monotonic() + 1
            try:
                while time.monotonic() < drain_end:
                    self.drain(0.05, cleanup=True)
                    outcome = self.last_drain_observation
                    if outcome == 'eof':
                        self.cleanup['finalMasterEOFObserved'] = True
                        break
                    if outcome == 'eio':
                        self.cleanup['finalMasterEIOObserved'] = True
                        break
                    if outcome == 'quiet':
                        self.cleanup['finalDrainQuietPolls'] += 1
                else:
                    self.cleanup['finalDrainTimedOut'] = True
                    errors.append('final-master-no-eof-within-bound')
            except Exception:
                errors.append('final-master-drain-unknown')
        finally:
            try:
                self.stream.close()
            except OSError:
                errors.append('original-stream-close-unknown')
            for name in ('master', 'slave'):
                fd = getattr(self, name)
                if fd is not None:
                    try:
                        os.close(fd)
                        self.cleanup[name + 'Closed'] = True
                    except OSError:
                        errors.append(name + '-close-unknown')
                    setattr(self, name, None)
        self.cleanup['cleanupErrors'] = errors
        return self.cleanup


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', required=True, type=Path)
    parser.add_argument('--build-receipt', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    os.umask(0o077)
    root = args.output
    if not root.is_absolute() or root.parent.resolve() != root.parent or root.resolve().is_relative_to(REPO):
        raise RuntimeError('fresh resolved output outside source required')
    build = json.loads(args.build_receipt.read_text())
    if not args.binary.is_absolute() or sha(args.binary) != build['binarySHA256']:
        raise RuntimeError('reviewed owned binary digest mismatch')
    for file, digest in build['sourceSHA256'].items():
        path = (REPO / file).resolve()
        if not path.is_relative_to(REPO) or sha(path) != digest:
            raise RuntimeError('compiled source-cut mismatch')
    # Review requires a complete exact source cut; no caller can omit guards.
    classified = json.loads((REPO / 'docs/validation/ux-terminal-source-classification-2026-10-29.json').read_text())['sourceEntries']
    required = set(classified) | {'internal/tui/ux_terminal_direct_fixture_test.go',
        'docs/validation/ux-terminal-source-classification-2026-10-29.json',
        'docs/validation/ux-terminal-direct-fixture-plan-2026-10-30.json',
        'internal/tui/ux_terminal_loop_fixture_test.go',
        'scripts/dashboard-screenshots/ux-terminal-loop-capture.py',
        'scripts/dashboard-screenshots/ux-terminal-loop-replay.mjs',
        'docs/validation/ux-terminal-loop-fixture-plan-2026-10-30.json'}
    if build.get('testSelector') != '^TestUXTerminalLoopFixture$' or set(build['sourceSHA256']) != required:
        raise RuntimeError('build receipt does not bind the complete reviewed cut')
    for file, item in classified.items():
        if build['sourceSHA256'][file] != item['sha256']:
            raise RuntimeError('classified product input changed')
    root.mkdir(mode=0o700)  # Existing evidence is never reused or cleared.
    (root / 'tmp').mkdir(mode=0o700)
    write_json(root / 'build-receipt.json', build)
    checkpoints, receipts = [], []
    failure = None
    deadline = time.monotonic() + 70
    try:
        for size in GRIDS:
            session = Session(root, args.binary, size, deadline)
            error = None
            try:
                session.wait(lambda s: s['initSuppressed'] and s['grid'] == list(size) and s['revision'] >= 2, 'program ready')
                session.checkpoint('leases-native', checkpoints)
                session.mouse(key='nav7')
                session.checkpoint('runs-native', checkpoints)
                session.mouse(row=0)
                session.wait(lambda s: s.get('reader', {}).get('done') is True, 'original drain')
                baseline = session.load()['reader']
                session.checkpoint('inline-follow-tail', checkpoints)
                session.key('f', lambda s: not s['reader']['following'])
                session.checkpoint('inline-paused-tail', checkpoints)
                session.scroll('first')
                session.checkpoint('inline-first', checkpoints)
                session.scroll('middle')
                session.checkpoint('inline-middle', checkpoints)
                session.end_tail()
                session.checkpoint('inline-end-tail', checkpoints)
                session.key('f', lambda s: not s['reader']['following'])
                session.key('o', lambda s: s['reader']['folded'])
                prior_offset = session.load()['viewport']['offset']
                session.checkpoint('folded', checkpoints)
                session.key('z', lambda s: s['reader']['full'] and not s['reader']['folded'])
                session.scroll('first')
                session.checkpoint('full-first', checkpoints)
                session.scroll('middle')
                session.key('f12', lambda s: s['stalePrepared'])
                other = (100, 35) if size == (72, 28) else (72, 28)
                session.resize(other)
                session.wait(lambda s: s['staleRejected'], 'original delayed-callback guard')
                session.resize(size)
                session.checkpoint('full-middle-resize', checkpoints)
                session.end_tail()
                session.checkpoint('full-end-tail', checkpoints)
                session.key('f', lambda s: not s['reader']['following'])
                session.key('esc', lambda s: not s['reader']['full'] and s['reader']['folded'])
                final = session.load()
                if final['viewport']['offset'] != prior_offset or any(final['reader'][k] != baseline[k] for k in ('cursor', 'bufferSHA256', 'bufferBytes')):
                    raise RuntimeError('original full-view return changed reader')
                session.checkpoint('return-folded', checkpoints)
            except Exception as exc:
                error = type(exc).__name__ + ': ' + str(exc)
            finally:
                cleanup = session.finish()
                child_path = root / session.grid / 'child-final.json'
                child = json.loads(child_path.read_text()) if child_path.exists() else None
                receipts.append({'grid': session.grid, 'error': error, 'cleanup': cleanup, 'originalChild': child, 'endEffects': session.end_effects})
                write_json(root / 'capture-receipt.json', {'complete': False, 'receipts': receipts, 'failure': error})
            if error or cleanup.get('childExitCode') != 0 or not cleanup['childWaited'] or not cleanup['finalDrainAfterChildJoin'] or not cleanup['termiosRestored'] or cleanup['ownedChildTerminationRequested'] or cleanup.get('cleanupErrors') or not (cleanup['finalMasterEOFObserved'] or cleanup['finalMasterEIOObserved']) or not cleanup['slaveClosed'] or not cleanup['masterClosed'] or cleanup.get('cleanupOutputTruncated') or not child or child['testFailed'] or not child['programReturned'] or not child['sameWrapperReturned']:
                raise RuntimeError('original owned child/capture/cleanup failed; no replacement')
        if len(checkpoints) != 36 or retained(root) > LIMIT:
            raise RuntimeError('capture roster/retention bound')
        if any(sha(REPO / f) != h for f, h in build['sourceSHA256'].items()):
            raise RuntimeError('source changed during original capture')
    except Exception as exc:
        failure = type(exc).__name__ + ': ' + str(exc)
    finally:
        write_json(root / 'capture-receipt.json', {'complete': failure is None, 'failure': failure, 'receipts': receipts, 'checkpoints': len(checkpoints), 'retainedBytes': retained(root), 'qualification': 'Original owned Program/PTY handles only; no production Run/native auth or blanket OS descendant reap.'})
    if failure:
        raise RuntimeError('original capture failed; inspect preserved private receipt')


if __name__ == '__main__':
    main()
