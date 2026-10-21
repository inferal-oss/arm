#!/usr/bin/env python3
"""Compiled host monitoring, bounded history, reconnect and physical/budget separation."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import select
import shutil
import signal
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    binary = str(Path(parser.parse_args().arm).resolve())
    root = Path(tempfile.mkdtemp(prefix='arm-monitor-'))
    home = root / 'state'
    prefix = [binary, '--home', str(home)]
    watcher = None

    def cli(*args):
        result = subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=15)
        assert result.returncode == 0, (args, result.returncode, result.stderr)
        return json.loads(result.stdout)

    try:
        cli('start', '--cpu', '1', '--memory', '1GiB', '--disk', '1GiB')
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            page = cli('monitor')
            latest = page['latest']
            if latest and latest['cpu_busy_percent']['value'] is not None:
                break
            time.sleep(.1)
        else:
            raise AssertionError('native CPU sampling never became available')
        assert 0 <= latest['cpu_busy_percent']['value'] <= 100
        assert latest['memory']['total']['value'] > 0 and latest['swap']['total']['value'] is not None
        assert latest['network'] and all(r['read_bytes_per_second']['value'] is None or r['read_bytes_per_second']['value'] >= 0 for r in latest['network'])
        assert latest['gpu_busy_percent']['value'] is None, 'missing GPU provider was shown as zero'
        assert page['host'] and page['epoch'] and len(page['history']) <= 60 and not page['stale']
        status = cli('status')
        assert status['reserved']['cpu'] == 0 and status['capacity']['cpu'] == 1
        assert page['host'] == status['host'] and 'host-monitor-stream' in status['capabilities']
        watcher = subprocess.Popen(prefix + ['monitor', '--follow', '--json', '--epoch', page['epoch'], '--after', str(page['cursor'])], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        buffer = bytearray()

        def read_page(seconds=15):
            deadline = time.monotonic() + seconds
            while b'\n' not in buffer:
                wait = deadline - time.monotonic()
                assert wait > 0 and select.select([watcher.stdout], [], [], wait)[0], 'monitor stream stalled'
                data = os.read(watcher.stdout.fileno(), 262144)
                assert data, 'monitor stream closed'
                buffer.extend(data)
            line, _, rest = buffer.partition(b'\n')
            buffer[:] = rest
            return json.loads(line)

        first = read_page()
        streamed = read_page()
        assert streamed['epoch'] == first['epoch'] and streamed['cursor'] > first['cursor']
        assert all(p['sequence'] > first['cursor'] for p in streamed['history'])
        assert cli('events', '--after', str(status['cursor'])) == [], 'monitor samples entered the durable audit log'
        resumed = cli('monitor', '--epoch', streamed['epoch'], '--after', str(streamed['cursor']))
        assert not resumed['gap'] and all(p['sequence'] > streamed['cursor'] for p in resumed['history'])
        cli('stop')
        cli('start', '--cpu', '1')
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            recovered = read_page()
            if recovered['epoch'] != streamed['epoch']:
                assert recovered['gap'], 'collector restart silently joined incompatible telemetry histories'
                break
        else:
            raise AssertionError('monitor did not reconnect after backend restart')
        watcher.send_signal(signal.SIGINT)
        _, errors = watcher.communicate(timeout=10)
        assert watcher.returncode == 130, (watcher.returncode, errors)
        assert all(json.loads(line) for line in errors.splitlines()), 'stream diagnostics were not JSON'
        cli('stop')
        print('Compiled monitor: native CPU/memory/swap/network, separate budgets, ephemeral history, cursor resume, restart gap/reconnect and interrupt passed')
    finally:
        if watcher and watcher.poll() is None:
            watcher.terminate()
            watcher.wait(timeout=10)
        subprocess.run(prefix + ['stop'], capture_output=True, timeout=15)
        # An unsuccessful assertion must not remove a live authority's state.
        try:
            with (home / 'daemon.lock').open('r+') as lock:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                if (home / 'endpoint.json').exists():
                    print('Retained unresolved monitoring fixture:', root)
                else:
                    shutil.rmtree(root)
        except (FileNotFoundError, BlockingIOError):
            print('Retained monitoring fixture without verified shutdown:', root)


if __name__ == '__main__':
    main()
