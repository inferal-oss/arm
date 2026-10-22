#!/usr/bin/env python3
"""Private compiled backend restart/cleanup gates; workspace RPC remains unrouted.

The explicitly supplied manager test executable creates reviewed producer state,
then deliberately exits at a crash boundary. It is never installed or used with
real agent/user state. The production ARM executable subsequently owns recovery.
"""
import argparse
from contextlib import contextmanager
import json
import os
from pathlib import Path
import sqlite3
import shutil
import sys
import subprocess
import tempfile
import time


@contextmanager
def private_fixture():
    root = Path(tempfile.mkdtemp(prefix='arm-workspace-recovery-'))
    root.chmod(0o700)
    try:
        yield root
    except BaseException:
        print('Failed workspace fixture retained for inspection: ' + str(root), file=sys.stderr)
        raise
    else:
        # The caller has proved full success, stopped its owned backend and
        # verified endpoint removal. Unknown provider evidence is never erased.
        shutil.rmtree(root)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', required=True)
    parser.add_argument('--manager-tests', required=True)
    args = parser.parse_args()
    binary, helper = str(Path(args.arm).resolve()), str(Path(args.manager_tests).resolve())
    for damage in ('missing-marker', 'missing-clock', 'null-policy', 'empty-group'):
        with private_fixture() as directory:
            state = Path(directory) / 'state'
            env = dict(os.environ, ARM_WORKSPACE_MANAGER_CRASH_HOME=str(state), ARM_WORKSPACE_MANAGER_DAMAGE=damage)
            child = subprocess.run([helper, '-test.run=^TestWorkspaceCrashFixtureChild$', '-test.timeout=15s'], env=env, capture_output=True, text=True, timeout=20)
            assert child.returncode == 71, (damage, child.returncode, child.stdout, child.stderr)
            storage = json.loads((state / 'crash-fixture.json').read_text())
            prefix = [binary, '--home', str(state)]
            backend = subprocess.Popen(prefix + ['serve', '--cpu', '2', '--memory', 'auto', '--disk', '1MiB'], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)

            def call(*argv):
                return subprocess.run(prefix + list(argv), capture_output=True, text=True, timeout=10)

            def cli(*argv):
                result = call(*argv)
                assert result.returncode == 0, (damage, argv, result.returncode, result.stdout, result.stderr)
                return json.loads(result.stdout)

            def until(check):
                deadline = time.monotonic() + 12
                while time.monotonic() < deadline:
                    value = check()
                    if value:
                        return value
                    assert backend.poll() is None, ('backend exited', backend.stderr.read())
                    time.sleep(.03)
                raise AssertionError(('workspace fixture did not settle', damage))

            try:
                until(lambda: (state / 'endpoint.json').exists())
                status = cli('status')
                assert status['workspaces']['ledger'] == 'invalid' and status['workspaces']['error']['code'] == 'workspace_state_invalid', status['workspaces']
                assert status['reserved']['disk_bytes'] == 1024, status['reserved']
                expiring = cli('acquire', '--cpu', '.5', '--ttl', '50ms', '--reason', 'ordinary expiry survives workspace corruption')
                until(lambda: cli('inspect', expiring['id'])['state'] == 'terminal')
                assert cli('inspect', expiring['id'])['outcome'] == 'expired'
                cli('release', storage['id'])
                until(lambda: cli('inspect', storage['id'])['state'] == 'terminal')
                assert not Path(storage['managed_dir']).exists(), storage['managed_dir']
                assert (state / 'unrelated.txt').read_text() == 'keep'
                assert cli('status')['workspaces']['ledger'] == 'invalid'
                cli('stop')
                assert backend.wait(timeout=8) == 0
                assert not (state / 'endpoint.json').exists(), 'backend endpoint remains after stop'
                with sqlite3.connect(state / 'state.db') as db:
                    if damage == 'missing-marker':
                        assert db.execute("SELECT value FROM metadata WHERE key='workspace_schema'").fetchone() is None
                    if damage == 'missing-clock':
                        assert db.execute('SELECT count(*) FROM workspace_clocks').fetchone()[0] == 0
                    if damage == 'empty-group':
                        assert db.execute('SELECT count(*) FROM workspace_instances').fetchone()[0] == 0
            finally:
                if backend.poll() is None:
                    backend.kill()
                    backend.wait(timeout=5)
                backend.stderr.close()
    print('compiled workspace crash/restart/status/expiry/provider-cleanup PASS')


if __name__ == '__main__':
    main()
