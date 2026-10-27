#!/usr/bin/env python3
"""Public CLI B→A→C forwarding when the owned relay process dies mid-request.

The destination records one effect before the relay is killed. The HTTP
client must report an unknown outcome and must not retry the request. A
replacement relay must retain its lost in-process transport charge.
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
import subprocess
import sys
import tempfile
import threading
import time
import uuid

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('routed_forward_acceptance', Path(__file__).with_name('test-routed-forward.py'))
forward = importlib.util.module_from_spec(spec)
spec.loader.exec_module(forward)
Node, mesh, ws = forward.Node, forward.mesh, forward.ws


class Effect(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'
    calls = 0
    seen = threading.Event()
    release = threading.Event()
    lock = threading.Lock()

    def do_GET(self):
        assert self.path == '/effect', self.path
        with type(self).lock:
            type(self).calls += 1
        type(self).seen.set()
        if not type(self).release.wait(30):
            raise AssertionError('relay-loss fixture never released service response')
        try:
            self.send_response(200)
            self.send_header('Content-Length', '4')
            self.end_headers()
            self.wfile.write(b'done')
        except (BrokenPipeError, ConnectionResetError):
            pass

    def log_message(self, *_):
        pass


def signal_exact_fixture_daemon(node, binary, signum):
    endpoint = json.loads((node.home / 'endpoint.json').read_text())
    pid = endpoint['pid']
    command = subprocess.check_output(['ps', '-p', str(pid), '-o', 'command='], text=True).strip()
    expected = f'{binary} --home {node.home} serve '
    assert command.startswith(expected), (pid, command, expected)
    os.kill(pid, signum)
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        try:
            with (node.home / 'daemon.lock').open('r+') as lock:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                return
        except BlockingIOError:
            time.sleep(.05)
    raise AssertionError('signaled fixture daemon retained its daemon lock')


def wait_for(predicate, message, seconds=15):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        result = predicate()
        if result:
            return result
        time.sleep(.05)
    raise AssertionError(message)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', required=True)
    args = parser.parse_args()
    binary = str(Path(args.arm).resolve())
    root = Path(tempfile.mkdtemp(prefix='arm-routed-forward-loss-')).resolve()
    root.chmod(0o700)
    a, b, c = [Node(binary, root, name) for name in ('relay-a', 'source-b', 'target-c')]
    nodes = [a, b, c]
    service = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Effect)
    service_thread = threading.Thread(target=service.serve_forever, daemon=True)
    service_thread.start()
    child = None
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

        created = a.cli('workspaces', 'create', '--name', 'Private TCP relay-loss acceptance',
                        '--key', 'create', '--reason', 'one effect before generic transport loss')
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
        assert all(peer['identity']['host'] != c.identity['host'] for peer in b.cli('mesh', 'peers'))
        generation = a.cli('workspaces', 'inspect', '--workspace', wid,
                           '--section', 'members', '--limit', '1')['workspace']['generation']
        a.cli('mesh', 'ssh-relay', 'grant-relay', '--workspace', wid,
              '--generation', generation, '--relay-cpu', '0.05', '--relay-memory', '8MiB',
              '--reason', 'generic bounded workspace transport', '--yes')
        c.cli('mesh', 'forward', 'grant', '--workspace', wid, '--source', b.identity['host'],
              '--service', 'effect', '--target-address', f'127.0.0.1:{service.server_port}',
              '--connections', '1', '--total-bytes', '64KiB',
              '--reason', 'one exact service effect before relay loss', '--yes')
        token_file = root / 'source-token'
        key = uuid.uuid4().hex
        started = b.cli('mesh', 'forward', 'start', '--workspace', wid,
                        '--target', c.identity['host'], '--service', 'effect', '--revision', '1',
                        '--key', key, '--token-file', str(token_file), '--ttl', '90s')
        assert started['state'] == 'active' and token_file.stat().st_mode & 0o777 == 0o600
        child = subprocess.Popen(b.prefix + ['mesh', 'forward', 'http', '--key', key,
                                             '--token-file', str(token_file), '--path', '/effect'],
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        assert Effect.seen.wait(30), 'destination did not record the original effect'
        assert child.poll() is None and Effect.calls == 1, 'client finished before route loss'
        signal_exact_fixture_daemon(a, binary, signal.SIGKILL)
        stdout, stderr = child.communicate(timeout=20)
        child = None
        assert stdout == b'' and b'remote_outcome_unknown' in stderr, (stdout, stderr)
        assert Effect.calls == 1, 'source silently replayed an effect after relay death'
        Effect.release.set()
        wait_for(lambda: b.cli('mesh', 'forward', 'status', '--key', key)['active'] == 0,
                 'original source connection did not join after relay death')
        wait_for(lambda: any(l['request'].get('key', '').startswith('tcp-final:') and
                             l['state'] in ('terminal', 'needs_attention')
                             for l in c.cli('status')['leases']),
                 'destination connection hold did not settle or retain uncertainty')
        # The relay process itself died without a join witness. Reopening its
        # private home must retain, never silently refund, its transport hold.
        a.start()
        a.stopped = False
        relay_holds = [l for l in a.cli('status')['leases'] if l['request'].get('kind') == 'relay-transit']
        assert relay_holds and all(l['state'] == 'needs_attention' and
                                   l['outcome'] == 'relay_supervision_lost' for l in relay_holds), relay_holds
        stopped = b.cli('mesh', 'forward', 'stop', '--key', key)
        assert stopped['state'] in ('terminal', 'needs_attention'), stopped
        if stopped['state'] == 'needs_attention':
            forward.reconcile_retained_sources(b, binary, [key])
        success = True
    finally:
        original_error = sys.exc_info()[0] is not None
        Effect.release.set()
        if child is not None:
            child.kill()
            child.communicate(timeout=5)
        service.shutdown()
        service.server_close()
        service_thread.join(5)
        failures = []
        for node in nodes:
            try:
                if not node.stop():
                    if node is a:
                        # The replacement relay intentionally retains an
                        # unresolved in-process charge; terminate only the
                        # exact private fixture process after its inspection.
                        signal_exact_fixture_daemon(a, binary, signal.SIGTERM)
                        a.stopped = True
                    else:
                        failures.append(f'{node.name} stop/ownership closure unconfirmed')
            except Exception as exc:
                failures.append(f'{node.name} stop failed: {exc}')
        if success and not original_error and not failures:
            shutil.rmtree(root)
        else:
            print('Retained routed forwarding loss fixture:', root, failures, file=sys.stderr)
        if not original_error and failures:
            raise RuntimeError('forwarding relay-loss acceptance cleanup unconfirmed')
    print('PASS public CLI B→A→C original service effect, abrupt relay death, unknown outcome without replay, and retained relay charge')


if __name__ == '__main__':
    main()
