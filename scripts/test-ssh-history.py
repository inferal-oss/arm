#!/usr/bin/env python3
"""Actual OpenSSH source-history compaction and permanent source-key fences.

Uses two fresh private loopback ARM authorities, actual system OpenSSH and public
CLI operations. No user homes, SSH keys/config, remote hostnames or synthetic
successful SSH receipts are used. Failed fixtures remain available for review.
"""
import argparse
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import sqlite3
import select
import struct
import stat
import subprocess
import sys
import tempfile
import termios
import time

sys.dont_write_bytecode = True
_spec = importlib.util.spec_from_file_location('ssh_history_mesh', Path(__file__).with_name('test-mesh.py'))
mesh = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(mesh)


def error(result):
    assert result.returncode != 0, ('unexpected success', result.args, result.stdout)
    assert not result.stdout, ('failed command emitted intermediate stdout', result.args, result.stdout)
    return json.loads(result.stderr)['error']


def one_json(result):
    assert result.returncode == 0, (result.args, result.returncode, result.stdout, result.stderr)
    assert not result.stderr, (result.args, result.stderr)
    # json.loads refuses multiple concatenated preview/result JSON values.
    return json.loads(result.stdout)


def evidence(home):
    """Complete bounded private source namespaces; exclude live backend SQLite."""
    out = {}
    for base in (home / 'mesh', home / 'ssh-source-history'):
        if not base.exists():
            continue
        paths = [base] + sorted(base.rglob('*'))
        assert len(paths) <= 2048, 'unexpected unbounded fixture namespace'
        for path in paths:
            info = path.lstat()
            assert not stat.S_ISLNK(info.st_mode), ('unexpected evidence symlink', path)
            assert stat.S_ISDIR(info.st_mode) or stat.S_ISREG(info.st_mode), ('unexpected evidence kind', path)
            digest = hashlib.sha256(path.read_bytes()).hexdigest() if path.is_file() else None
            out[str(path.relative_to(home))] = (info.st_dev, info.st_ino, info.st_uid,
                                                 info.st_mode, info.st_nlink, info.st_size if digest else 0, digest)
    return out


def history_rows(home):
    path = home / 'ssh-source-history' / 'catalog.db'
    with sqlite3.connect(path.as_uri() + '?mode=ro', uri=True) as db:
        return {table: db.execute('SELECT * FROM ' + table + ' ORDER BY 1').fetchall()
                for table in ('source_history_operations', 'source_history_packs', 'source_history_refs')}


def destination_executions(node):
    with sqlite3.connect((node.home / 'state.db').as_uri() + '?mode=ro', uri=True) as db:
        return db.execute('SELECT COUNT(*) FROM ssh_operations').fetchone()[0]


def execution(node, host, key, command, *options):
    return node.result('ssh', host, '--reason', 'real source-history acceptance', '--ttl', '15s',
                       '--key', key, *options, '--', command, timeout=30)


def until(function, predicate, seconds=10):
    deadline = time.monotonic() + seconds
    value = None
    while time.monotonic() < deadline:
        value = function()
        if predicate(value):
            return value
        time.sleep(.05)
    raise AssertionError(('condition timed out', value))


def settled_source(node, expected_hot):
    value = node.cli('ssh', 'pending')
    assert not value['pending'], value
    assert value['completed'] + value['sealed'] == expected_hot, value
    return value



def terminal_result(node, arguments, *, decline=False, no_color=False, dumb=False):
    master, slave = os.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 40, 105, 0, 0))
    env = dict(os.environ, TERM='dumb' if dumb else 'xterm-256color')
    env.pop('NO_COLOR', None)
    if no_color:
        env['NO_COLOR'] = '1'
    process = subprocess.Popen(node.prefix + arguments, stdin=slave, stdout=slave,
                               stderr=slave, env=env, start_new_session=True, close_fds=True)
    os.close(slave)
    output = bytearray()
    sent = False
    deadline = time.monotonic() + 20
    try:
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], .1)
            if ready:
                try:
                    data = os.read(master, 65536)
                except OSError:
                    break
                if not data:
                    break
                output.extend(data)
                assert len(output) <= 1 << 20, 'terminal history output exceeded review bound'
                if decline and not sent and b'[y/N]' in output:
                    os.write(master, b'n\n')
                    sent = True
            elif process.poll() is not None:
                break
        assert process.poll() is not None or process.wait(timeout=2) is not None, 'terminal client did not exit'
        if decline:
            assert sent, ('terminal default-no prompt missing', bytes(output))
        return process.returncode, output.decode('utf-8')
    finally:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=3)
        os.close(master)


