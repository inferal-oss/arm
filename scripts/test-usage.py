#!/usr/bin/env python3
"""Observe a real supervised workload using only test-owned process handles."""
import argparse
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    opts = parser.parse_args()
    binary = str(Path(opts.arm).resolve())
    children = []
    with tempfile.TemporaryDirectory(prefix='arm-usage-') as fixture:
        home = str(Path(fixture) / 'state')
        prefix = [binary, '--home', home]

        def cli(*args):
            p = subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=15)
            assert p.returncode == 0, (args, p.returncode, p.stderr)
            return json.loads(p.stdout)

        try:
            backend = subprocess.Popen(prefix + ['serve'], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            children.append(backend)
            deadline = time.monotonic() + 10
            while not (Path(home) / 'endpoint.json').exists():
                assert backend.poll() is None, backend.stderr.read().decode()
                assert time.monotonic() < deadline, 'backend did not start'
                time.sleep(.05)
            workload = '''import time
memory=bytearray(32<<20)
for i in range(0,len(memory),4096): memory[i]=1
until=time.monotonic()+25
n=1
while time.monotonic()<until:
 for _ in range(10000): n=(n*1664525+1013904223)&0xffffffff
print(len(memory),n)
'''
            run = subprocess.Popen(prefix + ['run', '--cpu', '1', '--memory', '64MiB', '--ttl', '20s', '--reason', 'usage fixture', '--json', '--', sys.executable, '-c', workload], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            children.append(run)
            deadline = time.monotonic() + 10
            view = None
            while time.monotonic() < deadline:
                leases = [l for l in cli('status')['leases'] if l['request']['reason'] == 'usage fixture']
                if leases:
                    lease = leases[0]
                    view = cli('usage', lease['id'])
                    usage = view.get('usage')
                    if usage and usage['cpu_cores'] is not None and usage['accounted_memory_bytes'] is not None:
                        break
                time.sleep(.1)
            else:
                raise AssertionError('attributable usage unavailable: ' + str(view))
            usage = view['usage']
            assert usage['identity_verified'] and usage['live'] and not usage['stale']
            assert usage['root'] == lease['process_identity']
            assert usage['cpu_cores'] > 0 and usage['accounted_memory_bytes'] > 16<<20
            assert usage['memory_metric'] in ('sum-process-physical-footprint', 'sum-private-resident')
            assert usage['member_count'] >= 1 and usage['cpu_quality'] == 'lower-bound-overlap-average'
            assert cli('inspect', lease['id'])['usage']['live']
            cli('cancel', lease['id'])
            stdout, stderr = run.communicate(timeout=10)
            assert run.returncode == 125, (run.returncode, stderr)
            ended = json.loads(stdout)
            assert ended['state'] == 'terminal' and not ended['usage']['live']
            assert ended['usage']['member_count'] >= 1 and not ended['usage']['members']
            cli('daemon', 'stop')
            backend.wait(timeout=10)
            print('Compiled usage: native CPU/memory, launch identity, freshness, inspect/status, cancellation and final summary passed')
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
