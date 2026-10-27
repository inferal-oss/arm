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
mesh, ws = forward.mesh, forward.ws


class Effect(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'
    calls = 0
    seen = threading.Event()
    release = threading.Event()
    lock = threading.Lock()
    known_threads = None

    def do_GET(self):
        type(self).known_threads.add(threading.current_thread())
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


def fixture_daemon_lock_free(node):
    try:
        with (node.home / 'daemon.lock').open('r+') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            return True
    except (FileNotFoundError, BlockingIOError):
        return False


class OwnedNode(forward.Node):
    """Foreground daemon kept as this fixture's original, unreaped child.

    No endpoint PID, command-line inspection or saved process ID confers
    signal authority. Each new serve invocation gets a fresh child handle.
    The sole wait retires that handle before a replacement can be launched.
    """
    def __init__(self, binary, root, name, known_threads):
        super().__init__(binary, root, name)
        self.original = None
        self.births = 0
        self.owner_waits = []
        self.known_threads = known_threads

    def assert_exclusive_reaper(self):
        assert signal.getsignal(signal.SIGCHLD) == signal.SIG_DFL, 'foreign SIGCHLD reaper installed'
        assert all(thread in self.known_threads for thread in threading.enumerate()), \
            'unknown thread could reap an original fixture child'

    def start(self):
        assert self.original is None, 'original daemon handle still owned'
        self.assert_exclusive_reaper()
        self.home.mkdir(mode=0o700, exist_ok=True)
        log_fd = os.open(self.home / 'foreground.log', os.O_CREAT | os.O_WRONLY | os.O_APPEND, 0o600)
        with os.fdopen(log_fd, 'ab') as log:
            self.original = subprocess.Popen(self.prefix + ['serve', '--cpu', '1',
                                                             '--memory', '256MiB', '--disk', '1GiB'],
                                             stdin=subprocess.DEVNULL, stdout=log, stderr=log,
                                             close_fds=True, start_new_session=True)
        self.births += 1
        self.stopped = False
        deadline = time.monotonic() + 12
        while time.monotonic() < deadline:
            if self.result('status', timeout=2).returncode == 0:
                return
            time.sleep(.05)
        self.retire_original(signal.SIGTERM)
        raise AssertionError(f'{self.name} original foreground daemon did not become ready')

    def retire_original(self, signum=None):
        self.assert_exclusive_reaper()
        owner = self.original
        assert owner is not None, 'no original unreaped child may be signalled'
        self.original = None  # retire class-level signalling authority now
        # This exact Popen child has not been polled, waited, released or
        # reaped. Even if it exits before the signal, it retains its PID as a
        # zombie until the sole wait below; the PID cannot name a replacement.
        signal_error = None
        if signum is not None:
            try:
                os.kill(owner.pid, signum)
            except OSError as error:
                signal_error = error
        try:
            code = owner.wait(timeout=10)
        except subprocess.TimeoutExpired:
            # wait(timeout) did not reap this child. Escalate only its still
            # retained original handle, then perform the sole successful wait.
            os.kill(owner.pid, signal.SIGKILL)
            code = owner.wait(timeout=10)
        self.owner_waits.append((signum, code))
        assert signal_error is None, (self.name, 'original signal failed', signal_error, code)
        assert signum != signal.SIGKILL or code == -signal.SIGKILL, (self.name, code)
        deadline = time.monotonic() + 5
        while not fixture_daemon_lock_free(self) and time.monotonic() < deadline:
            time.sleep(.05)
        assert fixture_daemon_lock_free(self), f'{self.name} original daemon lock not released after wait'
        self.stopped = True
        return code

    def stop(self):
        if self.original is None:
            return self.stopped
        result = self.result('stop', '--force', timeout=20)
        if result.returncode != 0:
            return False
        self.retire_original()
        return not (self.home / 'endpoint.json').exists()


def wait_for(predicate, message, seconds=15):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        result = predicate()
        if result:
            return result
        time.sleep(.05)
    raise AssertionError(message)


def wait_for_original_effect(source, key, child, stderr_path):
    deadline = time.monotonic() + 45
    while time.monotonic() < deadline:
        if Effect.seen.wait(.2):
            return
        try:
            code = child.wait(timeout=.001)
        except subprocess.TimeoutExpired:
            continue
        diagnostic = source.result('mesh', 'forward', 'status', '--key', key, timeout=5)
        status = json.loads(diagnostic.stdout) if diagnostic.returncode == 0 else diagnostic.stderr[-4096:]
        raise AssertionError(('HTTP client exited before destination effect', code,
                              stderr_path.read_bytes()[-4096:], status))
    diagnostic = source.result('mesh', 'forward', 'status', '--key', key, timeout=5)
    status = json.loads(diagnostic.stdout) if diagnostic.returncode == 0 else diagnostic.stderr[-4096:]
    raise AssertionError(('destination effect not observed before deadline',
                          stderr_path.read_bytes()[-4096:], status))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', required=True)
    args = parser.parse_args()
    assert threading.active_count() == 1 and signal.getsignal(signal.SIGCHLD) == signal.SIG_DFL, \
        'fixture requires exclusive child reaping authority before launch'
    binary = str(Path(args.arm).resolve())
    root = Path(tempfile.mkdtemp(prefix='arm-routed-forward-loss-')).resolve()
    root.chmod(0o700)
    known_threads = {threading.current_thread()}
    a, b, c = [OwnedNode(binary, root, name, known_threads) for name in ('relay-a', 'source-b', 'target-c')]
    nodes = [a, b, c]
    service = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Effect)
    service_thread = threading.Thread(target=service.serve_forever, daemon=True)
    known_threads.add(service_thread)
    Effect.known_threads = known_threads
    service_thread.start()
    child = None
    child_output = None
    child_error = None
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
        child_output = os.fdopen(os.open(root / 'client.stdout', os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600), 'wb')
        child_error = os.fdopen(os.open(root / 'client.stderr', os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600), 'wb')
        child = subprocess.Popen(b.prefix + ['mesh', 'forward', 'http', '--key', key,
                                             '--token-file', str(token_file), '--path', '/effect'],
                                 stdin=subprocess.DEVNULL, stdout=child_output, stderr=child_error,
                                 close_fds=True)
        wait_for_original_effect(b, key, child, root / 'client.stderr')
        try:
            child.wait(timeout=.01)
            raise AssertionError('client finished before route loss')
        except subprocess.TimeoutExpired:
            pass
        assert Effect.calls == 1
        a.retire_original(signal.SIGKILL)
        assert a.births == 1 and a.owner_waits == [(signal.SIGKILL, -signal.SIGKILL)]
        client_exit = child.wait(timeout=20)
        child = None
        child_output.close()
        child_error.close()
        child_output = child_error = None
        stdout_path, stderr_path = root / 'client.stdout', root / 'client.stderr'
        assert stdout_path.stat().st_size <= 64 << 10 and stderr_path.stat().st_size <= 64 << 10
        stdout, stderr = stdout_path.read_bytes(), stderr_path.read_bytes()
        assert client_exit != 0 and stdout == b'' and b'remote_outcome_unknown' in stderr, (client_exit, stdout, stderr)
        assert Effect.calls == 1, 'source silently replayed an effect after relay death'
        Effect.release.set()
        wait_for(lambda: b.cli('mesh', 'forward', 'status', '--key', key)['active_connections'] == 0,
                 'original source connection did not join after relay death')
        wait_for(lambda: any(l['request'].get('idempotency_key', '').startswith('tcp-final:') and
                             l['state'] in ('terminal', 'needs_attention')
                             for l in c.cli('status')['leases']),
                 'destination connection hold did not settle or retain uncertainty')
        # The relay process itself died without a join witness. Reopening its
        # private home must retain, never silently refund, its transport hold.
        a.start()
        assert a.births == 2 and a.original is not None, 'replacement did not mint a new child handle'
        relay_holds = [l for l in a.cli('status')['leases'] if l['request'].get('kind') == 'relay-transit']
        assert relay_holds and all(l['state'] == 'needs_attention' and
                                   l['outcome'] == 'relay_supervision_lost' for l in relay_holds), relay_holds
        stopped = b.cli('mesh', 'forward', 'stop', '--key', key)
        assert stopped['state'] in ('terminal', 'needs_attention'), stopped
        if stopped['state'] == 'needs_attention':
            assert b.result('stop', '--force', timeout=20).returncode != 0
            b.retire_original(signal.SIGTERM)
            b.start()
            deadline = time.monotonic() + 90
            while True:
                recovered = b.result('mesh', 'forward', 'recover', '--key', key,
                                     '--reason', 'reconcile original private fixture socket', '--yes')
                if recovered.returncode == 0:
                    assert json.loads(recovered.stdout)['outcome'] == 'reconciled'
                    break
                error = json.loads(recovered.stderr)['error']
                assert error['code'] == 'needs_attention' and time.monotonic() < deadline, error
                time.sleep(1)
        success = True
    finally:
        original_error = sys.exc_info()[0] is not None
        Effect.release.set()
        if child is not None:
            try:
                child.wait(timeout=.1)
            except subprocess.TimeoutExpired:
                os.kill(child.pid, signal.SIGKILL)  # same retained, unreaped child
                child.wait(timeout=5)
        if child_output is not None:
            child_output.close()
        if child_error is not None:
            child_error.close()
        service.shutdown()
        service.server_close()
        service_thread.join(5)
        failures = []
        for node in nodes:
            try:
                if not node.stop():
                    # An unresolved Manager hold may refuse public Stop. The
                    # original foreground child is still owned and unreaped.
                    # Retire only that handle; no saved PID becomes authority.
                    if node.original is not None:
                        node.retire_original(signal.SIGTERM)
                    else:
                        failures.append(f'{node.name} original owner unavailable')
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
