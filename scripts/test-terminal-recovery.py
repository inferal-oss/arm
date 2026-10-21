#!/usr/bin/env python3
"""Real new-process TUI recovery with deliberately withheld mutation replies.

Only a disposable local backend and polling recipient are used. The transparent
WebSocket relay records operation names/counts, never prints credentials/bodies.
"""
import argparse
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
import runpy
import select
import shutil
import signal
import socket
import struct
import subprocess
import tempfile
import termios
import threading
import time
import urllib.parse
import uuid

TerminalScreen = runpy.run_path(str(Path(__file__).with_name('test-terminal-pools.py')))['TerminalScreen']


def read_exact(sock, count):
    result = bytearray()
    while len(result) < count:
        part = sock.recv(count-len(result))
        if not part:
            raise EOFError()
        result.extend(part)
    return bytes(result)


def headers(sock):
    result = bytearray()
    while not result.endswith(b'\r\n\r\n'):
        result.extend(read_exact(sock, 1))
        assert len(result) <= 16384
    return bytes(result)


def frame(sock):
    head = read_exact(sock, 2)
    length = head[1] & 127
    extension = read_exact(sock, 2 if length == 126 else 8 if length == 127 else 0)
    size = int.from_bytes(extension, 'big') if extension else length
    assert size <= 4*1024*1024
    mask = read_exact(sock, 4) if head[1] & 128 else b''
    body = read_exact(sock, size)
    decoded = bytes(value ^ mask[i % 4] for i, value in enumerate(body)) if mask else body
    return head+extension+mask+body, decoded if head[0] & 15 in (0, 1, 2) else b''


class Relay:
    def __init__(self, target):
        self.target = target
        self.socket = socket.socket()
        self.socket.bind(('127.0.0.1', 0))
        self.socket.listen()
        self.socket.settimeout(.2)
        self.port = self.socket.getsockname()[1]
        self.connections = []
        self.lock = threading.Lock()
        self.stop = False
        self.hold = None
        self.held = threading.Event()
        self.counts = {'pool_mutate': 0, 'message_send': 0, 'pool_lookup': 0, 'message_lookup': 0}
        self.errors = []
        self.thread = threading.Thread(target=self.accept, daemon=True)
        self.thread.start()

    def arm(self, operation):
        with self.lock:
            self.hold = operation
            self.held.clear()

    def accept(self):
        while not self.stop:
            try:
                client, _ = self.socket.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            threading.Thread(target=self.connection, args=(client,), daemon=True).start()

    def connection(self, client):
        upstream = socket.create_connection(self.target, timeout=5)
        upstream.settimeout(None)
        self.connections.extend((client, upstream))
        requests = {}
        try:
            request = headers(client).split(b'\r\n')
            request = [b'Host: '+('%s:%s' % self.target).encode() if line.lower().startswith(b'host:') else line for line in request]
            upstream.sendall(b'\r\n'.join(request))
            client.sendall(headers(upstream))

            def forward(source, destination, incoming):
                try:
                    fragments, content = [], bytearray()
                    while True:
                        wire, data = frame(source)
                        if wire[0] & 15 in (8, 9, 10):
                            destination.sendall(wire)
                            continue
                        fragments.append(wire)
                        content.extend(data)
                        assert len(content) <= 4*1024*1024
                        if not wire[0] & 128:
                            continue
                        packet = json.loads(content)
                        wire = b''.join(fragments)
                        fragments, content = [], bytearray()
                        held = False
                        with self.lock:
                            if incoming and packet and packet.get('operation'):
                                op = packet['operation']
                                requests[str(packet.get('id'))] = op
                                if op in self.counts:
                                    self.counts[op] += 1
                            if not incoming and packet:
                                op = requests.get(str(packet.get('id')))
                                if op == self.hold and op in ('pool_mutate', 'message_send'):
                                    assert not packet.get('error'), packet.get('error')
                                    self.hold = None
                                    held = True
                                    self.held.set()
                        if not held:
                            destination.sendall(wire)
                except (EOFError, OSError):
                    pass
                except Exception as error:
                    self.errors.append(repr(error))
                finally:
                    for item in (source, destination):
                        try:
                            item.shutdown(socket.SHUT_RDWR)
                        except OSError:
                            pass
            a = threading.Thread(target=forward, args=(client, upstream, True), daemon=True)
            a.start()
            forward(upstream, client, False)
        except (EOFError, OSError):
            pass
        finally:
            client.close()
            upstream.close()

    def close(self):
        self.stop = True
        self.socket.close()
        for item in self.connections:
            try:
                item.close()
            except OSError:
                pass
        self.thread.join(timeout=1)


