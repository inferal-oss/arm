#!/usr/bin/env python3
"""Compiled local policy-budget acceptance; private state, real backend and commands."""
import argparse
from datetime import datetime, timedelta, timezone
import json
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import time


def acceptance(binary):
    children = []
    with tempfile.TemporaryDirectory(prefix='arm-policy-budgets-') as directory:
        root = Path(directory)
        prefix = [binary, '--home', str(root / 'state')]

        def call(*args):
            return subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=15)

        def cli(*args):
            result = call(*args)
            assert result.returncode == 0, (args, result.stdout, result.stderr)
            return json.loads(result.stdout)

        def until(check):
            end = time.monotonic() + 12
            while time.monotonic() < end:
                value = check()
                if value:
                    return value
                time.sleep(.03)
            raise AssertionError('policy fixture did not reach expected state')

        def start():
            process = subprocess.Popen(prefix + ['serve', '--cpu', '2', '--memory', 'auto', '--disk', '1GiB'], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
            children.append(process)
            until(lambda: (root / 'state' / 'endpoint.json').exists())
            return process

        def change(identifier, limits, key):
            current = cli('budget', 'inspect', identifier)['budget']
            return cli('budget', 'set', identifier, '--limits', json.dumps(limits), '--revision', str(current['revision']), '--reason', 'compiled policy acceptance', '--key', key)

        def queued(key):
            return next((lease for lease in cli('status')['leases'] if lease['request']['idempotency_key'] == key and lease['state'] == 'queued'), None)

        try:
            backend = start()
            owner = cli('budget', 'inspect', 'self')
            assert owner['budget']['root'] and owner['budget']['limits'] == {} and owner['budget']['owner']
            parent = cli('budget', 'create', 'builds', '--limits', '{"cpu":1}', '--reason', 'build policy', '--key', 'create-parent')['after']
            leaf = cli('budget', 'create', 'checks', '--parent', parent['id'], '--reason', 'nested policy', '--key', 'create-leaf')['after']
            lease = cli('acquire', '--budget', leaf['id'], '--cpu', '.75', '--ttl', '2m', '--reason', 'policy holder', '--key', 'holder')
            assert len(lease['budget_binding']['path']) == 3 and len(lease['budget_policy']) == 3
            assert cli('status')['reserved']['cpu'] == .75
            marker = root / 'must-not-run'
            deadline = (datetime.now(timezone.utc) + timedelta(seconds=1)).isoformat()
            waiting = subprocess.Popen(prefix + ['run', '--budget', leaf['id'], '--cpu', '.75', '--wait', '--start-deadline', deadline, '--reason', 'deadline behind policy ceiling', '--key', 'missed-policy', '--json', '--', '/bin/sh', '-c', 'touch "$1"', 'fixture', str(marker)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            children.append(waiting)
            waiting_lease = until(lambda: queued('missed-policy'))
            assert any(b['code'] == 'budget_limit' for b in waiting_lease['queue_info']['blockers'])
            assert waiting_lease['queue_info']['estimate']['kind'] == 'unknown'
            stdout, stderr = waiting.communicate(timeout=10)
            assert waiting.returncode == 1 and json.loads(stdout)['outcome'] == 'rejected' and not marker.exists(), (stdout, stderr)
            reduced = change('self', {'cpu': .5}, 'reduce-owner')
            owner = cli('budget', 'inspect', 'self')
            assert owner['deficit']['resources']['cpu'] == .25
            denied = call('acquire', '--budget', leaf['id'], '--cpu', '.1', '--reason', 'must respect owner root')
            assert denied.returncode != 0 and json.loads(denied.stderr)['error']['code'] == 'insufficient_capacity'
            # Complete JSON replacement: explicit zero remains zero; {} clears caps.
            zero = change(leaf['id'], {'memory_bytes': 0}, 'zero-memory')
            assert zero['after']['limits']['memory_bytes'] == 0
            cleared = change(leaf['id'], {}, 'clear-leaf')
            assert cleared['after']['limits'] == {}
            # Unrelated disk allocation remains usable during a CPU policy deficit.
            storage = cli('storage', 'create', '1MiB', '--budget', leaf['id'], '--reason', 'unrelated managed storage')
            assert storage['state'] == 'active' and storage['budget_binding']['selected'] == leaf['id']
            cli('release', storage['id'])
            until(lambda: cli('inspect', storage['id'])['state'] == 'terminal')
            assert cli('budget', 'lookup', '--key', 'reduce-owner')['change']['sequence'] == reduced['sequence']
            cli('release', lease['id'])
            change('self', {}, 'clear-owner')
            # FIFO work bound before drain can still progress; new bindings cannot.
            holder = cli('acquire', '--budget', leaf['id'], '--cpu', '.75', '--reason', 'drain predecessor')
            marker = root / 'ran-after-grant'
            waiting = subprocess.Popen(prefix + ['run', '--budget', leaf['id'], '--cpu', '.75', '--wait', '--reason', 'bound before drain', '--key', 'queued-before-drain', '--json', '--', '/bin/sh', '-c', 'touch "$1"', 'fixture', str(marker)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            children.append(waiting)
            until(lambda: queued('queued-before-drain'))
            current = cli('budget', 'inspect', parent['id'])['budget']
            cli('budget', 'drain', parent['id'], '--revision', str(current['revision']), '--reason', 'stop new work', '--key', 'drain-parent')
            denied = call('acquire', '--budget', leaf['id'], '--cpu', '.1', '--wait', '--reason', 'late arrival')
            assert denied.returncode == 1 and json.loads(denied.stderr)['error']['code'] == 'budget_unavailable'
            cli('release', holder['id'])
            stdout, stderr = waiting.communicate(timeout=12)
            completed = json.loads(stdout)
            assert waiting.returncode == 0 and completed['outcome'] == 'completed' and marker.exists(), (stdout, stderr)
            # Actual daemon restart preserves separate immutable binding/journal evidence.
            cli('stop')
            assert backend.wait(timeout=8) == 0
            backend = start()
            retained = cli('inspect', completed['id'])
            assert retained['budget_binding'] == completed['budget_binding']
            history = cli('budget', 'history', parent['id'], '--limit', '1')
            assert history['more'] and len(history['entries']) == 1
            following = cli('budget', 'history', parent['id'], '--after', str(history['cursor']))
            assert any(entry['operation'] == 'drain' for entry in following['entries'])
            consumers = cli('budget', 'consumers', 'self', '--limit', '1')
            assert consumers['leases'] == []
            cli('stop')
            assert backend.wait(timeout=8) == 0
            print('Compiled budgets: UID root/nested lineage, no synthetic reservations, policy-only queue blockers/deadline no-execution, reduction deficits, unrelated storage cleanup, zero/clear/lookup, prior queue through drain and restart journal/bindings passed')
        finally:
            for process in reversed(children):
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=8)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=5)


def recovery(binary):
    for damage in ('missing-root', 'malformed-binding'):
        children = []
        with tempfile.TemporaryDirectory(prefix='arm-budget-recovery-') as directory:
            root = Path(directory)
            state = root / 'state'
            prefix = [binary, '--home', str(state)]

            def run(*args):
                return subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=12)

            def cli(*args):
                result = run(*args)
                assert result.returncode == 0, (args, result.stdout, result.stderr)
                return json.loads(result.stdout)

            def until(check):
                end = time.monotonic() + 15
                while time.monotonic() < end:
                    value = check()
                    if value:
                        return value
                    time.sleep(.03)
                raise AssertionError('policy recovery did not reach expected state')

            def start():
                process = subprocess.Popen(prefix + ['serve', '--cpu', '1', '--memory', 'auto', '--disk', '1GiB'], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
                children.append(process)
                until(lambda: process.poll() is None and run('status').returncode == 0)
                return process

            def terminal(identifier):
                return cli('inspect', identifier)['state'] == 'terminal'

            try:
                backend = start()
                node = cli('budget', 'create', 'durable-lookup', '--reason', 'inspect during degraded recovery', '--key', 'saved-policy')
                owned = cli('storage', 'create', '1MiB', '--reason', 'verified empty storage cleanup')
                unsafe = cli('storage', 'create', '1MiB', '--reason', 'unverified path must remain')
                keep = Path(unsafe['managed_dir']) / 'keep'
                keep.write_text('unverified contents')
                foreign = root / 'foreign.txt'
                foreign.write_text('unrelated data')
                holder = cli('acquire', '--reason', 'queued recovery predecessor', '--key', 'holder')
                waiter = subprocess.Popen(prefix + ['acquire', '--wait', '--reason', 'must remain paused after corruption', '--key', 'queued'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                children.append(waiter)
                waiting = until(lambda: next((lease for lease in cli('status')['leases'] if lease['request']['idempotency_key'] == 'queued'), None))
                assert waiting['state'] == 'queued'
                # Kill only this fixture's subprocess to exercise actual abrupt
                # restart. SQLite/provider evidence, not old in-memory state,
                # must authorize subsequent cleanup.
                backend.kill()
                backend.wait(timeout=8)
                waiter.communicate(timeout=8)
                with sqlite3.connect(state / 'state.db') as database:
                    raw = database.execute('select data from leases where id=?', (unsafe['id'],)).fetchone()[0]
                    lease = json.loads(raw)
                    lease['artifact']['identity'] = None
                    database.execute('update leases set data=? where id=?', (json.dumps(lease), unsafe['id']))
                    root_id = database.execute('select id from budgets where parent is null').fetchone()[0]
                    if damage == 'missing-root':
                        database.execute('delete from budgets where id=?', (root_id,))
                    else:
                        database.execute("update lease_budget_bindings set data='not-json' where lease=?", (owned['id'],))
                backend = start()
                status = cli('status')
                assert status['budget_error']['code'] == 'invalid_accounting'
                assert status['reserved']['disk_bytes'] == 2 * 1024 * 1024
                assert cli('inspect', owned['id'])['budget_error']['code'] == 'invalid_accounting'
                for args in (
                    ('acquire', '--reason', 'fresh work must remain closed'),
                    ('renew', owned['id'], '--ttl', '3m'),
                    ('budget', 'create', 'forbidden', '--reason', 'new policy must remain closed'),
                ):
                    result = run(*args)
                    assert result.returncode == 1 and json.loads(result.stderr)['error']['code'] == 'invalid_accounting', (args, result.stdout, result.stderr)
                assert cli('budget', 'lookup', '--key', 'saved-policy')['change']['sequence'] == node['sequence']
                cli('release', holder['id'])
                cli('release', owned['id'])
                until(lambda: terminal(owned['id']))
                assert not Path(owned['managed_dir']).exists()
                assert cli('inspect', waiting['id'])['state'] == 'queued'
                cli('cancel', waiting['id'])
                cli('release', unsafe['id'])
                until(lambda: cli('inspect', unsafe['id'])['state'] == 'needs_attention')
                assert cli('status')['reserved']['disk_bytes'] == 1024 * 1024
                cli('storage', 'cleanup', unsafe['id'])
                until(lambda: cli('inspect', unsafe['id'])['state'] == 'needs_attention')
                cli('storage', 'handoff', unsafe['id'])
                assert keep.read_text() == 'unverified contents' and foreign.read_text() == 'unrelated data'
                assert cli('status')['budget_error']['code'] == 'invalid_accounting'
                cli('stop')
                assert backend.wait(timeout=8) == 0
                with sqlite3.connect(state / 'state.db') as database:
                    if damage == 'missing-root':
                        assert database.execute('select count(*) from budgets where id=?', (root_id,)).fetchone()[0] == 0
                    else:
                        assert database.execute('select data from lease_budget_bindings where lease=?', (owned['id'],)).fetchone()[0] == 'not-json'
                print('Compiled policy recovery:', damage, 'restart observable; grants/renewals/policy edits paused; verified cleanup, unverified charge/ownership/handoff, queue cancellation, lookup and unrelated data preservation passed')
            finally:
                for process in reversed(children):
                    if process.poll() is None:
                        process.terminate()
                        try:
                            process.wait(timeout=8)
                        except subprocess.TimeoutExpired:
                            process.kill()
                            process.wait(timeout=5)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    binary = str(Path(parser.parse_args().arm).resolve())
    acceptance(binary)
    recovery(binary)


if __name__ == '__main__':
    main()
