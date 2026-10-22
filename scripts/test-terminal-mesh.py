#!/usr/bin/env python3
"""Private compiled PTY Mesh/node-context acceptance; optional reviewed direct SSH.

Never modifies global installation/trust, selects unrelated sessions, or retries
an operation implicitly. SSH destination is supplied only at runtime.
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
import shlex
import shutil
import signal
import socket
import struct
import subprocess
import tempfile
import termios
import time
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--arm', required=True)
    parser.add_argument('--ssh')
    parser.add_argument('--remote-binary')
    parser.add_argument('--remote-root')
    args = parser.parse_args()
    arm = str(Path(args.arm).resolve())
    root = Path(tempfile.mkdtemp(prefix='arm-terminal-mesh-'))
    left = [arm, '--home', str(root / 'left')]
    right = [arm, '--home', str(root / 'right')]
    remote_root = None
    process = None
    master = slave = None

    def ssh(command, check=True):
        result = subprocess.run(['ssh', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'UpdateHostKeys=no', '--', args.ssh, shlex.join(command)], capture_output=True, text=True, timeout=30)
        if check:
            assert result.returncode == 0, result.stderr
        return result

    def call(prefix, *command, check=True, packet=None, remote=False):
        values = prefix + list(command)
        if remote:
            values = ['ssh', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'UpdateHostKeys=no', '--', args.ssh, shlex.join(values)]
        result = subprocess.run(values, input=None if packet is None else json.dumps(packet), capture_output=True, text=True, timeout=40)
        if check:
            assert result.returncode == 0, (command, result.stderr)
            return json.loads(result.stdout)
        return result

    def cli(*command, **kw):
        return call(left, *command, **kw)

    def drain(seconds=.15):
        output = bytearray()
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            if select.select([master], [], [], min(.04, max(0, deadline - time.monotonic())))[0]:
                try:
                    raw = os.read(master, 65536)
                except OSError as error:
                    if error.errno == errno.EIO:
                        break
                    raise
                output.extend(raw)
        return re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]', '', output.decode(errors='replace'))

    def send(value):
        os.write(master, value.encode())

    def wait(text, timeout=15):
        output = ''
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            output += drain()
            if text in output:
                return output
            assert process.poll() is None, ('terminal exited', process.returncode, output[-5000:])
        raise AssertionError(('missing terminal text', text, output[-7000:]))

    def connect_form():
        send('n')
        wait('CONNECT NODE')
        send('\r')
        wait('SSH destination')
        send('@fixture\r')
        drain()
        send('\x1b[B\r')
        wait('Reason')
        send('private terminal workflow fixture\r')
        drain()
        send('p')
        wait('PREPARE CONNECTION')

    try:
        cli('start', '--cpu', '2', '--memory', '256MiB', '--disk', '256MiB')
        lease = cli('acquire', '--cpu', '1', '--ttl', '1h', '--reason', 'LOCAL-ONLY-LEASE-FIXTURE')
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 42, 140, 0, 0))
        original = termios.tcgetattr(slave)
        env = dict(os.environ, TERM='xterm-256color', NO_COLOR='1')
        process = subprocess.Popen(left + ['dashboard', 'terminal'], stdin=slave, stdout=slave, stderr=slave, env=env, start_new_session=True)
        wait('connected')
        send('7')
        wait('unconfigured')
        assert not (root / 'left' / 'mesh').exists(), 'registry read initialized Mesh'
        send('\r')
        wait('LOCAL AUTHORITY')
        send('\x1b')
        drain()

        if args.ssh:
            assert not args.ssh.startswith('-')
            for value in (args.remote_binary, args.remote_root):
                assert value and re.fullmatch(r'/[A-Za-z0-9_./-]+', value) and '..' not in Path(value).parts
            assert args.remote_root.startswith('/tmp/')
            remote_root = args.remote_root + '/terminal-' + uuid.uuid4().hex
            ssh(['mkdir', '-m', '700', remote_root])
            ssh(['cp', args.remote_binary, remote_root + '/arm'])
            right = [remote_root + '/arm', '--home', remote_root + '/state']
            cli('mesh', 'connector', 'add', 'fixture', args.ssh, '--binary', right[0], '--remote-home', right[2], '--reason', 'private PTY compatible helper', '--yes')
            connect_form()
            send('n')
            drain()
            assert cli('mesh', 'peers', check=False).returncode != 0, 'declined preparation enabled trust'
            send('p')
            wait('PREPARE CONNECTION')
            send('y')
            wait('Waiting for your review', 45)
            send('c')
            wait('REVIEW MESH_OPERATOR_CANCEL')
            send('y')
            wait('Cancellation ended this attempt')
            assert cli('mesh', 'peers', check=False).returncode != 0, 'cancelled preparation enabled mesh'
            send('\x1b')
            drain()
            connect_form()
            send('y')
            wait('Waiting for your review', 45)
            send('n')
            wait('REVIEW DECLINE')
            send('y')
            wait('This exact trust review was declined')
            assert cli('mesh', 'peers', check=False).returncode != 0, 'declined trust review enabled mesh'
            send('\x1b')
            drain()
            connect_form()
            send('y')
            wait('Waiting for your review', 45)
            send('a')
            wait('REVIEW APPROVE')
            send('\x1b[F')
            drain()
            send('i')
            wait('RECORD')
            send('\x1b')
            drain()
            send('y')
            wait('Completed', 45)
            remote_identity = call(right, 'mesh', 'identity', remote=True)
            send('\x1b')
            drain()
            send('o')
        else:
            call(right, 'start', '--cpu', '3', '--memory', '256MiB', '--disk', '256MiB')
            identities = []
            for prefix, name in ((left, 'terminal-left'), (right, 'terminal-right')):
                with socket.socket() as listener:
                    listener.bind(('127.0.0.1', 0))
                    address = '127.0.0.1:' + str(listener.getsockname()[1])
                identities.append(call(prefix, 'mesh', 'init', '--name', name, '--listen', address, '--address', address, '--yes'))
            pair = uuid.uuid4().hex + uuid.uuid4().hex
            grant = {'permissions': ['observe-host'], 'limits': {}, 'max_ttl_ns': 0, 'contact_ids': [], 'operator_inbox': False}
            for prefix, identity in ((left, identities[1]), (right, identities[0])):
                call(prefix, 'mesh', 'bootstrap', packet={'Action': 'pair', 'Identity': identity, 'Grant': grant, 'Pair': pair, 'Reason': 'private terminal fixture'})
            for prefix, identity in ((left, identities[1]), (right, identities[0])):
                call(prefix, 'mesh', 'call', identity['host'], 'hello', '--params', json.dumps({'pair': pair}))
            for prefix, identity in ((left, identities[1]), (right, identities[0])):
                call(prefix, 'mesh', 'bootstrap', packet={'Action': 'activate', 'Host': identity['host'], 'Pair': pair})
            remote_identity = identities[1]
            send('r')
        wait(remote_identity['host'])
        send('\x1b[B\r')
        wait('WE MAY DO THERE')
        send('v')
        output = wait('REMOTE NODE', 15)
        output += drain(1)
        assert 'LOCAL-ONLY-LEASE-FIXTURE' not in output, 'remote workspace rendered local lease/feed'
        send('4')
        wait('UNAVAILABLE OVER THIS READ-ONLY GATEWAY')
        send('2')
        wait('Available:')
        if args.ssh:
            send('7')
            drain()
            send('d')
            wait('Reason')
            send('private terminal reviewed disconnect\r')
            wait('PREPARE EXACT DISCONNECT')
            send('y')
            wait('Waiting for your review', 45)
            send('a')
            wait('REVIEW APPROVE')
            send('y')
            wait('Completed', 45)
            # The last managed connection disables its listener, so native
            # mesh_peers may correctly be unavailable. Check the owned durable
            # configuration rather than mistaking disabled service for failure.
            source_config = json.loads((root / 'left' / 'mesh' / 'config.json').read_text())
            target_config = json.loads(ssh(['cat', right[2] + '/mesh/config.json']).stdout)
            assert next(peer for peer in source_config['peers'] if peer['identity']['host'] == remote_identity['host'])['state'] == 'revoked'
            assert all(peer['state'] == 'revoked' for peer in target_config['peers'])
            assert source_config['listener_enabled'] is False and target_config['listener_enabled'] is False
            send('\x1b')
            drain()
            send('o')
            wait(remote_identity['host'])
            send('\x1b[B\r')
            wait('trust revoked')
            send('v')
            wait('Disconnected')
            assert 'LOCAL-ONLY-LEASE-FIXTURE' not in drain(.5), 'revoked node open fell back to local'
            send('7')
            drain()
        else:
            cli('mesh', 'revoke', remote_identity['host'], '--reason', 'private selected-node invalidation', '--yes')
            wait('Disconnected', 10)
            send('r')
            assert 'LOCAL-ONLY-LEASE-FIXTURE' not in drain(.5), 'disconnected node fell back to local'
            send('7')
            drain()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 24, 45, 0, 0))
        os.kill(process.pid, signal.SIGWINCH)
        wait('7Mesh')
        send('q')
        process.wait(timeout=8)
        drain()
        assert process.returncode == 0
        after = termios.tcgetattr(slave)
        after[3] &= ~getattr(termios, 'PENDIN', 0)
        original[3] &= ~getattr(termios, 'PENDIN', 0)
        assert after == original, 'terminal state not restored'
        assert cli('inspect', lease['id'])['state'] == 'active', 'terminal exit changed local lease'
        print('Terminal Mesh: meaningful local/no-init, exact node and directional scope, pinned read-only workspace, no local data/actions/streams fallback, revocation, compact7tabs, terminal restoration and surviving lease PASS' + ('; real SSH prepare decline/cancel/trust decline/explicit approve/progress/structured review/reviewed disconnect PASS' if args.ssh else ''))
    finally:
        if process is not None and process.poll() is None:
            process.terminate()
            process.wait(timeout=10)
        if master is not None:
            os.close(master)
            os.close(slave)
        for prefix, remote in ((right, bool(args.ssh)), (left, False)):
            try:
                call(prefix, 'stop', '--force', check=False, remote=remote)
            except Exception:
                pass
        if remote_root:
            ssh(['rm', '-rf', '--', remote_root], check=False)
        shutil.rmtree(root)


if __name__ == '__main__':
    main()
