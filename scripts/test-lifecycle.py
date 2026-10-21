#!/usr/bin/env python3
"""Compiled lifecycle checks with real processes, signals, PTY and crash recovery."""
import argparse
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
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
    root = Path(tempfile.mkdtemp(prefix='arm-lifecycle-'))
    home = root / 'state'
    prefix = [binary, '--home', str(home)]
    backend = waiting = None

    def run(*args, timeout=15):
        return subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=timeout)

    def cli(*args):
        result = run(*args)
        assert result.returncode == 0, (args, result.stdout, result.stderr)
        return json.loads(result.stdout)

    def eventually(check, timeout=8):
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            try:
                value = check()
                if value:
                    return value
            except (AssertionError, OSError, ValueError):
                pass
            time.sleep(.03)
        raise AssertionError('lifecycle state did not arrive')

    def start():
        nonlocal backend
        backend = subprocess.Popen(prefix + ['serve', '--cpu', '1', '--memory', 'auto', '--disk', '1GiB'],
                                   stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
        eventually(lambda: cli('status'))

    def stopped():
        assert backend.wait(timeout=8) == 0, backend.stderr.read()
        assert not (home / 'endpoint.json').exists()

    try:
        start()
        first = cli('acquire', '--reason', 'bulk retry snapshot', '--ttl', '2m')
        report = cli('release', '--all', '--key', 'smoke-release')
        assert report['results'][0]['id'] == first['id'] and not report['remaining']
        later = cli('acquire', '--reason', 'outside original bulk scope', '--ttl', '2m')
        retry = cli('release', '--all', '--key', 'smoke-release')
        assert len(retry['results']) == 1
        assert cli('inspect', later['id'])['state'] == 'active', 'retry swept up new work'
        waiting = subprocess.Popen(prefix + ['acquire', '--wait', '--reason', 'queued bulk cancellation', '--key', 'queued-bulk'],
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        eventually(lambda: any(l['state'] == 'queued' for l in cli('status')['leases']))
        report = cli('cancel', '--all', '--key', 'smoke-cancel')
        assert len(report['results']) == 2 and not report['remaining']
        output, error = waiting.communicate(timeout=8)
        assert json.loads(output)['outcome'] == 'cancelled', (output, error)
        waiting = None
        cli('acquire', '--reason', 'timeout force release', '--ttl', '2m')
        report = cli('stop', '--wait', '--timeout', '1')
        assert report['stopped'] and report['force_applied'] and not report['remaining']
        stopped()

        start()
        active = cli('acquire', '--reason', 'independent backend drain', '--ttl', '2m')
        master, slave = pty.openpty()
        waiting = subprocess.Popen(prefix + ['stop', '--wait'], stdin=slave, stdout=slave, stderr=slave,
                                   env=dict(os.environ, TERM='xterm-256color'), start_new_session=True)
        os.close(slave)
        data = bytearray()
        try:
            eventually(lambda: cli('status')['draining'])
            waiting.send_signal(signal.SIGINT)
            end = time.monotonic() + 8
            while time.monotonic() < end:
                if select.select([master], [], [], .1)[0]:
                    try:
                        chunk = os.read(master, 65536)
                    except OSError as error:
                        if error.errno == errno.EIO:
                            break
                        raise
                    if not chunk:
                        break
                    data.extend(chunk)
                elif waiting.poll() is not None:
                    break
            assert waiting.wait(timeout=2) != 0
            assert b'Shutdown ' in data and b'continues independently' in data, data
            assert cli('status')['draining'], 'client signal abandoned backend drain'
            assert run('acquire', '--reason', 'must reject during drain').returncode != 0
            cli('release', active['id'])
            stopped()
        finally:
            os.close(master)
            if waiting.poll() is None:
                waiting.send_signal(signal.SIGINT)
                waiting.wait(timeout=3)
            waiting = None

        start()
        retained = cli('acquire', '--reason', 'interrupted drain recovery', '--ttl', '2m')
        waiting = subprocess.Popen(prefix + ['stop', '--wait'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        eventually(lambda: cli('status')['draining'])
        # This exact Popen child is the deliberately crashed fixture authority.
        backend.kill()
        backend.wait(timeout=5)
        output, error = waiting.communicate(timeout=5)
        assert waiting.returncode != 0 and 'shutdown_interrupted' in error, (output, error)
        waiting = None
        start()
        status = cli('status')
        assert not status['draining'], 'restart silently persisted a process-owned drain'
        assert any(e['type'] == 'shutdown.interrupted' for e in cli('events'))
        assert cli('inspect', retained['id'])['state'] == 'active'
        report = cli('stop', '--force')
        assert report['stopped'] and report['force_applied']
        stopped()
        start()
        cli('acquire', '--reason', 'external backend signal retains obligations', '--ttl', '2m')
        waiting = subprocess.Popen(prefix + ['stop', '--wait'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        eventually(lambda: cli('status')['draining'])
        backend.terminate()
        backend.wait(timeout=5)
        output, error = waiting.communicate(timeout=5)
        assert waiting.returncode != 0 and 'shutdown_interrupted' in error, (output, error)
        waiting = None
        start()
        report = cli('stop', '--force')
        assert report['stopped']
        stopped()
        print('compiled lifecycle: bulk snapshots/cancel, timeout, PTY signal, independent drain and crash recovery passed')
    finally:
        if waiting is not None and waiting.poll() is None:
            waiting.send_signal(signal.SIGINT)
            waiting.wait(timeout=5)
        if backend is not None and backend.poll() is None:
            result = run('stop', '--force')
            if result.returncode:
                print('Retained unresolved lifecycle fixture: ' + str(root))
            else:
                backend.wait(timeout=8)
        try:
            with (home / 'daemon.lock').open('r+') as lock:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                if not (home / 'endpoint.json').exists():
                    shutil.rmtree(root)
                else:
                    print('Retained lifecycle fixture with an endpoint: ' + str(root))
        except (OSError, BlockingIOError):
            print('Retained lifecycle fixture without verified shutdown: ' + str(root))


if __name__ == '__main__':
    main()
