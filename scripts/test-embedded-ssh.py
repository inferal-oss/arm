#!/usr/bin/env python3
"""Actual system-OpenSSH acceptance against two private ARM TLS backends.

No user SSH config/agent/keys or backend is used. Pairing is fixture-native;
execution uses only the public arm ssh command and system OpenSSH. An encrypted
relay drops acknowledgements without decrypting TLS or mocking the destination.
An optional revocation case deliberately retains its unresolved source evidence.
"""
import argparse
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import pty
import select
import shutil
import signal
import sqlite3
import struct
import subprocess
import tempfile
import termios
import time

spec = importlib.util.spec_from_file_location('arm_mesh_acceptance', Path(__file__).with_name('test-mesh.py'))
mesh = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mesh)


def until(function, predicate, seconds=8):
    end = time.monotonic() + seconds
    value = None
    while time.monotonic() < end:
        value = function()
        if predicate(value):
            return value
        time.sleep(.05)
    raise AssertionError(('condition timed out', value))


def pending(node):
    return node.cli('ssh', 'pending')['pending']


def exact_lookup(node, target, key):
    # Explicit read-only recovery requests. Never resend execution or a seal.
    return node.cli('ssh', 'lookup', target.name, '--key', key)


def settle(node, target, key):
    until(lambda: exact_lookup(node, target, key),
          lambda status: status.get('execution', {}).get('lease', {}).get('state') == 'terminal')
    assert not pending(node)


def execution(node, target, key, command, *options):
    return node.result('ssh', target.name, '--reason', 'public embedded SSH acceptance',
                       '--ttl', '15s', '--key', key, *options, '--', command, timeout=25)


