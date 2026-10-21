#!/usr/bin/env python3
"""Real PTY checks, isolated instruction roots, and redirected JSON contracts."""
import argparse
import errno
import json
import os
from pathlib import Path
import pty
import select
import signal
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    binary = str(Path(parser.parse_args().arm).resolve())
    with tempfile.TemporaryDirectory(prefix='arm-terminal-') as fixture:
        root = Path(fixture)
        home = root / 'state'
        prefix = [binary, '--home', str(home)]
        cx, cc = root / 'codex', root / 'claude'
        cx.mkdir()
        source = cx / 'AGENTS.md'
        source.write_text('User instructions\n')
        install = ['install', '--codex-home', str(cx), '--claude-home', str(cc)]
        backend = None

        def run(*args):
            return subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=15)

        def cli(*args):
            result = run(*args)
            assert result.returncode == 0, (args, result.stderr)
            return json.loads(result.stdout)

        def tty(args, response=None, before_response=None, env_extra=None):
            master, slave = pty.openpty()
            env = dict(os.environ, TERM='xterm-256color')
            env.pop('NO_COLOR', None)
            env.update(env_extra or {})
            process = subprocess.Popen(prefix + list(args), stdin=slave, stdout=slave, stderr=slave, env=env, start_new_session=True)
            os.close(slave)
            data = bytearray()
            answered = False
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
                        data.extend(chunk)
                    if b'Apply these changes? [y/N]' in data and not answered:
                        answered = True
                        if before_response:
                            before_response()
                        if response == 'interrupt':
                            process.send_signal(signal.SIGINT)
                        elif response is not None:
                            os.write(master, response)
                        else:
                            raise AssertionError('unexpected confirmation prompt')
                    if process.poll() is not None:
                        # Drain output after exit; the next read yields EOF/EIO.
                        if not select.select([master], [], [], .1)[0]:
                            break
                else:
                    raise AssertionError('terminal command stalled: ' + repr(bytes(data)))
                return process.wait(timeout=3), bytes(data).decode().replace('\r\n', '\n')
            finally:
                if process.poll() is None:
                    process.kill()
                    process.wait()
                os.close(master)

        try:
            code, text = tty(['version'])
            assert code == 0 and '\x1b[' in text and 'ARM · Version' in text, text
            for extra in ({'NO_COLOR': '1'}, {'TERM': 'dumb'}):
                code, text = tty(['version'], env_extra=extra)
                assert code == 0 and '\x1b' not in text and 'ARM · Version' in text
            for args in (['--json', 'version'], ['version', '--json']):
                code, text = tty(args)
                assert code == 0 and '\x1b' not in text and json.loads(text)['version']
            bad = run('version', '--bad')
            assert bad.returncode == 2 and json.loads(bad.stderr)['error']['code'] == 'invalid_request'
            code, text = tty(['version', '--bad'])
            assert code == 2 and 'Error:' in text and '\x1b[' in text
            code, text = tty(install + ['--dry-run'])
            assert code == 0 and 'Nothing has been changed' in text and 'Add ARM instructions' in text and not home.exists()
            blocked = run(*install)
            assert blocked.returncode == 1 and json.loads(blocked.stderr)['error']['code'] == 'confirmation_required'
            assert json.loads(blocked.stdout)['dry_run'] and not home.exists() and not cc.exists()
            for answer, expected in ((b'\n', 1), (b'no\n', 1), (b'\x04', 1), ('interrupt', 130)):
                code, text = tty(install, response=answer)
                assert code == expected and not home.exists() and source.read_text() == 'User instructions\n', (code, text)
            code, text = tty(install, response=b'yes\n', before_response=lambda: source.write_text('Edited while reviewing\n'))
            assert code == 1 and 'plan_changed' in text and not home.exists()
            assert source.read_text() == 'Edited while reviewing\n' and not cc.exists()
            code, text = tty(install, response=b'yes\n')
            assert code == 0 and 'Install result' in text and (cc / 'CLAUDE.md').exists(), text
            installed = source.read_bytes()
            assert b'Edited while reviewing' in installed and b'<!-- arm:begin' in installed
            manifest = (home / 'install.json').read_bytes()
            code, text = tty(['uninstall'], response=b'n\n')
            assert code == 1 and source.read_bytes() == installed and (home / 'install.json').read_bytes() == manifest
            code, text = tty(['--json', 'uninstall'])
            assert code == 1 and '\x1b' not in text and 'Apply these changes?' not in text
            # JSON stdout preview and stderr error share this PTY, so decode lines
            # separately only for their marker; the redirected contract above is
            # one JSON document on each stream.
            assert 'confirmation_required' in text
            code, text = tty(['uninstall'], response=b'yes\n', before_response=lambda: source.write_bytes(installed + b'New user note\n'))
            assert code == 1 and 'plan_changed' in text and (cc / 'CLAUDE.md').exists()
            report = cli('uninstall', '--yes')
            assert report['complete'] and source.read_text() == 'Edited while reviewing\nNew user note\n'
            assert not (cc / 'CLAUDE.md').exists() and home.exists()
            cli(*install, '--yes')
            backend = subprocess.Popen(prefix + ['serve'], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            deadline = time.monotonic() + 10
            while not (home / 'endpoint.json').exists():
                assert backend.poll() is None and time.monotonic() < deadline
                time.sleep(.05)
            lease = cli('acquire', '--cpu', '1', '--reason', 'safe\x1b[2J\nforged prompt')
            code, text = tty(['resources'])
            assert code == 0 and 'TOTAL' in text and 'AVAILABLE' in text and 'safe\\u001b[2J\\nforged prompt' in text and '\x1b[2J' not in text
            code, text = tty(['uninstall'])
            assert code == 1 and 'active_leases' in text and 'Apply these changes?' not in text
            cli('release', lease['id'])
            code, text = tty(['uninstall'], response=b'n\n')
            assert code == 1 and cli('status')['host'] and backend.poll() is None, 'declining uninstall stopped an idle backend'
            code, text = tty(['uninstall'], response=b'y\n')
            assert code == 0 and 'Uninstall result' in text and 'Retained state and history' in text, text
            backend.wait(timeout=10)
            assert source.read_text() == 'Edited while reviewing\nNew user note\n' and home.exists()
            print('Terminal CLI: ANSI/NO_COLOR, explicit/piped JSON, safe data rendering, preview, default-no/EOF/Ctrl-C, stale files, confirmations, automation, active-lease guard and idle-backend preservation passed')
        finally:
            if backend is not None and backend.poll() is None:
                backend.terminate()
                try:
                    backend.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    backend.kill()
                    backend.wait()


if __name__ == '__main__':
    main()
