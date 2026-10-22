#!/usr/bin/env python3
"""Compiled dynamic-disk acceptance with actual statfs, controlled writes and queues."""
import argparse
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import struct
import subprocess
import tempfile
import termios
import time



def terminal_disk(prefix):
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 44, 140, 0, 0))
    original = termios.tcgetattr(slave)
    process = subprocess.Popen(prefix + ['dashboard', 'terminal'], stdin=slave, stdout=slave, stderr=slave, env=dict(os.environ, TERM='xterm-256color', NO_COLOR='1'), start_new_session=True)
    transcript = bytearray()

    def wait_text(*needles):
        output = ''
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            if select.select([master], [], [], .1)[0]:
                try:
                    chunk = os.read(master, 65536)
                except OSError as error:
                    if error.errno == errno.EIO:
                        break
                    raise
                transcript.extend(chunk)
                output += chunk.decode(errors='replace')
            clean = ' '.join(re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]', '', output).split())
            if all(needle in clean for needle in needles):
                return
            assert process.poll() is None, bytes(transcript).decode(errors='replace')
        raise AssertionError(('missing live disk PTY text', needles, output[-6000:]))

    try:
        wait_text('connected')
        os.write(master, b'2jj')
        wait_text('LIVE HEADROOM', 'Operator ceiling', 'full promises remain charged')
        os.write(master, b'?')
        wait_text('Dashboard help', 'Storage:')
        os.write(master, b'jjjjjjj\r')
        wait_text('state filesystem', 'Full outstanding promises')
        os.write(master, b'\x1b')
        wait_text('LIVE HEADROOM')
        os.write(master, b'q')
        deadline = time.monotonic() + 8
        while process.poll() is None and time.monotonic() < deadline:
            if select.select([master], [], [], .05)[0]:
                try:
                    transcript.extend(os.read(master, 65536))
                except OSError as error:
                    if error.errno != errno.EIO:
                        raise
        assert process.poll() == 0, ('terminal did not quit', bytes(transcript).decode(errors='replace')[-8000:])
        restored = termios.tcgetattr(slave)
        # macOS may set its kernel-maintained pending-input reprint marker
        # independently when canonical mode is restored (same as main PTY suite).
        restored[3] &= ~getattr(termios, 'PENDIN', 0)
        original[3] &= ~getattr(termios, 'PENDIN', 0)
        assert restored == original, ('terminal mode was not restored', original, restored)
        assert b'\x1b[?1049h' in transcript and b'\x1b[?1049l' in transcript, 'alternate screen not restored'
    finally:
        if process.poll() is None:
            process.kill()
            process.wait(timeout=5)
        os.close(master)
        os.close(slave)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    binary = str(Path(parser.parse_args().arm).resolve())
    children = []
    with tempfile.TemporaryDirectory(prefix='arm-live-disk-') as directory:
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
            raise AssertionError('live disk fixture did not reach expected state')

        def start(*disk):
            process = subprocess.Popen(prefix + ['serve', '--cpu', '2', '--memory', 'auto'] + list(disk), stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
            children.append(process)
            until(lambda: process.poll() is None and run('status').returncode == 0)
            return process

        def fresh(after=None):
            def read():
                status = cli('status')
                budget = status['disk_budget']
                return status if budget['known'] and (after is None or budget['observation']['at'] != after) else None
            return until(read)

        def verify(status, reserved, ceiling):
            budget = status['disk_budget']
            assert budget['known'] and not budget['stale']
            assert budget['ceiling_bytes'] == ceiling
            expected_pool = budget['observation']['available_bytes']
            if ceiling is not None:
                expected_pool = min(expected_pool, ceiling)
            assert budget['capacity_bytes'] == expected_pool
            assert budget['reserved_bytes'] == reserved
            assert budget['available_bytes'] == max(0, expected_pool - reserved)
            assert status['capacity']['disk_bytes'] == expected_pool
            assert status['reserved']['disk_bytes'] == reserved
            resource = next(r for r in status['resources'] if r['kind'] == 'disk')
            assert resource['budget_total'] == expected_pool and resource['reserved'] == reserved
            assert resource['available'] == budget['available_bytes']
            return budget

        try:
            # No --disk argument: the ordinary default must be live, never an
            # implicit20GiB reservation ceiling or a saved free-space snapshot.
            backend = start()
            before = verify(fresh(), 0, None)
            assert before['observation']['volume'] and before['observation']['path'] == str(state)
            if before['available_bytes'] < 32 * 1024 * 1024:
                raise AssertionError('controlled-write test requires32MiB available; it never fills the host disk')
            promise = cli('acquire', '--cpu', '0', '--disk', '1MiB', '--ttl', '2m', '--reason', 'full promise while writing unrelated fixture data')
            pressure = root / 'controlled-write.bin'
            native_before = os.statvfs(state)
            with pressure.open('wb') as output:
                for _ in range(8):
                    output.write(os.urandom(1024 * 1024))
                output.flush()
                os.fsync(output.fileno())
            after = verify(fresh(before['observation']['at']), 1024 * 1024, None)
            native_after = os.statvfs(state)
            # APFS may apply a lower shared-container bound. External host I/O
            # can also move counters concurrently; do not claim a byte-for-byte
            # exclusive impact from this file or invent reclaimable extents.
            native_upper = max(native_before.f_bavail * native_before.f_frsize, native_after.f_bavail * native_after.f_frsize)
            assert after['observation']['available_bytes'] <= native_upper + 16 * 1024 * 1024
            assert pressure.stat().st_size == 8 * 1024 * 1024
            assert after['reserved_bytes'] == 1024 * 1024
            cli('release', promise['id'])
            assert pressure.exists(), 'lease release deleted unrelated data'
            pressure.unlink()
            verify(fresh(after['observation']['at']), 0, None)
            cli('stop')
            assert backend.wait(timeout=8) == 0
            # A positive configured ceiling is separate from mutable free space
            # and from peer grants or owner policy zero values.
            backend = start('--disk', '4MiB')
            verify(fresh(), 0, 4 * 1024 * 1024)
            holder = cli('acquire', '--cpu', '0', '--disk', '2MiB', '--reason', 'disk queue predecessor')
            before_terminal = cli('inspect', holder['id'])['revision']
            terminal_disk(prefix)
            assert cli('inspect', holder['id'])['revision'] == before_terminal
            marker = root / 'granted-command'
            waiting = subprocess.Popen(prefix + ['run', '--disk', '3MiB', '--cpu', '.25', '--wait', '--reason', 'wait for actual disk promise headroom', '--key', 'disk-wait', '--json', '--', '/bin/sh', '-c', 'touch "$1"', 'fixture', str(marker)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            children.append(waiting)
            queued = until(lambda: next((l for l in cli('status')['leases'] if l['request']['idempotency_key'] == 'disk-wait' and l['state'] == 'queued'), None))
            assert any(b['code'] == 'disk_headroom' for b in queued['queue_info']['blockers'])
            assert queued['queue_info']['estimate']['kind'] == 'unknown' and not marker.exists()
            too_big = run('acquire', '--cpu', '0', '--disk', '5MiB', '--wait', '--reason', 'explicit ceiling impossible')
            assert too_big.returncode == 1 and json.loads(too_big.stderr)['error']['code'] == 'capacity_exceeded'
            cli('release', holder['id'])
            stdout, stderr = waiting.communicate(timeout=12)
            assert waiting.returncode == 0 and json.loads(stdout)['outcome'] == 'completed' and marker.exists(), (stdout, stderr)
            cli('stop')
            assert backend.wait(timeout=8) == 0
            # An ordinary explicit-ceiling restart preserves that configured
            # policy; automatic-mesh saved-startup provenance is tested separately.
            backend = start('--disk', '4MiB')
            verify(fresh(), 0, 4 * 1024 * 1024)
            cli('stop')
            assert backend.wait(timeout=8) == 0
            backend = start('--disk', 'auto')
            verify(fresh(), 0, None)
            cli('stop')
            assert backend.wait(timeout=8) == 0
            print('Compiled live disk: default auto/native statfs, controlled8MiB write/fresh samples/full promises, unrelated-file survival, optional ceiling, FIFO no-estimate actual command, impossible configured claim explicit auto/ceiling restart and actual read-only PTY live labels/help/terminal restore passed')
        finally:
            for process in reversed(children):
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=8)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=5)


if __name__ == '__main__':
    main()
