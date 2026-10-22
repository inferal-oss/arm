#!/usr/bin/env python3
"""Private actual backend/CLI and pinned-mTLS node metadata acceptance."""
import argparse
import json
from pathlib import Path
import platform
import socket
import subprocess
import tempfile
import uuid


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    binary = str(Path(parser.parse_args().arm).resolve())
    with tempfile.TemporaryDirectory(prefix='arm-node-metadata-') as directory:
        root = Path(directory)
        nodes = []

        def call(prefix, *args, packet=None, ok=True):
            result = subprocess.run(prefix + list(args), input=None if packet is None else json.dumps(packet), capture_output=True, text=True, timeout=15)
            assert (result.returncode == 0) == ok, (args, result.returncode, result.stdout, result.stderr)
            return json.loads(result.stdout if ok else result.stderr)

        def check(value, host):
            assert set(value) == {'schema_version', 'host', 'hostname', 'platform', 'build', 'inventory', 'capabilities', 'reported_at', 'unavailable'}
            assert value['schema_version'] == 1 and value['host'] == host
            assert value['hostname'] == socket.gethostname()
            assert value['platform']['os'] == {'Darwin': 'darwin', 'Linux': 'linux'}[platform.system()]
            assert value['platform']['process_architecture'] == {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'amd64'}[platform.machine()]
            assert value['platform']['kernel_release'] == platform.release()
            assert value['build']['arm_version'] == call(nodes[0][0], 'version')['version']
            assert value['inventory']['logical_cpus'] > 0 and 'reported-node-metadata' in value['capabilities']
            for dimension in ('host_memory', 'state_filesystem'):
                observation = value['inventory'][dimension]
                assert observation['total_bytes'] is None or isinstance(observation['total_bytes'], int) and observation['total_bytes'] > 0
                assert not observation['known'] or not observation['stale'] and observation['observed_at'] is not None
            assert 'sentinel' not in json.dumps(value) and str(root) not in json.dumps(value)

        try:
            for name in ('metadata-source', 'metadata-target'):
                prefix = [binary, '--home', str(root / name)]
                call(prefix, 'start', '--cpu', '2', '--memory', 'auto', '--disk', 'auto')
                nodes.append((prefix, None))
                with socket.socket() as listener:
                    listener.bind(('127.0.0.1', 0))
                    address = '127.0.0.1:' + str(listener.getsockname()[1])
                identity = call(prefix, 'mesh', 'init', '--name', name, '--listen', address, '--address', address, '--yes')
                nodes[-1] = (prefix, identity)
                check(call(prefix, 'node'), identity['host'])
                check(call(prefix, 'node', '--host', identity['host']), identity['host'])
            left, right = nodes
            # A private lease reason/contact/path exists but observe-host never
            # includes these in the metadata schema.
            call(right[0], 'acquire', '--cpu', '.1', '--reason', 'private-reason-sentinel')
            call(right[0], 'agent', 'register', '--id', 'private-contact-sentinel', '--adapter', 'generic', '--delivery', 'poll', '--session', str(uuid.uuid4()), '--cwd', str(root))
            operation = uuid.uuid4().hex + uuid.uuid4().hex
            for target, other, permissions in ((right, left, ['observe-host']), (left, right, [])):
                grant = {'permissions': permissions, 'limits': {}, 'max_ttl_ns': 0, 'contact_ids': [], 'operator_inbox': False}
                call(target[0], 'mesh', 'bootstrap', packet={'Action': 'pair', 'Identity': other[1], 'Grant': grant, 'Pair': operation, 'Reason': 'metadata-only fixture'})
            for source, target in ((left, right), (right, left)):
                call(source[0], 'mesh', 'call', target[1]['host'], 'hello', '--params', json.dumps({'pair': operation}))
            for target, other in ((left, right), (right, left)):
                call(target[0], 'mesh', 'bootstrap', packet={'Action': 'activate', 'Host': other[1]['host'], 'Pair': operation})
            for selector in (right[1]['host'], 'metadata-target'):
                check(call(left[0], 'node', '--host', selector), right[1]['host'])
            denied = call(right[0], 'node', '--host', left[1]['host'], ok=False)
            assert denied['error']['code'] == 'forbidden'
            for operation in ('contacts', 'acquire'):
                args = ['mesh', 'call', right[1]['host'], operation, '--params', '{}']
                if operation == 'acquire':
                    args += ['--key', 'metadata-cannot-grant']
                denied = call(left[0], *args, ok=False)
                assert denied['error']['code'] == 'forbidden'
            print('Node metadata: identical local/stable-ID/name schema, native OS/build/hostname/inventory, original observation freshness, private-data exclusion, actual pinned-mTLS directional observe-host read and no contact/lease grant expansion passed')
        finally:
            for prefix, _ in reversed(nodes):
                subprocess.run(prefix + ['stop', '--force'], capture_output=True, timeout=15)


if __name__ == '__main__':
    main()
