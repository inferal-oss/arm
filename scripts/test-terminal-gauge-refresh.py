#!/usr/bin/env python3
"""Disposable native PTY regression for between-tick gauge receipts.

Retains raw transcripts and timed observations. This checks emitted terminal
text, not a reconstructed terminal screen or a remote dashboard.
"""
import argparse
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import shutil
import struct
import subprocess
import tempfile
import termios
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', required=True)
    parser.add_argument('--output-dir', required=True)
    args = parser.parse_args()
    binary = str(Path(args.arm).resolve())
    output = Path(args.output_dir)
    output.mkdir(parents=True, exist_ok=False)
    root = Path(tempfile.mkdtemp(prefix='arm-gauge-refresh-'))
    prefix = [binary, '--home', str(root / 'state')]
    process = None
    master = slave = None
    transcript = bytearray()
    observations = []
    ansi = re.compile(r'\x1b\[[0-?]*[ -/]*[@-~]')

    def cli(*words):
        result = subprocess.run(prefix + list(words), capture_output=True, text=True, timeout=15)
        assert result.returncode == 0, (words, result.stderr)
        return json.loads(result.stdout)

    def drain(seconds, expect_running=True):
        end = time.monotonic() + seconds
        fresh = bytearray()
        while time.monotonic() < end:
            if select.select([master], [], [], min(.025, max(0, end-time.monotonic())))[0]:
                try:
                    data = os.read(master, 65536)
                except OSError as error:
                    if error.errno == errno.EIO:
                        break
                    raise
                fresh.extend(data)
                transcript.extend(data)
            if expect_running:
                assert process.poll() is None, 'dashboard exited early'
        return ansi.sub('', fresh.decode(errors='replace'))

    def wait_for(text, seconds=8):
        fresh = ''
        end = time.monotonic() + seconds
        while time.monotonic() < end:
            fresh += drain(.1)
            if text in fresh:
                return fresh
        raise AssertionError(('missing fresh text', text, fresh[-5000:]))

    try:
        cli('start')
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 50, 160, 0, 0))
        original = termios.tcgetattr(slave)
        process = subprocess.Popen(prefix + ['dashboard', 'terminal'], stdin=slave, stdout=slave, stderr=slave,
                                   env=dict(os.environ, TERM='xterm-256color', NO_COLOR='1'), start_new_session=True)
        wait_for('RAM remaining')
        drain(3)  # Permit the native first sample and independent collectors.
        for index in range(24):
            # Force fresh panes and native snapshot requests at many timer phases.
            os.write(master, b'2r')
            resources = drain(.19)
            os.write(master, b'1r')
            monitor = drain(.31)
            combined = resources + monitor
            observations.append({'iteration': index, 'monotonic': time.monotonic(),
                                 'ram_measured': bool(re.search(r'(?:GiB|MiB) RAM remaining', monitor)),
                                 'space_measured': bool(re.search(r'(?:GiB|MiB) space remaining', monitor)),
                                 'false_unavailable': 'unavailable RAM remaining' in combined or 'unavailable space remaining' in combined})
        assert not any(row['false_unavailable'] for row in observations), observations
        assert sum(row['ram_measured'] for row in observations) >= 12, observations
        assert sum(row['space_measured'] for row in observations) >= 12, observations
        cli('stop', '--force')
        disconnected = wait_for('disconnected') + drain(1)
        assert 'unavailable RAM remaining' in disconnected and 'unavailable space remaining' in disconnected, disconnected
        cli('start')
        recovered = wait_for('● connected', seconds=25) + drain(3)
        os.write(master, b'21')
        recovered += drain(.5)
        assert re.search(r'(?:GiB|MiB) RAM remaining', recovered), recovered
        assert re.search(r'(?:GiB|MiB) space remaining', recovered), recovered
        os.write(master, b'q')
        process.wait(timeout=5)
        assert process.returncode == 0
        drain(.1, expect_running=False)
        restored = termios.tcgetattr(slave)
        restored[3] &= ~getattr(termios, 'PENDIN', 0)
        original[3] &= ~getattr(termios, 'PENDIN', 0)
        assert restored == original, 'terminal flags not restored'
        assert b'\x1b[?1049l' in transcript, 'alternate screen not restored'
        print(json.dumps({'iterations': len(observations), 'ram_measured': sum(row['ram_measured'] for row in observations),
                          'space_measured': sum(row['space_measured'] for row in observations),
                          'disconnect_unavailable': True, 'reconnect_measured': True, 'terminal_restored': True}))
    finally:
        try:
            if process is not None and process.poll() is None:
                process.kill()
                process.wait(timeout=5)
            for fd in (master, slave):
                if fd is not None:
                    os.close(fd)
            subprocess.run(prefix + ['stop', '--force'], capture_output=True, timeout=15)
            shutil.rmtree(root)
        finally:
            (output / 'terminal.ansi').write_bytes(transcript)
            (output / 'observations.json').write_text(json.dumps(observations, indent=2) + '\n')


if __name__ == '__main__':
    main()