class Terminal:
    def __init__(self, prefix):
        self.master, self.slave = pty.openpty()
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack('HHHH', 40, 140, 0, 0))
        self.screen = TerminalScreen()
        self.process = subprocess.Popen(prefix+['dashboard', 'terminal'], stdin=self.slave, stdout=self.slave, stderr=self.slave, env=dict(os.environ, TERM='xterm-256color', NO_COLOR='1'), start_new_session=True)
        self.wait('● connected')

    def send(self, text):
        os.write(self.master, text.encode())

    def drain(self, seconds=.1):
        end = time.monotonic()+seconds
        while time.monotonic() < end:
            if select.select([self.master], [], [], min(.03, max(0, end-time.monotonic())))[0]:
                try:
                    data = os.read(self.master, 65536)
                except OSError as error:
                    if error.errno == errno.EIO:
                        return
                    raise
                self.screen.feed(data)

    def wait(self, text, seconds=8):
        end = time.monotonic()+seconds
        while time.monotonic() < end:
            self.drain()
            if text in self.screen.text():
                return
            assert self.process.poll() is None, 'terminal exited early'
        raise AssertionError(('missing screen text', text, self.screen.text()))

    def field(self, value, next_label):
        self.send('\x15'+value+'\r')
        self.wait(next_label)

    def close(self, crash=False):
        if self.process.poll() is None:
            if crash:
                self.process.kill()
            else:
                self.send('q')
            self.process.wait(timeout=5)
        os.close(self.master)
        os.close(self.slave)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    binary = str(Path(parser.parse_args().arm).resolve())
    root = Path(tempfile.mkdtemp(prefix='arm-recovery-'))
    home = root/'state'
    prefix = [binary, '--home', str(home)]
    relay = terminal = None
    original = None

    def cli(*args):
        result = subprocess.run(prefix+list(args), capture_output=True, text=True, timeout=15)
        assert result.returncode == 0, (args, result.stderr)
        return json.loads(result.stdout)

    try:
        cli('start', '--cpu', '2', '--memory', '1GiB', '--disk', '1GiB')
        contact = cli('agent', 'register', '--id', 'recovery-poll', '--adapter', 'generic', '--delivery', 'poll', '--session', str(uuid.uuid4()), '--cwd', str(root))
        endpoint_path = home/'endpoint.json'
        original = endpoint_path.read_bytes()
        endpoint = json.loads(original)
        upstream = urllib.parse.urlsplit(endpoint['url'])
        relay = Relay((upstream.hostname, upstream.port))
        endpoint['url'] = 'ws://127.0.0.1:%s%s' % (relay.port, upstream.path)
        endpoint_path.write_text(json.dumps(endpoint))
        os.chmod(endpoint_path, 0o600)

        terminal = Terminal(prefix)
        terminal.send('2')
        terminal.wait('RESOURCES')
        terminal.send('p')
        terminal.wait('CUSTOM POOLS')
        terminal.send('n')
        terminal.wait('Pool name')
        terminal.field('Recovered permits', 'Scope: host or global')
        terminal.field('global', 'Integral count unit')
        terminal.field('count', 'Capacity (0..9223372036854775807)')
        terminal.field('9223372036854775807', 'Description (optional)')
        terminal.field('', 'Required reason')
        terminal.field('Durable pool creation', 'REVIEW POOL')
        relay.arm('pool_mutate')
        terminal.send('y')
        assert relay.held.wait(8), relay.errors
        pending = list((home/'dashboard-pending').glob('op-*.json'))
        assert len(pending) == 1
        saved_pool = json.loads(pending[0].read_text())
        assert saved_pool['kind'] == 'pool' and saved_pool['attempts'] == 1
        terminal.close(crash=True)
        terminal = Terminal(prefix)
        terminal.drain(.3)
        assert relay.counts['pool_mutate'] == 1 and relay.counts['pool_lookup'] == 0, relay.counts
        terminal.send('u')
        terminal.wait('RETAINED OPERATIONS')
        terminal.send('\r')
        terminal.wait('UNKNOWN')
        assert saved_pool['request']['idempotency_key'] in terminal.screen.text()
        assert relay.counts['pool_lookup'] == 0 and relay.counts['pool_mutate'] == 1
        terminal.send('y')  # Explicit same-key retry, never automatic.
        terminal.wait('Recorded journal #')
        assert not list((home/'dashboard-pending').glob('op-*.json'))
        assert relay.counts['pool_mutate'] == 2 and relay.counts['pool_lookup'] == 0
        pools = cli('pool', 'list')
        assert len(pools) == 1 and pools[0]['pool']['capacity'] == 9223372036854775807
        assert len(cli('pool', 'history', pools[0]['pool']['id'])['entries']) == 1

        terminal.send('4')
        terminal.wait('MESSAGES')
        terminal.send('n')
        terminal.wait('Recipient ID')
        terminal.field('recovery-poll', 'Message (one line)')
        terminal.field('Retain original poll delivery exactly once', 'REVIEW MESSAGE')
        relay.arm('message_send')
        terminal.send('y')
        assert relay.held.wait(8), relay.errors
        pending = list((home/'dashboard-pending').glob('op-*.json'))
        assert len(pending) == 1
        saved_message = json.loads(pending[0].read_text())
        assert saved_message['kind'] == 'message'
        assert saved_message['request']['targets'][0]['registration'] == contact['registration']
        terminal.close(crash=True)
        terminal = Terminal(prefix)
        terminal.drain(.3)
        assert relay.counts['message_send'] == 1 and relay.counts['message_lookup'] == 0
        terminal.send('u')
        terminal.wait('RETAINED OPERATIONS')
        terminal.send('\r')
        terminal.wait('UNKNOWN OUTCOME')
        assert saved_message['request']['idempotency_key'] in terminal.screen.text()
        terminal.send('i')
        terminal.wait('Message found:')
        assert not list((home/'dashboard-pending').glob('op-*.json'))
        assert relay.counts['message_send'] == 1 and relay.counts['message_lookup'] == 1
        inbox = cli('agent', 'inbox', '--id', contact['id'], '--session', contact['session'])
        assert len(inbox['items']) == 1, inbox
        terminal.close()
        terminal = None
        assert not relay.errors, relay.errors
        print('Recovery PTY: withheld ACK + killed/restarted process for exact MAX pool and polling message; startup/selection sent nothing; explicit same-key pool retry and message lookup settled exact retained keys; one journal change/one inbox delivery, private intent cleared passed')
    finally:
        if terminal is not None:
            terminal.close(crash=True)
        if original is not None:
            (home/'endpoint.json').write_bytes(original)
            os.chmod(home/'endpoint.json', 0o600)
        if relay is not None:
            relay.close()
        subprocess.run(prefix+['stop', '--force'], capture_output=True, timeout=15)
        shutil.rmtree(root)


if __name__ == '__main__':
    main()
