#!/usr/bin/env python3
"""Compiled subpool acceptance; private namespaces and exact test-owned PIDs only."""
import argparse
from datetime import datetime, timedelta, timezone
import json
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    binary = str(Path(parser.parse_args().arm).resolve())
    processes = []
    with tempfile.TemporaryDirectory(prefix='arm-subpools-') as directory:
        fixture = Path(directory)
        state = fixture / 'state'
        prefix = [binary, '--home', str(state)]

        def call(*args):
            return subprocess.run(prefix + list(args), text=True, capture_output=True, timeout=15)

        def cli(*args):
            result = call(*args)
            assert result.returncode == 0, (args, result.returncode, result.stdout, result.stderr)
            return json.loads(result.stdout)

        def until(check):
            end = time.monotonic() + 15
            while time.monotonic() < end:
                result = check()
                if result:
                    return result
                time.sleep(.04)
            raise AssertionError('subpool fixture did not reach expected state')

        def start():
            process = subprocess.Popen(prefix + ['serve', '--cpu', '2', '--disk', '1GiB'], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
            processes.append(process)
            until(lambda: call('status').returncode == 0)
            return process

        def view(pool):
            return cli('pool', 'inspect', pool)

        def child(parent, name, capacity, key=None):
            args = ['pool', 'create', name, '--parent', parent, '--parent-revision', str(view(parent)['pool']['revision']), '--capacity', str(capacity), '--reason', 'compiled owned carve']
            if key:
                args += ['--key', key]
            return cli(*args)

        def resize(pool, n):
            return cli('pool', 'set', pool, str(n), '--revision', str(view(pool)['pool']['revision']), '--reason', 'retain backing until settlement')

        def policy(limits, key):
            current = cli('budget', 'inspect', 'self')['budget']
            return cli('budget', 'set', 'self', '--revision', str(current['revision']), '--limits', json.dumps(limits), '--reason', 'actual ancestor use ceiling', '--key', key)

        try:
            backend = start()
            root = cli('pool', 'create', 'root', '--scope', 'host', '--unit', 'permit', '--capacity', '10', '--reason', 'private root')['after']['id']
            created = child(root, 'child', 6, 'create-child')
            leaf = created['after']['id']
            assert created['after']['unit'] == 'permit' and created['after']['parent'] == root
            assert view(root)['available'] == 4 and view(root)['reserved'] == 0
            assert call('acquire', '--pool', root + '=5', '--reason', 'cannot borrow child').returncode == 1
            held = cli('acquire', '--pool', leaf + '=5', '--reason', 'active child permits')
            assert held['pool_binding']['claims'][0]['path'] == [root, leaf]
            resize(leaf, 2)
            assert view(leaf)['partition']['pending_return'] == 3 and view(root)['available'] == 5
            assert cli('pool', 'lookup', '--key', 'create-child')['change']['sequence'] == created['sequence']
            cli('release', held['id'])
            assert view(root)['available'] == 8
            grand = child(leaf, 'grandchild', 2)['after']['id']
            bundle = cli('acquire', '--pool', root + '=8', '--pool', grand + '=2', '--reason', 'atomic disjoint bundle')
            assert view(root)['available'] == 0 and view(root)['partition']['committed'] == 10
            cli('release', bundle['id'])
            resize(leaf, 6)
            policy({'pools': {root: 2}}, 'ancestor-cap')
            held = cli('acquire', '--pool', grand + '=2', '--reason', 'nested actual permits')
            assert call('acquire', '--pool', root + '=1', '--reason', 'must honor ancestor cap').returncode == 1
            budget = cli('budget', 'inspect', 'self')
            assert budget['charged']['pools'][root] == 2 and budget['charged']['pools'][leaf] == 2
            policy({}, 'clear-cap')
            deadline = (datetime.now(timezone.utc) + timedelta(seconds=1)).isoformat()
            marker = fixture / 'must-not-run'
            waiting = subprocess.Popen(prefix + ['run', '--pool', grand + '=1', '--wait', '--start-deadline', deadline, '--reason', 'missed subpool start', '--json', '--', '/bin/sh', '-c', 'touch "$1"', 'fixture', str(marker)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            processes.append(waiting)
            stdout, stderr = waiting.communicate(timeout=12)
            assert waiting.returncode == 1 and json.loads(stdout)['outcome'] == 'rejected' and not marker.exists(), (stdout, stderr)
            # Crash while child/grandchild reductions retain active backing.
            resize(grand, 1)
            resize(leaf, 1)
            assert view(root)['partition']['child_backing'] == 2
            backend.kill()
            backend.wait(timeout=8)
            backend = start()
            assert view(leaf)['partition']['pending_return'] == 1 and view(root)['available'] == 8
            consumers = cli('pool', 'consumers', root, '--descendants', '--limit', '1')
            assert consumers['entries'][0]['use']['lease'] == held['id']
            assert cli('pool', 'children', root, '--limit', '1')['nodes'][0]['pool']['id'] == leaf
            assert any(node['pool']['id'] == root for node in cli('pool', 'roots')['nodes'])
            cli('release', held['id'])
            assert view(root)['available'] == 9 and view(grand)['partition']['pending_return'] == 0
            large = cli('pool', 'create', 'large', '--scope', 'global', '--capacity', '9223372036854775807', '--reason', 'exact integer backing')['after']['id']
            child(large, 'large-child', 9007199254740993)
            assert view(large)['partition']['child_backing'] == 9007199254740993
            # Missing durable lineage must retain cleanup authority, never repair.
            held = cli('acquire', '--pool', grand + '=1', '--reason', 'damage only private binding')
            storage = cli('storage', 'create', '1MiB', '--reason', 'owned empty cleanup during degraded accounting')
            backend.kill()
            backend.wait(timeout=8)
            with sqlite3.connect(state / 'state.db') as db:
                db.execute('DELETE FROM pool_claim_bindings WHERE lease=?', (held['id'],))
            backend = start()
            status = cli('status')
            assert status['pool_error']['code'] == 'invalid_accounting' and status['reserved']['disk_bytes'] == 1048576
            assert call('acquire', '--cpu', '.1', '--reason', 'degraded refuses new work').returncode == 1
            cli('release', held['id'])
            cli('release', storage['id'])
            until(lambda: cli('inspect', storage['id'])['state'] in ('terminal', 'needs_attention'))
            observed = cli('inspect', storage['id'])
            if observed['state'] == 'needs_attention':
                assert observed['artifact']['namespace_removed'], observed
                end = time.monotonic() + 5
                while True:
                    handed = call('storage', 'handoff', storage['id'])
                    if handed.returncode == 0:
                        break
                    assert json.loads(handed.stderr)['error']['code'] == 'operation_busy' and time.monotonic() < end, handed.stderr
                    time.sleep(.02)
            assert not Path(storage['managed_dir']).exists()
            with sqlite3.connect(state / 'state.db') as db:
                assert db.execute('SELECT count(*) FROM pool_claim_bindings WHERE lease=?', (held['id'],)).fetchone()[0] == 0
            cli('stop')
            assert backend.wait(timeout=8) == 0
            print('Compiled subpools: disjoint carve/bundle, deferred backing, exact counts, ancestor owner cap, deadline no-execution, crash/restart, paged reads/key lookup and degraded provider cleanup passed')
        finally:
            for process in reversed(processes):
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=8)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=5)


if __name__ == '__main__':
    main()
