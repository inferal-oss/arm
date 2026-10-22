#!/usr/bin/env python3
"""Actual three-daemon native workspace CLI and closed nested-TLS acceptance.

Only private fixture nodes are enrolled. A transparent ciphertext relay drops
one real candidate proof response after the candidate's SQLite write; it neither
terminates TLS nor substitutes a proof. Failed evidence is retained. This is
workspace admission/discovery, not routed message or agent-endpoint completion.
"""
import argparse
import importlib.util
import json
import errno
import os
import pty
import select
from pathlib import Path
import shutil
import socket
import sqlite3
import subprocess
import sys
import tempfile
import threading
import time
import uuid

spec = importlib.util.spec_from_file_location('mesh_acceptance', Path(__file__).with_name('test-mesh.py'))
mesh = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mesh)


class ProofReplyRelay(mesh.TLSRelay):
    def __init__(self, upstream):
        self.proof_home = None
        self.proof_key = None
        self.proof_armed = False
        super().__init__(upstream)

    def should_drop_proof(self):
        if not self.proof_armed:
            return False
        try:
            uri = (self.proof_home / 'state.db').as_uri() + '?mode=ro'
            with sqlite3.connect(uri, uri=True, timeout=.1) as db:
                for row in db.execute('SELECT data FROM workspace_operations'):
                    record = json.loads(row[0])
                    if record['key'] == self.proof_key and record.get('private', {}).get('proof_count', 0) > 0:
                        self.proof_armed = False
                        self.dropped.set()
                        return True
        except (sqlite3.Error, OSError):
            return False
        return False

    def pipe(self, source, target, server=False):
        try:
            while not self.closed.is_set():
                header = self.exact(source, 5)
                if header is None:
                    return
                record = self.exact(source, int.from_bytes(header[3:5], 'big'))
                if record is None:
                    return
                if server and self.should_drop_proof():
                    return
                target.sendall(header + record)
        except (OSError, ConnectionError):
            pass
        finally:
            try:
                source.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            try:
                target.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass

    def forward(self, downstream, drop):
        # Override the base encrypted-record worker; all normal traffic is
        # unchanged. One explicit producer observation arms the reply loss.
        upstream = None
        try:
            upstream = socket.create_connection(self.upstream, timeout=10)
            with self.lock:
                self.connections.add(upstream)
            left = threading.Thread(target=self.pipe, args=(downstream, upstream, False), daemon=True)
            right = threading.Thread(target=self.pipe, args=(upstream, downstream, True), daemon=True)
            left.start()
            right.start()
            left.join(45)
            right.join(45)
        finally:
            for conn in (downstream, upstream):
                if conn:
                    conn.close()
                    with self.lock:
                        self.connections.discard(conn)


def enroll(a, b):
    pair = uuid.uuid4().hex + uuid.uuid4().hex
    grant = {'permissions': ['observe-host'], 'limits': {}, 'max_ttl_ns': 0, 'contact_ids': [], 'operator_inbox': False}
    for target, source in ((a, b), (b, a)):
        target.bootstrap(Action='pair', Identity=source.identity, Grant=grant, Pair=pair, Reason='private workspace direct transport')
    for target, source in ((a, b), (b, a)):
        source.call(target.identity['host'], 'hello', {'pair': pair})
    for target, source in ((a, b), (b, a)):
        target.bootstrap(Action='activate', Host=source.identity['host'], Pair=pair)


