#!/usr/bin/env python3
"""Hosted-only independent credit/lifecycle composition checks; receipts are data."""
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import signal
import subprocess
import sys
import time

from owned_process import OwnedCommand

ROOT = Path(__file__).resolve().parent.parent
BRANCH = 'refs/heads/codex/forward-total-credit-independent-review'


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def record(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + '\n')


def source():
    git = lambda *args: subprocess.check_output(['git', *args], cwd=ROOT).decode().strip()
    names = subprocess.check_output(['git', 'ls-files', '-z'], cwd=ROOT).decode().split('\0')
    return {'commit': git('rev-parse', 'HEAD'), 'tree': git('rev-parse', 'HEAD^{tree}'),
            'status': git('status', '--porcelain'), 'files': {n: digest(ROOT / n) for n in names if n}}


def main():
    label = os.environ.get('CREDIT_REVIEW_RUNNER')
    expected_os = {'ubuntu-24.04': 'linux', 'macos-15': 'darwin'}.get(label)
    if (os.environ.get('GITHUB_ACTIONS') != 'true' or
            os.environ.get('GITHUB_REPOSITORY') != 'inferal-oss/arm' or
            os.environ.get('GITHUB_REF') != BRANCH or
            os.environ.get('GITHUB_EVENT_NAME') != 'push' or
            not re.fullmatch('[0-9a-f]{40}', os.environ.get('GITHUB_SHA', '')) or
            expected_os != sys.platform or any(n.startswith('ARM_') for n in os.environ)):
        print('refused before any validation child: reviewed hosted scope required')
        return 2
    out = ROOT / '.cache/credit-independent-review'
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    receipts, work = out / 'receipts', out / 'work'
    receipts.mkdir(mode=0o700); work.mkdir(mode=0o700)
    before = source()
    record(receipts / 'source-before.json', before)
    if before['status'] or before['commit'] != os.environ['GITHUB_SHA']:
        raise RuntimeError('immutable clean checkout required')
    env = dict(os.environ, GOMAXPROCS='1', GOFLAGS='-p=1', GOTOOLCHAIN='local')
    go = shutil.which('go')
    if not go:
        raise RuntimeError('selected Go unavailable')
    record(receipts / 'native.json', {'os': sys.platform, 'machine': platform.machine(),
        'python': sys.version, 'runner_label': label, 'macos': platform.mac_ver()[0],
        'workflow_sha': os.environ['GITHUB_SHA'], 'go_binary_sha256': digest(Path(go)),
        'ownership_helper_sha256': digest(ROOT / 'scripts/owned_process.py')})
    commands, failures, rosters = [], [], {}
    images = {}

    def interrupted(signum, _frame):
        raise KeyboardInterrupt('hosted validation interrupted: ' + str(signum))

    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGHUP, interrupted)

    def run(name, argv, timeout=600, tests=False):
        print('START', name, flush=True)
        start = time.monotonic()
        owner = OwnedCommand(argv, env=env, cwd=ROOT, stop_timeout=10)
        error = None
        try:
            owner.start()
            owner._drain(timeout)
            owner._reap()
        except BaseException as exc:
            error = type(exc).__name__ + ': ' + str(exc)
            try:
                owner.cancel()
            except BaseException as cleanup:
                error += '; cleanup: ' + type(cleanup).__name__ + ': ' + str(cleanup)
        finally:
            owner.close_pipes()
        result = owner.result()
        stdout, stderr = result.stdout, result.stderr or b''
        (receipts / (name + '.stdout.txt')).write_bytes(stdout)
        (receipts / (name + '.stderr.txt')).write_bytes(stderr)
        after = source()
        unchanged = before == after
        record(receipts / (name + '-source.json'), after)
        item = {'name': name, 'argv': argv, 'exit': result.returncode, 'controller_error': error,
                'seconds': time.monotonic() - start, 'original_wait_returned': owner.returncode is not None,
                'signal_authority_retired': not owner.authority, 'ownership_lost': owner.lost,
                'pipes_closed': not owner.read_fds and owner.selector_closed,
                'source_unchanged': unchanged, 'stdout_sha256': hashlib.sha256(stdout).hexdigest(),
                'stderr_sha256': hashlib.sha256(stderr).hexdigest()}
        commands.append(item)
        if result.returncode != 0 or error or not unchanged or not item['pipes_closed']:
            failures.append(name)
        if not unchanged:
            raise RuntimeError('source changed during original command')
        if tests:
            events = [json.loads(line) for line in stdout.decode().splitlines() if line.startswith('{')]
            terminal = {action: sorted(e['Test'] for e in events if e.get('Test') and e.get('Action') == action)
                        for action in ('pass', 'fail', 'skip')}
            if not terminal['pass'] or terminal['fail'] or terminal['skip']:
                failures.append(name + '-test-roster')
            rosters[name] = terminal
            record(receipts / (name + '-roster.json'), terminal)
        record(receipts / 'commands.json', commands)
        print('DONE', name, 'exit', result.returncode, 'error', error, flush=True)
        if error:
            raise RuntimeError('original command ownership/cleanup unconfirmed')
        return result

    public, review = work / 'arm-public', work / 'arm-review'
    try:
        run('ownership-controls', [sys.executable, '-B', 'scripts/test_owned_process.py'])
        run('toolchain', [go, 'version'])
        environment = json.loads(run('go-environment', [go, 'env', '-json', 'GOOS', 'GOARCH', 'CGO_ENABLED', 'GOVERSION', 'GOROOT', 'CC']).stdout)
        expected_arch = 'arm64' if expected_os == 'darwin' else 'amd64'
        if (environment['GOOS'] != expected_os or environment['GOARCH'] != expected_arch or
                environment['CGO_ENABLED'] != '1' or environment['GOVERSION'] != 'go1.27.3'):
            raise RuntimeError('actual selected native Go/CGo differs from reviewed matrix')
        run('credit-race', [go, 'test', '-mod=readonly', '-race', '-p=1', '-count=1', '-json', '-timeout=5m',
            './internal/meshforward', './internal/meshsshrelay', '-run', '^(TestForwardCredit|TestProxy|TestPacedWriter|TestGrant)'], tests=True)
        run('policy-fence-race', [go, 'test', '-mod=readonly', '-race', '-p=1', '-count=1', '-json', '-timeout=5m',
            './internal/meshsshrelay', './internal/server', './internal/cli', '-run',
            '^(TestReviewedPolicyCommitCASAndNoFollow|TestDestinationPolicyRoundTripAndRevocation|TestLegacyUnbudgetedRelayCanBeReviewedButCannotForward|TestForwardRouteBindsServiceAndRefusesSSHClassConfusion|TestForwardSourceTLSKeepsOriginalFailureClassification|TestDisabledForwardAuthorityUnavailable|TestForwardHTTPClientProductionFence)$'], tests=True)
        run('relay-cleanup-race', [go, 'test', '-mod=readonly', '-race', '-p=1', '-count=1', '-json', '-timeout=5m',
            './internal/manager', '-run', '^(TestRelayTransit|TestRelayCleanup|TestForwardTransitSettledMarkerAllowsResetProof)'], tests=True)
        run('review-route-race', [go, 'test', '-mod=readonly', '-race', '-p=1', '-count=1', '-json', '-timeout=5m',
            '-tags', 'arm_forward_review', './internal/server', '-run', '^TestForwardThreeNodesHTTPExactOriginalAndRevocation$'], tests=True)
        run('affected-vet', [go, 'vet', '-mod=readonly', './internal/meshforward', './internal/meshsshrelay', './internal/server', './internal/cli', './internal/manager'])
        for name, path, extra in [('public', public, []), ('review', review, ['-tags', 'arm_forward_review'])]:
            run(name + '-build', [go, 'build', '-mod=readonly', *extra, '-o', str(path), './cmd/arm'])
            if path.is_file():
                images[name] = {'sha256': digest(path), 'bytes': path.stat().st_size}
                run(name + '-buildinfo', [go, 'version', '-m', str(path)])
        if review.is_file():
            result = run('public-three-node-cli', [sys.executable, '-B', 'scripts/test-routed-forward.py', '--arm', str(review)], timeout=300)
            if b'PASS public CLI B' not in result.stdout:
                failures.append('public-three-node-cli-result')
        else:
            failures.append('public-three-node-cli-unlaunched')
    finally:
        closure = {'source_unchanged': before == source(), 'images': {}}
        for name, path in [('public', public), ('review', review)]:
            if name in images:
                closure['images'][name] = {'before': images[name], 'after_sha256': digest(path),
                    'unchanged': images[name]['sha256'] == digest(path)}
        record(receipts / 'source-after.json', source())
        record(receipts / 'closure.json', closure)
        record(receipts / 'commands.json', commands)
        record(receipts / 'rosters.json', rosters)
        shutil.rmtree(work)
        record(receipts / 'cleanup.json', {'own_binary_work_removed': not work.exists(),
            'fixture_cleanup_requires_public_three_node_success': True,
            'user_install_or_backend_accessed': False, 'hosted_tool_caches_not_claimed_removed': True})
        if not closure['source_unchanged'] or not all(v['unchanged'] for v in closure['images'].values()):
            failures.append('source-image-closure')
        complete = len(commands) == 13
        if not complete:
            failures.append('incomplete-original-command-roster')
        record(receipts / 'result.json', {'complete': complete, 'failures': failures,
            'source': before['commit'], 'scope': 'current-main-credit-and-cleanup-composition'})
        record(receipts / 'manifest.json', {p.name: {'sha256': digest(p), 'bytes': p.stat().st_size}
            for p in sorted(receipts.iterdir()) if p.name != 'manifest.json'})
    return bool(failures)


if __name__ == '__main__':
    raise SystemExit(main())
