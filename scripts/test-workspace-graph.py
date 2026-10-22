#!/usr/bin/env python3
"""Actual four-daemon graph refresh/routing after explicit direct admissions.

The bootstrap A-D pair is explicitly revoked before graph probes. No candidate
is admitted through an unreviewed relay. A switchable opaque TLS proxy proves
alternate paths are attempted before application write; failed evidence stays.
"""
import argparse
import importlib.util
import json
from pathlib import Path
import shutil
import socket
import sys
import tempfile
import time

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('workspace_graph_acceptance', Path(__file__).with_name('test-workspaces.py'))
ws = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ws)
mesh = ws.mesh

class Gate(mesh.TLSRelay):
    def __init__(self, upstream):
        self.blocked = False
        self.refused = 0
        super().__init__(upstream)

    def forward(self, downstream, drop):
        with self.lock:
            blocked = self.blocked
            if blocked:
                self.refused += 1
                self.connections.discard(downstream)
        if blocked:
            downstream.close()
            return
        super().forward(downstream, drop)

    def block(self, value):
        with self.lock:
            self.blocked = value
            connections = list(self.connections) if value else []
        for connection in connections:
            try:
                connection.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', required=True)
    parser.add_argument('--topology', choices=('all', 'diamond', 'chain'), default='all')
    args = parser.parse_args()
    binary = str(Path(args.arm).resolve())
    if args.topology == 'chain':
        run_chain(binary)
        return
    root = Path(tempfile.mkdtemp(prefix='arm-workspace-graph-'))
    root.chmod(0o700)
    nodes = [mesh.Node(binary, root, name) for name in ('directory-a', 'relay-one', 'relay-two', 'origin-d')]
    a, one, two, d = nodes
    gates = {}
    success = False
    try:
        for node in nodes:
            node.start()
            port = mesh.free_port()
            gate = Gate(('127.0.0.1', port))
            gates[node] = gate
            node.identity = node.cli('mesh', 'init', '--name', node.name, '--listen', f'127.0.0.1:{port}', '--address', gate.address, '--yes')
        # Label B as the lexically first shortest-path relay, making the blocked
        # first path deterministic without fabricating host IDs or graph rows.
        b, c = sorted((one, two), key=lambda n: n.identity['host'])
        for node in (b, c, d):
            ws.enroll(a, node)
        ws.enroll(b, d)
        ws.enroll(c, d)

        def decide(node, operation):
            return node.cli('workspaces', 'decide', '--workspace', operation['workspace'], '--key', operation['key'], '--revision', str(operation['revision']), '--review', operation['review_sha256'], '--approve', '--yes')

        created = a.cli('workspaces', 'create', '--name', 'Private four-node graph', '--key', 'create', '--reason', 'real mutual reachability reports and reviewed membership')
        wid = created['workspace']
        decide(a, created)
        invitation = root / 'invitation.json'
        a.cli('workspaces', 'invitation', '--workspace', wid, '--output', str(invitation))
        for node in (b, c, d):
            join = node.cli('workspaces', 'join', '--invitation', str(invitation), '--key', 'join', '--reason', 'explicit bootstrap admission before graph routing')
            decide(node, join)
            consent = root / (node.name + '-consent.json')
            node.cli('workspaces', 'consent', '--workspace', wid, '--key', 'join', '--output', str(consent))
            request = ['workspaces', 'admit', '--workspace', wid, '--consent', str(consent), '--key', 'admit-' + node.name, '--reason', 'exact current candidate scope']
            review = a.cli(*request)
            admitted = a.cli(*request, '--review', review['review_sha256'], '--yes')
            assert admitted['phase'] == 'completed', admitted
            node.cli('workspaces', 'refresh', '--workspace', wid)
            operation = node.cli('workspaces', 'lookup', '--workspace', wid, '--key', 'join')['operation']
            node.cli('workspaces', 'finalize', '--workspace', wid, '--key', 'join', '--revision', str(operation['revision']))
        for _ in range(2):
            for node in (b, c, d, a):
                node.cli('workspaces', 'refresh', '--workspace', wid)
        for target, other in ((a, d), (d, a)):
            target.cli('mesh', 'revoke', other.identity['host'], '--reason', 'remove temporary direct bootstrap edge', '--yes')
        # Reports are all real current registry observations. No SQLite writes or
        # injected TLS proofs create this diamond.
        for _ in range(2):
            for node in (b, c, d, a):
                node.cli('workspaces', 'refresh', '--workspace', wid)
        paths = d.cli('workspaces', 'graph', '--workspace', wid, '--section', 'paths', '--to', a.identity['host'])
        expected = [[d.identity['host'], relay.identity['host'], a.identity['host']] for relay in (b, c)]
        assert paths['current_observation'] and paths['paths'][:2] == expected, paths
        edges = d.cli('workspaces', 'graph', '--workspace', wid, '--section', 'edges')
        assert edges['total'] == 4 and edges['authority'] == 'reachability_only', edges
        for edge in edges['edges']:
            assert {edge['a'], edge['b']} != {a.identity['host'], d.identity['host']}, edge
        # Pure native reads do not refresh. Observe a later actual daemon tick
        # updating D's own committed bundle, with no explicit refresh/send call.
        previous_revision = edges['revision']
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            periodic = d.cli('workspaces', 'graph', '--workspace', wid, '--section', 'nodes')
            if periodic['revision'] > previous_revision and periodic['current_observation']:
                break
            time.sleep(.2)
        else:
            raise AssertionError(('background graph refresh did not publish a new complete bundle', periodic))
        # Block the first lexically selected relay. Both directory refresh and
        # actual delivery must use the other path, before any application replay.
        gates[b].block(True)
        refused_before = gates[b].refused
        refreshed = d.cli('workspaces', 'refresh', '--workspace', wid)
        assert refreshed['graph_sha256'] and gates[b].refused > refused_before, refreshed
        sent = d.cli('workspaces', 'send', '--workspace', wid, '--to', a.identity['host'], '--key', 'alternate-send', '--text', 'D via C to A with B unavailable')
        assert sent['phase'] == 'completed' and sent['origin_host'] == d.identity['host'], sent
        inbox = a.cli('workspaces', 'inbox', '--workspace', wid)
        assert inbox['total'] == 1 and inbox['inbox'][0]['envelope']['origin_host'] == d.identity['host'], inbox
        original = inbox['inbox'][0]
        replied = a.cli('workspaces', 'reply', '--workspace', wid, '--message', original['envelope']['message_id'], '--key', 'alternate-reply', '--text', 'A replies to original D via C')
        assert replied['phase'] == 'completed', replied
        back = d.cli('workspaces', 'inbox', '--workspace', wid)
        assert back['total'] == 1 and back['inbox'][0]['envelope']['reply_to']['message_id'] == sent['message_id'], back
        # Restart destroys live membership/cache. Saved paths may probe ONLY the
        # pinned directory; successful real reception restores a fresh bundle.
        assert d.stop()
        d.stopped = False
        d.start()
        restarted = d.cli('workspaces', 'graph', '--workspace', wid, '--section', 'paths', '--to', a.identity['host'])
        assert not restarted['current_observation'] and restarted['authority'] == 'reachability_only', restarted
        fresh = d.cli('workspaces', 'refresh', '--workspace', wid)
        assert fresh['graph_sha256'], fresh
        assert d.cli('workspaces', 'message-lookup', '--workspace', wid, '--key', 'alternate-send', '--reconcile') == sent
        assert a.cli('workspaces', 'inbox', '--workspace', wid)['total'] == 1
        gates[b].block(False)
        for node in nodes:
            if node is not a:
                for operation in ('workspace_graph_read', 'workspace_refresh', 'workspace_message_send', 'run', 'ssh_account'):
                    node.call(a.identity['host'], operation, {}, key='foreign-run' if operation == 'run' else None, codes={'forbidden'})
        for node in (a, d):
            forbidden = d if node is a else a
            records = node.cli('mesh', 'peers')
            record = next(p for p in records if p['identity']['host'] == forbidden.identity['host'])
            assert record['state'] == 'revoked', records
        print('PASS actual four-daemon diamond: complete mutual graph, revoked A-D bootstrap, first-path failure and alternate directory/message/reply, original endpoints, source restart saved-directory-only probe, keyed lookup and forwarded authority denial; no direct pair promotion')
        success = True
    finally:
        for gate in gates.values():
            gate.close()
        stopped = all([node.stop() for node in nodes])
        if success and stopped:
            shutil.rmtree(root)
        else:
            print('Graph workspace fixture retained for inspection: ' + str(root), file=sys.stderr)

    if args.topology == 'all':
        run_chain(binary)


