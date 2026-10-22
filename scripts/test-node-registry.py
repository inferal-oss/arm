#!/usr/bin/env python3
"""Compiled native registry/current-scope acceptance; optional direct SSH peer.

Only explicitly supplied test binaries and newly created private state are used.
No identity initialization occurs from registry reads. No private test destination
is stored in this source. --ssh mode uses existing hardened mesh connect.
"""
import argparse
import json
from pathlib import Path
import re
import shlex
import socket
import subprocess
import tempfile
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--arm', required=True)
    parser.add_argument('--probe', required=True)
    parser.add_argument('--ssh')
    parser.add_argument('--remote-binary')
    parser.add_argument('--remote-probe')
    parser.add_argument('--remote-root')
    args = parser.parse_args()
    if args.ssh:
        assert not args.ssh.startswith('-')
        for value in (args.remote_binary, args.remote_probe, args.remote_root):
            assert value and re.fullmatch(r'/[A-Za-z0-9_./-]+', value) and '..' not in Path(value).parts
        assert args.remote_root.startswith('/tmp/')
    arm, probe = str(Path(args.arm).resolve()), str(Path(args.probe).resolve())
    nodes = []

    def run(command, ok=True, packet=None):
        process = subprocess.run(command, input=None if packet is None else json.dumps(packet), capture_output=True, text=True, timeout=60)
        assert (process.returncode == 0) == ok, (process.returncode, process.stderr)
        return json.loads(process.stdout) if ok else process

    def ssh(command):
        return ['ssh', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'UpdateHostKeys=no', '--', args.ssh, shlex.join(command)]

    with tempfile.TemporaryDirectory(prefix='arm-node-registry-') as directory:
        local_home = str(Path(directory) / 'left')
        left = [arm, '--home', local_home]
        right_home = (args.remote_root + '/registry-' + uuid.uuid4().hex) if args.ssh else str(Path(directory) / 'right')
        right = ([args.remote_binary, '--home', right_home] if args.ssh else [arm, '--home', right_home])

        def remote(command):
            return ssh(command) if args.ssh else command

        def read(home, host=None, peer=False):
            command = [args.remote_probe if peer and args.ssh else probe, home] + ([] if host is None else [host])
            return run(remote(command) if peer else command)

        try:
            run(left + ['start', '--cpu', '2', '--memory', 'auto', '--disk', 'auto'])
            nodes.append((left, False))
            initial = read(local_home)
            assert initial['mesh_state'] == 'unconfigured' and len(initial['nodes']) == 1 and initial['nodes'][0]['local']
            assert initial['nodes'][0]['metadata']['host'] == initial['source_host']
            assert not (Path(local_home) / 'mesh').exists(), 'node_list initialized mesh'
            if args.ssh:
                nodes.append((right, True))
                connected = run(left + ['mesh', 'connect', args.ssh, '--binary', args.remote_binary, '--remote-home', right_home, '--reverse-grant', 'observe-host', '--reason', 'private registry cross-host acceptance', '--key', uuid.uuid4().hex, '--yes'])
                assert connected['connected'] and connected['local_identity']['host'] == initial['source_host']
                lidentity = run(left + ['mesh', 'identity'])
                ridentity = run(remote(right + ['mesh', 'identity']))
            else:
                run(right + ['start', '--cpu', '2', '--memory', 'auto', '--disk', 'auto'])
                nodes.append((right, True))
                identities = []
                for command, peer, name in ((left, False, 'registry-left'), (right, True, 'registry-right')):
                    with socket.socket() as listener:
                        listener.bind(('127.0.0.1', 0))
                        address = '127.0.0.1:' + str(listener.getsockname()[1])
                    identities.append(run(command + ['mesh', 'init', '--name', name, '--listen', address, '--address', address, '--yes']))
                lidentity, ridentity = identities
                pair = uuid.uuid4().hex + uuid.uuid4().hex
                grant = {'permissions': ['observe-host'], 'limits': {}, 'max_ttl_ns': 0, 'contact_ids': [], 'operator_inbox': False}
                for command, identity in ((left, ridentity), (right, lidentity)):
                    run(command + ['mesh', 'bootstrap'], packet={'Action': 'pair', 'Identity': identity, 'Grant': grant, 'Pair': pair, 'Reason': 'private registry acceptance'})
                for command, identity in ((left, ridentity), (right, lidentity)):
                    run(command + ['mesh', 'call', identity['host'], 'hello', '--params', json.dumps({'pair': pair})])
                for command, identity in ((left, ridentity), (right, lidentity)):
                    run(command + ['mesh', 'bootstrap'], packet={'Action': 'activate', 'Host': identity['host'], 'Pair': pair})
            listing = read(local_home)
            assert len(listing['nodes']) == 2 and listing['mesh_state'] == 'enabled'
            peer = next(node for node in listing['nodes'] if not node['local'])
            assert peer['metadata'] is None and peer['outbound_scope'] is None and peer['health'] == 'unobserved'
            detail = read(local_home, ridentity['host'])
            scope = detail['outbound_scope']
            assert detail['metadata']['host'] == ridentity['host'] and detail['health'] == 'reachable'
            assert scope['source_host'] == lidentity['host'] and scope['target_host'] == ridentity['host'] and scope['revision'] > 0 and 'observe-host' in scope['grant']['permissions']
            assert not detail['scope_freshness']['stale'] and not detail['metadata_freshness']['stale']
            reverse = read(right_home, lidentity['host'], peer=True)
            assert reverse['outbound_scope']['source_host'] == ridentity['host'] and reverse['metadata']['host'] == lidentity['host']
            raw = json.dumps(read(local_home))
            assert 'certificate"' not in raw and 'node-key' not in raw and 'private_key' not in raw and local_home not in raw and right_home not in raw
            run(remote(right + ['stop', '--force']))
            stale = read(local_home, ridentity['host'])
            assert stale['metadata']['host'] == ridentity['host'] and stale['scope_freshness']['stale'] and stale['health'] == 'unavailable'
            run(left + ['mesh', 'revoke', ridentity['host'], '--reason', 'private registry revocation', '--yes'])
            revoked = read(local_home, ridentity['host'])
            assert revoked['trust']['state'] == 'revoked' and revoked['metadata'] is None and revoked['outbound_scope'] is None
            print('Node registry: compiled daemon/native reads, meaningful localhost/no initialization, list no fanout, chosen stable-ID pinned-mTLS metadata/current destination scope, private-data exclusion, offline stale retention and revocation invalidation passed' + (' across actual SSH-connected hosts' if args.ssh else ''))
        finally:
            for command, peer in reversed(nodes):
                subprocess.run(remote(command + ['stop', '--force']) if peer else command + ['stop', '--force'], capture_output=True, timeout=30)
            if args.ssh:
                subprocess.run(ssh(['rm', '-rf', '--', right_home]), capture_output=True, timeout=30)


if __name__ == '__main__':
    main()
