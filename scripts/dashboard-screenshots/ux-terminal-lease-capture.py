#!/usr/bin/env python3
"""SOURCE PREPARATION ONLY. Separate review/build/PTY/browser allocation required."""
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
import struct
import subprocess
import termios
import time

REPO = Path(__file__).resolve().parents[2]
SUPPORT_FILE = REPO / 'scripts/dashboard-screenshots/ux-terminal-loop-capture.py'
if hashlib.sha256(SUPPORT_FILE.read_bytes()).hexdigest() != '980fc761382c0de2854f43decb854658c69462ac05ad5b899105585eac8231d6':
    raise RuntimeError('frozen PTY support source changed before import')
spec = importlib.util.spec_from_file_location('_arm_owned_pty_support', SUPPORT_FILE)
support = importlib.util.module_from_spec(spec)
spec.loader.exec_module(support)  # Reviewed definitions only; __main__ not invoked.
GRIDS, LIMIT = support.GRIDS, support.LIMIT
sha, retained, write_json = support.sha, support.retained, support.write_json
FILTER = 'no-synthetic-match'
KEYS = {'i': b'i', 'x': b'x', '/': b'/', 'enter': b'\r', 'esc': b'\x1b',
        'end': b'\x1b[F', 'pgdown': b'\x1b[6~', 'down': b'\x1b[B', 'up': b'\x1b[A'}
EXTRA_SOURCES = {
 'internal/tui/ux_terminal_direct_fixture_test.go',
 'docs/validation/ux-terminal-direct-fixture-plan-2026-10-30.json',
 'docs/validation/ux-terminal-source-classification-2026-10-29.json',
 'internal/tui/ux_terminal_loop_fixture_test.go',
 'scripts/dashboard-screenshots/ux-terminal-loop-capture.py',
 'scripts/dashboard-screenshots/ux-terminal-loop-replay.mjs',
 'docs/validation/ux-terminal-loop-fixture-plan-2026-10-30.json',
 'docs/validation/ux-terminal-loop-replay-correction-plan-2026-10-30.json',
 'internal/tui/ux_terminal_lease_reading_fixture_test.go',
 'scripts/dashboard-screenshots/ux-terminal-lease-capture.py',
 'scripts/dashboard-screenshots/ux-terminal-lease-replay.mjs',
 'docs/validation/ux-terminal-lease-reading-plan-2026-10-30.json'}

PRESERVED_MANIFESTS = {
 '60cf482f0aa12b2b95f6569ffd3608ff60048c57a13bbd86d9ddac632126402d',
 '01283d9b87a645d7c5f443f783ffb21707e8196a583369cd8eb0db3f40dbb3ce',
 'bb963ddd2c5583bc689e5bf3d0916671797b05da9118af78fbb572ef0b12b510',
 '17a72af61c172d6620e77dd42f58b42d0f2c259933ff712a0766c803dcd1da67'}
ROSTER = {
 '72x28':['lease-detail-first','lease-detail-tail','lease-record-exact-number','release-review-first','release-review-tail','filter-draft','leases-filter-empty','leases-restored'],
 '100x35':['lease-detail-first','lease-detail-tail','lease-record-exact-number','release-review-fit','filter-draft','leases-filter-empty','leases-restored'],
 '160x50':['lease-detail-fit','lease-record-exact-number','release-review-fit','filter-draft','leases-filter-empty','leases-restored']}