def run_chain(binary):
    root = Path(tempfile.mkdtemp(prefix='arm-workspace-graph-chain-'))
    root.chmod(0o700)
    nodes = [mesh.Node(binary, root, name) for name in ('directory-a', 'relay-b', 'relay-c', 'origin-d')]
    a, b, c, d = nodes
    success = False
    try:
        for node in nodes:
            node.start()
            port = mesh.free_port()
            node.identity = node.cli('mesh', 'init', '--name', node.name, '--listen', f'127.0.0.1:{port}', '--address', f'127.0.0.1:{port}', '--yes')
        for node in (b, c, d):
            ws.enroll(a, node)
        ws.enroll(b, c)
        ws.enroll(c, d)
        def decide(node, operation):
            return node.cli('workspaces', 'decide', '--workspace', operation['workspace'], '--key', operation['key'], '--revision', str(operation['revision']), '--review', operation['review_sha256'], '--approve', '--yes')
        create = a.cli('workspaces', 'create', '--name', 'Private genuine multihop recovery', '--key', 'create', '--reason', 'directory restart sparse graph must preserve actual path')
        wid = create['workspace']
        decide(a, create)
        invitation = root / 'invitation.json'
        a.cli('workspaces', 'invitation', '--workspace', wid, '--output', str(invitation))
        for node in (b, c, d):
            join = node.cli('workspaces', 'join', '--invitation', str(invitation), '--key', 'join', '--reason', 'explicit temporary direct proof before chain')
            decide(node, join)
            consent = root / (node.name + '-consent.json')
            node.cli('workspaces', 'consent', '--workspace', wid, '--key', 'join', '--output', str(consent))
            request = ['workspaces', 'admit', '--workspace', wid, '--consent', str(consent), '--key', 'admit-' + node.name, '--reason', 'exact admitted chain member']
            review = a.cli(*request)
            assert a.cli(*request, '--review', review['review_sha256'], '--yes')['phase'] == 'completed'
            node.cli('workspaces', 'refresh', '--workspace', wid)
            operation = node.cli('workspaces', 'lookup', '--workspace', wid, '--key', 'join')['operation']
            node.cli('workspaces', 'finalize', '--workspace', wid, '--key', 'join', '--revision', str(operation['revision']))
        for _ in range(2):
            for node in (b, c, d, a):
                node.cli('workspaces', 'refresh', '--workspace', wid)
        for member in (c, d):
            for target, other in ((a, member), (member, a)):
                target.cli('mesh', 'revoke', other.identity['host'], '--reason', 'remove direct bootstrap; chain only', '--yes')
        for _ in range(2):
            for node in (b, c, d, a):
                node.cli('workspaces', 'refresh', '--workspace', wid)
        expected = [d.identity['host'], c.identity['host'], b.identity['host'], a.identity['host']]
        path = d.cli('workspaces', 'graph', '--workspace', wid, '--section', 'paths', '--to', a.identity['host'])
        assert path['paths'] == [expected], path
        # Observe a new periodic refresh on both relays; their next tick is at
        # least13.5s away. This gives the actual restart/probe a bounded window
        # where no relay report has reached A's newly empty live witness cache.
        previous = {node: node.cli('workspaces', 'graph', '--workspace', wid)['revision'] for node in (b, c)}
        ready = set()
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline and len(ready) < 2:
            for node in (b, c):
                if node.cli('workspaces', 'graph', '--workspace', wid)['revision'] > previous[node]:
                    ready.add(node)
            if len(ready) < 2:
                time.sleep(.1)
        assert len(ready) == 2, 'relay background refresh did not complete'
        assert a.stop()
        a.stopped = False
        a.start()
        # Directory send hints are not promoted from saved LastIssue either.
        directory_saved = a.cli('workspaces', 'graph', '--workspace', wid)
        assert not directory_saved['current_observation'], directory_saved
        d.cli('workspaces', 'refresh', '--workspace', wid)
        sparse = d.cli('workspaces', 'graph', '--workspace', wid, '--section', 'edges')
        assert sparse['total'] == 0, ('restart window did not produce the required sparse graph', sparse)
        historical = d.cli('workspaces', 'graph', '--workspace', wid, '--section', 'paths', '--to', a.identity['host'])
        assert not historical.get('paths') and expected in historical['historical_directory_paths'], historical
        assert d.stop()
        d.stopped = False
        d.start()
        saved = d.cli('workspaces', 'graph', '--workspace', wid, '--section', 'paths', '--to', a.identity['host'])
        assert not saved['current_observation'] and expected in saved['historical_directory_paths'], saved
        # TWO-hop guesses cannot reach A here: C has no current A pair, and D
        # has no B pair. Only retained actual D-C-B-A reachability can restore it.
        fresh = d.cli('workspaces', 'refresh', '--workspace', wid)
        assert fresh['graph_sha256'], fresh
        for node in (b, c, d):
            node.cli('workspaces', 'refresh', '--workspace', wid)
        current = d.cli('workspaces', 'graph', '--workspace', wid, '--section', 'paths', '--to', a.identity['host'])
        assert current['current_observation'] and current['paths'] == [expected], current
        sent = d.cli('workspaces', 'send', '--workspace', wid, '--to', a.identity['host'], '--key', 'three-hop-send', '--text', 'D through C and B after directory and source restart')
        assert sent['phase'] == 'completed', sent
        inbox = a.cli('workspaces', 'inbox', '--workspace', wid)
        assert inbox['total'] == 1 and inbox['inbox'][0]['envelope']['origin_host'] == d.identity['host'], inbox
        reply = a.cli('workspaces', 'reply', '--workspace', wid, '--message', inbox['inbox'][0]['envelope']['message_id'], '--key', 'three-hop-reply', '--text', 'A replies to original D through B and C')
        assert reply['phase'] == 'completed', reply
        for node in (a, b, c, d):
            active = {p['identity']['host'] for p in node.cli('mesh', 'peers') if p['state'] == 'active'}
            allowed = {a: {b.identity['host']}, b: {a.identity['host'], c.identity['host']}, c: {b.identity['host'], d.identity['host']}, d: {c.identity['host']}}[node]
            assert active == allowed, (node.name, active, allowed)
        print('PASS actual4-daemon D-C-B-A3-hop chain: directory restart produces empty graph, durable authenticated path survives sparse overwrite and source restart, fresh graph restored then original send/reply; no shortcut pairs or historical message authority')
        success = True
    finally:
        stopped = all([node.stop() for node in nodes])
        if success and stopped:
            shutil.rmtree(root)
        else:
            print('Chain graph workspace fixture retained for inspection: ' + str(root), file=sys.stderr)

if __name__ == '__main__':
    main()
