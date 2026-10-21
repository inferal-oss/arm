#!/usr/bin/env python3
"""Compiled counted-pool acceptance with only test-owned state/processes."""
import argparse
import json
from pathlib import Path
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    opts = parser.parse_args()
    binary = str(Path(opts.arm).resolve())
    children = []
    with tempfile.TemporaryDirectory(prefix='arm-pools-') as fixture:
        home = str(Path(fixture) / 'state')
        prefix = [binary, '--home', home]

        def cli(*args, check=True):
            result = subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=15)
            if not check:
                return result
            assert result.returncode == 0, (args, result.returncode, result.stderr)
            return json.loads(result.stdout) if result.stdout else None

        def start():
            process = subprocess.Popen(prefix + ['serve', '--cpu', '1'], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            children.append(process)
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                if cli('status', check=False).returncode == 0:
                    return process
                assert process.poll() is None, process.stderr.read().decode()
                time.sleep(.05)
            raise TimeoutError('pool fixture backend did not start')

        def create(name, scope, capacity):
            return cli('pool', 'create', name, '--scope', scope, '--capacity', str(capacity), '--reason', 'coordinate the fixture')['after']

        def revision(pool):
            return str(cli('pool', 'inspect', pool)['pool']['revision'])

        try:
            backend = start()
            host = create('shared-name', 'host', 1)
            global_pool = create('shared-name', 'global', 3)
            pool = global_pool['id']
            assert host['host'] and 'host' not in global_pool
            assert host['authority'] == global_pool['authority']
            held = cli('acquire', '--pool', pool + '=1', '--reason', 'hold a counted permit')
            assert held['request']['resources']['cpu'] == 0
            args = ['pool', 'adjust', pool, '-1', '--reason', 'one device unavailable', '--key', 'adjust-fixture']
            change = cli(*args)
            assert change['delta'] == -1 and change['after']['capacity'] == 2
            assert cli(*args) == change
            cli('pool', 'set', pool, '0', '--revision', revision(pool), '--reason', 'all devices offline')
            view = cli('pool', 'inspect', pool)
            assert view['reserved'] == 1 and view['deficit'] == 1 and view['available'] == 0
            waiting = subprocess.Popen(prefix + ['acquire', '--pool', pool + '=1', '--wait', '--reason', 'queued pool consumer'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            children.append(waiting)
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                view = cli('pool', 'inspect', pool)
                if view['queued']:
                    break
                time.sleep(.05)
            else:
                raise TimeoutError('counted claim did not queue')
            assert cli('status')['reserved']['cpu'] == 0
            cli('pool', 'adjust', pool, '2', '--reason', 'devices restored')
            stdout, stderr = waiting.communicate(timeout=10)
            assert waiting.returncode == 0, stderr
            granted = json.loads(stdout)
            cli('release', granted['id'])
            run = subprocess.run(prefix + ['run', '--pool', pool + '=1', '--pool', host['id'] + '=1', '--reason', 'consume both scopes', '--', '/bin/sh', '-c', 'printf "%s" "$ARM_POOLS"'], capture_output=True, text=True, timeout=15)
            assert run.returncode == 0 and json.loads(run.stdout)['exit_code'] == 0
            claims = json.loads(run.stderr)
            assert len(claims) == 2 and {c['scope'] for c in claims} == {'host', 'global'}
            backend.kill()  # exact test-owned PID, no PID recovered from ARM state
            backend.wait(timeout=10)
            backend = start()
            view = cli('pool', 'inspect', pool)
            assert view['reserved'] == 1 and view['pool']['capacity'] == 2
            assert cli(*args)['sequence'] == change['sequence'], 'restart duplicated adjustment'
            history = cli('pool', 'history', pool)
            assert len(history['entries']) == 4
            assert all(e['actor'] and e['reason'] and e['at'] for e in history['entries'])
            assert cli('daemon', 'stop', check=False).returncode != 0
            cli('release', held['id'])
            cli('pool', 'drain', pool, '--revision', revision(pool), '--reason', 'end fixture')
            assert cli('acquire', '--pool', pool + '=1', '--reason', 'must be rejected', check=False).returncode != 0
            cli('pool', 'delete', pool, '--revision', revision(pool), '--reason', 'all claims finished')
            assert cli('pool', 'inspect', pool)['pool']['state'] == 'deleted'
            assert len(cli('pool', 'history', pool)['entries']) == 6
            replacement = create('shared-name', 'global', 1)
            assert replacement['id'] != pool
            large = create('exact-int64', 'global', 9223372036854775807)
            assert cli('pool', 'inspect', large['id'])['pool']['capacity'] == 9223372036854775807
            resource = next(row for row in cli('resources') if row['id'] == large['id'])
            assert resource['budget_total'] == 9223372036854775807
            cli('daemon', 'stop')
            backend.wait(timeout=10)
            print('Compiled pools: host/global scopes, signed idempotent adjustments, deficit/queue growth, supervised bundles, crash recovery, journal, safe deletion and exact int64 counts passed')
        finally:
            for process in reversed(children):
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait()


if __name__ == '__main__':
    main()
