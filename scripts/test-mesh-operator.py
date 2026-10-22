#!/usr/bin/env python3
"""Actual browser-authorized WS -> coordinator -> verified SSH/mTLS acceptance.

Requires an explicitly supplied trusted direct SSH fixture and compatible ARM
binary. All homes/helpers are private disposable copies; no global installation,
user dashboard, SSH trust file or credential configuration is modified.
This is protocol acceptance, not rendered browser layout acceptance.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import socket
import struct
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request
import uuid


class Wire:
    def __init__(self, endpoint, origin, token):
        address = urllib.parse.urlsplit(endpoint)
        self.socket = socket.create_connection((address.hostname, address.port), timeout=10)
        key = base64.b64encode(os.urandom(16)).decode()
        request = (f'GET {address.path} HTTP/1.1\r\nHost: {address.netloc}\r\n'
                   f'Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: {key}\r\n'
                   f'Sec-WebSocket-Version: 13\r\nOrigin: {origin}\r\n'
                   f'Sec-WebSocket-Protocol: arm.v1, arm.auth.{token}\r\n\r\n')
        self.socket.sendall(request.encode())
        head = bytearray()
        while not head.endswith(b'\r\n\r\n'):
            head.extend(self.exact(1))
            assert len(head) <= 16384
        assert head.startswith(b'HTTP/1.1 101 '), head.decode()
        expected = base64.b64encode(hashlib.sha1((key + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest())
        assert b'sec-websocket-accept: ' + expected.lower() in bytes(head).lower()

    def exact(self, count):
        out = bytearray()
        while len(out) < count:
            value = self.socket.recv(count - len(out))
            assert value, 'unexpected websocket close'
            out.extend(value)
        return bytes(out)

    def send(self, body, opcode=1):
        mask = os.urandom(4)
        length = len(body)
        prefix = bytes([0x80 | opcode])
        if length < 126:
            prefix += bytes([0x80 | length])
        elif length < 65536:
            prefix += bytes([0x80 | 126]) + struct.pack('!H', length)
        else:
            prefix += bytes([0x80 | 127]) + struct.pack('!Q', length)
        self.socket.sendall(prefix + mask + bytes(v ^ mask[i % 4] for i, v in enumerate(body)))

    def response(self):
        out = bytearray()
        while True:
            first, second = self.exact(2)
            assert not second & 128
            count = second & 127
            if count == 126:
                count = struct.unpack('!H', self.exact(2))[0]
            elif count == 127:
                count = struct.unpack('!Q', self.exact(8))[0]
            assert count <= 4 << 20
            body = self.exact(count)
            opcode = first & 15
            if opcode == 9:
                self.send(body, 10)
                continue
            assert opcode in (0, 1, 2), 'unexpected websocket control frame'
            out.extend(body)
            assert len(out) <= 4 << 20
            if first & 128:
                return json.loads(out)

    def close(self):
        self.socket.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--arm', required=True)
    parser.add_argument('--ssh', required=True)
    parser.add_argument('--remote-binary', required=True)
    parser.add_argument('--remote-root', required=True)
    args = parser.parse_args()
    assert not args.ssh.startswith('-')
    assert args.remote_root.startswith('/tmp/') and '..' not in Path(args.remote_root).parts
    assert re.fullmatch(r'/[A-Za-z0-9_./-]+', args.remote_root)
    assert re.fullmatch(r'/[A-Za-z0-9_./-]+', args.remote_binary)
    arm = str(Path(args.arm).resolve())
    root = Path(tempfile.mkdtemp(prefix='arm-mesh-operator-'))
    home = root / 'state'
    remote_root = args.remote_root + '/operator-' + uuid.uuid4().hex
    remote_home, remote_arm = remote_root + '/state', remote_root + '/arm'
    success = False

    def ssh(command, check=True, stdin=None):
        result = subprocess.run(['ssh', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'UpdateHostKeys=no', '--', args.ssh, command], input=stdin, text=True, capture_output=True, timeout=30)
        if check:
            assert result.returncode == 0, result.stderr
        return result

    def cli(*command, check=True):
        result = subprocess.run([arm, '--home', str(home), *command], capture_output=True, text=True, timeout=30)
        if check:
            assert result.returncode == 0, (command, result.stderr)
            return json.loads(result.stdout)
        return result

    def remote(*command, check=True, stdin=None):
        result = ssh(shlex.join([remote_arm, '--home', remote_home, *command]), check, stdin)
        return json.loads(result.stdout) if check else result

    endpoint = origin = token = None
    watch = None
    gamma_home = root / "other-state"

    def gamma(*command, check=True):
        result = subprocess.run([arm, "--home", str(gamma_home), *command], capture_output=True, text=True, timeout=30)
        if check:
            assert result.returncode == 0, (command, result.stderr)
            return json.loads(result.stdout)
        return result

    def rpc(operation, params=None, error=None):
        connection = Wire(endpoint, origin, token)
        try:
            key = uuid.uuid4().hex
            connection.send(json.dumps({'version': 1, 'id': key, 'operation': operation, 'params': params or {}}).encode())
            response = connection.response()
            assert response['id'] == key
            if error is not None:
                assert response.get('error', {}).get('code') in error, response
                return response['error']
            assert 'error' not in response, response
            return response['result']
        finally:
            connection.close()

    def phase(key, desired):
        until = time.monotonic() + 35
        while time.monotonic() < until:
            value = rpc('mesh_operator_get', {'key': key})
            if value['phase'] == desired:
                return value
            assert value['phase'] not in ('incomplete', 'rejected', 'completed') or value['phase'] == desired, value
            time.sleep(.05)
        raise AssertionError('workflow did not reach ' + desired)

    def decide(value, approve):
        return rpc('mesh_operator_decide', {'key': value['key'], 'review_revision': value['review_revision'], 'review_sha256': value['review_sha256'], 'approve': approve})

    def prepare(key):
        return rpc('mesh_operator_prepare', {'key': key, 'kind': 'connect', 'connect': {'connector': 'fixture', 'reason': 'isolated operator workflow acceptance', 'source_permissions_on_target': {'permissions': ['observe-host'], 'max_ttl_ns': 3600000000000}, 'target_permissions_on_source': {'permissions': [], 'max_ttl_ns': 3600000000000}}})

    ssh('umask 077; mkdir ' + shlex.quote(remote_root) + ' && cp ' + shlex.quote(args.remote_binary) + ' ' + shlex.quote(remote_arm) + ' && chmod 700 ' + shlex.quote(remote_arm))
    try:
        cli('start', '--cpu', '2', '--memory', '128MiB', '--disk', '128MiB')
        cli('mesh', 'connector', 'add', 'fixture', args.ssh, '--binary', remote_arm, '--remote-home', remote_home, '--reason', 'test-owned compatible helper and home', '--yes')
        link = urllib.parse.urlsplit(cli('dashboard')['url'])
        origin = urllib.parse.urlunsplit((link.scheme, link.netloc, '', '', ''))
        ticket = urllib.parse.parse_qs(link.fragment)['connect'][0]
        request = urllib.request.Request(origin + '/v1/browser/session', data=json.dumps({'ticket': ticket}).encode(), headers={'Content-Type': 'application/json', 'Origin': origin})
        with urllib.request.urlopen(request, timeout=5) as response:
            token = json.load(response)['token']
        endpoint = json.loads((home / 'endpoint.json').read_text())['url']
        watch = Wire(endpoint, origin, token)
        watch_id = uuid.uuid4().hex
        watch.send(json.dumps({'version': 1, 'id': watch_id, 'operation': 'mesh_operator_watch', 'params': {}}).encode())
        watch_ack = watch.response()
        assert watch_ack['id'] == watch_id
        watch_generation = watch_ack['result']['subscription']
        assert watch_generation.startswith('mesh_operator:')
        rpc('mesh_operator_prepare', {'key': 'bad', 'kind': 'connect', 'connect': {'ssh_destination': 'fixture', 'remote_binary': '/bad/command'}}, error={'invalid_request'})
        prepare('decline')
        review = phase('decline', 'awaiting_review')
        while True:
            notification = watch.response()
            assert notification.get('subscription') == watch_generation
            change = notification['result']
            assert set(change) == {'key', 'revision'}, 'workflow stream leaked snapshots/secrets'
            if change['key'] == 'decline':
                break
        assert 'token' not in json.dumps(review).lower()
        assert not json.loads((home / 'mesh' / 'config.json').read_text())['peers']
        address = review['review']['local_prepared']['listen']
        host, port = address.rsplit(':', 1)
        with socket.socket() as probe:
            probe.settimeout(.3)
            assert probe.connect_ex((host, int(port))) != 0, 'prepared socket was listening'
        decide(review, False)
        phase('decline', 'rejected')
        rpc('mesh_operator_decide', {'key': review['key'], 'review_revision': review['review_revision'], 'review_sha256': review['review_sha256'], 'approve': True}, error={'review_expired'})
        assert not json.loads((home / 'mesh' / 'config.json').read_text())['peers']

        prepare('cancel')
        review = phase('cancel', 'awaiting_review')
        rpc('mesh_operator_cancel', {'key': 'cancel', 'revision': review['revision']})
        phase('cancel', 'rejected')
        assert not json.loads((home / 'mesh' / 'config.json').read_text())['peers']

        prepare('connect')
        review = phase('connect', 'awaiting_review')
        rpc('mesh_operator_decide', {'key': 'connect', 'review_revision': review['review_revision'], 'review_sha256': 'changed', 'approve': True}, error={'review_changed'})
        decide(review, True)
        completed = phase('connect', 'completed')
        target = completed['result']['connected']
        cli('status', '--host', target)
        assert prepare('connect')['phase'] == 'completed', 'exact API start replayed a completed workflow'

        # A different source prepares against a STOPPED configured target.
        # Restart restores only its previously approved listener/old source;
        # declining the new source leaves that existing trust and port intact.
        alpha_context = endpoint, origin, token
        gamma('start', '--cpu', '1', '--memory', '128MiB', '--disk', '128MiB')
        gamma('mesh', 'connector', 'add', 'fixture', args.ssh, '--binary', remote_arm, '--remote-home', remote_home, '--reason', 'test-owned existing target', '--yes')
        remote('stop')
        gamma_link = urllib.parse.urlsplit(gamma('dashboard')['url'])
        origin = urllib.parse.urlunsplit((gamma_link.scheme, gamma_link.netloc, '', '', ''))
        gamma_ticket = urllib.parse.parse_qs(gamma_link.fragment)['connect'][0]
        gamma_request = urllib.request.Request(origin + '/v1/browser/session', data=json.dumps({'ticket': gamma_ticket}).encode(), headers={'Content-Type': 'application/json', 'Origin': origin})
        with urllib.request.urlopen(gamma_request, timeout=5) as response:
            token = json.load(response)['token']
        endpoint = json.loads((gamma_home / 'endpoint.json').read_text())['url']
        prepare('existing-listener-decline')
        existing_review = phase('existing-listener-decline', 'awaiting_review')
        assert existing_review['review']['remote_prepared']['existing_listener'] is True
        assert 'previously approved' in existing_review['notice']
        assert len(remote('mesh', 'peers')) == 1, 'new peer trusted before confirmation'
        cli('status', '--host', target)  # old approved source still works
        decide(existing_review, False)
        phase('existing-listener-decline', 'rejected')
        assert len(remote('mesh', 'peers')) == 1
        cli('status', '--host', target)  # decline preserves old listener/trust
        gamma('stop')
        endpoint, origin, token = alpha_context

        # Local-first revocation commits even when the exact private helper is
        # unavailable. Resuming uses immutable draft + separate child evidence.
        ssh('mv ' + shlex.quote(remote_arm) + ' ' + shlex.quote(remote_arm + '.offline'))
        rpc('mesh_operator_prepare', {'key': 'disconnect', 'kind': 'disconnect', 'disconnect': {'host': target, 'reason': 'end isolated operator connection'}})
        review = phase('disconnect', 'awaiting_review')
        decide(review, True)
        pending = phase('disconnect', 'incomplete')
        assert pending['result']['remote_pending'] is True
        assert cli('status', '--host', target, check=False).returncode != 0
        ssh('mv ' + shlex.quote(remote_arm + '.offline') + ' ' + shlex.quote(remote_arm))
        child = rpc('mesh_operator_resume', {'key': 'disconnect', 'revision': pending['revision']})
        assert child['parent_key'] == 'disconnect'
        review = phase(child['key'], 'awaiting_review')
        decide(review, True)
        done = phase(child['key'], 'completed')
        assert done['result']['remote_pending'] is False
        # Child completion is durable before linking the untouched parent;
        # the parent's target protection stays conservative until that link.
        until = time.monotonic() + 5
        while True:
            original = rpc('mesh_operator_get', {'key': 'disconnect'})
            if original.get('resolved_by') == child['key']:
                break
            assert time.monotonic() < until, original
            time.sleep(.02)
        assert original['result'] == pending['result'] and original['review'] == pending['review']
        # Last automatic peer revocation deliberately disables its listener;
        # inspect the configured receipt through the read-only helper.
        final_remote = remote('mesh', 'bootstrap', stdin=json.dumps({'Action': 'lifecycle_inspect', 'Home': remote_home}))
        assert final_remote['config']['peers'][0]['state'] == 'revoked'
        success = True
        print('PASS browser-authorized WS mesh coordinator: native-reviewed private connector, concrete non-listening preparation, default decline/cancel, immutable confirmation, actual pinned connect, no replay, stopped-target restoration preserving existing peers on decline, local-first offline disconnect and separately reviewed child recovery; no rendered-browser acceptance claimed')
    finally:
        if watch is not None:
            watch.close()
        if ssh('test -e ' + shlex.quote(remote_arm + '.offline'), False).returncode == 0:
            ssh('mv ' + shlex.quote(remote_arm + '.offline') + ' ' + shlex.quote(remote_arm), False)
        gamma('stop', '--force', check=False)
        cli('stop', '--force', check=False)
        remote('stop', '--force', check=False)
        if success:
            shutil.rmtree(root)
            ssh('rm -rf ' + shlex.quote(remote_root))
        else:
            print('Private local failure evidence retained:', root)


if __name__ == '__main__':
    main()