class LeaseSession(support.Session):
    def __init__(self, root, binary, size, deadline, retained_inputs):
        self.retained_inputs = retained_inputs
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
                   'TMPDIR': str(root / 'tmp'), 'ARM_TUI_LEASE_ROOT': str(root),
                   'ARM_TUI_LEASE_GRID': self.grid}
            # Inherit the original disposable FIFO process group. Outer queue
            # termination therefore includes this child; no setsid/setpgid or
            # detached session can escape that group. Local fallback targets
            # only this retained original Popen handle.
            self.child = subprocess.Popen(
                [str(binary), '-test.run=^TestUXTerminalLeaseReadingFixture$',
                 '-test.timeout=35s', '-test.count=1'],
                cwd=REPO / 'internal/tui', env=env,
                stdin=self.slave, stdout=self.slave, stderr=self.slave)
        except Exception:
            for fd in (self.master, self.slave):
                if fd is not None:
                    os.close(fd)
            self.stream.close()
            raise

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
            if len(self.bytes) + len(data) > 4 * 1024 * 1024 or retained(self.root) + sum(retained(p) for p in self.retained_inputs) + len(data) + 131072 > LIMIT:
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

    def bound(self):
        super().bound()
        if retained(self.root) + sum(retained(p) for p in self.retained_inputs) + 131072 > LIMIT:
            raise RuntimeError('combined retained lease evidence bound')

    def key(self, key, predicate=lambda s: True):
        before = self.load()['inputs']
        os.write(self.master, KEYS[key])
        return self.wait(lambda s: s['inputs'] > before and predicate(s), key)

    def mouse(self, key=None, row=None):
        state = self.load()
        r = next(r for r in state['regions']
                 if (r['key'] == key if key is not None else r['key'] == '' and r['row'] == row)
                 and not any(r[k] for k in ('feed','record','mesh','pool','recovery','picker'))
                 and r['x'] >= 0 and r['y'] >= 0 and r['width'] > 0 and r['height'] > 0
                 and r['x'] + r['width'] <= self.size[0] and r['y'] + r['height'] <= self.size[1])
        before = state['mouseDelivered']
        os.write(self.master, f"\x1b[<0;{r['x']+1};{r['y']+1}M".encode())
        result = self.wait(lambda s: s['mouseDelivered'] > before, 'visible owned lease mouse')
        os.write(self.master, f"\x1b[<0;{r['x']+1};{r['y']+1}m".encode())
        return result

    def paste_filter(self):
        before = self.load()['inputs']
        os.write(self.master, b'\x1b[200~' + FILTER.encode('ascii') + b'\x1b[201~')
        return self.wait(lambda s: s['inputs'] > before and s['pastes'] == 1 and s['inputValue'] == FILTER, 'exact native filter paste')

    def record_path(self, path):
        wanted = json.dumps(path, separators=(',',':'))
        for _ in range(65):
            state = self.load()
            target = state['recordPaths'].index(wanted)
            if target > 128:
                raise RuntimeError('record projection bound')
            if state['recordSelected'] == target:
                return state
            if state['recordKeys'] >= 64:
                raise RuntimeError('native record key bound')
            step = 1 if state['recordSelected'] < target else -1
            expected_selected = state['recordSelected'] + step
            self.key('down' if step > 0 else 'up', lambda s: s['recordOpen'] and s['recordSelected'] == expected_selected)
        raise RuntimeError('record path not reached')

    def checkpoint(self, name, checkpoints):
        if len(checkpoints) >= 21:
            raise RuntimeError('lease reading frame bound')
        super().checkpoint(name, checkpoints)


