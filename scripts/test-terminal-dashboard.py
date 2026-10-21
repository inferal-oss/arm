#!/usr/bin/env python3
"""Real PTY acceptance for ARM's terminal dashboard and shared local backend.

Creates only private state and polling recipients. Never opens a browser or
contacts Codex/Claude sessions. Screen assertions use emitted terminal text;
Go layout tests additionally enforce viewport sizes and control-data escaping.
"""
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
import struct
import subprocess
import tempfile
import termios
import time
import uuid


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    binary = str(Path(parser.parse_args().arm).resolve())
    root = Path(tempfile.mkdtemp(prefix='arm-terminal-'))
    prefix = [binary, '--home', str(root / 'state')]
    process = None
    master = slave = None

    def cli(*args):
        result = subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=15)
        assert result.returncode == 0, (args, result.stderr)
        return json.loads(result.stdout)

    try:
        cli('start', '--cpu', '2', '--memory', '1GiB', '--disk', '1GiB')
        lease = cli('acquire', '--cpu', '1', '--ttl', '1m', '--reason', 'PTY build fixture')
        cli('pool', 'create', 'exact-tokens', '--scope', 'global', '--capacity', '9223372036854775807', '--reason', 'PTY exact count')
        contact = cli('agent', 'register', '--id', 'pty-agent', '--adapter', 'generic', '--delivery', 'poll', '--session', str(uuid.uuid4()), '--cwd', str(root))
        for args, code in [(['terminal'], 'terminal_required'), (['terminal', '--json'], 'terminal_required'), (['terminal', '--no-open'], 'invalid_request')]:
            result = subprocess.run(prefix + ['dashboard'] + args, capture_output=True, text=True, timeout=5)
            assert result.returncode == 1 and json.loads(result.stderr)['error']['code'] == code
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 40, 140, 0, 0))
        original = termios.tcgetattr(slave)
        env = dict(os.environ, TERM='xterm-256color', NO_COLOR='1')
        process = subprocess.Popen(prefix + ['dashboard', 'terminal'], stdin=slave, stdout=slave, stderr=slave, env=env, start_new_session=True)
        transcript = bytearray()

        def drain(seconds=.1):
            end = time.monotonic() + seconds
            fresh = bytearray()
            while time.monotonic() < end:
                if select.select([master], [], [], min(.05, max(0, end - time.monotonic())))[0]:
                    try:
                        data = os.read(master, 65536)
                    except OSError as error:
                        if error.errno == errno.EIO:
                            break
                        raise
                    fresh.extend(data)
                    transcript.extend(data)
            return fresh.decode(errors='replace')

        def wait_text(text, seconds=5):
            output = ''
            end = time.monotonic() + seconds
            while time.monotonic() < end:
                output += drain(.1)
                if text in output:
                    return output
                assert process.poll() is None, ('dashboard exited early', bytes(transcript).decode(errors='replace'))
            raise AssertionError(('missing terminal text', text, output[-6000:]))

        def send(text):
            os.write(master, text.encode())

        wait_text('connected')
        send('3')
        wait_text('PTY build fixture')
        send('\r')
        wait_text('Why: PTY build fixture')
        send('x')
        wait_text('REVIEW RELEASE')
        send('n')
        drain(.2)
        assert cli('inspect', lease['id'])['revision'] == lease['revision'], 'declined action changed lease'
        send('e')
        wait_text('FROM NOW')
        send('\x152m\r')
        wait_text('REVIEW RENEW')
        send('y')
        wait_text('current state: active')
        renewed = cli('inspect', lease['id'])
        assert renewed['revision'] == lease['revision'] + 1 and renewed['state'] == 'active'
        send('x')
        wait_text('REVIEW RELEASE')
        send('y')
        wait_text('State: terminal')
        assert cli('inspect', lease['id'])['state'] == 'terminal'
        send('\x1b')
        drain(.15)
        send('2')
        wait_text('9223372036854775807')
        send('4n')
        wait_text('Recipient ID')
        send('pty-agent\r')
        wait_text('Message (one line)')
        send('PTY reviewed polling message\r')
        wait_text('y send exactly this request')
        review = bytes(transcript).decode(errors='replace')
        assert contact['registration'] in review and contact['session'] in review, review
        send('n')
        drain(.15)
        assert not cli('messages'), 'declined message was sent'
        send('n')
        wait_text('Recipient ID')
        send('pty-agent\r')
        drain(.15)
        send('PTY reviewed polling message\r')
        wait_text('y send exactly this request')
        send('y')
        wait_text('Message recorded')
        messages = cli('messages')
        assert len(messages) == 1 and messages[0]['deliveries']['pty-agent']['state'] == 'available'
        send('5')
        wait_text('Activity')
        send('6')
        wait_text('Local authority')
        send('1')
        wait_text('CPU')
        guard = cli('acquire', '--cpu', '1', '--ttl', '5m', '--reason', 'must survive dashboard exit')
        # Actual SIGWINCH handling and compact feed mode.
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 24, 70, 0, 0))
        os.kill(process.pid, signal.SIGWINCH)
        drain(.2)
        send('f')
        wait_text('LIVE FEED')
        send('q')
        process.wait(timeout=5)
        drain(.1)
        assert process.returncode == 0
        restored = termios.tcgetattr(slave)
        # PENDIN is a kernel-maintained pending-input reprint marker on macOS.
        # Restoring canonical mode may set it independently of the saved flags.
        restored[3] &= ~getattr(termios, 'PENDIN', 0)
        original[3] &= ~getattr(termios, 'PENDIN', 0)
        assert restored == original, ('terminal mode was not restored', original, restored)
        assert b'\x1b[?1049h' in transcript and b'\x1b[?1049l' in transcript, 'alternate screen not restored'
        # Exiting the dashboard must not stop the daemon or mutate other work.
        assert cli('status')['host']
        assert cli('inspect', guard['id'])['state'] == 'active'
        # A signal exit must restore the same terminal and leave work intact too.
        process = subprocess.Popen(prefix + ['dashboard', 'terminal'], stdin=slave, stdout=slave, stderr=slave, env=env, start_new_session=True)
        wait_text('connected')
        process.send_signal(signal.SIGTERM)
        process.wait(timeout=5)
        drain(.1)
        assert process.returncode == 130
        restored = termios.tcgetattr(slave)
        restored[3] &= ~getattr(termios, 'PENDIN', 0)
        assert restored == original, 'signal exit did not restore terminal mode'
        assert cli('inspect', guard['id'])['state'] == 'active'
        print('Terminal dashboard PTY: six-view navigation, exact counts, declined/reviewed extension+release, polling message review/send, resize/feed, q/SIGTERM raw mode and alternate-screen restoration, surviving active lease passed')
    finally:
        if process is not None and process.poll() is None:
            process.kill()
            process.wait()
        for fd in (master, slave):
            if fd is not None:
                os.close(fd)
        subprocess.run(prefix + ['stop', '--force'], capture_output=True, timeout=15)
        shutil.rmtree(root)


if __name__ == '__main__':
    main()
