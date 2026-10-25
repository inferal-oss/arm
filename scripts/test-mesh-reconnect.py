#!/usr/bin/env python3
"""Actual public reconnect against a disposable fixed-command userspace SSH server.

Run this same pinned script/binaries natively and on Linux; neither run is a
cross-host-network claim. Existing verified native->Linux SSH is a separate gate.
No real keys, authorized_keys, host SSH service/config or user ARM home are edited.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shlex
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('reviewed_mesh_wire', ROOT/'scripts/test-mesh-operator.py')
wire = importlib.util.module_from_spec(spec)
spec.loader.exec_module(wire)
process_spec = importlib.util.spec_from_file_location('owned_reconnect_process', ROOT/'scripts/mesh_reconnect_fixture_process.py')
owned_process = importlib.util.module_from_spec(process_spec)
process_spec.loader.exec_module(owned_process)
evidence_spec = importlib.util.spec_from_file_location('owned_reconnect_evidence', ROOT/'scripts/mesh_reconnect_fixture_evidence.py')
evidence = importlib.util.module_from_spec(evidence_spec)
evidence_spec.loader.exec_module(evidence)
terminal_spec = importlib.util.spec_from_file_location('owned_terminal_state', ROOT/'scripts/mesh_reconnect_terminal_state.py')
terminal_state = importlib.util.module_from_spec(terminal_spec)
terminal_spec.loader.exec_module(terminal_state)
TERMINAL_CLOSE_SECONDS = 10


def sha(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as stream:
        total = 0
        while chunk := stream.read(1 << 20):
            total += len(chunk)
            if total > 512 << 20:
                raise ValueError('fixture artifact exceeds 512 MiB')
            h.update(chunk)
    return h.hexdigest()


def exclusive_json(path, value):
    raw = (json.dumps(value, indent=2) + '\n').encode()
    with Path(path).open('xb') as stream:
        os.chmod(path, 0o600)
        stream.write(raw)
        stream.flush()
        os.fsync(stream.fileno())


def inventory(root):
    """Closed native identity+hash roster of this newly created private fixture."""
    rows = []
    total = 0
    for path in [root, *sorted(root.rglob('*'))]:
        info = path.lstat()
        if info.st_uid != os.getuid() or stat.S_ISLNK(info.st_mode):
            raise ValueError('foreign/symlink fixture entry preserved: ' + str(path))
        if not (stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode)):
            raise ValueError('unsettled native fixture entry preserved: ' + str(path))
        if len(rows) >= 10000:
            raise ValueError('fixture roster exceeds bound')
        total += info.st_size if stat.S_ISREG(info.st_mode) else 0
        if total > 1 << 30:
            raise ValueError('fixture cleanup bytes exceed bound')
        rows.append({'path': str(path.relative_to(root)), 'device': info.st_dev,
                     'inode': info.st_ino, 'uid': info.st_uid, 'mode': info.st_mode,
                     'size': info.st_size if stat.S_ISREG(info.st_mode) else None,
                     'sha256': sha(path) if stat.S_ISREG(info.st_mode) else None})
    return rows


def remove_exact(root, rows):
    if inventory(root) != rows:
        raise ValueError('fixture identity/hash/roster changed; preserved')
    for row in reversed(rows):
        path = root/row['path']
        info = path.lstat()
        if (info.st_dev, info.st_ino, info.st_uid, info.st_mode) != (row['device'], row['inode'], row['uid'], row['mode']):
            raise ValueError('fixture entry identity changed; preserved')
        if row['sha256'] is None:
            path.rmdir()
        else:
            if sha(path) != row['sha256']:
                raise ValueError('fixture bytes changed; preserved')
            path.unlink()


FIXTURE_IDENTITY_FIELDS = frozenset(('owner', 'owner_username', 'host', 'name',
    'address', 'certificate', 'spki_sha256', 'fingerprint', 'expires_at'))


def stable_reconnect_identity(before, after, actual_listen):
    """Fixture oracle: a managed listener may rebind; its principal must not.

    The exact private fixture keeps every other field (including attribution).
    Closed schema and actual config.listen binding prevent ignoring unrelated
    identity changes or accepting an unreviewed endpoint as a mere port update.
    This compares saved facts, not certificate verification or native effects.
    """
    for identity in (before, after):
        if type(identity) is not dict or set(identity) != FIXTURE_IDENTITY_FIELDS:
            raise AssertionError('exact closed fixture identity schema required')
        if any(type(value) is not str for value in identity.values()):
            raise AssertionError('fixture identity fields must remain exact strings')
    for field in FIXTURE_IDENTITY_FIELDS - {'address'}:
        if before[field] != after[field]:
            raise AssertionError('stopped destination identity field changed: '+field)
    for address in (before['address'], after['address'], actual_listen):
        if type(address) is not str or len(address) > 21:
            raise AssertionError('exact private fixture listener required')
        match = re.fullmatch(r'127\.0\.0\.1:([1-9][0-9]{0,4})', address)
        if match is None or int(match.group(1)) > 65535:
            raise AssertionError('exact private fixture loopback/numeric listener required')
    if after['address'] != actual_listen:
        raise AssertionError('destination identity endpoint differs from actual config.listen')
    principal = {key: after[key] for key in sorted(FIXTURE_IDENTITY_FIELDS - {'address'})}
    return {'before_endpoint': before['address'], 'after_endpoint': after['address'],
            'actual_listen': actual_listen,
            'unchanged_fields': sorted(principal),
            'principal_sha256': hashlib.sha256(json.dumps(principal, sort_keys=True,
                separators=(',', ':')).encode()).hexdigest()}


def reconnect_pair_id(key, source, destination):
    if (type(key) is not str or not 1 <= len(key.encode('utf-8')) <= 128 or
            any(ord(c) < 32 or ord(c) == 127 for c in key) or
            any(type(h) is not str or re.fullmatch('[0-9a-f]{32}', h) is None
                for h in (source, destination)) or source == destination):
        raise ValueError('exact bounded key and distinct stable host IDs required')
    return hashlib.sha256(('arm-connect:'+key+':'+source+':'+destination).encode()).hexdigest()


def pair_entries(events, pair):
    if type(pair) is not str or re.fullmatch('[0-9a-f]{64}', pair) is None:
        raise ValueError('exact Pair required')
    if type(events) is not list or len(events) > 64:
        raise ValueError('bounded exact event roster required')
    previous = 0
    for row in events:
        previous = evidence.validate_event_row(row, previous)
    # Activation legitimately carries this Pair too. Native entry counts select
    # only Pair, while a fault label under another action must never be hidden.
    selected = [row for row in events if row.get('pair') == pair]
    if any('fault' in row and row['action'] != 'pair' for row in selected):
        raise AssertionError('selected Pair fault appeared under an unexpected action')
    return [row for row in selected if row['action'] == 'pair']


def committed_pair_lost_ack(events, pair):
    rows = pair_entries(events, pair)
    if len(rows) != 1:
        raise AssertionError('exactly one native Pair before explicit recovery required')
    row = rows[0]
    if (row.get('fault') != 'after' or row['child_started'] is not True or
            row['child_wait_failed'] is not False or row['child_exit'] != 0):
        raise AssertionError('actual committed Pair with discarded response required')
    return dict(row)


def recovered_pair_entries(events, pair, original):
    rows = pair_entries(events, pair)
    if len(rows) != 2 or rows[0] != original:
        raise AssertionError('original remote Pair and exactly one explicit recovery Pair required')
    row = rows[1]
    if ('fault' in row or row['sequence'] <= original['sequence'] or
            row['child_started'] is not True or row['child_wait_failed'] is not False or
            row['child_exit'] != 0):
        raise AssertionError('exact positively joined recovery Pair required')
    return [dict(item) for item in rows]


class Fixture:
    def __init__(self, args):
        self.args = args
        self.root = Path(tempfile.mkdtemp(prefix='arm-reconnect-')).resolve()
        os.chmod(self.root, 0o700)
        self.homes = [self.root/'source', self.root/'destination']
        self.image = self.root/'arm'
        self.server_image = self.root/'fixture-sshd'
        self.server = None
        self.server_log = None
        self.calls = []
        self.checks = []
        self.evidence = {}
        self.env = dict(os.environ)
        self.env.pop('SSH_AUTH_SOCK', None)
        self.endpoint = self.origin = self.token = None
        self.backend_pids = set()
        self.shutdown_evidence = evidence.BackendShutdownEvidence()
        self.terminal_selection = None

    def observe_backend_endpoints(self):
        if not hasattr(self, 'backend_pids'):
            self.backend_pids = set()
        if not hasattr(self, 'shutdown_evidence'):
            self.shutdown_evidence = evidence.BackendShutdownEvidence()
        if not hasattr(self, 'evidence'):
            self.evidence = {}
        rows = self.evidence.setdefault('owned-backend-endpoints', [])
        for home in self.homes:
            raw = evidence.read_fixture_endpoint(home)
            if raw is None:
                continue
            row = self.shutdown_evidence.observe(home, raw)
            self.backend_pids.add(row['pid'])
            if len(self.backend_pids) > 16:
                raise ValueError('owned backend process roster exceeds bound')
            if row not in rows:
                rows.append(row)

    def stop_backend(self, home, timeout=120):
        self.observe_backend_endpoints()
        raw = evidence.read_fixture_endpoint(home)
        if raw is None:
            raise ValueError('cannot issue stop without the exact current endpoint; inspect positive shutdown evidence')
        before = self.shutdown_evidence.observe(home, raw)
        result = self.command([self.image, '--home', home, '--json', 'stop', '--force'],
            check=False, timeout=timeout)
        after = evidence.read_fixture_endpoint(home)
        record = self.shutdown_evidence.record_stop(home, before, result.returncode,
            result.stdout, after)
        self.evidence.setdefault('positive-backend-shutdowns', []).append(record)
        return record

    def command(self, argv, stdin=None, check=True, timeout=120):
        self.observe_backend_endpoints()
        try:
            result, receipt = owned_process.bounded_command(argv, stdin=stdin, env=self.env, timeout=timeout)
        except owned_process.CommandFailure as error:
            self.calls.append(dict(error.receipt,
                stdout_sha256=hashlib.sha256(error.stdout.encode()).hexdigest(),
                stderr_sha256=hashlib.sha256(error.stderr.encode()).hexdigest()))
            raise
        self.calls.append(dict(receipt,
            stdout_sha256=hashlib.sha256(result.stdout.encode()).hexdigest(),
            stderr_sha256=hashlib.sha256(result.stderr.encode()).hexdigest()))
        self.observe_backend_endpoints()
        if check and result.returncode:
            raise AssertionError((argv, result.returncode, result.stderr[-8192:]))
        return result

    def cli(self, *args, check=True):
        result = self.command([self.image, '--home', self.homes[0], '--json', *args], check=check)
        return json.loads(result.stdout) if check else result

    def config(self, side):
        return json.loads((self.homes[side]/'mesh/config.json').read_bytes())

    def peer(self, side):
        peers = self.config(side)['peers']
        assert len(peers) == 1, peers
        return peers[0]

    def facts(self, label):
        paths = []
        for home in self.homes:
            paths.extend([home/'mesh/config.json', *sorted((home/'mesh-lifecycle').glob('*.json'))])
        snapshot = {str(p.relative_to(self.root)): {'sha256': sha(p), 'json': json.loads(p.read_bytes())}
                    for p in paths if p.is_file()}
        self.evidence[label] = snapshot
        return snapshot

    def setup(self):
        for original, dest in ((self.args.arm, self.image), (self.args.fixture_sshd, self.server_image)):
            shutil.copyfile(original, dest)
            os.chmod(dest, 0o700)
            assert sha(original) == sha(dest)
        for name in ('client', 'server'):
            self.command(['/usr/bin/ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', self.root/name])
            os.chmod(self.root/(name+'.pub'), 0o600)
        self.server_log = (self.root/'server.log').open('xb')
        self.server = subprocess.Popen([str(self.server_image), '--arm', str(self.image), '--home', str(self.homes[1]),
            '--host-key', str(self.root/'server'), '--client-public-key', str(self.root/'client.pub'),
            '--ready', str(self.root/'ready.json'), '--events', str(self.root/'events.jsonl'),
            '--fault-control', str(self.root/'fault.json')], stdout=self.server_log, stderr=subprocess.STDOUT,
            start_new_session=True, env=self.env)
        deadline = time.monotonic()+10
        while not (self.root/'ready.json').exists():
            assert self.server.poll() is None, 'fixture server exited'
            assert time.monotonic() < deadline, 'bounded fixture readiness timeout'
            time.sleep(.02)
        ready = json.loads((self.root/'ready.json').read_bytes())
        assert ready['pid'] == self.server.pid and ready['arm_sha256'] == sha(self.image)
        host, port = ready['address'].split(':')
        assert host == '127.0.0.1'
        known = self.root/'known_hosts'
        known.write_text(f'[{host}]:{port} {ready["server_key"]}\n')
        os.chmod(known, 0o600)
        config = self.root/'ssh_config'
        config.write_text(f'Host arm-reconnect-fixture\n HostName {host}\n Port {port}\n User {ready["account"]}\n'
            f' IdentityFile {self.root}/client\n IdentitiesOnly yes\n UserKnownHostsFile {known}\n'
            ' GlobalKnownHostsFile /dev/null\n StrictHostKeyChecking yes\n BatchMode yes\n'
            ' ForwardAgent no\n ClearAllForwardings yes\n ControlMaster no\n ControlPath none\n')
        os.chmod(config, 0o600)
        launchers = self.root/'bin'
        launchers.mkdir(mode=0o700)
        launcher = launchers/'ssh'
        launcher.write_text('#!/bin/sh\nexec /usr/bin/ssh -F '+shlex.quote(str(config))+' "$@"\n')
        os.chmod(launcher, 0o700)
        self.env['PATH'] = str(launchers)+os.pathsep+self.env.get('PATH', '')
        self.cli('start', '--cpu', '2', '--memory', '128MiB', '--disk', '128MiB')

    def browser(self):
        link = urllib.parse.urlsplit(self.cli('dashboard')['url'])
        self.origin = urllib.parse.urlunsplit((link.scheme, link.netloc, '', '', ''))
        ticket = urllib.parse.parse_qs(link.fragment)['connect'][0]
        request = urllib.request.Request(self.origin+'/v1/browser/session',
            data=json.dumps({'ticket': ticket}).encode(), headers={'Content-Type': 'application/json', 'Origin': self.origin})
        with urllib.request.urlopen(request, timeout=5) as response:
            self.token = json.load(response)['token']
        self.endpoint = json.loads((self.homes[0]/'endpoint.json').read_bytes())['url']

    def rpc(self, operation, params, error=None):
        channel = wire.Wire(self.endpoint, self.origin, self.token)
        key = 'reconnect-fixture-rpc'
        try:
            channel.send(json.dumps({'version': 1, 'id': key, 'operation': operation, 'params': params}).encode())
            response = channel.response()
            assert response['id'] == key
            if error:
                assert response.get('error', {}).get('code') == error, response
                return response['error']
            assert 'error' not in response, response
            return response['result']
        finally:
            channel.close()

    def run(self):
        if getattr(self.args, 'scenario', 'revoke-lost-ack') == 'pair-lost-ack':
            return self.run_pair_lost_ack()
        connected = self.cli('mesh', 'connect', 'arm-reconnect-fixture', '--binary', self.image,
            '--remote-home', self.homes[1], '--grant', 'observe-host,message', '--key', 'initial',
            '--reason', 'private old broader enrollment', '--yes')
        host = connected['connected']
        assert 'message' in self.peer(1)['grant']['permissions']
        self.cli('mesh', 'disconnect', host, '--key', 'normal-disconnect', '--reason', 'private retirement', '--yes')
        assert self.peer(0)['state'] == self.peer(1)['state'] == 'revoked'
        old_pair = self.peer(0)['pair']
        old_identity = self.config(1)['identity']
        self.stop_backend(self.homes[1])
        before = self.facts('before-reconnect-dry')
        self.cli('mesh', 'reconnect', host, '--key', 'first-reconnect', '--reason', 'fresh observe only', '--dry-run')
        assert self.facts('after-reconnect-dry') == before, 'dry run mutated durable trust/intent'
        no_yes = self.cli('mesh', 'reconnect', host, '--key', 'first-reconnect', '--reason', 'fresh observe only', check=False)
        assert no_yes.returncode and 'confirmation_required' in no_yes.stderr+no_yes.stdout
        assert self.facts('after-default-decline') == before
        new = self.cli('mesh', 'reconnect', host, '--key', 'first-reconnect', '--reason', 'fresh observe only', '--yes')
        assert new['connected'] == host
        destination = self.config(1)
        self.evidence['stopped-destination-stable-identity'] = stable_reconnect_identity(
            old_identity, destination['identity'], destination['listen'])
        assert self.peer(0)['pair'] == self.peer(1)['pair'] != old_pair
        assert self.peer(1)['grant']['permissions'] == ['observe-host']
        assert self.peer(0)['grant']['permissions'] == []
        self.cli('status', '--host', host)
        self.checks.append('real fixed-SSH connect/disconnect/stopped-destination reconnect; same identity, fresh permissions; dry/default refusal')
        self.browser()

        # Commit remote revocation through the real ARM helper, then deliberately
        # discard its response. This is not a synthetic claimed native effect.
        exclusive_json(self.root/'fault.json', {'key': 'lost-disconnect', 'stage': 'after'})
        pending = self.cli('mesh', 'disconnect', host, '--key', 'lost-disconnect', '--reason', 'lost destination acknowledgement', '--yes')
        assert pending['remote_pending'] is True
        operation = self.cli('mesh', 'operations', 'lost-disconnect')
        assert operation['phase'] == 'incomplete'
        assert self.peer(0)['state'] == self.peer(1)['state'] == 'revoked'
        remote_receipts = self.config(1).get('disconnect_receipts', [])
        assert any(row.get('Key') == 'lost-disconnect' for row in remote_receipts), remote_receipts
        events = evidence.read_fixture_events(self.root/'events.jsonl')
        self.evidence['committed-revoke-lost-ack-event'] = evidence.committed_revoke_fault(events, 'lost-disconnect')
        old_history = self.facts('lost-ack-before-handoff')
        context = self.rpc('mesh_operator_reconnect_context', {'host': host})
        assert any(row['key'] == 'lost-disconnect' and row['phase'] == 'incomplete' for row in context['blockers'])
        blocked = self.cli('mesh', 'reconnect', host, '--key', 'blocked', '--yes', check=False)
        assert blocked.returncode and 'lost-disconnect' in blocked.stderr+blocked.stdout
        assert self.facts('blocked-reconnect') == old_history
        ui_result = None
        if self.args.interactive != 'none':
            # A reviewed controller drives the real Web or compiled terminal UI.
            # The controller supplies exact recorded keys, never a latest-by-host guess.
            link = self.cli('dashboard')['url']
            if self.args.interactive == 'terminal':
                # Native CLI summary supplies the concrete recovery fingerprint;
                # the file binder also checks its exact raw coordinator revision.
                exclusive_json(self.root/'terminal-reviewed-operation.json', operation)
                self.terminal_selection = terminal_state.reserve(self.root,
                    sha(self.homes[0]/'endpoint.json'), sha(self.image), operation)
                exclusive_json(self.root/'terminal-reservation.json', self.terminal_selection)
            exclusive_json(self.root/'ui-ready.json', {'mode': self.args.interactive,
                'dashboard_url': link, 'source_home': str(self.homes[0]), 'arm': str(self.image),
                'host': host, 'pending_key': operation['key'], 'outcome_file': str(self.root/'ui-outcome.json'),
                'required': 'inspect blocker; explicit reviewed handoff; decline one exact reconnect review; approve a fresh exact reconnect'})
            print('Interactive fixture ready:', self.root/'ui-ready.json', flush=True)
            deadline = time.monotonic()+420
            while not (self.root/'ui-outcome.json').exists():
                assert self.server.poll() is None, 'interactive fixture server exited'
                assert time.monotonic() < deadline, 'bounded interactive review window expired'
                time.sleep(.1)
            raw = (self.root/'ui-outcome.json').read_bytes()
            assert len(raw) <= 4096
            ui_result = json.loads(raw)
            assert set(ui_result) == {'handoff_key', 'declined_key', 'reconnect_key'}
            assert ui_result['handoff_key'] == operation['key']
            handed = self.cli('mesh', 'operations', operation['key'])
            declined = self.cli('mesh', 'operations', ui_result['declined_key'])
            completed = self.cli('mesh', 'operations', ui_result['reconnect_key'])
            assert handed['phase'] == 'handed_off'
            assert declined['kind'] == 'reconnect' and declined['phase'] == 'rejected' and not declined['approved']
            assert completed['kind'] == 'reconnect' and completed['phase'] == 'completed'
            assert completed['result']['connected'] == host
            self.checks.append('actual interactive '+self.args.interactive+' blocker/reviewed handoff/decline/fresh approval, bound exact keys')
        else:
            self.rpc('mesh_operator_handoff', {'key': operation['key'], 'revision': operation['revision'],
                'review_sha256': operation['recovery_sha256'], 'reason': 'explicit private handoff; original native obligations retained'})
        after = self.facts('after-handoff')
        for name, row in old_history.items():
            if '/mesh-lifecycle/operation-' in name:
                assert after[name] == row, 'handoff changed original native lifecycle evidence'
        previous = next(row['json']['peers'][0] for name, row in old_history.items() if name.endswith('destination/mesh/config.json'))
        completed_key = ui_result['reconnect_key'] if ui_result else 'after-handoff'
        if ui_result is None:
            self.cli('mesh', 'reconnect', host, '--key', completed_key, '--reason', 'new separately reviewed enrollment', '--yes')
        replacement = self.peer(1)
        assert replacement['pair'] != previous['pair'] and replacement['state'] == 'active'
        saved = next(row for row in remote_receipts if row.get('Key') == 'lost-disconnect')
        for exact in (True, False):
            request = {'Home': str(self.homes[1]), 'Action': 'lifecycle_revoke', 'Revoke': {
                'Key': saved['Key'] if exact else 'delayed-other-key', 'Host': saved['Host'], 'Pair': saved['Pair'],
                'Fingerprint': saved['Fingerprint'], 'Revision': saved['Revision'], 'Reason': saved['Reason']}}
            result = self.command(['ssh', '--', 'arm-reconnect-fixture', self.image, 'mesh', 'bootstrap'],
                stdin=json.dumps(request), check=exact)
            if not exact:
                assert result.returncode != 0, 'old tuple under a new key matched replacement'
            assert self.peer(1) == replacement, 'delayed old cleanup changed replacement'
        self.checks.append('actual lost revoke ACK; exact blocker; explicit browser-authorized handoff; delayed old tuple preserved replacement')
        self.facts('completed')
        # Once both private backends are stopped, the saved key must still return
        # its receipt even without current source registry, with no SSH dispatch.
        self.stop_backend(self.homes[0])
        self.stop_backend(self.homes[1])
        config = self.homes[0]/'mesh/config.json'
        config.rename(config.with_name('fixture-retained-config.json'))
        before_lookup_events = evidence.read_fixture_events(self.root/'events.jsonl')
        receipt = self.cli('mesh', 'reconnect', host, '--key', completed_key)
        assert receipt['kind'] == 'reconnect' and receipt['phase'] == 'completed'
        assert evidence.read_fixture_events(self.root/'events.jsonl') == before_lookup_events, 'receipt lookup changed native event count/order/content'
        assert not config.exists()
        self.checks.append('saved-key lookup with stopped backends/removed current registry; no native redispatch')

    def run_pair_lost_ack(self):
        # This is a distinct fresh fixture; no key or home from a failed gate is reused.
        connected = self.cli('mesh', 'connect', 'arm-reconnect-fixture', '--binary', self.image,
            '--remote-home', self.homes[1], '--grant', 'observe-host,message', '--key', 'pair-initial',
            '--reason', 'private initial enrollment for Pair response-loss gate', '--yes')
        host = connected['connected']
        source = self.config(0)['identity']['host']
        self.cli('mesh', 'disconnect', host, '--key', 'pair-retire', '--reason', 'private prior retirement', '--yes')
        assert self.peer(0)['state'] == self.peer(1)['state'] == 'revoked'
        prior_source, prior_target = self.peer(0), self.peer(1)
        key = 'pair-response-lost'
        pair = reconnect_pair_id(key, source, host)
        assert pair != prior_source['pair']
        self.evidence['pair-expected-enrollment'] = {'source': source, 'destination': host, 'key': key, 'pair': pair}
        self.facts('pair-before-dispatch')
        # Fixture executes actual pinned ARM Pair, waits successfully, discards only
        # that selected response. The event alone is backed by real pending peer state.
        exclusive_json(self.root/'fault.json', {'pair': pair, 'stage': 'after'})
        lost = self.cli('mesh', 'reconnect', host, '--key', key,
            '--reason', 'private exact Pair response-loss observation', '--yes', check=False)
        assert lost.returncode != 0, 'lost Pair response was falsely reported completed'
        operation = self.cli('mesh', 'operations', key)
        assert operation['key'] == key and operation['kind'] == 'reconnect'
        assert operation['phase'] == 'incomplete' and operation['approved'] is True
        assert not operation['worker_live'] and not operation.get('resolved_by')
        target_after_drop = self.peer(1)
        assert target_after_drop['pair'] == pair and target_after_drop['state'] == 'pending'
        assert target_after_drop['grant']['permissions'] == ['observe-host']
        assert self.peer(0) == prior_source, 'local Pair ran before remote response was known'
        first = committed_pair_lost_ack(evidence.read_fixture_events(self.root/'events.jsonl'), pair)
        self.evidence['pair-actual-lost-ack-event'] = first
        self.evidence['pair-original-unknown-operation'] = operation
        unknown = self.facts('pair-unknown-before-offline-retry')
        before_retry = evidence.read_fixture_events(self.root/'events.jsonl')
        retry = self.cli('mesh', 'reconnect', host, '--key', key, '--yes')
        assert retry['key'] == key and retry['phase'] == 'incomplete'
        assert self.facts('pair-after-offline-retry') == unknown
        assert evidence.read_fixture_events(self.root/'events.jsonl') == before_retry
        self.checks.append('actual remote Pair committed/pending then response lost; exact ordinary retry reads only with zero native redispatch')

        # Every recovery goes through its own normal plan and explicit approval.
        # Read-only SSH inspections may occur here; no new Pair, grant or intent may.
        self.cli('mesh', 'reconnect', host, '--key', key, '--resume', '--dry-run')
        assert self.facts('pair-after-resume-dry') == unknown
        declined = self.cli('mesh', 'reconnect', host, '--key', key, '--resume', check=False)
        assert declined.returncode != 0 and 'confirmation_required' in declined.stdout+declined.stderr
        assert self.facts('pair-after-resume-default-no') == unknown
        assert pair_entries(evidence.read_fixture_events(self.root/'events.jsonl'), pair) == [first]
        recovered = self.cli('mesh', 'reconnect', host, '--key', key, '--resume', '--yes')
        assert recovered['connected'] == host and recovered['pair'] == pair
        root = self.cli('mesh', 'operations', key)
        child_key = root.get('resolved_by')
        assert type(child_key) is str and child_key and child_key != key
        child = self.cli('mesh', 'operations', child_key)
        assert root['phase'] == 'incomplete' and root['approved'] is True
        assert child['key'] == child_key and child['parent_key'] == key and child['resume_from'] == key
        assert child['kind'] == 'reconnect' and child['phase'] == 'completed' and child['approved'] is True
        assert child['has_review'] is True and child['review_revision'] > 0 and child.get('review')
        assert child['result']['pair'] == pair and child['result']['connected'] == host
        assert self.peer(0)['pair'] == self.peer(1)['pair'] == pair
        assert self.peer(0)['state'] == self.peer(1)['state'] == 'active'
        assert self.peer(0)['grant']['permissions'] == []
        assert self.peer(1)['grant']['permissions'] == ['observe-host']
        self.evidence['pair-actual-remote-entries'] = recovered_pair_entries(
            evidence.read_fixture_events(self.root/'events.jsonl'), pair, first)
        self.evidence['pair-original-and-child-receipts'] = {'original': root, 'child': child}
        self.facts('pair-recovery-completed')
        self.checks.append('explicit recovery reviewed independently; same Pair, exact original/child link, two actual remote Pair entries, active exact grants')
        # Snapshot distinguishes local committed state from native-entry evidence:
        # this public fixture observes remote Pair entry, not the local call entry.
        self.stop_backend(self.homes[0])
        self.stop_backend(self.homes[1])
        config = self.homes[0]/'mesh/config.json'
        config.rename(config.with_name('fixture-retained-config.json'))
        events = evidence.read_fixture_events(self.root/'events.jsonl')
        receipt = self.cli('mesh', 'reconnect', host, '--key', key)
        assert receipt['key'] == key and receipt['phase'] == 'incomplete' and receipt['resolved_by'] == child_key
        assert evidence.read_fixture_events(self.root/'events.jsonl') == events
        assert not config.exists()
        self.checks.append('original unknown receipt retains exact completed child link offline with stopped backends/removed registry; no native redispatch')

    def cleanup(self):
        errors = []
        if getattr(self, 'terminal_selection', None) is not None:
            try:
                closed = terminal_state.close_and_wait(self.terminal_selection, TERMINAL_CLOSE_SECONDS)
                self.evidence['terminal-child-closure'] = closed
            except BaseException as error:
                # No fixture server/backend stop or reclamation while a TUI
                # launch/Wait/drain/terminal restoration remains unknown.
                return ['terminal closure unknown; all fixture obligations retained: '+repr(error)]
        try:
            self.observe_backend_endpoints()
        except BaseException as error:
            errors.append('owned endpoint observation: '+repr(error))
        # Stop native admissions and positively join the exact owned server
        # before stopping backends: a late bootstrap could otherwise restart one.
        server_joined = self.server is None
        if self.server is not None:
            try:
                if self.server.poll() is None:
                    self.server.terminate()
                exit_code = self.server.wait(timeout=40)
                if exit_code != 0:
                    errors.append('owned fixture graceful shutdown unconfirmed: native exit '+str(exit_code))
                else:
                    server_joined = True
            except BaseException as error:
                errors.append('owned fixture server shutdown unconfirmed: '+repr(error))
        pending_reuse = []
        if server_joined:
            for home in self.homes:
                try:
                    current = evidence.read_fixture_endpoint(home)
                    candidate = self.shutdown_evidence.pending_reuse(home, current)
                    if candidate is None:
                        self.stop_backend(home, timeout=15)
                    else:
                        # Exact positive stop evidence is pending, not a claim
                        # that the daemon has finished exiting. Observe all
                        # saved PIDs in the one shared bounded phase below.
                        pending_reuse.append(home)
                except BaseException as error:
                    errors.append('owned backend stop/positive closure unknown: '+repr(error))
        else:
            errors.append('backend shutdown deferred: fixture may still admit/bootstrap; private state retained')
        if server_joined:
            for home in self.homes:
                if (Path(home)/'endpoint.json').exists():
                    errors.append('owned backend endpoint remains: '+str(home))
            # Observation only; saved PIDs are never signalled. A reused/still
            # present PID is conservative unknown, not permission to adopt it.
            deadline = time.monotonic()+2
            pending = set(getattr(self, 'backend_pids', set()))
            observed_absent = set()
            while pending:
                for pid in list(pending):
                    try:
                        os.kill(pid, 0)
                    except ProcessLookupError:
                        pending.remove(pid)
                        observed_absent.add(pid)
                    except OSError as error:
                        errors.append('owned backend absence observation: '+repr(error))
                        pending.remove(pid)
                if not pending or time.monotonic() >= deadline:
                    break
                time.sleep(.02)
            if pending:
                errors.append('owned backend PID absence unconfirmed: '+repr(sorted(pending)))
            for home in pending_reuse:
                try:
                    # Re-read the endpoint after the observation window. A
                    # current/replacement endpoint invalidates the old receipt;
                    # it is not stopped again after this cleanup phase.
                    current = evidence.read_fixture_endpoint(home)
                    reused = self.shutdown_evidence.reuse(home, current,
                        lambda pid: pid in observed_absent)
                    if reused is None:
                        raise ValueError('endpoint appeared during shutdown observation; exact private state retained')
                    self.evidence.setdefault('verified-shutdown-reuse', []).append(reused)
                except BaseException as error:
                    errors.append('owned backend final shutdown reuse unknown: '+repr(error))
        if self.server_log is not None:
            self.server_log.close()
        return errors


def replace_owned_report(path, value, identity, previous_hash):
    info = path.lstat()
    if (info.st_dev, info.st_ino) != identity or sha(path) != previous_hash:
        raise ValueError('owned report changed; preserved')
    stage = path.with_name(path.name+'.stage-'+uuid.uuid4().hex)
    exclusive_json(stage, value)
    current = path.lstat()
    if (current.st_dev, current.st_ino) != identity or sha(path) != previous_hash:
        raise ValueError('owned report changed before publication; private stage retained')
    os.replace(stage, path)


def execute(args):
    fixture = Fixture(args)
    failure = None
    report = {'schema': 'arm.mesh-reconnect-live/v1', 'platform': platform.platform(),
              'root': str(fixture.root), 'uid': os.getuid(), 'checks': fixture.checks,
              'inputs': {}, 'passed': False, 'fixture_removed': False}
    old_alarm = signal.getsignal(signal.SIGALRM)
    if signal.getitimer(signal.ITIMER_REAL)[0]:
        raise ValueError('dedicated fixture process required; existing alarm is not replaced')
    def operation_expired(*_):
        raise TimeoutError('fixture operation deadline; inspect retained receipts, never replay')
    signal.signal(signal.SIGALRM, operation_expired)
    signal.setitimer(signal.ITIMER_REAL, getattr(args, 'operation_timeout', 180))
    report['operation_timeout_seconds'] = getattr(args, 'operation_timeout', 180)
    if getattr(args, 'interactive', 'none') == 'terminal':
        report['terminal_close_seconds_extra'] = TERMINAL_CLOSE_SECONDS
    try:
        report['inputs'] = {str(Path(p).resolve()): sha(p) for p in
            (args.arm, args.fixture_sshd, __file__, ROOT/'scripts/test-mesh-operator.py', ROOT/'scripts/mesh_reconnect_fixture_process.py', ROOT/'scripts/mesh_reconnect_fixture_evidence.py')}
        if getattr(args, 'interactive', 'none') == 'terminal':
            report['inputs'].update({str(ROOT/'scripts'/name): sha(ROOT/'scripts'/name) for name in ('mesh_reconnect_terminal_state.py', 'mesh_reconnect_terminal.py', 'mesh_reconnect_terminal_binding.py')})
        fixture.setup()
        fixture.run()
    except BaseException as error:
        failure = repr(error)
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGALRM, old_alarm)
        try:
            cleanup = fixture.cleanup()
        except BaseException as error:
            cleanup = ['cleanup itself raised: '+repr(error)]
        report.update(failure=failure, cleanup_errors=cleanup, calls=fixture.calls, evidence=fixture.evidence)
        eligible = failure is None and not cleanup
        try:
            report['final_roster'] = inventory(fixture.root)
        except BaseException as error:
            report['cleanup_errors'].append('closed-roster observation: '+repr(error))
            eligible = False
        # Persist a conservative failure/incomplete receipt BEFORE removing any
        # evidence. A cleanup or final report error cannot leave only a false PASS.
        report['status'] = 'cleanup_pending' if eligible else 'failed'
        exclusive_json(args.report, report)
        info = args.report.lstat()
        identity, previous = (info.st_dev, info.st_ino), sha(args.report)
        try:
            if eligible and not args.keep:
                remove_exact(fixture.root, report['final_roster'])
                report['fixture_removed'] = not fixture.root.exists()
            report['passed'] = eligible
            report['status'] = 'passed_retained' if eligible and args.keep else 'passed' if eligible else 'failed'
        except BaseException as error:
            report['cleanup_errors'].append('closed-roster cleanup: '+repr(error))
            report['passed'] = False
            report['status'] = 'failed'
        try:
            replace_owned_report(args.report, report, identity, previous)
        except BaseException as error:
            print('Final report update failed; earlier conservative report retained:', args.report, repr(error), file=sys.stderr)
            return 1
    return 0 if report['passed'] else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--arm', required=True, type=Path)
    parser.add_argument('--fixture-sshd', required=True, type=Path)
    parser.add_argument('--report', required=True, type=Path)
    parser.add_argument('--keep', action='store_true')
    parser.add_argument('--scenario', choices=('revoke-lost-ack', 'pair-lost-ack'), default='revoke-lost-ack')
    parser.add_argument('--operation-timeout', type=float, default=180, help='active fixture budget, 1–540s; cleanup has separate fixed bounds')
    parser.add_argument('--interactive', choices=('none','web','terminal'), default='none')
    args = parser.parse_args()
    if not 1 <= args.operation_timeout <= 540:
        parser.error('--operation-timeout must be 1–540 seconds')
    for name in ('arm', 'fixture_sshd'):
        value = getattr(args, name).resolve(strict=True)
        if not value.is_file():
            parser.error('pinned executable input required')
        setattr(args, name, value)
    args.report = args.report.absolute()
    if args.report.exists() or not args.report.parent.is_dir():
        parser.error('new report path in existing directory required')
    def interrupted(signum, _frame):
        raise SystemExit(128+signum)
    signal.signal(signal.SIGTERM, interrupted)
    return execute(args)


if __name__ == '__main__':
    sys.exit(main())
