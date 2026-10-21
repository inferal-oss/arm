#!/usr/bin/env python3
"""Compiled local deadline/FIFO acceptance with actual sockets, commands and restart."""
import argparse
from datetime import datetime, timedelta, timezone
import json
from pathlib import Path
import socket
import subprocess
import tempfile
import time


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--arm', default='bin/arm')
    binary = str(Path(p.parse_args().arm).resolve())
    children = []
    with tempfile.TemporaryDirectory(prefix='arm-scheduling-') as directory:
        root = Path(directory)
        prefix = [binary, '--home', str(root / 'state')]

        def run(*args):
            return subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=12)

        def cli(*args):
            result = run(*args)
            assert result.returncode == 0, (args, result.stdout, result.stderr)
            return json.loads(result.stdout)

        def until(check):
            end = time.monotonic() + 10
            while time.monotonic() < end:
                value = check()
                if value:
                    return value
                time.sleep(.03)
            raise AssertionError('scheduling fixture did not reach expected state')

        def stamp(seconds):
            return (datetime.now(timezone.utc) + timedelta(seconds=seconds)).isoformat()

        def start():
            process = subprocess.Popen(prefix + ['serve', '--cpu', '1', '--memory', 'auto', '--disk', '1GiB'], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
            children.append(process)
            until(lambda: (root / 'state' / 'endpoint.json').exists())
            return process

        try:
            backend = start()
            missed = run('acquire', '--reason', 'already missed', '--start-deadline', stamp(-5), '--key', 'missed')
            assert missed.returncode == 1, (missed.stdout, missed.stderr)
            rejected = json.loads(missed.stdout)
            assert rejected['outcome'] == 'rejected' and rejected['queue_info']['rejection']['code'] == 'start_deadline_exceeded'
            assert not rejected.get('started_at') and not rejected.get('expires_at')
            holder = cli('acquire', '--reason', 'FIFO blocker', '--ttl', '30s')
            marker = root / 'forbidden'
            waiting = subprocess.Popen(prefix + ['run', '--wait', '--key', 'no-execution', '--reason', 'missed command', '--start-deadline', stamp(1), '--json', '--', '/bin/sh', '-c', 'touch "$1"', 'fixture', str(marker)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            children.append(waiting)
            queued = until(lambda: next((l for l in cli('status')['leases'] if l['request'].get('idempotency_key') == 'no-execution'), None))
            assert queued['state'] == 'queued' and queued['queue_info']['position'] == 1
            assert queued['queue_info']['estimate']['kind'] == 'advisory'
            assert queued['queue_info']['blockers'][0]['blocking_leases'] == [holder['id']]
            stdout, stderr = waiting.communicate(timeout=8)
            assert waiting.returncode == 1, (stdout, stderr)
            result = json.loads(stdout)
            assert result['outcome'] == 'rejected' and not marker.exists()
            cli('release', holder['id'])
            retry = run('run', '--wait', '--key', 'no-execution', '--reason', 'missed command', '--start-deadline', queued['request']['start_deadline_at'], '--json', '--', '/bin/sh', '-c', 'touch "$1"', 'fixture', str(marker))
            assert retry.returncode == 1 and json.loads(retry.stdout)['id'] == result['id'] and not marker.exists(), (retry.stdout, retry.stderr)

            with socket.socket() as external:
                external.bind(('127.0.0.1', 0))
                external.listen()
                port = external.getsockname()[1]
                ports = run('ports', 'reserve', str(port), '--wait', '--reason', 'occupied external port deadline', '--start-deadline', stamp(1))
                assert ports.returncode == 1 and json.loads(ports.stdout)['outcome'] == 'rejected', (ports.stdout, ports.stderr)
                assert external.getsockname()[1] == port

            storage = run('storage', 'create', '1MiB', '--reason', 'missed storage', '--start-deadline', stamp(-1))
            assert storage.returncode == 1
            storage_record = json.loads(storage.stdout)
            assert storage_record['outcome'] == 'rejected' and not storage_record.get('artifact') and not storage_record.get('managed_dir')
            events = cli('events')
            assert any(e['type'] == 'lease.rejected' and e['entity'] == result['id'] for e in events)
            cli('stop')
            assert backend.wait(timeout=8) == 0
            backend = start()
            retained = cli('inspect', result['id'])
            assert retained['outcome'] == 'rejected' and retained['queue_info']['rejection']['code'] == 'start_deadline_exceeded'
            cli('stop')
            assert backend.wait(timeout=8) == 0
            print('Compiled scheduling: durable past/queued deadlines, FIFO blockers/advisory estimates, no target execution, exact retry, real occupied port survival, no missed storage creation and restart retention passed')
        finally:
            for process in reversed(children):
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=8)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait()


if __name__ == '__main__':
    main()
