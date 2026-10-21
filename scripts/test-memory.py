#!/usr/bin/env python3
"""Real host memory headroom and compiled auto/ceiling startup behavior.

Changing-capacity/unknown/concurrent admission scenarios use injected native
readings in Go tests; this check never creates artificial host memory pressure.
"""
import argparse
import fcntl
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    binary = str(Path(parser.parse_args().arm).resolve())
    root = Path(tempfile.mkdtemp(prefix='arm-memory-'))
    home = root / 'state'
    prefix = [binary, '--home', str(home)]
    lease = None

    def cli(*args):
        result = subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=15)
        assert result.returncode == 0, (args, result.returncode, result.stderr)
        return json.loads(result.stdout)

    def check(status, ceiling):
        memory = status['memory_budget']
        source = memory['observation']
        assert not memory['stale'] and source['quality'] == 'estimated'
        assert source['total_bytes'] > 0 and 0 <= source['available_bytes'] <= source['total_bytes']
        assert memory['ceiling_bytes'] == ceiling
        assert memory['capacity_bytes'] == (source['available_bytes'] if ceiling is None else min(ceiling, source['available_bytes']))
        assert status['capacity']['memory_bytes'] == memory['capacity_bytes']
        assert memory['available_bytes'] == max(0, memory['capacity_bytes'] - status['reserved']['memory_bytes'])
        assert memory['deficit_bytes'] == max(0, status['reserved']['memory_bytes'] - memory['capacity_bytes'])
        assert 'live-memory-admission' in status['capabilities']
        return source

    try:
        cli('start', '--cpu', '1', '--disk', '1GiB')
        first = cli('status')
        source = check(first, None)
        deadline = time.monotonic() + 6
        while time.monotonic() < deadline:
            current = cli('status')
            if check(current, None)['at'] != source['at']:
                break
            time.sleep(.1)
        else:
            raise AssertionError('live memory observation never advanced')
        assert cli('events', '--after', str(first['cursor'])) == [], 'memory samples entered durable operational history'
        lease = cli('acquire', '--memory', '64KiB', '--reason', 'native memory-pool acceptance', '--ttl', '1m')
        assert lease['state'] == 'active'
        check(cli('status'), None)
        cli('release', lease['id'])
        lease = None
        cli('stop')
        cli('start', '--memory', '16MiB', '--cpu', '1')
        check(cli('status'), 16 << 20)
        cli('stop')
        print('Compiled memory: native availability, auto default, refreshed pool, no per-sample audit events, ordinary lease accounting and optional ceiling passed')
    finally:
        if lease:
            subprocess.run(prefix + ['release', lease['id']], capture_output=True, timeout=15)
        subprocess.run(prefix + ['stop'], capture_output=True, timeout=15)
        try:
            with (home / 'daemon.lock').open('r+') as lock:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                if not (home / 'endpoint.json').exists():
                    shutil.rmtree(root)
                else:
                    print('Retained unresolved memory fixture:', root)
        except (FileNotFoundError, BlockingIOError):
            print('Retained memory fixture without verified shutdown:', root)


if __name__ == '__main__':
    main()
