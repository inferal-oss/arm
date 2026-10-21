#!/usr/bin/env python3
"""Actual two-backend TLS mesh acceptance using private disposable state.

Pairing uses the local account-authorized bootstrap CLI on both fixture nodes;
proof and every dispatch cross the real pinned mutual-TLS listener. No SSH,
agent CLI, third-party Python package, real credentials or user backend needed.
An encrypted TCP relay drops one committed response without terminating TLS,
so exact-key recovery is verified without mocking either ARM backend.
"""
import argparse
import fcntl
import hashlib
import http.client
import json
import os
from pathlib import Path
import shutil
import socket
import ssl
import struct
import subprocess
import tempfile
import threading
import time
import uuid


SECOND = 1_000_000_000


def free_port():
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        return listener.getsockname()[1]


class TLSRelay:
    """Forward TLS records unchanged; optionally drop the next HTTP reply.

    ARM uses fresh connections, TLS1.3 mutual auth, and no session tickets/0RTT.
    The first encrypted client record completes client authentication; the
    second carries HTTP request data. We forward that request, then discard its
    first server response record. Target-native inspection proves the effect
    committed before the explicit same-key retry. No TLS keys/decryption here.
    """
    def __init__(self, upstream):
        self.upstream = upstream
        self.listener = socket.socket()
        self.listener.bind(('127.0.0.1', 0))
        self.listener.listen(16)
        self.listener.settimeout(.2)
        self.address = '127.0.0.1:' + str(self.listener.getsockname()[1])
        self.lock = threading.Lock()
        self.closed = threading.Event()
        self.dropped = threading.Event()
        self.armed = False
        self.skip = 0
        self.connections = set()
        self.workers = []
        self.thread = threading.Thread(target=self.accept, daemon=True)
        self.thread.start()

    def drop_next_reply(self, skip=0):
        with self.lock:
            assert not self.armed
            self.armed = True
            self.skip = skip
            self.dropped.clear()

    @staticmethod
    def exact(connection, count):
        value = bytearray()
        while len(value) < count:
            block = connection.recv(count - len(value))
            if not block:
                return None
            value.extend(block)
        return bytes(value)

    def accept(self):
        while not self.closed.is_set():
            try:
                downstream, _ = self.listener.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            with self.lock:
                drop = self.armed and self.skip == 0
                if self.armed and self.skip:
                    self.skip -= 1
                elif drop:
                    self.armed = False
                self.connections.add(downstream)
            worker = threading.Thread(target=self.forward, args=(downstream, drop), daemon=True)
            self.workers.append(worker)
            worker.start()

    def forward(self, downstream, drop):
        upstream = None
        request_sent = threading.Event()
        finished = threading.Event()

        def close_both():
            finished.set()
            for connection in (downstream, upstream):
                if connection is not None:
                    try:
                        connection.shutdown(socket.SHUT_RDWR)
                    except OSError:
                        pass

        def pump(source, target, client):
            application_records = 0
            try:
                while not self.closed.is_set() and not finished.is_set():
                    header = self.exact(source, 5)
                    if header is None:
                        break
                    length = struct.unpack('!H', header[3:])[0]
                    if length > 18432:
                        raise AssertionError('unexpected TLS record bound')
                    body = self.exact(source, length)
                    if body is None:
                        break
                    if client and header[0] == 23:
                        application_records += 1
                        if application_records >= 2:
                            request_sent.set()
                    if not client and drop and request_sent.is_set() and header[0] == 23:
                        self.dropped.set()
                        break
                    target.sendall(header + body)
            except OSError:
                pass
            finally:
                close_both()

        try:
            upstream = socket.create_connection(self.upstream, timeout=5)
            upstream.settimeout(None)
            with self.lock:
                self.connections.add(upstream)
            sender = threading.Thread(target=pump, args=(downstream, upstream, True), daemon=True)
            sender.start()
            pump(upstream, downstream, False)
            sender.join(timeout=2)
        finally:
            close_both()
            with self.lock:
                for connection in (downstream, upstream):
                    self.connections.discard(connection)
                    if connection is not None:
                        connection.close()

    def close(self):
        self.closed.set()
        self.listener.close()
        with self.lock:
            connections = list(self.connections)
        for connection in connections:
            try:
                connection.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
        self.thread.join(timeout=2)
        for worker in self.workers:
            worker.join(timeout=2)