def covered(frames):
    total = frames[0]['state']['viewport']['total']
    rows = set()
    for f in frames:
        v = f['state']['viewport']
        if v['total'] != total:
            raise RuntimeError('reading content changed during scroll')
        rows.update(range(v['offset'], min(total, v['offset'] + v['height'])))
    if rows != set(range(total)):
        raise RuntimeError('reading logical-row coverage incomplete')
    return {'totalLogicalRows': total, 'coveredLogicalRows': len(rows)}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', required=True, type=Path)
    parser.add_argument('--build-receipt', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--retained-input-root', required=True, action='append', type=Path)
    args = parser.parse_args(); os.umask(0o077)
    root = args.output
    if not root.is_absolute() or root.parent.resolve() != root.parent or root.resolve().is_relative_to(REPO):
        raise RuntimeError('fresh resolved private lease output required')
    inputs = args.retained_input_root
    if len(inputs) != 4 or len(set(inputs)) != 4:
        raise RuntimeError('four preserved runtime input containers required')
    for p in inputs:
        if not p.is_absolute() or p.resolve() != p or not p.is_dir() or p.is_relative_to(REPO) or p.stat().st_mode & 0o077 or root.is_relative_to(p) or p.is_relative_to(root):
            raise RuntimeError('private disjoint retained input required')
    if any(a.is_relative_to(b) or b.is_relative_to(a) for i,a in enumerate(inputs) for b in inputs[i+1:]):
        raise RuntimeError('retained inputs overlap')
    if {sha(p/'artifact-manifest.json') for p in inputs} != PRESERVED_MANIFESTS:
        raise RuntimeError('preserved runtime manifest identity mismatch')
    input_entries = {p: json.loads((p/'artifact-manifest.json').read_text()) for p in inputs}
    if any((p/f).is_symlink() or not (p/f).resolve().is_relative_to(p) or sha(p/f) != h for p,entries in input_entries.items() for f,h in entries.items()):
        raise RuntimeError('preserved runtime artifact changed')
    classification_path = REPO/'docs/validation/ux-terminal-source-classification-2026-10-29.json'
    if sha(classification_path) != '574c62ed1e2d66828082880beeaa4d706b7690cf4f38b1ff3a229aa1165cae5d':
        raise RuntimeError('original classification identity changed')
    build = json.loads(args.build_receipt.read_text())
    classified = json.loads((REPO/'docs/validation/ux-terminal-source-classification-2026-10-29.json').read_text())['sourceEntries']
    required = set(classified) | EXTRA_SOURCES
    if len(required) != 62 or set(build['sourceSHA256']) != required or build['testSelector'] != '^TestUXTerminalLeaseReadingFixture$':
        raise RuntimeError('new reviewed lease compile receipt required; old binary not reusable')
    if not args.binary.is_absolute() or sha(args.binary) != build['binarySHA256']:
        raise RuntimeError('owned lease binary digest')
    if any(sha(REPO/f) != digest for f,digest in build['sourceSHA256'].items()) or any(build['sourceSHA256'][f] != item['sha256'] for f,item in classified.items()):
        raise RuntimeError('lease source cut changed')
    root.mkdir(mode=0o700);(root/'tmp').mkdir(mode=0o700);write_json(root/'build-receipt.json',build)
    checkpoints,receipts = [],[];failure=None;deadline=time.monotonic()+70
    try:
        for size in GRIDS:
            if retained(root)+sum(retained(p) for p in inputs)+131072>LIMIT:
                raise RuntimeError('combined native budget before child launch')
            session = LeaseSession(root,args.binary,size,deadline,inputs)
            error=None;coverage={}
            try:
                session.wait(lambda s:s['initSuppressed'] and s['revision']>=2 and s['restored'],'lease ready')
                session.mouse(row=0);session.wait(lambda s:s['detailOpen'] and s['readClosures']==1,'original lease inspect')
                start=len(checkpoints);v=session.load()['viewport']
                session.checkpoint('lease-detail-first' if v['total']>v['height'] else 'lease-detail-fit',checkpoints)
                if v['total']>v['height']:
                    session.key('end',lambda s:s['viewport']['offset']==s['viewport']['total']-s['viewport']['height'])
                    session.checkpoint('lease-detail-tail',checkpoints)
                coverage['detail']=covered(checkpoints[start:])
                session.key('i',lambda s:s['recordOpen'])
                session.record_path(['request']);session.key('enter',lambda s:'["request","resources"]' in s['recordPaths'])
                session.record_path(['request','resources']);session.key('enter',lambda s:'["request","resources","memory_bytes"]' in s['recordPaths'])
                session.record_path(['request','resources','memory_bytes'])
                session.wait(lambda s:s.get('recordValueType')=='json.Number' and s.get('recordValueDecimal')=='9007199254740993','typed decimal')
                session.checkpoint('lease-record-exact-number',checkpoints)
                session.key('esc',lambda s:not s['recordOpen'] and s['detailOpen'])
                session.key('x',lambda s:s['reviewOpen'] and s['reviewAction']=='release' and not s['pending'])
                start=len(checkpoints);v=session.load()['viewport']
                session.checkpoint('release-review-first' if v['total']>v['height'] else 'release-review-fit',checkpoints)
                if v['total']>v['height']:
                    session.key('end',lambda s:s['viewport']['offset']==s['viewport']['total']-s['viewport']['height'])
                    session.checkpoint('release-review-tail',checkpoints)
                coverage['review']=covered(checkpoints[start:])
                session.mouse(key='n');session.wait(lambda s:not s['reviewOpen'] and s['detailOpen'],'original decline')
                session.key('esc',lambda s:not s['detailOpen'])
                session.key('/',lambda s:s['inputMode']=='filter');session.paste_filter()
                session.checkpoint('filter-draft',checkpoints)
                session.key('enter',lambda s:s['filter']==FILTER and s['visibleLeaseIDs']==[])
                session.checkpoint('leases-filter-empty',checkpoints)
                session.key('esc',lambda s:s['restored'])
                session.checkpoint('leases-restored',checkpoints)
                if [c['name'] for c in checkpoints if c['grid']==session.grid] != ROSTER[session.grid]:
                    raise RuntimeError('native lease roster geometry drift')
            except Exception as exc:
                error=type(exc).__name__+': '+str(exc)
            finally:
                cleanup=session.finish();p=root/session.grid/'child-final.json';child=json.loads(p.read_text()) if p.exists() else None
                receipts.append({'grid':session.grid,'error':error,'cleanup':cleanup,'originalChild':child,'readingCoverage':coverage})
                write_json(root/'capture-receipt.json',{'complete':False,'receipts':receipts,'failure':error})
            positive=['testFailed','programStarted','programReturned','sameWrapperReturned','sameModel','restored','quitRequested','initSuppressed','sawExactNumber','sawReview','declined','fakeClosed','contextCanceled']
            if error or not child or child['testFailed'] or not all(child[k] for k in positive if k!='testFailed') or child['pastes']!=1 or child['recordKeys']>64 or child['mouseProduced']!=2 or child['mouseDelivered']!=2 or child['mouseActive']!=0 or child['reads']!=1 or len(child['calls'])!=1 or child['calls'][0]['Operation']!='inspect' or child['activeFakeCalls']!=0 or child['forbidden'] or cleanup.get('childExitCode')!=0 or not all(cleanup[k] for k in ['childWaited','finalDrainAfterChildJoin','termiosRestored','slaveClosed','masterClosed']) or not(cleanup['finalMasterEOFObserved'] or cleanup['finalMasterEIOObserved']) or cleanup['finalDrainTimedOut'] or cleanup['ownedChildTerminationRequested'] or cleanup['cleanupErrors'] or cleanup.get('cleanupOutputTruncated'):
                raise RuntimeError('original lease child/capture/cleanup failed; no replacement')
        if len(checkpoints)!=21 or retained(root)+sum(retained(p) for p in inputs)>LIMIT:
            raise RuntimeError('native lease roster/retention bound')
        if any(sha(REPO/f)!=h for f,h in build['sourceSHA256'].items()):raise RuntimeError('lease source changed during capture')
        if any(sha(p/f)!=h for p,entries in input_entries.items() for f,h in entries.items()):raise RuntimeError('preserved input changed during lease capture')
    except Exception as exc:failure=type(exc).__name__+': '+str(exc)
    finally:write_json(root/'capture-receipt.json',{'complete':failure is None,'failure':failure,'receipts':receipts,'checkpoints':len(checkpoints),'qualification':'Original lease reading Program/PTY/fake only; no mutation/native transport/universal reap'})
    if failure:raise RuntimeError('original lease capture failed; inspect preserved receipt')

if __name__=='__main__':main()