def terminal_review(node, arguments):
    before = evidence(node.home)
    code, text = terminal_result(node, arguments, decline=True)
    assert code != 0 and '\x1b[' in text and 'history' in text.lower(), text
    assert 'permanent' in text.lower() and 'No remote execution' in text, text
    assert evidence(node.home) == before, 'terminal decline changed evidence'
    code, text = terminal_result(node, arguments + ['--dry-run'])
    assert code == 0 and '\x1b[' in text and '[y/N]' not in text, text
    assert 'source_certificate' not in text and 'permanent' in text.lower(), 'dry-run dumped raw producer record instead of concise review'
    for options in ({'no_color': True}, {'dumb': True}):
        code, text = terminal_result(node, arguments + ['--dry-run'], **options)
        assert code == 0 and '\x1b[' not in text and 'history' in text.lower(), text
    code, text = terminal_result(node, arguments + ['--dry-run', '--json'])
    assert code == 0 and '\x1b[' not in text and json.loads(text)['dry_run'], text
    code, text = terminal_result(node, arguments + ['--json'])
    assert code != 0 and '\x1b[' not in text and json.loads(text)['error']['code'] == 'confirmation_required', text
    assert evidence(node.home) == before, 'terminal preview/JSON confirmation changed evidence'

def archive(node, key, limit=64):
    preview = node.cli('history', 'ssh-source', 'plan', '--limit', str(limit))
    plan = preview['plan']
    assert preview['plan_sha256'] == plan['plan_sha256'], preview
    arguments = ['history', 'ssh-source', 'archive', '--key', key, '--reason',
                 'Preserve verified receipts while freeing source hot slots', '--expect-plan',
                 plan['plan_sha256'], '--limit', str(limit)]
    before = evidence(node.home)
    if key == 'archive-original-64':
        terminal_review(node, arguments)
    dry = node.cli(*arguments, '--dry-run')
    assert dry['dry_run'], dry
    assert evidence(node.home) == before, 'archive dry-run changed private evidence'
    refused = error(node.result(*arguments))
    assert refused['code'] == 'confirmation_required', refused
    assert evidence(node.home) == before, 'missing consent changed private evidence'
    operation = node.cli(*arguments, '--yes')
    assert operation['phase'] == 'completed', operation
    rows = history_rows(node.home)
    before = evidence(node.home)
    retry = node.cli('history', 'ssh-source', 'archive', '--key', key)
    assert retry == operation, ('exact key inspection changed operation', retry, operation)
    assert history_rows(node.home) == rows and evidence(node.home) == before, 'exact retry grew audit or changed native evidence'
    for changed in (['--reason', 'Changed request'], ['--expect-plan', '0' * 64],
                    ['--limit', '1'], ['--target', '0' * 32],
                    ['--before', '2000-01-01T00:00:00Z']):
        result = node.result('history', 'ssh-source', 'archive', '--key', key, *changed, '--yes')
        detail = error(result)
        assert 'conflict' in detail['code'] or 'conflict' in detail['message'].lower(), detail
        assert history_rows(node.home) == rows and evidence(node.home) == before, 'conflicting retry modified history'
    assert node.cli('history', 'ssh-source', 'inspect', key) == operation
    return operation


def cold_receipt(node, target, key, kind):
    receipt = node.cli('history', 'ssh-source', 'receipt', target, '--key', key)
    assert receipt['found'] and receipt['archived'] and receipt['kind'] == kind, receipt
    original = receipt[kind]['intent']
    assert original['request']['key'] == key and original['request']['pin']['target'] == target, receipt
    lookup = node.cli('ssh', 'lookup', target, '--key', key)
    assert lookup == {'source': 'local', 'receipt': receipt}, lookup
    return receipt


