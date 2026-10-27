#!/usr/bin/env python3
"""Public CLI B→A→C TCP forwarding through three private loopback daemons.

The source and destination never enroll each other. The destination HTTP
service is a literal loopback endpoint and the source listener requires its
new private token file. A failed fixture is retained for inspection.
"""
import argparse
import fcntl
import http.server
import importlib.util
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import socketserver
import subprocess
import sys
import tempfile
import threading
import time
import uuid

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('routed_ssh_acceptance', Path(__file__).with_name('test-routed-ssh.py'))
route = importlib.util.module_from_spec(spec)
spec.loader.exec_module(route)
mesh, ws = route.mesh, route.ws


class Node(route.Node):
    def start(self):
        self.cli('start', '--cpu', '1', '--memory', '256MiB', '--disk', '1GiB')


class Service(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def do_GET(self):
        assert self.path == '/exact-service', self.path
        assert self.headers.get('Proxy-Authorization') is None, 'local bearer reached destination service'
        body = b'forwarded-b-to-c'
        self.send_response(200)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_):
        pass


class Echo(socketserver.BaseRequestHandler):
    def handle(self):
        while block := self.request.recv(4096):
            self.request.sendall(block)


def request(bind, token=None):
    host, port = bind.rsplit(':', 1)
    with socket.create_connection((host.strip('[]'), int(port)), timeout=15) as conn:
        conn.settimeout(15)
        credential = f'Proxy-Authorization: Bearer {token}\r\n' if token else ''
        conn.sendall(('GET /exact-service HTTP/1.1\r\nHost: exact-service\r\n'
                      + credential + 'Connection: close\r\n\r\n').encode())
        result = bytearray()
        while True:
            try:
                block = conn.recv(4096)
            except (ConnectionResetError, TimeoutError):
                break
            if not block:
                break
            result.extend(block)
        return bytes(result)


def raw_request(bind, token, payload):
    host, port = bind.rsplit(':', 1)
    with socket.create_connection((host.strip('[]'), int(port)), timeout=15) as conn:
        conn.settimeout(15)
        conn.sendall(f'ARM-FORWARD/1 {token}\n'.encode() + payload)
        conn.shutdown(socket.SHUT_WR)
        result = bytearray()
        while True:
            try:
                block = conn.recv(4096)
            except (ConnectionResetError, TimeoutError):
                break
            if not block:
                break
            result.extend(block)
        return bytes(result)


