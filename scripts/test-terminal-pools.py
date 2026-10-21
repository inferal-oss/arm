#!/usr/bin/env python3
"""Private real-PTY acceptance of guarded dashboard pool operations.

No user state, browser, native agent session or external resource is touched.
"""
import argparse
import codecs
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import shutil
import signal
import struct
import subprocess
import tempfile
import termios
import time
import unicodedata



class TerminalScreen:
    """Track the emitted VT cursor/erase subset, including incremental redraws."""
    def __init__(self, width=140, height=40):
        self.width, self.height = width, height
        self.cells = [[' ']*width for _ in range(height)]
        self.x = self.y = 0
        self.buffer = ''
        self.decoder = codecs.getincrementaldecoder('utf-8')('replace')

    def feed(self, data):
        self.buffer += self.decoder.decode(data)
        while self.buffer:
            c = self.buffer[0]
            if c == '\x1b':
                if len(self.buffer) < 2:
                    return
                if self.buffer[1] == '[':
                    match = re.match(r'\x1b\[([0-?]*)([ -/]*)([@-~])', self.buffer)
                    if not match:
                        return
                    self.control(match[1], match[3])
                    self.buffer = self.buffer[match.end():]
                    continue
                if self.buffer[1] == ']':
                    match = re.search(r'\x07|\x1b\\', self.buffer[2:])
                    if not match:
                        return
                    self.buffer = self.buffer[2+match.end():]
                    continue
                self.buffer = self.buffer[2:]
                continue
            self.buffer = self.buffer[1:]
            if c == '\r':
                self.x = 0
            elif c == '\n':
                self.y = min(self.height-1, self.y+1)
            elif c == '\b':
                self.x = max(0, self.x-1)
            elif c >= ' ' and not unicodedata.combining(c):
                width = 2 if unicodedata.east_asian_width(c) in ('W', 'F') else 1
                if self.x >= self.width:
                    self.x, self.y = 0, min(self.height-1, self.y+1)
                self.cells[self.y][self.x] = c
                if width == 2 and self.x+1 < self.width:
                    self.cells[self.y][self.x+1] = ''
                self.x += width

    def control(self, args, command):
        if args.startswith('?'):
            if args == '?1049' and command == 'h':
                self.cells = [[' ']*self.width for _ in range(self.height)]
            return
        if not re.fullmatch(r'[0-9;]*', args):
            return
        values = [int(v or 0) for v in args.split(';')]
        n = values[0] or 1
        if command in ('H', 'f'):
            self.y = min(self.height-1, max(0, n-1))
            self.x = min(self.width-1, max(0, (values[1] if len(values)>1 else 1)-1))
        elif command == 'G':
            self.x = min(self.width-1, max(0, n-1))
        elif command == 'd':
            self.y = min(self.height-1, max(0, n-1))
        elif command in ('A', 'B'):
            self.y = max(0, min(self.height-1, self.y + (n if command == 'B' else -n)))
        elif command in ('C', 'D'):
            self.x = max(0, min(self.width-1, self.x + (n if command == 'C' else -n)))
        elif command == 'X':
            for x in range(self.x, min(self.width, self.x+n)):
                self.cells[self.y][x] = ' '
        elif command == 'K':
            start, end = (0, self.width) if values[0] == 2 else ((0, self.x+1) if values[0] == 1 else (self.x, self.width))
            self.cells[self.y][start:end] = [' ']*(end-start)
        elif command == 'J':
            if values[0] == 2:
                self.cells = [[' ']*self.width for _ in range(self.height)]
            elif values[0] == 0:
                self.cells[self.y][self.x:] = [' ']*(self.width-self.x)
                for row in range(self.y+1, self.height):
                    self.cells[row] = [' ']*self.width

    def text(self):
        return '\n'.join(''.join(row) for row in self.cells)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    binary = str(Path(parser.parse_args().arm).resolve())
    root = Path(tempfile.mkdtemp(prefix='arm-pool-pty-'))
    prefix = [binary, '--home', str(root / 'state')]
    process = None
    master = slave = None

    def cli(*args):
        result = subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=15)
        assert result.returncode == 0, (args, result.stderr)
        return json.loads(result.stdout)

    try:
        cli('start', '--cpu', '2', '--memory', '1GiB', '--disk', '1GiB')
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 40, 140, 0, 0))
        process = subprocess.Popen(prefix + ['dashboard', 'terminal'], stdin=slave, stdout=slave, stderr=slave, env=dict(os.environ, TERM='xterm-256color', NO_COLOR='1'), start_new_session=True)
        transcript = bytearray()
        screen = TerminalScreen()

        def drain(seconds=.1):
            end = time.monotonic() + seconds
            fresh = bytearray()
            while time.monotonic() < end:
                if select.select([master], [], [], min(.05, max(0, end-time.monotonic())))[0]:
                    try:
                        data = os.read(master, 65536)
                    except OSError as error:
                        if error.errno == errno.EIO:
                            break
                        raise
                    fresh.extend(data)
                    transcript.extend(data)
                    screen.feed(data)
            return fresh.decode(errors='replace')

        def wait_text(text, seconds=6):
            output = ''
            end = time.monotonic() + seconds
            while time.monotonic() < end:
                output += drain()
                if text in screen.text():
                    return
                assert process.poll() is None, 'TUI exited early'
            raise AssertionError(('missing screen text', text, screen.text(), 'recent bytes', output[-2000:]))

        def send(text):
            os.write(master, text.encode())

        def field(value, next_label):
            send('\x15' + value + '\r')
            wait_text(next_label)

        wait_text('connected')
        send('2')
        wait_text('RESOURCES')
        send('p')
        wait_text('CUSTOM POOLS')
        send('n')
        wait_text('Pool name')
        field('PTY exact pool', 'Scope: host or global')
        field('global', 'Integral count unit')
        field('permits', 'Capacity (0..9223372036854775807)')
        field('9223372036854775807', 'Description (optional)')
        field('', 'Required reason')
        field('PTY exact creation', 'REVIEW POOL')
        assert cli('pool', 'list') == [], 'wizard/review mutated before confirmation'
        send('y')
        wait_text('Recorded journal #')
        pools = cli('pool', 'list')
        assert len(pools) == 1 and pools[0]['pool']['capacity'] == 9223372036854775807
        pool_id = pools[0]['pool']['id']
        lease = cli('acquire', '--pool', pool_id+'=9223372036854775807', '--ttl', '5m', '--reason', 'PTY retain live obligation')
        send('\x1b')
        drain(.2)
        send('r')
        wait_text('9223372036854775807')
        send('\r')
        wait_text('CURRENT CONSUMERS')
        send('a')
        wait_text('Select a reviewed operation')
        send('\r')
        wait_text('Capacity (0..9223372036854775807)')
        field('0', 'Required reason')
        field('PTY decline reduction', 'REVIEW POOL')
        send('n')
        drain(.2)
        assert cli('pool', 'inspect', pool_id)['pool']['revision'] == 1, 'decline changed pool'
        send('a')
        wait_text('Select a reviewed operation')
        send('\r')
        wait_text('Capacity (0..9223372036854775807)')
        field('0', 'Required reason')
        field('PTY reviewed reduction', 'REVIEW POOL')
        send('y')
        wait_text('Recorded journal #')
        view = cli('pool', 'inspect', pool_id)
        assert (view['pool']['capacity'], view['reserved'], view['deficit']) == (0, 9223372036854775807, 9223372036854775807)
        assert cli('inspect', lease['id'])['state'] == 'active'

        def operation(index, reason, value=None):
            send('\x1b')
            wait_text('CUSTOM POOLS · LIST')
            # Opening the cached stable ID performs a fresh pool_inspect. A
            # redundant list refresh may emit only ANSI cell deltas, so it is
            # not a meaningful synchronization point for a text-stream test.
            send('\r')
            wait_text('CURRENT CONSUMERS')
            send('a')
            wait_text('Select a reviewed operation')
            for _ in range(index):
                send('\x1b[B')
                drain(.08)
            send('\r')
            wait_text('Signed nonzero adjustment' if value is not None else 'Required reason')
            if value is not None:
                field(value, 'Required reason')
            field(reason, 'REVIEW POOL')
            send('y')
            wait_text('Recorded journal #' if not reason.startswith('refuse') else 'pool_in_use')

        operation(1, 'PTY adjustment', '5')
        assert cli('pool', 'inspect', pool_id)['pool']['capacity'] == 5
        operation(2, 'PTY drain')
        assert cli('pool', 'inspect', pool_id)['pool']['state'] == 'draining'
        operation(2, 'PTY resume')
        assert cli('pool', 'inspect', pool_id)['pool']['state'] == 'active'
        operation(3, 'refuse active deletion')
        assert cli('pool', 'inspect', pool_id)['pool']['state'] == 'active'
        cli('release', lease['id'])
        operation(3, 'PTY tombstone')
        assert cli('pool', 'inspect', pool_id)['pool']['state'] == 'deleted'
        history = cli('pool', 'history', pool_id)
        assert [e['operation'] for e in history['entries']] == ['create', 'set', 'adjust', 'drain', 'resume', 'delete']
        assert all(e['actor'] and e['reason'] for e in history['entries'])
        send('\x1b')
        wait_text('CUSTOM POOLS · LIST')
        send('\r')
        wait_text('CURRENT CONSUMERS')
        send('h')
        wait_text('CAPACITY-CHANGE JOURNAL')
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 14, 45, 0, 0))
        os.kill(process.pid, signal.SIGWINCH)
        drain(.3)
        send('q')
        process.wait(timeout=5)
        assert process.returncode == 0
        print('Pool PTY: exact int64 creation, default-no decline, live-obligation deficit, set/adjust/drain/resume, active-delete refusal, tombstone journal, narrow resize and clean exit passed')
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