def refuse_old_keys(node, target, keys, marker):
    before = evidence(node.home)
    for key in keys:
        detail = error(execution(node, target, key, 'printf forbidden >> ' + str(marker)))
        assert detail['code'] == 'ssh_intent_exists', (key, detail)
    assert not marker.exists(), 'an archived key executed again'
    assert evidence(node.home) == before, 'offline old-key check changed source artifacts/evidence'



def recovery_cli(node):
    catalog = node.home / 'ssh-source-history' / 'catalog.db'
    original = history_rows(node.home)
    original_bytes = hashlib.sha256(catalog.read_bytes()).hexdigest()
    # A real separate SQLite process spills uncommitted pages then exits without
    # rollback/close. No handwritten journal or committed receipt is fabricated.
    child = r'''import os, sqlite3, sys
from pathlib import Path
path = Path(sys.argv[1])
db = sqlite3.connect(path.as_uri() + '?mode=rw', uri=True)
for statement in ('PRAGMA synchronous=FULL', 'PRAGMA cache_size=1', 'PRAGMA cache_spill=ON'):
    db.execute(statement)
db.execute('BEGIN IMMEDIATE')
for index in range(32):
    db.execute('INSERT INTO source_history_operations(key,request,phase,data) VALUES(?,?,?,?)',
               ('uncommitted-public-recovery-' + str(index), '{}', 'prepared', 'x' * 8192))
assert Path(str(path) + '-journal').stat().st_size > 512
os._exit(0)
'''
    subprocess.run([sys.executable, '-c', child, str(catalog)], check=True, timeout=15)
    journal = Path(str(catalog) + '-journal')
    assert journal.is_file() and journal.stat().st_size > 512
    header = journal.read_bytes()[:28]
    assert header[:8] == bytes.fromhex('d9d505f920a163d7') and int.from_bytes(header[8:12], 'big') > 0, header.hex()
    assert hashlib.sha256(catalog.read_bytes()).hexdigest() != original_bytes, 'uncommitted DB pages were not actually spilled'
    key, reason = 'actual-public-recovery', 'Recover actual private crashed SQLite journal and inspect all original history'
    planned = node.cli('history', 'ssh-source', 'recovery', 'plan', '--key', key, '--reason', reason)
    digest = planned['review_sha256']
    assert len(digest) == 64 and planned['request']['journal_bytes'] == journal.stat().st_size, planned
    before = evidence(node.home)
    arguments = ['history', 'ssh-source', 'recovery', 'apply', '--key', key, '--reason', reason,
                 '--expect-review', digest]
    dry = node.cli(*arguments, '--dry-run')
    assert dry['dry_run'] and evidence(node.home) == before
    detail = error(node.result(*arguments))
    assert detail['code'] == 'confirmation_required' and evidence(node.home) == before, detail
    done = node.cli(*arguments, '--yes', timeout=120)
    assert done.get('completed_at') and done.get('result') and not journal.exists(), done
    assert history_rows(node.home) == original, 'recovery lost original rows or retained uncommitted fixture rows'
    assert node.cli('history', 'ssh-source', 'recovery', 'inspect', key) == done
    repeated_plan = node.cli('history', 'ssh-source', 'recovery', 'plan', '--key', key, '--reason', reason)
    assert repeated_plan == planned, 'gone journal discarded the original exact request'
    after = evidence(node.home)
    assert node.cli('history', 'ssh-source', 'recovery', 'apply', '--key', key) == done
    for change in (['--reason', 'Different original purpose'], ['--expect-review', '0' * 64]):
        detail = error(node.result('history', 'ssh-source', 'recovery', 'apply', '--key', key, *change, '--yes'))
        assert 'conflict' in detail['code'] or 'conflict' in detail['message'].lower(), detail
    assert evidence(node.home) == after and history_rows(node.home) == original