def terminal_execution(node, target):
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 29, 103, 0, 0))
    env = dict(os.environ, TERM='xterm-256color')
    arguments = ['ssh', target.name, '--reason', 'real terminal and resize',
                               '--ttl', '10s', '--key', 'actual-terminal', '-t', '--',
                               'stty size; read answer; attempts=0; while [ "$(stty size)" != "37 119" ] && [ "$attempts" -lt 50 ]; do sleep 0.02; attempts=$((attempts+1)); done; stty size; printf "answer=%s\\n" "$answer"']
    argv = node.terminal_argv(arguments) if hasattr(node, 'terminal_argv') else node.prefix + arguments
    process = subprocess.Popen(argv,
                               stdin=slave, stdout=slave, stderr=slave, env=env,
                               start_new_session=True, close_fds=True)
    output = bytearray()
    def read_until(text):
        end = time.monotonic() + 8
        while text.encode() not in output and time.monotonic() < end:
            ready, _, _ = select.select([master], [], [], .1)
            if ready:
                try:
                    output.extend(os.read(master, 65536))
                except OSError:
                    break
        assert text.encode() in output, (text, bytes(output))
    try:
        read_until('29 103')
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 37, 119, 0, 0))
        process.send_signal(signal.SIGWINCH)
        os.write(master, b'fixture-answer\n')
        read_until('37 119')
        read_until('answer=fixture-answer')
        assert process.wait(timeout=8) == 0, bytes(output)
    finally:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=3)
        os.close(master)
        os.close(slave)
    assert not pending(node)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    parser.add_argument('--test-revocation', action='store_true',
                        help='retain exact pending source evidence after deliberate grant revocation')
    options = parser.parse_args()
    binary = str(Path(options.arm).resolve())
    assert shutil.which('ssh'), 'system OpenSSH is required, no mocked skip'
    root = Path(tempfile.mkdtemp(prefix='arm-embedded-ssh-')).resolve()
    root.chmod(0o700)
    left, right = mesh.Node(binary, root, 'source'), mesh.Node(binary, root, 'destination')
    relay = None
    children = []
    success = False
    retain = False
    try:
        left.start()
        right.start()
        left_port, right_port = mesh.free_port(), mesh.free_port()
        relay = mesh.TLSRelay(('127.0.0.1', right_port))
        for node, port, address in ((left, left_port, f'127.0.0.1:{left_port}'),
                                    (right, right_port, relay.address)):
            node.identity = node.cli('mesh', 'init', '--name', node.name, '--listen', f'127.0.0.1:{port}',
                                     '--address', address, '--yes')
        grant = {'permissions': ['ssh-exec', 'lease-own'], 'limits': {'cpu': 2}, 'max_ttl_ns': 30 * mesh.SECOND}
        mesh.pair(left, right, grant, grant)
        right.fails('ssh', 'setup', '--reason', 'default decline', codes={'confirmation_required'})
        assert not (right.home / 'mesh' / 'ssh-host-key.json').exists()
        assert not (right.home / 'mesh' / 'ssh-service.json').exists()
        for node in (left, right):
            node.cli('ssh', 'setup', '--reason', 'explicit private service fixture', '--dry-run')
            assert not (node.home / 'mesh' / 'ssh-host-key.json').exists()
            node.cli('ssh', 'setup', '--reason', 'explicit private service fixture', '--yes')
        result = execution(left, right, 'initial-decline', 'printf must-not-run')
        assert result.returncode != 0 and json.loads(result.stderr)['error']['code'] == 'confirmation_required'
        assert not pending(left)
        result = execution(left, right, 'actual-exit', 'printf separate-out; printf separate-error >&2; exit 23', '--yes')
        assert (result.returncode, result.stdout, result.stderr) == (23, 'separate-out', 'separate-error'), result
        assert not pending(left)
        result = execution(left, right, 'actual-exit', 'printf must-not-repeat')
        assert result.returncode != 0 and json.loads(result.stderr)['error']['code'] == 'ssh_intent_exists'
        assert result.stdout == ''
        result = execution(right, left, 'reverse-direction', 'printf reverse-stdout', '--yes')
        assert (result.returncode, result.stdout, result.stderr) == (0, 'reverse-stdout', ''), result
        terminal_execution(left, right)
        # Multiple fresh execution channels share the actual destination budget.
        workers = [subprocess.Popen(left.prefix + ['ssh', right.name, '--reason', 'concurrent fresh execution channel',
                    '--ttl', '5s', '--key', 'parallel-' + str(index), '--',
                    'sleep 0.1; printf channel-' + str(index)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                   for index in range(2)]
        children.extend(workers)
        for index, worker in enumerate(workers):
            out, err = worker.communicate(timeout=10)
            assert (worker.returncode, out, err) == (0, 'channel-' + str(index), ''), (out, err)
        assert not pending(left)
        result = execution(left, right, 'fresh-reconnect', 'printf fresh-lease')
        assert (result.returncode, result.stdout, result.stderr) == (0, 'fresh-lease', ''), result

        # A dropped terminal-status reply cannot undo an execution or trigger replay.
        marker = root / 'executed-once'
        relay.drop_next_reply(skip=3)  # access_scope, ssh_identity, WebSocket; drop status.
        result = execution(left, right, 'lost-terminal-reply', f'printf x >> "{marker}"')
        assert result.returncode != 0 and json.loads(result.stderr)['error']['code'] == 'ssh_outcome_unknown', result
        assert relay.dropped.wait(2) and marker.read_text() == 'x'
        assert len(pending(left)) == 1
        blocked = execution(left, right, 'replacement-refused', 'printf forbidden')
        assert blocked.returncode != 0 and json.loads(blocked.stderr)['error']['code'] == 'ssh_intent_exists'
        settle(left, right, 'lost-terminal-reply')
        assert marker.read_text() == 'x'

        # Drop stream setup before any control/admission; absent remains unknown.
        relay.drop_next_reply(skip=2)
        result = execution(left, right, 'never-admitted', 'printf forbidden')
        assert result.returncode != 0 and relay.dropped.wait(2), result
        assert len(pending(left)) == 1
        status = exact_lookup(left, right, 'never-admitted')
        assert status['state'] == 'absent' and len(pending(left)) == 1
        before = (left.home / 'mesh' / next(p.name for p in (left.home / 'mesh').iterdir() if p.name.startswith('ssh-client-'))).read_bytes()
        left.cli('ssh', 'seal', right.name, '--key', 'never-admitted', '--reason', 'prevent delayed admission', '--dry-run')
        assert len(pending(left)) == 1
        status = left.cli('ssh', 'seal', right.name, '--key', 'never-admitted', '--reason', 'prevent delayed admission', '--yes')
        assert status['state'] == 'sealed' and not pending(left)
        assert left.cli('ssh', 'pending')['sealed'] == 1
        assert b'forbidden' not in before

        # Destination expiry kills the leased group; no post-expiry marker.
        marker = root / 'must-not-complete'
        result = execution(left, right, 'target-expiry', f'sleep 3; printf late > "{marker}"', '--ttl', '500ms')
        assert result.returncode != 0 and not marker.exists(), result
        if pending(left):
            status = until(lambda: exact_lookup(left, right, 'target-expiry'),
                           lambda s: s.get('execution', {}).get('lease', {}).get('state') == 'terminal')
            assert status['execution']['lease']['outcome'] == 'expired'
        assert not pending(left)

        # No command/keystrokes/output persisted in either authority ledger.
        for node in (left, right):
            with sqlite3.connect(node.home / 'state.db') as db:
                for table in ('leases', 'events', 'ssh_operations', 'ssh_key_fences'):
                    rows = db.execute(f'SELECT data FROM {table}').fetchall()
                    assert all('printf separate-out' not in row[0] and 'fixture-answer' not in row[0] and 'separate-error' not in row[0] for row in rows)
        if options.test_revocation:
            ready, forbidden = root / 'revoke-ready', root / 'revoke-forbidden'
            process = subprocess.Popen(left.prefix + ['ssh', right.name, '--reason', 'revocation closes leased channel',
                                       '--ttl', '10s', '--key', 'revoked-active', '--',
                                       f'printf ready > "{ready}"; sleep 5; printf forbidden > "{forbidden}"'],
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            children.append(process)
            until(lambda: ready.exists(), bool)
            right.cli('mesh', 'revoke', left.identity['host'], '--reason', 'revoke active SSH', '--yes')
            out, err = process.communicate(timeout=8)
            assert process.returncode != 0 and not forbidden.exists(), (out, err)
            assert len(pending(left)) == 1
            retain = True  # Honest lost authority: no forged settlement/re-pair.
        if not options.test_revocation:
            for node in (right, left):
                assert node.stop(), 'settled fixture failed to stop before strict proof'
                preview = node.cli('database', 'remove', '--reason', 'read-only public SSH producer proof', '--retire-mesh', '--dry-run')
                assert preview['plan']['settled_proof']['settled'], preview
        success = True
        print('Embedded SSH: actual system OpenSSH, exact host pin/default decline, raw stdout/stderr/exit, both local directions, PTY+resize, lost reply/no replay, same-target refusal, absent→explicit transactional seal, expiry and redacted durable evidence passed')
    finally:
        for child in children:
            if child.poll() is None:
                child.terminate()
                try:
                    child.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    child.kill()
                    child.wait(timeout=3)
        if relay:
            relay.close()
        right_stopped = right.stop()
        left_stopped = left.stop()
        stopped = right_stopped and left_stopped
        if success and stopped and not retain:
            shutil.rmtree(root)
        else:
            print('Retained private SSH acceptance evidence:', root, flush=True)
        assert stopped, 'fixture backends did not prove stopped; retained'


if __name__ == '__main__':
    main()