class Node:
    def __init__(self, binary, root, name):
        self.home = root / name
        self.prefix = [binary, '--home', str(self.home)]
        self.name = name
        self.identity = None
        self.stopped = False

    def result(self, *args, stdin=None, timeout=20):
        return subprocess.run(self.prefix + list(args), input=stdin, text=True,
                              capture_output=True, timeout=timeout)

    def cli(self, *args, stdin=None, timeout=20):
        result = self.result(*args, stdin=stdin, timeout=timeout)
        assert result.returncode == 0, (self.name, args, result.returncode, result.stderr, result.stdout)
        return json.loads(result.stdout)

    def fails(self, *args, codes, stdin=None):
        result = self.result(*args, stdin=stdin)
        assert result.returncode != 0, (self.name, 'unexpected success', args, result.stdout)
        error = json.loads(result.stderr)['error']
        assert error['code'] in codes, (self.name, args, error)
        return error

    def call(self, host, operation, params=None, key=None, codes=None):
        args = ['mesh', 'call', host, operation, '--params', json.dumps(params or {}, separators=(',', ':'))]
        if key is not None:
            args += ['--key', key]
        return self.fails(*args, codes=codes) if codes else self.cli(*args)

    def bootstrap(self, **packet):
        return self.cli('mesh', 'bootstrap', stdin=json.dumps(packet))

    def start(self):
        self.cli('start', '--cpu', '4', '--memory', '128MiB', '--disk', '1GiB')

    def stop(self):
        if self.stopped:
            return True
        result = self.result('stop', '--force', timeout=20)
        if result.returncode != 0:
            return False
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            try:
                with (self.home / 'daemon.lock').open('r+') as lock:
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    if not (self.home / 'endpoint.json').exists():
                        self.stopped = True
                        return True
            except (FileNotFoundError, BlockingIOError):
                pass
            time.sleep(.05)
        return False


def pair(left, right, left_grant, right_grant):
    # left_grant is permission left receives ON right; no transitive trust.
    operation = uuid.uuid4().hex + uuid.uuid4().hex
    for target, other, grant in ((right, left, left_grant), (left, right, right_grant)):
        pending = target.bootstrap(Action='pair', Identity=other.identity, Grant=grant,
                                   Pair=operation, Reason='two-local-node TLS acceptance')
        assert pending['state'] == 'pending'
    for source, target in ((left, right), (right, left)):
        proved = source.call(target.identity['host'], 'hello', {'pair': operation})
        assert proved['host'] == target.identity['host']
    for target, other in ((left, right), (right, left)):
        target.bootstrap(Action='activate', Host=other.identity['host'], Pair=operation)
        peers = target.cli('mesh', 'peers')
        assert len(peers) == 1 and peers[0]['state'] == 'active' and peers[0]['proven']


def tls_request(source, target, root, operation='status', host=None, params=None,
                client_certificate=True, alpn='http/1.1', tls12=False):
    """Actual TLS probe with fixture keys, no local bearer token forwarding."""
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    context.minimum_version = ssl.TLSVersion.TLSv1_2 if tls12 else ssl.TLSVersion.TLSv1_3
    context.maximum_version = ssl.TLSVersion.TLSv1_2 if tls12 else ssl.TLSVersion.TLSv1_3
    context.load_verify_locations(cadata=target.identity['certificate'])
    context.set_alpn_protocols([alpn])
    if client_certificate:
        certificate = root / (source.name + '-public.pem')
        certificate.write_text(source.identity['certificate'])
        certificate.chmod(0o600)
        context.load_cert_chain(str(certificate), str(source.home / 'mesh' / 'node-key.pem'))
    body = json.dumps({'version': 1, 'host': host or target.identity['host'],
                       'operation': operation, 'params': params or {}}).encode()
    listen = json.loads((target.home / 'mesh' / 'config.json').read_text())['listen']
    address, port = listen.rsplit(':', 1)
    with socket.create_connection((address, int(port)), timeout=5) as raw:
        with context.wrap_socket(raw, server_hostname='node-' + target.identity['host'] + '.arm.invalid') as connection:
            version = connection.version()
            protocol = connection.selected_alpn_protocol()
            fingerprint = hashlib.sha256(connection.getpeercert(binary_form=True)).hexdigest()
            request = (f'POST /v1/mesh HTTP/1.1\r\nHost: {listen}\r\n'
                       f'Content-Type: application/json\r\nContent-Length: {len(body)}\r\nConnection: close\r\n\r\n').encode() + body
            connection.sendall(request)
            response = http.client.HTTPResponse(connection)
            response.begin()
            assert response.status == 200
            payload = json.loads(response.read())
            assert version == 'TLSv1.3'
            assert protocol == 'http/1.1'
            assert fingerprint == target.identity['fingerprint']
            return payload


