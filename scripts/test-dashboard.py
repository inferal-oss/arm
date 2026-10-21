#!/usr/bin/env python3
"""Compiled dashboard bootstrap, exact CLI JSON and loopback HTTP safeguards.

This does not drive a browser or establish rendered layout acceptance.
"""
import argparse
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import shutil
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    binary = str(Path(parser.parse_args().arm).resolve())
    root = Path(tempfile.mkdtemp(prefix='arm-dashboard-'))
    home = root / 'state'
    prefix = [binary, '--home', str(home)]

    def cli(*args):
        result = subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=15)
        assert result.returncode == 0, (args, result.returncode, result.stderr)
        return json.loads(result.stdout)

    def request(url, data=None, headers=None):
        req = urllib.request.Request(url, data=data, headers=headers or {})
        try:
            with urllib.request.urlopen(req, timeout=5) as response:
                return response.status, response.headers, response.read()
        except urllib.error.HTTPError as error:
            return error.code, error.headers, error.read()

    def tty(args, env):
        master, slave = pty.openpty()
        process = subprocess.Popen(prefix + args, stdin=slave, stdout=slave, stderr=slave,
                                   env=env, start_new_session=True)
        os.close(slave)
        data, deadline = bytearray(), time.monotonic() + 15
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
                if process.poll() is not None and not select.select([master], [], [], .1)[0]:
                    break
            else:
                raise AssertionError('dashboard terminal command stalled')
            return process.wait(timeout=3), bytes(data).decode().replace('\r\n', '\n')
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
            os.close(master)

    try:
        cli('start', '--cpu', '1', '--memory', '1GiB', '--disk', '1GiB')
        link = cli('dashboard')  # Pipes never invoke a browser opener.
        url = urllib.parse.urlsplit(link['url'])
        assert url.scheme == 'http' and url.hostname == '127.0.0.1' and not url.query
        ticket = urllib.parse.parse_qs(url.fragment)['connect'][0]
        base = urllib.parse.urlunsplit((url.scheme, url.netloc, '', '', ''))
        endpoint = json.loads((home / 'endpoint.json').read_text())
        for path in ('/', '/app.mjs', '/core.mjs', '/json-view.mjs', '/metric-help.mjs', '/lease-actions.mjs', '/lease-state.mjs', '/route.mjs', '/activity-feed.mjs', '/operator-messaging.mjs', '/connection-links.mjs', '/style.css'):
            status, headers, body = request(base + path)
            assert status == 200 and body and endpoint['token'].encode() not in body
            assert headers['Cache-Control'] == 'no-store' and 'Set-Cookie' not in headers
            assert "frame-ancestors 'none'" in headers['Content-Security-Policy']
        payload = json.dumps({'ticket': ticket}).encode()
        headers = {'Content-Type': 'application/json', 'Origin': 'http://external.invalid'}
        assert request(base + '/v1/browser/session', payload, headers)[0] == 403
        headers['Origin'] = base
        status, response_headers, data = request(base + '/v1/browser/session', payload, headers)
        assert status == 200 and 'Set-Cookie' not in response_headers
        session = json.loads(data)
        assert len(session['token']) == 64 and session['token'] != endpoint['token']
        assert request(base + '/v1/browser/session', payload, headers)[0] == 401
        assert request(base + '/', headers={'Host': 'localhost:' + str(url.port)})[0] == 403
        assert request(base + '/missing')[0] == 404
        # A real executable with spaces/metacharacters in its filename receives
        # one URL argument. It never opens a graphical app or reads user pages.
        browser = root / 'browser with spaces; literal'
        browser.write_text('#!/bin/sh\nprintf \'%s\\n\' "$#" "$1" > "$ARM_BROWSER_TEST_LOG"\n')
        browser.chmod(0o700)
        argv_log = root / 'browser-argv'
        env = dict(os.environ, ARM_BROWSER_TEST_LOG=str(argv_log), TERM='xterm-256color')
        for args in ([str(browser)], [str(browser), '--json'], [str(browser), '--no-open']):
            result = subprocess.run(prefix + ['dashboard'] + args, capture_output=True, text=True,
                                    timeout=15, env=env)
            assert result.returncode == 0 and json.loads(result.stdout)['url']
            assert not argv_log.exists(), 'piped dashboard unexpectedly launched browser'
        for flags in (['--json'], ['--no-open']):
            code, output = tty(['dashboard', str(browser)] + flags, env)
            assert code == 0 and not argv_log.exists(), (flags, code, output)
            if flags == ['--json']:
                assert json.loads(output)['url']
        code, output = tty(['dashboard', str(browser)], env)
        assert code == 0, output
        deadline = time.monotonic() + 5
        while (not argv_log.exists() or not argv_log.stat().st_size) and time.monotonic() < deadline:
            time.sleep(.01)
        argv = argv_log.read_text().splitlines()
        assert len(argv) == 2 and argv[0] == '1'
        selected = urllib.parse.urlsplit(argv[1])
        assert selected.netloc == url.netloc and selected.fragment.startswith('connect=')
        result = subprocess.run(prefix + ['dashboard', 'terminal'], capture_output=True, text=True, timeout=15)
        assert result.returncode == 1 and json.loads(result.stderr)['error']['code'] == 'terminal_required'
        cli('stop')
        print('Compiled dashboard: one-use CLI link, embedded assets, CSP/no cookies, origin/authority guards, bootstrap/replay, literal executable URL argument, real PTY launcher and no-launch JSON/piped modes passed; script does not establish browser rendering acceptance')
    finally:
        subprocess.run(prefix + ['stop'], capture_output=True, timeout=15)
        try:
            with (home / 'daemon.lock').open('r+') as lock:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                if not (home / 'endpoint.json').exists():
                    shutil.rmtree(root)
                else:
                    print('Retained unresolved dashboard fixture:', root)
        except (FileNotFoundError, BlockingIOError):
            print('Retained dashboard fixture without verified shutdown:', root)


if __name__ == '__main__':
    main()
