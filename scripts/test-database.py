#!/usr/bin/env python3
"""Compiled database maintenance acceptance using only private local fixtures."""
import argparse
import errno
import fcntl
import signal
import hashlib
import json
import os
from pathlib import Path
import pty
import select
import shutil
import sqlite3
import subprocess
import tempfile
import threading
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    binary = str(Path(parser.parse_args().arm).resolve())
    root = Path(tempfile.mkdtemp(prefix='arm-database-test-')).resolve()
    home = root / 'state'
    prefix = [binary, '--home', str(home)]

    def cli(*args, check=True):
        result = subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=30)
        if not check:
            return result
        assert result.returncode == 0, (args, result.stderr)
        return json.loads(result.stdout)

    def source_image():
        return {p.name: (p.stat().st_mode & 0o777, hashlib.sha256(p.read_bytes()).hexdigest())
                for p in home.iterdir() if p.is_file()}

    def terminal(args):
        master, slave = pty.openpty()
        process = subprocess.Popen(prefix + list(args), stdin=slave, stdout=slave, stderr=slave,
                                   env=dict(os.environ, TERM='xterm-256color'))
        os.close(slave)
        result = bytearray()
        deadline = time.monotonic() + 15
        try:
            while time.monotonic() < deadline:
                if select.select([master], [], [], .1)[0]:
                    try:
                        chunk = os.read(master, 65536)
                    except OSError as error:
                        if error.errno == errno.EIO:
                            break
                        raise
                    if not chunk:
                        break
                    result.extend(chunk)
                if process.poll() is not None:
                    break
            assert process.wait(timeout=3) == 0, bytes(result)
            return bytes(result)
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=3)
            os.close(master)

    try:
        cli('daemon', 'start', '--cpu', '1')
        stats = cli('database', 'stats')
        assert stats['host'] and stats['scope'].startswith('online')
        assert 'known_obligation_counts' in stats and stats['page_size_bytes'] > 0
        assert cli('database', 'check')['mode'] == 'quick'
        full = cli('database', 'check', '--full')
        assert full['ok'] and full['foreign_keys'] == 'none declared in schema'
        pool = cli('pool', 'create', 'backup-consistency', '--scope', 'global', '--capacity', '3',
                   '--reason', 'verify transactional snapshot')['after']['id']
        errors = []
        started = threading.Event()

        def write_journal():
            try:
                for _ in range(25):
                    cli('pool', 'adjust', pool, '1', '--reason', 'concurrent backup fixture')
                    started.set()
            except BaseException as error:
                errors.append(error)
                started.set()

        writer = threading.Thread(target=write_journal)
        writer.start()
        assert started.wait(10)
        destination = root / 'online.db'
        backup = cli('database', 'backup', str(destination))
        writer.join(timeout=20)
        assert not writer.is_alive() and not errors, errors
        assert backup['host'] == stats['host'] and len(backup['sha256']) == 64
        assert destination.stat().st_mode & 0o777 == 0o600
        with sqlite3.connect('file:' + str(destination) + '?mode=ro', uri=True) as db:
            assert db.execute('pragma integrity_check').fetchone()[0] == 'ok'
            definition = json.loads(db.execute('select data from pools where id=?', (pool,)).fetchone()[0])
            change = json.loads(db.execute('select data from pool_changes where pool=? order by sequence desc limit 1', (pool,)).fetchone()[0])
            assert definition['capacity'] == change['after']['capacity']
            assert db.execute("select value from metadata where key='host'").fetchone()[0] == stats['host']
        unchanged = destination.read_bytes()
        assert cli('database', 'backup', str(destination), check=False).returncode != 0
        assert destination.read_bytes() == unchanged
        assert b'Page' in terminal(['database', 'stats'])
        structured = terminal(['database', 'stats', '--json'])
        assert b'\x1b' not in structured and json.loads(structured)['host'] == stats['host']
        cli('stop')
        # Existing shutdown-result observation can itself leave an empty WAL
        # index. Prepare a test-owned absent-index source explicitly; subsequent
        # maintenance must preserve that absence.
        wal = home / 'state.db-wal'
        assert not wal.exists() or wal.stat().st_size == 0
        for name in ('state.db-wal', 'state.db-shm'):
            (home / name).unlink(missing_ok=True)
        # Normal older SQLite files may have read bits; maintenance must not
        # silently repair them inside the already-private ARM directory.
        os.chmod(home / 'state.db', 0o644)
        before = source_image()
        stopped = cli('database', 'stats')
        assert stopped['scope'] == 'stopped consistent copy'
        assert cli('database', 'check', '--full')['ok']
        cli('database', 'backup', str(root / 'offline.db'))
        assert source_image() == before, 'offline inspection changed source bytes/names/modes'
        assert not (home / 'state.db-shm').exists(), 'read created source SHM'
        os.chmod(home / 'state.db', 0o664)
        assert cli('database', 'stats', check=False).returncode != 0
        os.chmod(home / 'state.db', 0o644)
        # Abrupt death deliberately leaves a valid committed WAL and a stale
        # endpoint. Inspection must route by stopped authority, preserve both,
        # and never initialize/reconcile a manager as a read side effect.
        cli('daemon', 'start')
        cli('acquire', '--cpu', '1', '--ttl', '1m', '--reason', 'crash inspection remains read-only')
        endpoint = json.loads((home / 'endpoint.json').read_text())
        os.kill(endpoint['pid'], signal.SIGKILL)
        with (home / 'daemon.lock').open('rb') as lock:
            for _ in range(100):
                try:
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    fcntl.flock(lock, fcntl.LOCK_UN)
                    break
                except BlockingIOError:
                    time.sleep(.02)
            else:
                raise TimeoutError('test backend did not release authority after abrupt death')
        assert (home / 'state.db-wal').exists()
        crashed_image = source_image()
        crash_stats = cli('database', 'stats')
        assert crash_stats['scope'] == 'stopped consistent copy'
        assert crash_stats['lease_states']['active'] == 1
        assert cli('database', 'check', '--full')['ok']
        cli('database', 'backup', str(root / 'crashed.db'))
        assert source_image() == crashed_image, 'crash inspection repaired/replayed source or changed stale endpoint'
        print('Compiled database: online/stopped stats, quick/full/FK scope, consistent no-clobber private backup, concurrent transactional journal, real PTY/JSON, source permissions/WAL safety and abrupt-death/stale-endpoint inspection passed; removal is separate')
    finally:
        subprocess.run(prefix + ['stop', '--force'], capture_output=True, timeout=15)
        shutil.rmtree(root)


if __name__ == '__main__':
    main()