def eventually(fn, predicate, seconds=7):
    end = time.monotonic() + seconds
    while time.monotonic() < end:
        value = fn()
        if predicate(value):
            return value
        time.sleep(.05)
    raise AssertionError(('condition did not settle', value))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    parser.add_argument('--require-tls-probes', action='store_true', help='fail if Python cannot run TLS1.3 wire probes; core actual-Go-mTLS checks always run')
    options = parser.parse_args()
    binary = str(Path(options.arm).resolve())
    if options.require_tls_probes and not ssl.HAS_TLSv1_3:
        parser.error('TLS wire probes require a Python linked against TLS1.3-capable OpenSSL')
    root = Path(tempfile.mkdtemp(prefix='arm-mesh-'))
    left, right = Node(binary, root, 'alpha'), Node(binary, root, 'beta')
    relay = None
    try:
        left.start()
        right.start()
        left_port, right_port = free_port(), free_port()
        relay = TLSRelay(('127.0.0.1', right_port))
        relay_port = int(relay.address.rsplit(':', 1)[1])
        while left_port == relay_port:
            left_port = free_port()
        while right_port in (left_port, relay_port):
            right_port = free_port()
        relay.upstream = ('127.0.0.1', right_port)
        for node, port, address in ((left, left_port, '127.0.0.1:' + str(left_port)),
                                    (right, right_port, relay.address)):
            init_args = ['mesh', 'init', '--name', node.name, '--listen', '127.0.0.1:' + str(port), '--address', address]
            node.cli(*init_args, '--dry-run')
            assert not (node.home / 'mesh').exists(), 'dry-run created mesh identity'
            node.fails(*init_args, codes={'confirmation_required'})
            assert not (node.home / 'mesh').exists(), 'unconfirmed init created identity'
            node.identity = node.cli(*init_args, '--yes')
            assert node.identity == node.cli('mesh', 'identity')
        assert left.identity['host'] != right.identity['host']
        contact = right.cli('agent', 'register', '--id', 'allowed-poll', '--adapter', 'generic', '--delivery', 'poll', '--session', str(uuid.uuid4()), '--cwd', str(root))
        private_contact = right.cli('agent', 'register', '--id', 'private-poll', '--adapter', 'generic', '--delivery', 'poll', '--session', str(uuid.uuid4()), '--cwd', str(root))
        native = right.cli('acquire', '--cpu', '1', '--ttl', '5m', '--reason', 'private native owner reason')
        grant = {'permissions': ['observe-host', 'observe-own', 'lease-own', 'message', 'contact-read'],
                 'limits': {'cpu': 1, 'memory_bytes': 0, 'disk_bytes': 0}, 'max_ttl_ns': 20 * SECOND,
                 'contact_ids': ['allowed-poll'], 'operator_inbox': True}
        reverse = {'permissions': ['observe-host'], 'limits': {}, 'max_ttl_ns': 0, 'contact_ids': [], 'operator_inbox': False}
        pair(left, right, grant, reverse)
        for target in (right.identity['host'], right.name):
            visible = left.cli('status', '--host', target)
            assert visible['host'] == right.identity['host'] and visible['leases'] == []
            assert native['id'] not in json.dumps(visible) and 'private native owner reason' not in json.dumps(visible)
            assert all('users' not in resource for resource in visible['resources'])
        if ssl.HAS_TLSv1_3:
            health = tls_request(left, right, root)
            assert health['result']['host'] == right.identity['host']
            assert tls_request(left, right, root, host=left.identity['host'])['error']['code'] == 'wrong_host'
            assert tls_request(left, right, root, operation='shutdown')['error']['code'] == 'forbidden'
            for probe_options in ({'client_certificate': False}, {'alpn': 'arm-mesh/1'}, {'tls12': True}):
                try:
                    result = tls_request(left, right, root, **probe_options)
                except (ssl.SSLError, OSError, http.client.HTTPException):
                    pass
                else:
                    raise AssertionError(('unauthenticated/downgraded TLS request succeeded', probe_options, result))
            print('Mesh wire probes: TLS1.3, exact server certificate pin, wrong stable host, closed admin RPC, missing client certificate, wrong ALPN and TLS1.2 rejection passed')
        else:
            print('Mesh wire probes SKIPPED: this Python lacks TLS1.3; wire version/pin/negative probe subset remains unverified here. Actual two-Go-backend mutual-TLS core checks remain mandatory.')
        # Native local-owner work remains outside peer ownership, even with its ID.
        for operation in ('inspect', 'release', 'cancel', 'renew'):
            left.call(right.name, operation, {'id': native['id'], 'ttl_ns': 10 * SECOND},
                      key='foreign-' + operation if operation != 'inspect' else None, codes={'forbidden'})
        assert right.cli('inspect', native['id'])['revision'] == native['revision']
        right.call(left.name, 'acquire', {'reason': 'reverse forbidden', 'resources': {'cpu': .1}, 'ttl_ns': SECOND}, key='reverse-denied', codes={'forbidden'})
        left.call(right.name, 'run', {'request': {'reason': 'execution not granted', 'resources': {'cpu': .1}, 'ttl_ns': SECOND}, 'command': ['/bin/sh', '-c', 'exit 0'], 'cwd': str(root)}, key='run-denied', codes={'forbidden'})
        left.call(right.name, 'shutdown', codes={'forbidden'})
        right.fails('mesh', 'contacts', left.name, codes={'forbidden'})
        right.fails('mesh', 'send', left.name, '--body', 'reverse message forbidden', '--key', 'reverse-message-denied', codes={'forbidden'})
        # Name-based standard CLI dispatch, stable host attribution and ceilings.
        own = left.cli('acquire', '--host', right.name, '--cpu', '.5', '--ttl', '15s', '--reason', 'peer owns this claim', '--key', 'own-claim')
        assert own['host'] == right.identity['host'] and own['request']['origin_peer'] == left.identity['host']
        assert own['request']['origin_key'] == 'own-claim'
        assert left.cli('inspect', own['id'], '--host', right.identity['host'])['id'] == own['id']
        left.call(right.name, 'acquire', {'reason': 'too much', 'resources': {'cpu': 1.1}, 'ttl_ns': SECOND}, key='ceiling-denied', codes={'forbidden'})
        left.call(right.name, 'acquire', {'reason': 'too long', 'resources': {'cpu': .1}, 'ttl_ns': 21 * SECOND}, key='ttl-denied', codes={'forbidden'})
        # Drop only a real TLS response after target commit, then retry exact key.
        lost_params = {'reason': 'lost encrypted response', 'resources': {'cpu': .5}, 'ttl_ns': 15 * SECOND}
        relay.drop_next_reply()
        left.call(right.name, 'acquire', lost_params, key='lost-acquire', codes={'remote_outcome_unknown'})
        assert relay.dropped.wait(1), 'relay did not drop the application reply'
        committed = [lease for lease in right.cli('status')['leases'] if lease['request'].get('origin_key') == 'lost-acquire']
        assert len(committed) == 1 and committed[0]['state'] == 'active'
        recovered = left.call(right.identity['host'], 'acquire', lost_params, key='lost-acquire')
        assert recovered['id'] == committed[0]['id']
        left.call(right.name, 'acquire', dict(lost_params, reason='changed exact key'), key='lost-acquire', codes={'idempotency_conflict'})
        left.call(right.name, 'acquire', {'reason': 'aggregate overflow', 'resources': {'cpu': .1}, 'ttl_ns': SECOND}, key='aggregate-denied', codes={'forbidden'})
        assert {lease['id'] for lease in left.cli('status', '--host', right.name)['leases']} == {own['id'], recovered['id']}
        # Mutation replay returns its original result without another journal edge.
        release = left.call(right.name, 'release', {'id': recovered['id']}, key='release-once')
        cursor = right.cli('status')['cursor']
        repeated = left.call(right.identity['host'], 'release', {'id': recovered['id']}, key='release-once')
        assert release == repeated and right.cli('status')['cursor'] == cursor
        left.call(right.name, 'cancel', {'id': recovered['id']}, key='release-once', codes={'idempotency_conflict'})
        left.cli('release', own['id'], '--host', right.name)
        # Contacts and messages use explicit host routing plus exact registration.
        contacts = left.cli('mesh', 'contacts', right.name)
        assert [entry['id'] for entry in contacts] == ['allowed-poll']
        operator = left.cli('mesh', 'send', right.identity['host'], '--body', 'host-addressed operator message', '--key', 'operator-once')
        assert operator['sender'] == 'peer:' + left.identity['host'] and operator['audience'] == 'operator'
        assert left.cli('mesh', 'send', right.name, '--body', 'host-addressed operator message', '--key', 'operator-once')['id'] == operator['id']
        left.fails('mesh', 'send', right.name, '--body', 'changed retry', '--key', 'operator-once', codes={'idempotency_conflict'})
        poll = left.cli('mesh', 'send', right.name, '--to', contact['id'], '--registration', contact['registration'], '--body', 'exact allowed polling contact', '--key', 'poll-once')
        assert poll['deliveries'][contact['id']]['state'] == 'available'
        assert poll['deliveries'][contact['id']]['registration'] == contact['registration']
        assert left.cli('mesh', 'send', right.identity['host'], '--to', contact['id'], '--registration', contact['registration'], '--body', 'exact allowed polling contact', '--key', 'poll-once')['id'] == poll['id']
        assert len(right.cli('messages')) == 2
        left.fails('mesh', 'send', right.name, '--to', 'private-poll', '--registration', 'forged', '--body', 'not authorized', '--key', 'private-denied', codes={'contact_changed', 'forbidden'})
        left.call(right.name, 'message', {'audience': private_contact['id'], 'body': 'raw ungranted contact', 'targets': [private_contact]}, key='raw-private-denied', codes={'forbidden'})
        left.call(right.name, 'message', {'audience': 'broadcast', 'body': 'no peer broadcast', 'targets': []}, key='broadcast-denied', codes={'forbidden'})
        # Lose a polling-message acknowledgment, then replace that contact. The
        # original key must resolve its original delivery, never retarget it.
        # mesh send first reads the exact contact; drop the subsequent mutation
        # response, so a read failure cannot masquerade as an uncertain send.
        relay.drop_next_reply(skip=1)
        left.fails('mesh', 'send', right.name, '--to', contact['id'], '--registration', contact['registration'], '--body', 'lost polling acknowledgment', '--key', 'lost-poll', codes={'remote_outcome_unknown'})
        assert relay.dropped.wait(1)
        originals = [message for message in right.cli('messages') if message['body'] == 'lost polling acknowledgment']
        assert len(originals) == 1 and originals[0]['deliveries'][contact['id']]['registration'] == contact['registration']
        replacement = right.cli('agent', 'register', '--id', contact['id'], '--adapter', 'generic', '--delivery', 'poll', '--session', str(uuid.uuid4()), '--cwd', str(root))
        looked_up = left.cli('mesh', 'message-lookup', right.identity['host'], '--key', 'lost-poll')
        assert looked_up['found'] and looked_up['message']['id'] == originals[0]['id']
        assert looked_up['message']['deliveries'][contact['id']]['registration'] == contact['registration']
        left.fails('mesh', 'send', right.name, '--to', replacement['id'], '--registration', replacement['registration'], '--body', 'lost polling acknowledgment', '--key', 'lost-poll', codes={'idempotency_conflict'})
        left.fails('mesh', 'send', right.name, '--to', contact['id'], '--registration', contact['registration'], '--body', 'stale contact', '--key', 'stale-contact', codes={'contact_changed'})
        left.call(right.name, 'message', {'audience': contact['id'], 'body': 'raw stale contact', 'targets': [contact]}, key='raw-stale-contact', codes={'contact_changed'})
        assert len(right.cli('messages')) == 3
        # Revocation blocks new work; an existing target-owned lease still expires
        # with the origin daemon stopped, without reclaiming native-owner work.
        expires = left.call(right.name, 'acquire', {'reason': 'expires without origin', 'resources': {'cpu': .5}, 'ttl_ns': 2 * SECOND}, key='independent-expiry')
        right.cli('mesh', 'revoke', left.name, '--reason', 'acceptance revocation', '--yes')
        left.call(right.name, 'status', codes={'host_unavailable', 'forbidden'})
        assert left.stop(), 'origin backend did not stop'
        ended = eventually(lambda: right.cli('inspect', expires['id']), lambda lease: lease['state'] == 'terminal')
        assert ended['outcome'] == 'expired'
        assert right.cli('inspect', native['id'])['state'] == 'active'
        assert right.cli('status')['reserved']['cpu'] == 1
        assert len(right.cli('messages')) == 3
        print('Mesh: actual two-node TLS1.3 mTLS/pins, explicit pairing, host ID/name CLI dispatch, directional/owner/CPU+TTL scopes, dropped encrypted reply exact-key recovery, operator+exact polling messages, revocation and target-authoritative expiry passed')
    finally:
        if relay is not None:
            relay.close()
        stopped = [node.stop() for node in (left, right)]
        if all(stopped):
            shutil.rmtree(root)
        else:
            print('Retained unresolved mesh fixture:', root)


if __name__ == '__main__':
    main()
