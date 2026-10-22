#!/usr/bin/env python3
"""Compiled reset/confirmation acceptance, exclusively disposable ARM homes.

Does not open the user's database, browser or agent sessions. Go subprocess
fault tests additionally cover exact journal-boundary process exit/recovery.
"""
import argparse
import errno
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import re
import select
import shutil
import signal
import socket
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    binary = str(Path(parser.parse_args().arm).resolve())
    root = Path(tempfile.mkdtemp(prefix='arm-database-reset-test-')).resolve()
    homes = []

    def cli(home, *args, check=True, stdin=None):
        result = subprocess.run([binary, '--home', str(home)] + list(args),
                                capture_output=True, text=True, timeout=40, input=stdin)
        if not check:
            return result
        assert result.returncode == 0, (args, result.stdout, result.stderr)
        return json.loads(result.stdout)

    def snapshot(home):
        values = {}
        for path in sorted(home.rglob('*')):
            info = path.lstat()
            values[str(path.relative_to(home))] = (
                info.st_mode, hashlib.sha256(path.read_bytes()).hexdigest()
                if path.is_file() else None)
        return values

    def terminal(home, answer):
        master, slave = pty.openpty()
        terminal_env = dict(os.environ, TERM='xterm-256color')
        terminal_env.pop('NO_COLOR', None)
        process = subprocess.Popen(
            [binary, '--home', str(home), 'database', 'remove', '--reason', 'PTY reset review'],
            stdin=slave, stdout=slave, stderr=slave, start_new_session=True,
            env=terminal_env)
        os.close(slave)
        transcript = bytearray()
        replied = False
        deadline = time.monotonic() + 20
        try:
            while time.monotonic() < deadline:
                if select.select([master], [], [], .1)[0]:
                    try:
                        data = os.read(master, 65536)
                    except OSError as error:
                        if error.errno == errno.EIO:
                            break
                        raise
                    if not data:
                        break
                    transcript.extend(data)
                text = re.sub(rb'\x1b\[[0-?]*[ -/]*[@-~]', b'', transcript)
                if b'[y/N]' in text and not replied:
                    assert b'Exact SQLite erase scope' in text
                    assert b'Authority:' in text and b'Preserved' in text
                    assert b'\x1b[' in transcript, 'terminal preview lacks styling'
                    if answer == 'interrupt':
                        process.send_signal(signal.SIGINT)
                    elif answer == 'eof':
                        os.write(master, b'\x04')
                    else:
                        os.write(master, answer.encode())
                    replied = True
                if process.poll() is not None:
                    break
            assert replied, transcript.decode(errors='replace')
            code = process.wait(timeout=3)
            if answer == 'y\n':
                assert code == 0 and b'Reviewed database reset completed' in transcript, transcript
            else:
                assert code != 0, transcript
            return transcript
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=3)
            os.close(master)

    try:
        home = root / 'ordinary'
        homes.append(home)
        cli(home, 'start', '--cpu', '2', '--disk', '1GiB')
        original_host = cli(home, 'status')['host']
        lease = cli(home, 'acquire', '--cpu', '1', '--key', 'old-exact-key', '--ttl', '2m',
                    '--reason', 'compiled private reset fixture')
        cli(home, 'release', lease['id'])
        cli(home, 'stop')
        before = snapshot(home)
        preview = cli(home, 'database', 'remove', '--dry-run')
        assert preview['plan']['settled_proof']['settled']
        assert preview['plan']['old_authority'] == original_host
        assert snapshot(home) == before, 'dry-run changed source'
        refused = cli(home, 'database', 'remove', '--reason', 'script missing consent', check=False)
        assert refused.returncode != 0 and json.loads(refused.stderr)['error']['code'] == 'confirmation_required'
        assert snapshot(home) == before
        for answer in ('n\n', '\n', 'eof', 'interrupt'):
            terminal(home, answer)
            assert snapshot(home) == before, ('decline/interrupt changed source', answer)
        terminal(home, 'y\n')
        assert not (home / 'state.db').exists()
        pointer = json.loads((home / 'database-reset-seed.json').read_text())
        assert pointer['authority'] != original_host and not pointer['installed']
        operation = pointer['operation']
        before_recovery = snapshot(home)
        cli(home, 'database', 'remove', '--continue', operation, '--dry-run')
        assert snapshot(home) == before_recovery
        cli(home, 'start')
        assert cli(home, 'status')['host'] == pointer['authority']
        assert not cli(home, 'status')['leases']
        fresh = cli(home, 'acquire', '--cpu', '1', '--key', 'old-exact-key', '--ttl', '2m',
                    '--reason', 'new authority private fixture')
        assert fresh['id'] != lease['id'], 'old idempotency namespace reused'
        cli(home, 'release', fresh['id'])
        cli(home, 'stop')
        assert json.loads((home / 'database-reset-seed.json').read_text())['installed']
        # A second reset proves the applied-marker/receipt chain and seeds a
        # different authority rather than repairing the established database.
        second = cli(home, 'database', 'remove', '--yes', '--reason', 'second private reset')
        assert second['result']['completed'] and second['result']['new_authority'] != pointer['authority']

        mesh_home = root / 'mesh'
        homes.append(mesh_home)
        cli(mesh_home, 'start', '--cpu', '2', '--disk', '1GiB')
        old_mesh_host = cli(mesh_home, 'status')['host']
        with socket.socket() as reservation:
            reservation.bind(('127.0.0.1', 0))
            mesh_address = '127.0.0.1:' + str(reservation.getsockname()[1])
        cli(mesh_home, 'mesh', 'init', '--name', 'private-reset-node', '--listen', mesh_address,
            '--address', mesh_address, '--yes')
        cli(mesh_home, 'stop')
        private_key = (mesh_home / 'mesh' / 'node-key.pem').read_bytes()
        before = snapshot(mesh_home)
        refused = cli(mesh_home, 'database', 'remove', '--yes', '--reason', 'missing retirement', check=False)
        assert refused.returncode != 0 and json.loads(refused.stderr)['error']['code'] == 'mesh_retirement_required'
        assert snapshot(mesh_home) == before
        result = cli(mesh_home, 'database', 'remove', '--yes', '--retire-mesh',
                     '--reason', 'retire disposable identity')['result']
        archive = mesh_home / 'database-maintenance' / ('reset-' + result['operation']) / 'mesh'
        assert result['completed'] and (archive / 'node-key.pem').read_bytes() == private_key
        assert not (mesh_home / 'mesh').exists()
        cli(mesh_home, 'start')
        assert cli(mesh_home, 'status')['host'] != old_mesh_host
        assert not (mesh_home / 'mesh').exists(), 'old mesh pins silently regenerated'
        cli(mesh_home, 'stop')

        # Retiring a source must never silently release/revoke destination work.
        peers = []
        for name in ('claim-source', 'claim-target'):
            peer_home = root / name
            homes.append(peer_home)
            cli(peer_home, 'start', '--cpu', '2', '--disk', '1GiB')
            with socket.socket() as reservation:
                reservation.bind(('127.0.0.1', 0))
                address = '127.0.0.1:' + str(reservation.getsockname()[1])
            identity = cli(peer_home, 'mesh', 'init', '--name', name, '--listen', address,
                           '--address', address, '--yes')
            peers.append((peer_home, identity))
        source, target = peers
        pair_key = os.urandom(32).hex()
        forward = {'permissions': ['observe-host', 'observe-own', 'lease-own'],
                   'limits': {'cpu': 1, 'memory_bytes': 0, 'disk_bytes': 0},
                   'max_ttl_ns': 30_000_000_000, 'contact_ids': [], 'operator_inbox': False}
        reverse = {'permissions': ['observe-host'], 'limits': {}, 'max_ttl_ns': 0,
                   'contact_ids': [], 'operator_inbox': False}
        for node, other, grant in ((target, source, forward), (source, target, reverse)):
            cli(node[0], 'mesh', 'bootstrap', stdin=json.dumps({
                'Action': 'pair', 'Identity': other[1], 'Grant': grant,
                'Pair': pair_key, 'Reason': 'private reset expiry acceptance'}))
        for node, other in ((source, target), (target, source)):
            cli(node[0], 'mesh', 'call', other[1]['host'], 'hello',
                '--params', json.dumps({'pair': pair_key}))
        for node, other in ((source, target), (target, source)):
            cli(node[0], 'mesh', 'bootstrap', stdin=json.dumps({
                'Action': 'activate', 'Host': other[1]['host'], 'Pair': pair_key}))
        remote_claim = cli(source[0], 'acquire', '--host', target[1]['host'], '--cpu', '.5',
                           '--ttl', '12s', '--key', 'destination-owned-claim',
                           '--reason', 'destination keeps original expiry after source reset')
        cli(source[0], 'stop')
        cli(source[0], 'database', 'remove', '--yes', '--retire-mesh',
            '--reason', 'retire source while destination retains claim')
        retained = cli(target[0], 'inspect', remote_claim['id'])
        assert retained['state'] == 'active' and retained['expires_at'] == remote_claim['expires_at']
        assert retained['request']['origin_peer'] == source[1]['host']
        target_trust = cli(target[0], 'mesh', 'peers')
        assert target_trust[0]['state'] == 'active' and target_trust[0]['pair'] == pair_key
        cli(source[0], 'start')
        assert cli(source[0], 'status')['host'] != source[1]['host']
        assert not (source[0] / 'mesh').exists()
        deadline = time.monotonic() + 25
        while time.monotonic() < deadline:
            retained = cli(target[0], 'inspect', remote_claim['id'])
            if retained['state'] == 'terminal':
                break
            time.sleep(.1)
        assert retained['state'] == 'terminal' and retained['outcome'] == 'expired'
        assert retained['expires_at'] == remote_claim['expires_at']
        print('PASS: real PTY default-no/EOF/interrupt/yes; JSON scope; fresh authority/retry fencing; repeat reset; explicit mesh archival; two-TLS-node destination-owned expiry unchanged')
    finally:
        for home in homes:
            try:
                cli(home, 'stop', '--force', check=False)
            except (OSError, subprocess.TimeoutExpired):
                pass
        safe_cleanup = True
        for home in homes:
            lock_path = home / 'daemon.lock'
            if lock_path.exists():
                try:
                    with lock_path.open('r+') as lock:
                        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                except BlockingIOError:
                    safe_cleanup = False
        if safe_cleanup:
            shutil.rmtree(root)  # only this script's uniquely created private fixture
        else:
            print('Retained private fixture because a test backend still owns its lock:', root)


if __name__ == '__main__':
    main()