def terminal_decline(node, operation, root):
    master, slave = pty.openpty()
    process = subprocess.Popen(node.prefix + ['workspaces', 'decide', '--workspace', operation['workspace'],
                                '--key', operation['key'], '--revision', str(operation['revision']),
                                '--review', operation['review_sha256'], '--approve'],
                               stdin=slave, stdout=slave, stderr=slave,
                               env=dict(os.environ, TERM='xterm-256color', COLORTERM='truecolor'))
    os.close(slave)
    text = bytearray()
    answered = False
    try:
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            if select.select([master], [], [], .05)[0]:
                try:
                    data = os.read(master, 65536)
                except OSError as error:
                    if error.errno == errno.EIO:
                        break
                    raise
                if not data:
                    break
                text.extend(data)
                if b'[y/N]' in text and not answered:
                    os.write(master, b'n\n')
                    answered = True
            if process.poll() is not None:
                break
        process.wait(timeout=2)
        value = text.decode(errors='replace')
        (root / 'pty-review.txt').write_text(value)
        assert answered and process.returncode == 1, (process.returncode, value)
        for word in ('Workspace review', 'Directory pin', 'No resource', 'declined'):
            assert word.lower() in value.lower(), (word, value)
    finally:
        if process.poll() is None:
            process.terminate()
            process.wait(timeout=3)
        os.close(master)


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--arm', required=True)
    args = p.parse_args()
    binary = str(Path(args.arm).resolve())
    root = Path(tempfile.mkdtemp(prefix='arm-workspaces-'))
    root.chmod(0o700)
    a, b, c = [mesh.Node(binary, root, n) for n in ('directory', 'member-b', 'member-c')]
    nodes = (a, b, c)
    relay = ProofReplyRelay(('127.0.0.1', mesh.free_port()))
    success = False
    try:
        ports = [mesh.free_port() for _ in nodes]
        assert len(set(ports)) == 3
        relay.upstream = ('127.0.0.1', ports[1])
        for node, port in zip(nodes, ports):
            node.start()
            node.identity = node.cli('mesh', 'init', '--name', node.name,
                                     '--listen', '127.0.0.1:' + str(port),
                                     '--address', relay.address if node is b else '127.0.0.1:' + str(port), '--yes')
        enroll(a, b)
        enroll(a, c)

        def decide(node, operation, recovery=False):
            return node.cli('workspaces', 'recovery-decide' if recovery else 'decide',
                            '--workspace', operation['workspace'], '--key', operation['key'],
                            '--revision', str(operation['revision']), '--review', operation['review_sha256'], '--approve', '--yes')

        def lookup(node, key):
            result = node.cli('workspaces', 'lookup', '--workspace', wid, '--key', key)
            assert result['found'], result
            return result['operation']

        declined = c.cli('workspaces', 'create', '--name', 'Terminal review', '--key', 'decline', '--reason', 'private PTY default-decline gate')
        terminal_decline(c, declined, root)
        prepared = a.cli('workspaces', 'create', '--name', 'Private three-node acceptance', '--key', 'create', '--reason', 'actual reviewed membership')
        wid = prepared['workspace']
        decided = decide(a, prepared)
        assert decided['phase'] == 'completed'
        invite = root / 'invitation.json'
        a.cli('workspaces', 'invitation', '--workspace', wid, '--output', str(invite))
        assert (invite.stat().st_mode & 0o777) == 0o600
        # Existing documents cannot be overwritten even with valid content.
        result = a.result('workspaces', 'invitation', '--workspace', wid, '--output', str(invite))
        assert result.returncode != 0

        def join(node, key):
            op = node.cli('workspaces', 'join', '--invitation', str(invite), '--key', key, '--reason', 'exact local candidate review')
            decided = decide(node, op)
            assert decided['phase'] == 'approved'
            consent = root / (key + '-consent.json')
            node.cli('workspaces', 'consent', '--workspace', wid, '--key', key, '--output', str(consent))
            return consent

        def admission(consent, key):
            argv = ['workspaces', 'admit', '--workspace', wid, '--consent', str(consent), '--key', key, '--reason', 'exact directory candidate review']
            review = a.cli(*argv)
            assert review['request']['candidate_consent']['workspace'] == wid
            return argv + ['--review', review['review_sha256'], '--yes']

        bconsent = join(b, 'join-b')
        # Drop the real candidate proof reply after its native transaction.
        relay.proof_home, relay.proof_key, relay.proof_armed = b.home, 'join-b', True
        send = admission(bconsent, 'admit-b')
        lost = a.result(*send)
        assert lost.returncode != 0, ('expected lost proof', lost.stdout, lost.stderr)
        assert json.loads(lost.stderr)['error']['code'] == 'workspace_outcome_unknown', lost.stderr
        assert relay.dropped.wait(2), 'ciphertext relay did not observe/drop actual candidate proof'
        assert lookup(a, 'admit-b')['phase'] == 'unknown'
        bproof = lookup(b, 'join-b')
        assert bproof['phase'] == 'approved'
        # Explicit same-key retry is an inspection, never another probe.
        before = lookup(a, 'admit-b')['revision']
        known = a.cli(*send)
        assert known['phase'] == 'unknown' and known['revision'] == before

        for node in (a, b):
            assert node.stop()
            node.stopped = False
            node.start()
        # Fresh read-only candidate review reconciles the pending directory
        # before either owner considers a new candidate proof.
        original = lookup(b, 'join-b')
        read_child = b.cli('workspaces', 'resume-join', '--workspace', wid, '--original', 'join-b',
                           '--revision', str(original['revision']), '--key', 'status-b', '--reason', 'read-only original admission reconciliation')
        read_child = decide(b, read_child, True)
        status = b.cli('workspaces', 'recovery-run', '--workspace', wid, '--key', 'status-b', '--revision', str(read_child['revision']))
        assert status['state'] == 'pending'
        b.cli('workspaces', 'cancel-recovery', '--workspace', wid, '--key', 'status-b', '--revision', str(read_child['revision']))
        original = lookup(b, 'join-b')
        child = b.cli('workspaces', 'resume-join', '--workspace', wid, '--original', 'join-b',
                      '--revision', str(original['revision']), '--key', 'resume-b', '--permit-proof', '--reason', 'fresh explicit candidate proof review')
        child = decide(b, child, True)
        fresh = root / 'fresh-consent.json'
        b.cli('workspaces', 'consent', '--workspace', wid, '--key', child['key'], '--output', str(fresh))
        original = lookup(a, 'admit-b')
        child_a = a.cli('workspaces', 'resume-admission', '--workspace', wid, '--original', 'admit-b',
                        '--revision', str(original['revision']), '--key', 'resume-admit-b', '--permit-proof',
                        '--consent', str(fresh), '--reason', 'fresh exact directory proof after read-only diagnosis')
        child_a = decide(a, child_a, True)
        active = a.cli('workspaces', 'recovery-run', '--workspace', wid, '--key', child_a['key'], '--revision', str(child_a['revision']))
        assert active['phase'] == 'completed' and active['member']['membership_generation'] == 1
        child = lookup(b, child['key'])
        final = b.cli('workspaces', 'recovery-run', '--workspace', wid, '--key', child['key'], '--revision', str(child['revision']))
        assert final['phase'] == 'completed'

        cconsent = join(c, 'join-c')
        admitted = a.cli(*admission(cconsent, 'admit-c'))
        assert admitted['phase'] == 'completed'
        c.cli('workspaces', 'refresh', '--workspace', wid)
        join_c = lookup(c, 'join-c')
        final = c.cli('workspaces', 'finalize', '--workspace', wid, '--key', 'join-c', '--revision', str(join_c['revision']))
        assert final['phase'] == 'completed'
        refreshed = b.cli('workspaces', 'refresh', '--workspace', wid)
        assert refreshed['members'] == 3
        trusted = b.cli('workspaces', 'inspect', '--workspace', wid, '--section', 'trusted', '--limit', '20')
        assert c.identity['host'] in {item['identity']['host'] for item in trusted['items']}
        assert len(b.cli('mesh', 'peers')) == 1 and len(c.cli('mesh', 'peers')) == 1
        for operation in ('workspace_create', 'workspace_admit', 'workspace_recovery_run'):
            b.call(a.identity['host'], operation, {}, codes={'forbidden'})
        print('PASS real three-daemon reviewed create/join, pinned proof, lost reply+restart+fresh recovery, directory discovery, PTY decline, no B-C peer promotion')
        success = True
    finally:
        relay.close()
        stopped = all([node.stop() for node in nodes])
        if success and stopped:
            shutil.rmtree(root)
        else:
            print('Workspace fixture retained for inspection: ' + str(root), file=sys.stderr)


if __name__ == '__main__':
    main()