def reconcile_retained_sources(node, binary, keys):
    # The strict local rebind probe can retain a charge after an HTTP socket
    # enters TIME_WAIT. Only the exact owned fixture process is terminated;
    # the replacement backend then verifies its exclusive lock and free port.
    stop = node.result('stop', '--force', timeout=20)
    assert stop.returncode != 0, 'retained source charge unexpectedly permitted ordinary Stop'
    endpoint = json.loads((node.home / 'endpoint.json').read_text())
    pid = endpoint['pid']
    command = subprocess.check_output(['ps', '-p', str(pid), '-o', 'command='], text=True).strip()
    expected = f'{binary} --home {node.home} serve '
    assert command.startswith(expected), (pid, command, expected)
    os.kill(pid, signal.SIGTERM)
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            break
        time.sleep(.05)
    else:
        raise AssertionError('original private source daemon did not exit')
    with (node.home / 'daemon.lock').open('r+') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    node.start()
    for key in keys:
        deadline = time.monotonic() + 90
        while True:
            current = node.cli('mesh', 'forward', 'status', '--key', key)
            assert current['key'] == key and current['state'] == 'needs_attention' and not current['original_owner'], current
            result = node.result('mesh', 'forward', 'recover', '--key', key,
                                 '--reason', 'reconcile exact private fixture port after original exit', '--yes')
            if result.returncode == 0:
                recovered = json.loads(result.stdout)
                break
            error = json.loads(result.stderr)['error']
            assert error['code'] == 'needs_attention' and time.monotonic() < deadline, error
            time.sleep(1)
        assert recovered['key'] == key and recovered['state'] == 'terminal' and recovered['outcome'] == 'reconciled', recovered
    # The old Stop request left this backend draining. Settlement may let it
    # exit before a second public Stop request can reach its endpoint.
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        if not (node.home / 'endpoint.json').exists():
            with (node.home / 'daemon.lock').open('r+') as lock:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            node.stopped = True
            break
        if node.stop():
            break
        time.sleep(.1)
    assert node.stopped, 'reconciled private source backend did not close'


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', required=True)
    args = parser.parse_args()
    binary = str(Path(args.arm).resolve())
    root = Path(tempfile.mkdtemp(prefix='arm-routed-forward-cli-')).resolve()
    root.chmod(0o700)
    a, b, c = [Node(binary, root, name) for name in ('relay-a', 'source-b', 'target-c')]
    nodes = [a, b, c]
    service = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Service)
    thread = threading.Thread(target=service.serve_forever, daemon=True)
    thread.start()
    echo = socketserver.ThreadingTCPServer(('127.0.0.1', 0), Echo)
    echo_thread = threading.Thread(target=echo.serve_forever, daemon=True)
    echo_thread.start()
    success = False
    try:
        for node in nodes:
            node.start()
            port = mesh.free_port()
            node.identity = node.cli('mesh', 'init', '--name', node.name,
                                     '--listen', f'127.0.0.1:{port}',
                                     '--address', f'127.0.0.1:{port}', '--yes')
        ws.enroll(a, b)
        ws.enroll(a, c)

        def decide(node, operation):
            node.cli('workspaces', 'decide', '--workspace', operation['workspace'],
                     '--key', operation['key'], '--revision', str(operation['revision']),
                     '--review', operation['review_sha256'], '--approve', '--yes')

        created = a.cli('workspaces', 'create', '--name', 'Private TCP forwarding CLI',
                        '--key', 'create', '--reason', 'source B reaches target C through A')
        wid = created['workspace']
        decide(a, created)
        invite = root / 'invitation.json'
        a.cli('workspaces', 'invitation', '--workspace', wid, '--output', str(invite))
        for node in (b, c):
            join = node.cli('workspaces', 'join', '--invitation', str(invite),
                            '--key', 'join', '--reason', 'original member admission')
            decide(node, join)
            consent = root / f'{node.name}-consent.json'
            node.cli('workspaces', 'consent', '--workspace', wid, '--key', 'join', '--output', str(consent))
            command = ['workspaces', 'admit', '--workspace', wid, '--consent', str(consent),
                       '--key', f'admit-{node.name}', '--reason', 'exact current candidate']
            review = a.cli(*command)
            assert a.cli(*command, '--review', review['review_sha256'], '--yes')['phase'] == 'completed'
            node.cli('workspaces', 'refresh', '--workspace', wid)
            current = node.cli('workspaces', 'lookup', '--workspace', wid, '--key', 'join')['operation']
            node.cli('workspaces', 'finalize', '--workspace', wid, '--key', 'join',
                     '--revision', str(current['revision']))
        for node in (b, c):
            node.cli('workspaces', 'refresh', '--workspace', wid)
            peers = node.cli('mesh', 'peers')
            assert len(peers) == 1 and peers[0]['identity']['host'] == a.identity['host'], peers
        generation = a.cli('workspaces', 'inspect', '--workspace', wid,
                           '--section', 'members', '--limit', '1')['workspace']['generation']
        a.cli('mesh', 'ssh-relay', 'grant-relay', '--workspace', wid,
              '--generation', generation, '--relay-cpu', '0.05', '--relay-memory', '8MiB',
              '--reason', 'generic bounded workspace transport', '--yes')
        c.cli('mesh', 'forward', 'grant', '--workspace', wid, '--source', b.identity['host'],
              '--service', 'web', '--target-address', f'127.0.0.1:{service.server_port}',
              '--connections', '2', '--reason', 'exact original HTTP service', '--yes')
        c.cli('mesh', 'forward', 'grant', '--workspace', wid, '--source', b.identity['host'],
              '--service', 'echo', '--target-address', f'127.0.0.1:{echo.server_address[1]}',
              '--connections', '2', '--reason', 'exact original raw TCP service', '--yes')
        token_file = root / 'source-token'
        key = uuid.uuid4().hex
        try:
            started = b.cli('mesh', 'forward', 'start', '--workspace', wid,
                            '--target', c.identity['host'], '--service', 'web', '--revision', '1',
                            '--key', key, '--token-file', str(token_file), '--ttl', '90s')
        except AssertionError as failure:
            status = b.cli('status')
            cpu = status.get('cpu_budget', {})
            raise AssertionError((failure, {'cpu_known': cpu.get('known'), 'cpu_detail': cpu.get('observation', {}).get('detail'), 'cpu_available': cpu.get('available_cores')})) from failure
        assert started['key'] == key and token_file.stat().st_mode & 0o777 == 0o600, started
        assert b.cli('mesh', 'forward', 'status', '--key', key)['key'] == key
        assert any(row['key'] == key for row in b.cli('mesh', 'forward', 'list'))
        token = token_file.read_text().strip()
        assert len(token) == 64
        assert b'forwarded-b-to-c' not in request(started['bind']), 'unauthenticated local access succeeded'
        response = request(started['bind'], token)
        assert b'HTTP/1.1 200 OK' in response and response.endswith(b'forwarded-b-to-c'), response
        http_client = subprocess.run(b.prefix + ['mesh', 'forward', 'http', '--key', key,
                                                 '--token-file', str(token_file),
                                                 '--path', '/exact-service'],
                                     capture_output=True, timeout=20)
        assert (http_client.returncode, http_client.stdout, http_client.stderr) == (0, b'forwarded-b-to-c', b''), http_client
        # The carrier admits at most two new neighbor streams per second.
        time.sleep(1.1)
        raw_key = uuid.uuid4().hex
        raw_file = root / 'raw-token'
        raw = b.cli('mesh', 'forward', 'start', '--workspace', wid,
                    '--target', c.identity['host'], '--service', 'echo', '--revision', '1',
                    '--key', raw_key, '--token-file', str(raw_file), '--ttl', '90s')
        raw_token = raw_file.read_text().strip()
        payload = b'\x00raw-duplex\xff' * 128
        assert raw_request(raw['bind'], raw_token, payload) == payload
        adapter = subprocess.run(b.prefix + ['mesh', 'forward', 'stream', '--key', raw_key,
                                            '--token-file', str(raw_file)], input=payload,
                                 capture_output=True, timeout=20)
        assert (adapter.returncode, adapter.stdout, adapter.stderr) == (0, payload, b''), adapter
        assert raw_request(raw['bind'], '0' * 64, payload) != payload, 'invalid raw bearer succeeded'
        c.cli('mesh', 'forward', 'revoke', '--workspace', wid, '--generation', generation,
              '--source', b.identity['host'], '--service', 'echo',
              '--reason', 'prove raw destination service denial', '--yes')
        assert raw_request(raw['bind'], raw_token, payload) != payload, 'revoked raw service succeeded'
        denied = subprocess.run(b.prefix + ['mesh', 'forward', 'stream', '--key', raw_key,
                                           '--token-file', str(raw_file)], input=payload,
                                capture_output=True, timeout=20)
        assert denied.returncode != 0 and denied.stdout == b'', denied
        c.cli('mesh', 'forward', 'revoke', '--workspace', wid, '--generation', generation,
              '--source', b.identity['host'], '--service', 'web',
              '--reason', 'prove live destination-only service denial', '--yes')
        assert b'forwarded-b-to-c' not in request(started['bind'], token), 'revoked destination service succeeded'
        denied_http = subprocess.run(b.prefix + ['mesh', 'forward', 'http', '--key', key,
                                                 '--token-file', str(token_file),
                                                 '--path', '/exact-service'],
                                     capture_output=True, timeout=20)
        assert denied_http.returncode != 0 and denied_http.stdout == b'', denied_http
        assert all(peer['identity']['host'] != c.identity['host'] for peer in b.cli('mesh', 'peers'))
        retained = []
        for exact in (key, raw_key):
            stopped = b.cli('mesh', 'forward', 'stop', '--key', exact)
            assert stopped['key'] == exact and stopped['state'] in ('terminal', 'needs_attention'), stopped
            if stopped['state'] == 'needs_attention':
                retained.append(exact)
        if retained:
            reconcile_retained_sources(b, binary, retained)
        success = True
    finally:
        original_error = sys.exc_info()[0] is not None
        service.shutdown()
        service.server_close()
        thread.join(5)
        echo.shutdown()
        echo.server_close()
        echo_thread.join(5)
        failures = []
        for node in nodes:
            try:
                if not node.stop():
                    failures.append(f'{node.name} stop/ownership closure unconfirmed')
            except Exception as exc:
                failures.append(f'{node.name} stop failed: {exc}')
        if success and not original_error and not failures:
            shutil.rmtree(root)
        else:
            print('Retained routed forwarding CLI fixture:', root, failures, file=sys.stderr)
        if not original_error and failures:
            raise RuntimeError('forwarding CLI acceptance cleanup unconfirmed')
    print('PASS public CLI B→A→C authenticated HTTP and raw duplex TCP, private-token HTTP and stdio clients, no B–C enrollment, source lifecycle and destination revocation')


if __name__ == '__main__':
    main()