def retire_and_verify(node, expected):
    preview = node.cli('database', 'remove', '--reason', 'retire only disposable accepted test state',
                       '--retire-mesh', '--dry-run', timeout=120)
    proof = preview['plan']['settled_proof']
    assert proof['complete'] and proof['settled'] and not proof.get('blockers'), preview
    before = evidence(node.home)
    outcome = node.cli('database', 'remove', '--reason', 'retire only disposable accepted test state',
                       '--retire-mesh', '--yes', timeout=120)
    assert outcome['result']['completed'], outcome
    archive_root = node.home / 'database-maintenance' / ('reset-' + outcome['result']['operation'])
    for name, prior in before.items():
        path = archive_root / name
        info = path.lstat()
        digest = hashlib.sha256(path.read_bytes()).hexdigest() if path.is_file() else None
        actual = (info.st_dev, info.st_ino, info.st_uid, info.st_mode, info.st_nlink,
                  info.st_size if digest else 0, digest)
        assert actual == prior, ('reset did not retain exact whole source namespace', name)
    if expected:
        with sqlite3.connect((archive_root / 'ssh-source-history' / 'catalog.db').as_uri() + '?mode=ro', uri=True) as db:
            keys = {row[0] for row in db.execute('SELECT key FROM source_history_refs')}
            assert keys == set(expected), ('retirement lost archived original tuples', keys, expected)
    assert not (node.home / 'mesh').exists() and not (node.home / 'ssh-source-history').exists()


