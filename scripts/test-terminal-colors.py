#!/usr/bin/env python3
"""Private real-PTY visual-profile, compact navigation and review-decline gates."""
import argparse
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import subprocess
import tempfile
import termios
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    binary = str(Path(parser.parse_args().arm).resolve())
    with tempfile.TemporaryDirectory(prefix='arm-tui-colors-') as directory:
        prefix = [binary, '--home', str(Path(directory) / 'state')]

        def cli(*args):
            result = subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=15)
            assert result.returncode == 0, (args, result.stdout, result.stderr)
            return json.loads(result.stdout)

        try:
            cli('start', '--cpu', '2')
            lease = cli('acquire', '--cpu', '1', '--ttl', '1h', '--reason', 'Color-profile review fixture')
            for profile, term, colorterm, mono in [('truecolor', 'xterm-256color', 'truecolor', False), ('256', 'xterm-256color', '', False), ('16', 'xterm', '', False), ('mono', 'xterm-256color', 'truecolor', True)]:
                master, slave = pty.openpty()
                process = None
                transcript = bytearray()
                fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 40, 140, 0, 0))
                original = termios.tcgetattr(slave)
                env = dict(os.environ, TERM=term, COLORTERM=colorterm)
                for key in ('NO_COLOR', 'CLICOLOR', 'CLICOLOR_FORCE', 'FORCE_COLOR'):
                    env.pop(key, None)
                if mono:
                    env['NO_COLOR'] = '1'

                def drain(seconds=.1):
                    fresh = bytearray()
                    end = time.monotonic() + seconds
                    while time.monotonic() < end:
                        if select.select([master], [], [], min(.04, max(0, end - time.monotonic())))[0]:
                            try:
                                raw = os.read(master, 65536)
                            except OSError as error:
                                if error.errno == errno.EIO:
                                    break
                                raise
                            fresh.extend(raw)
                            transcript.extend(raw)
                    return fresh.decode(errors='replace')

                def wait_text(text):
                    output = ''
                    end = time.monotonic() + 8
                    while time.monotonic() < end:
                        output += drain()
                        if text in re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]', '', output):
                            return
                        assert process.poll() is None, (profile, 'exited early', bytes(transcript)[-6000:])
                    raise AssertionError((profile, text, output[-5000:]))

                def send(text):
                    os.write(master, text.encode())

                try:
                    process = subprocess.Popen(prefix + ['dashboard', 'terminal'], stdin=slave, stdout=slave, stderr=slave, env=env, start_new_session=True)
                    wait_text('CPU')
                    send('3')
                    wait_text('Hide inactive leases')
                    send('\r')
                    wait_text('State: Active')
                    send('x')
                    wait_text('REVIEW RELEASE')
                    send('n')
                    drain(.15)
                    assert cli('inspect', lease['id'])['revision'] == lease['revision'], 'visual-profile decline mutated lease'
                    send('\x1b')
                    drain(.12)
                    send('1')
                    drain(.12)
                    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 22, 45, 0, 0))
                    os.kill(process.pid, signal.SIGWINCH)
                    wait_text('6Host')
                    send('q')
                    process.wait(timeout=5)
                    drain(.15)
                    assert process.returncode == 0
                    restored = termios.tcgetattr(slave)
                    restored[3] &= ~getattr(termios, 'PENDIN', 0)
                    original[3] &= ~getattr(termios, 'PENDIN', 0)
                    assert restored == original, (profile, 'terminal modes not restored')
                    assert b'\x1b[?1049h' in transcript and b'\x1b[?1049l' in transcript
                    sgr = re.findall(rb'\x1b\[([0-9;]*)m', transcript)
                    rgb = any(b'38;2;' in code for code in sgr)
                    indexed = any(b'38;5;' in code for code in sgr)
                    if profile == 'truecolor':
                        assert rgb, 'capable terminal received no truecolor rendering'
                    else:
                        assert not rgb, (profile, 'unsupported RGB rendering')
                    if profile == '256':
                        assert indexed, '256-color terminal received no indexed rendering'
                    if profile == '16':
                        assert not indexed, '16-color terminal received unsupported indexed colors'
                    if mono:
                        color_params = {str(n).encode() for n in [*range(30, 38), *range(40, 48), *range(90, 98), *range(100, 108), 38, 48]}
                        assert not any(color_params.intersection(code.split(b';')) for code in sgr), 'NO_COLOR emitted color SGR'
                    assert cli('inspect', lease['id'])['state'] == 'active', 'dashboard exit released work'
                finally:
                    if process is not None and process.poll() is None:
                        process.terminate()
                        process.wait(timeout=8)
                    os.close(master)
                    os.close(slave)
            print('Real PTY: TrueColor/256/16/NO_COLOR, reviewed decline, compact labels/resize, alternate screen+termios restore and surviving lease passed')
        finally:
            cli('stop', '--force')


if __name__ == '__main__':
    main()
