#!/usr/bin/env python3
"""Native public CLI acceptance for B→A→C SSH without B–C enrollment.

Run on native macOS or Linux with a complete CPU quota hierarchy. Fixtures
use only private homes and public CLI commands. A failed fixture is retained
for inspection.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import shutil
import statistics
import subprocess
import sys
import tempfile
import time
import uuid

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('workspace_acceptance', Path(__file__).with_name('test-workspaces.py'))
ws = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ws)
mesh = ws.mesh


class Node(mesh.Node):
    def start(self):
        self.cli('start', '--cpu', '0.2', '--memory', '128MiB', '--disk', '1GiB')


def error(result):
    assert result.returncode != 0, (result.stdout, result.stderr)
    for line in reversed(result.stderr.splitlines()):
        if line.startswith('{'):
            return json.loads(line)['error']['code']
    raise AssertionError(('missing structured CLI error', result.returncode, result.stdout, result.stderr))


def benchmark_environment():
    try:
        cpu_scope = {'kind': 'process_affinity', 'count': len(os.sched_getaffinity(0))}
    except (AttributeError, OSError):
        cpu_scope = {'kind': 'os_reported', 'count': os.cpu_count()}
    return {'os': platform.system(), 'architecture': platform.machine(),
            'kernel': platform.release(), 'cpu_scope': cpu_scope,
            'topology': 'local daemons on loopback; B-C has no mesh enrollment during routed samples; direct B-C enrollment is added afterward for the baseline'}


def file_sha256(path):
    digest = hashlib.sha256()
    with Path(path).open('rb') as source:
        for block in iter(lambda: source.read(1024 * 1024), b''):
            digest.update(block)
    return digest.hexdigest()



def source_intent_published(home, source, target, key):
    """Observe the fixture producer without blocking its TLS reply on a CLI.

    This is fault-injection evidence only; it grants no execution authority.
    Dispatch, lookup, seal and no-replay assertions still use the public CLI.
    """
    for path in (Path(home) / 'mesh').glob('ssh-client-*.json'):
        try:
            if path.is_symlink():
                continue
            with path.open('rb') as stream:
                raw = stream.read((64 << 10) + 1)
            if len(raw) > 64 << 10:
                continue
            intent = json.loads(raw)
            request = intent.get('request', {})
            pin = request.get('pin', {})
            if (intent.get('schema') == 1 and intent.get('source') == source
                    and request.get('key') == key and pin.get('source') == source
                    and pin.get('target') == target):
                return True
        except (OSError, ValueError, AttributeError):
            continue
    return False


def finish_fixture(root, nodes, ciphertext, success, original_error):
    failures = []
    try:
        ciphertext.close()
    except Exception as exc:
        failures.append(f'ciphertext relay did not close: {exc}')
    for node in nodes:
        try:
            if not node.stop():
                failures.append(f'{node.name} stop/ownership closure unconfirmed')
        except Exception as exc:
            failures.append(f'{node.name} stop failed: {exc}')
    if success and not original_error and not failures:
        try:
            shutil.rmtree(root)
        except OSError as exc:
            failures.append(f'fixture removal failed: {exc}')
    if failures or not success or original_error:
        print('Retained routed SSH CLI fixture:', root, file=sys.stderr)
        for failure in failures:
            print('Cleanup failure:', failure, file=sys.stderr)
    # A failing test keeps its original exception; a passing test may never
    # report success when process ownership or fixture removal is uncertain.
    if not original_error and (failures or not success):
        raise RuntimeError('routed SSH acceptance cleanup unconfirmed')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', required=True)
    parser.add_argument('--benchmark', action='store_true',
                        help='measure three 32 MiB routed samples, then an explicitly enrolled direct baseline')
    args = parser.parse_args()
    binary = str(Path(args.arm).resolve())
    input_paths = {'binary': Path(binary), 'harness': Path(__file__).resolve(),
                   'workspace_harness': Path(__file__).with_name('test-workspaces.py').resolve(),
                   'mesh_harness': Path(__file__).with_name('test-mesh.py').resolve()}
    input_hashes = {name: file_sha256(path) for name, path in input_paths.items()}
    receipt = {'environment': benchmark_environment(), 'sha256': input_hashes}
    root = Path(tempfile.mkdtemp(prefix='arm-routed-ssh-cli-')).resolve()
    root.chmod(0o700)
    (root / 'acceptance-inputs.json').write_text(json.dumps(receipt, sort_keys=True) + '\n')
    a, b, c = [Node(binary, root, name) for name in ('relay-a', 'source-b', 'target-c')]
    nodes = [a, b, c]
    ciphertext = ws.ProofReplyRelay(('127.0.0.1', mesh.free_port()))
    success = False
    final_report = None
    try:
        for node in nodes:
            node.start()
            port = mesh.free_port()
            if node is c:
                ciphertext.upstream = ('127.0.0.1', port)
            node.identity = node.cli('mesh', 'init', '--name', node.name,
                                     '--listen', f'127.0.0.1:{port}',
                                     '--address', ciphertext.address if node is c else f'127.0.0.1:{port}', '--yes')
        ws.enroll(a, b)
        ws.enroll(a, c)
        def decide(node, operation):
            return node.cli('workspaces', 'decide', '--workspace', operation['workspace'],
                            '--key', operation['key'], '--revision', str(operation['revision']),
                            '--review', operation['review_sha256'], '--approve', '--yes')
        create = a.cli('workspaces', 'create', '--name', 'Private routed SSH CLI',
                       '--key', 'create', '--reason', 'native B through A to C')
        wid = create['workspace']
        decide(a, create)
        invite = root / 'invitation.json'
        a.cli('workspaces', 'invitation', '--workspace', wid, '--output', str(invite))
        for node in (b, c):
            join = node.cli('workspaces', 'join', '--invitation', str(invite),
                            '--key', 'join', '--reason', 'original host membership')
            decide(node, join)
            consent = root / f'{node.name}-consent.json'
            node.cli('workspaces', 'consent', '--workspace', wid, '--key', 'join', '--output', str(consent))
            command = ['workspaces', 'admit', '--workspace', wid, '--consent', str(consent),
                       '--key', f'admit-{node.name}', '--reason', 'exact current candidate']
            review = a.cli(*command)
            admitted = a.cli(*command, '--review', review['review_sha256'], '--yes')
            assert admitted['phase'] == 'completed', admitted
            node.cli('workspaces', 'refresh', '--workspace', wid)
            current = node.cli('workspaces', 'lookup', '--workspace', wid, '--key', 'join')['operation']
            node.cli('workspaces', 'finalize', '--workspace', wid, '--key', 'join',
                     '--revision', str(current['revision']))
        for node in (b, c):
            node.cli('workspaces', 'refresh', '--workspace', wid)
            peers = node.cli('mesh', 'peers')
            assert len(peers) == 1 and peers[0]['identity']['host'] == a.identity['host'], peers
        generation = a.cli('workspaces', 'inspect', '--workspace', wid,
                           '--section', 'members', '--limit', '1')['workspace']['generation']
        for node in (b, c):
            node.cli('ssh', 'setup', '--reason', 'private routed SSH CLI fixture', '--yes')
        a.cli('mesh', 'ssh-relay', 'grant-relay', '--workspace', wid,
              '--generation', generation, '--relay-cpu', '0.05', '--relay-memory', '8MiB',
              '--reason', 'private native SSH transit', '--yes')
        c.cli('mesh', 'ssh-relay', 'grant-destination', '--workspace', wid,
              '--source', b.identity['host'], '--cpu', '0.1', '--ttl', '120s' if args.benchmark else '30s',
              '--reason', 'exact original source SSH execution', '--yes')
        command = ['ssh', c.identity['host'], '--workspace', wid, '--reason', 'native public routed SSH',
                   '--ttl', '15s', '--cpu', '0.05', '--key', 'routed-exec', '--yes',
                   '--', 'printf routed-stdout; printf routed-stderr >&2; exit 23']
        result = b.result(*command, timeout=35)
        assert (result.returncode, result.stdout, result.stderr) == (23, 'routed-stdout', 'routed-stderr'), result
        assert error(b.result(*command, timeout=15)) == 'ssh_intent_exists'
        lookup = b.cli('ssh', 'lookup', c.identity['host'], '--key', 'routed-exec')
        assert lookup.get('source') == 'local' and lookup['receipt']['found'], lookup
        if args.benchmark:
            size = 32 * 1024 * 1024
            expected = hashlib.sha256(bytes(size)).hexdigest()
            def measure(label, routed):
                samples = []
                for index in range(3):
                    argv = ['ssh', c.identity['host']]
                    if routed:
                        argv += ['--workspace', wid]
                    argv += ['--reason', 'native scoped SSH throughput sample', '--ttl', '60s',
                             '--cpu', '0.05', '--key', f'{label}-{index}', '--yes']
                    if not routed:
                        argv += ['--update-pin']
                    argv += ['--', f'head -c {size} /dev/zero']
                    started = time.perf_counter()
                    result = subprocess.run(b.prefix + argv, capture_output=True, timeout=90)
                    elapsed = time.perf_counter() - started
                    assert result.returncode == 0 and result.stderr == b'', (label, index, result.returncode, result.stderr[:400])
                    assert len(result.stdout) == size and hashlib.sha256(result.stdout).hexdigest() == expected
                    samples.append({'seconds': round(elapsed, 6),
                                    'mib_per_second': round(size / (1024 * 1024) / elapsed, 3)})
                return samples
            routed_samples = measure('routed-bench', True)
            # Establish a separate direct grant only after routed samples.
            pair = uuid.uuid4().hex + uuid.uuid4().hex
            grant = {'permissions': ['ssh-exec', 'lease-own'], 'limits': {'cpu': 0.1},
                     'max_ttl_ns': 120 * mesh.SECOND}
            for target, source in ((b, c), (c, b)):
                target.bootstrap(Action='pair', Identity=source.identity, Grant=grant,
                                 Pair=pair, Reason='separate same-guest direct throughput baseline')
            for source, target in ((b, c), (c, b)):
                source.call(target.identity['host'], 'hello', {'pair': pair})
            for target, source in ((b, c), (c, b)):
                target.bootstrap(Action='activate', Host=source.identity['host'], Pair=pair)
            direct_samples = measure('direct-bench', False)
            result = {'environment': benchmark_environment(),
                      'cli_sha256': file_sha256(binary),
                      'harness_sha256': file_sha256(__file__),
                      'bytes_per_sample': size, 'samples_per_path': 3,
                      'routed_path': ['B', 'A', 'C'], 'routed': routed_samples,
                      'direct_path': ['B', 'C'], 'direct': direct_samples,
                      'routed_median_mib_per_second': round(statistics.median(x['mib_per_second'] for x in routed_samples), 3),
                      'direct_median_mib_per_second': round(statistics.median(x['mib_per_second'] for x in direct_samples), 3)}
            final_report = json.dumps(result, sort_keys=True)
            success = True
            return
        marker = root / 'executed-once'
        def drop_after_effect():
            if ciphertext.proof_armed and marker.exists():
                ciphertext.proof_armed = False
                ciphertext.dropped.set()
                return True
            return False
        ciphertext.should_drop_proof = drop_after_effect
        ciphertext.proof_armed = True
        uncertain_command = ['ssh', c.identity['host'], '--workspace', wid,
                             '--reason', 'lost routed terminal receipt', '--ttl', '15s',
                             '--cpu', '0.05', '--key', 'lost-receipt', '--yes',
                             '--', f'printf x >> "{marker}"']
        uncertain = b.result(*uncertain_command, timeout=35)
        assert uncertain.returncode != 0, uncertain
        assert ciphertext.dropped.wait(2) and marker.read_text() == 'x'
        assert error(b.result(*uncertain_command, timeout=15)) == 'ssh_intent_exists'
        deadline = time.monotonic() + 10
        while True:
            resolved = b.cli('ssh', 'lookup', c.identity['host'], '--key', 'lost-receipt')
            if resolved.get('source') == 'local' and resolved['receipt']['found']:
                break
            assert time.monotonic() < deadline, resolved
            time.sleep(.1)
        assert marker.read_text() == 'x', 'lookup replayed the remote command'
        before_marker = root / 'never-admitted-marker'
        def drop_after_source_intent():
            if not ciphertext.proof_armed:
                return False
            if not source_intent_published(b.home, b.identity['host'],
                                           c.identity['host'], 'lost-before-admission'):
                return False
            ciphertext.proof_armed = False
            ciphertext.dropped.set()
            return True
        ciphertext.should_drop_proof = drop_after_source_intent
        ciphertext.dropped.clear()
        ciphertext.proof_armed = True
        absent = b.result('ssh', c.identity['host'], '--workspace', wid,
                          '--reason', 'lost routed admission response', '--ttl', '15s',
                          '--cpu', '0.05', '--key', 'lost-before-admission', '--yes',
                          '--', f'printf forbidden > "{before_marker}"', timeout=35)
        assert error(absent) == 'ssh_outcome_unknown', absent
        assert ciphertext.dropped.wait(2) and not before_marker.exists(), {
            'fault_dropped': ciphertext.dropped.is_set(), 'marker_exists': before_marker.exists(),
            'returncode': absent.returncode, 'stderr': absent.stderr[:2048]}
        assert error(b.result('ssh', c.identity['host'], '--workspace', wid,
                              '--reason', 'replacement refused', '--ttl', '15s',
                              '--cpu', '0.05', '--key', 'replacement-refused', '--yes',
                              '--', 'printf forbidden', timeout=15)) == 'ssh_intent_exists'
        sealed = b.cli('ssh', 'seal', c.identity['host'], '--key', 'lost-before-admission',
                       '--reason', 'prevent late original execution', '--yes')
        assert sealed['state'] == 'sealed' and not before_marker.exists(), sealed
        peers = b.cli('mesh', 'peers')
        assert len(peers) == 1 and peers[0]['identity']['host'] == a.identity['host'], peers
        c.cli('mesh', 'ssh-relay', 'revoke-destination', '--workspace', wid,
              '--generation', generation, '--source', b.identity['host'],
              '--reason', 'native exact grant revocation', '--yes')
        revoked = b.result('ssh', c.identity['host'], '--workspace', wid, '--reason', 'revoked grant',
                           '--ttl', '15s', '--cpu', '0.05', '--key', 'after-revoke', '--yes',
                           '--', 'printf must-not-run', timeout=20)
        assert revoked.returncode != 0 and revoked.stdout == '', revoked
        c.cli('mesh', 'ssh-relay', 'grant-destination', '--workspace', wid,
              '--source', b.identity['host'], '--cpu', '0.1', '--ttl', '30s',
              '--reason', 'review renewed exact original source', '--yes')
        d = Node(binary, root, 'alternate-d')
        nodes.append(d)
        d.start()
        port = mesh.free_port()
        d.identity = d.cli('mesh', 'init', '--name', d.name,
                           '--listen', f'127.0.0.1:{port}',
                           '--address', f'127.0.0.1:{port}', '--yes')
        ws.enroll(a, d)
        join = d.cli('workspaces', 'join', '--invitation', str(invite),
                     '--key', 'join', '--reason', 'alternate path member')
        decide(d, join)
        consent = root / 'alternate-d-consent.json'
        d.cli('workspaces', 'consent', '--workspace', wid, '--key', 'join', '--output', str(consent))
        command = ['workspaces', 'admit', '--workspace', wid, '--consent', str(consent),
                   '--key', 'admit-alternate-d', '--reason', 'current alternate candidate']
        review = a.cli(*command)
        admitted = a.cli(*command, '--review', review['review_sha256'], '--yes')
        assert admitted['phase'] == 'completed', admitted
        d.cli('workspaces', 'refresh', '--workspace', wid)
        current = d.cli('workspaces', 'lookup', '--workspace', wid, '--key', 'join')['operation']
        d.cli('workspaces', 'finalize', '--workspace', wid, '--key', 'join',
              '--revision', str(current['revision']))
        for node in (b, c):
            node.cli('workspaces', 'refresh', '--workspace', wid)
        ws.enroll(b, d)
        ws.enroll(c, d)
        # Reports must reach the directory before the source's fresh snapshot
        # can include both new D edges. One pass in admission order is stale.
        for node in (d, c, b, a, b):
            node.cli('workspaces', 'refresh', '--workspace', wid)
        paths = b.cli('workspaces', 'graph', '--workspace', wid,
                      '--section', 'paths', '--to', c.identity['host'])['paths']
        assert [b.identity['host'], d.identity['host'], c.identity['host']] in paths, paths
        d.cli('mesh', 'ssh-relay', 'grant-relay', '--workspace', wid,
              '--generation', generation, '--relay-cpu', '0.05', '--relay-memory', '8MiB',
              '--reason', 'reviewed alternate transit', '--yes')
        a.cli('mesh', 'ssh-relay', 'revoke-relay', '--workspace', wid,
              '--generation', generation, '--reason', 'force alternate path', '--yes')
        alternate = b.cli('ssh', c.identity['host'], '--workspace', wid,
                          '--reason', 'alternate route proof', '--ttl', '15s', '--cpu', '0.05',
                          '--key', 'alternate-exec', '--update-pin', '--dry-run',
                          '--', 'printf alternate-stdout')
        assert alternate['route'] == [b.identity['host'], d.identity['host'], c.identity['host']], alternate
        result = b.result('ssh', c.identity['host'], '--workspace', wid,
                          '--reason', 'alternate route proof', '--ttl', '15s', '--cpu', '0.05',
                          '--key', 'alternate-exec', '--update-pin', '--yes',
                          '--', 'printf alternate-stdout', timeout=35)
        assert (result.returncode, result.stdout, result.stderr) == (0, 'alternate-stdout', ''), result
        assert b.cli('ssh', 'lookup', c.identity['host'], '--key', 'alternate-exec')['receipt']['found']
        assert all(peer['identity']['host'] != c.identity['host'] for peer in b.cli('mesh', 'peers'))
        final_report = 'PASS native ' + platform.system() + ' ' + platform.machine() + ' public CLI B→A→C and B→D→C routed SSH, exact exit/stdout/stderr, ciphertext reply loss, original-key lookup, replay refusal, unknown admission/nonexecution seal, grant revocation and alternate route; no B–C enrollment'
        assert {name: file_sha256(path) for name, path in input_paths.items()} == input_hashes, 'acceptance inputs changed during run'
        success = True
    finally:
        original_error = sys.exc_info()[0] is not None
        finish_fixture(root, nodes, ciphertext, success, original_error)
        if success and not original_error and final_report is not None:
            print(final_report)
            print('Acceptance inputs: ' + json.dumps(receipt, sort_keys=True))


if __name__ == '__main__':
    main()