def run(binary, keep):
    assert shutil.which('ssh'), 'actual system OpenSSH is required; this gate does not skip'
    root = Path(tempfile.mkdtemp(prefix='arm-ssh-history-')).resolve()
    root.chmod(0o700)
    born = root.stat()
    # Freeze the exact candidate for every child, even if another build later
    # replaces the caller's original output pathname.
    candidate = Path(binary)
    initial = candidate.stat()
    assert stat.S_ISREG(initial.st_mode) and initial.st_size <= 256 << 20
    copied = root / 'arm'
    shutil.copyfile(candidate, copied)
    copied.chmod(0o700)
    final = candidate.stat()
    version = lambda info: (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
    assert version(initial) == version(final), 'candidate changed during fixture capture'
    binary_sha = hashlib.sha256(copied.read_bytes()).hexdigest()
    assert binary_sha == hashlib.sha256(candidate.read_bytes()).hexdigest(), 'candidate copy differs'
    binary = str(copied)
    source, target = mesh.Node(binary, root, 'history-source'), mesh.Node(binary, root, 'history-target')
    relay = None
    success = False
    started = []
    try:
        for node in (source, target):
            node.start()
            started.append(node)
        source_port, target_port = mesh.free_port(), mesh.free_port()
        relay = mesh.TLSRelay(('127.0.0.1', target_port))
        for node, port, address in ((source, source_port, f'127.0.0.1:{source_port}'),
                                     (target, target_port, relay.address)):
            node.identity = node.cli('mesh', 'init', '--name', node.name, '--listen', f'127.0.0.1:{port}',
                                     '--address', address, '--yes')
        grant = {'permissions': ['ssh-exec', 'lease-own'], 'limits': {'cpu': 2}, 'max_ttl_ns': 30 * mesh.SECOND}
        mesh.pair(source, target, grant, grant)
        target.cli('ssh', 'setup', '--reason', 'actual private OpenSSH history acceptance', '--yes')
        keys = ['actual-history-' + str(index).zfill(2) for index in range(64)]
        for index, key in enumerate(keys):
            result = execution(source, target.name, key, 'printf history-' + str(index), *(['--yes'] if index == 0 else []))
            assert (result.returncode, result.stdout, result.stderr) == (0, 'history-' + str(index), ''), (index, result)
            settled_source(source, index + 1)
        assert destination_executions(target) == 64
        marker = root / 'must-not-execute'
        prior_count = destination_executions(target)
        capacity = error(execution(source, target.name, 'capacity-65', 'printf forbidden > ' + str(marker)))
        assert not marker.exists() and destination_executions(target) == prior_count
        settled_source(source, 64)
        first = archive(source, 'archive-original-64')
        assert len(first['request']['plan']['members']) == 64
        settled_source(source, 0)
        assert len(history_rows(source.home)['source_history_refs']) == 64
        result = execution(source, target.name, 'fresh-after-archive', 'printf fresh-after-archive')
        assert (result.returncode, result.stdout, result.stderr) == (0, 'fresh-after-archive', ''), result
        settled_source(source, 1)
        # An opaque relay discards stream setup before admission. Only explicit
        # transactional nonexecution sealing may settle that original key.
        relay.drop_next_reply(skip=2)
        result = execution(source, target.name, 'actual-never-admitted', 'printf forbidden > ' + str(marker))
        assert result.returncode != 0 and relay.dropped.wait(2), result
        pending = source.cli('ssh', 'pending')['pending']
        assert len(pending) == 1 and pending[0]['request']['key'] == 'actual-never-admitted', pending
        absent = source.cli('ssh', 'lookup', target.name, '--key', 'actual-never-admitted')
        assert absent['state'] == 'absent' and len(source.cli('ssh', 'pending')['pending']) == 1, absent
        sealed = source.cli('ssh', 'seal', target.name, '--key', 'actual-never-admitted',
                            '--reason', 'prevent late execution of exact discarded stream', '--yes')
        assert sealed['state'] == 'sealed', sealed
        settled_source(source, 2)
        archive(source, 'archive-extra-and-seal')
        settled_source(source, 0)
        cold_receipt(source, target.identity['host'], keys[0], 'completion')
        cold_receipt(source, target.identity['host'], 'actual-never-admitted', 'seal')
        assert destination_executions(target) == 65 and not marker.exists()
        assert source.stop() and target.stop(), 'private backends did not stop'
        # With no source backend, ssh_intent_exists proves the precheck precedes
        # ssh_account; source control-flow then places signer/runtime/artifacts
        # after that unavailable RPC. Current receipt inspection stays offline.
        refuse_old_keys(source, target.identity['host'], keys, marker)
        cold_receipt(source, target.identity['host'], keys[-1], 'completion')
        source.stopped = False
        source.start()
        assert source.stop(), 'restarted source did not stop'
        refuse_old_keys(source, target.identity['host'], keys, marker)
        config_path = source.home / 'mesh' / 'config.json'
        original_config = config_path.read_bytes()
        config = json.loads(original_config)
        config['peers'] = [peer for peer in config['peers'] if peer['identity']['host'] != target.identity['host']]
        # Fixture-only row removal while stopped. Public revoke retains a row;
        # this tests the stronger stable-ID offline lookup contract explicitly.
        config_path.write_text(json.dumps(config, separators=(',', ':')))
        refuse_old_keys(source, target.identity['host'], keys, marker)
        cold_receipt(source, target.identity['host'], keys[0], 'completion')
        removed = error(execution(source, target.name, keys[0], 'printf forbidden'))
        assert removed['code'] in ('not_found', 'host_unavailable', 'ambiguous_host'), removed
        assert 'stable' in removed['message'].lower(), removed
        config_path.write_bytes(original_config)
        recovery_cli(source)
        retire_and_verify(source, keys + ['fresh-after-archive', 'actual-never-admitted'])
        retire_and_verify(target, [])
        success = True
        print(json.dumps({'result': 'passed', 'arm_sha256': binary_sha, 'actual_openssh_executions': 65, 'permanent_archived_keys': 66,
                          'old_key_checks': 192, 'capacity_refusal_code': capacity['code'],
                          'offline_after_restart_and_peer_removal': True, 'actual_nonexecution_seal': True,
                          'reset_preserved_whole_history': True, 'actual_crashed_journal_cli_recovery': True, 'actual_pty_reviews': True, 'python_sqlite_version': sqlite3.sqlite_version}))
    finally:
        if relay:
            relay.close()
        stop_results = [node.stop() for node in reversed(started)]
        stopped = all(stop_results)
        now = root.lstat()
        assert stat.S_ISDIR(now.st_mode) and (now.st_dev, now.st_ino, now.st_uid) == (born.st_dev, born.st_ino, born.st_uid)
        if success and stopped and not keep:
            # All owned process locks have been proved free, and retired receipts
            # were checked above before ordinary disposable-fixture cleanup.
            shutil.rmtree(root)
        else:
            print('Retained private SSH history acceptance fixture:', root, flush=True)
        assert stopped, 'owned backend still running; evidence retained'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--arm', required=True, help='candidate executable (native or Linux)')
    parser.add_argument('--keep', action='store_true', help='retain successful private fixture for inspection')
    args = parser.parse_args()
    binary = str(Path(args.arm).resolve())
    assert Path(binary).is_file() and os.access(binary, os.X_OK), 'candidate ARM executable required'
    run(binary, args.keep)


if __name__ == '__main__':
    main()
