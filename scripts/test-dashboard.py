#!/usr/bin/env python3
"""Compiled dashboard bootstrap, exact CLI JSON and loopback HTTP safeguards.

This does not drive a browser or establish rendered layout acceptance.
"""
import argparse
import fcntl
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
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

    try:
        cli('start', '--cpu', '1', '--memory', '1GiB', '--disk', '1GiB')
        link = cli('dashboard')  # Pipes never invoke a browser opener.
        url = urllib.parse.urlsplit(link['url'])
        assert url.scheme == 'http' and url.hostname == '127.0.0.1' and not url.query
        ticket = urllib.parse.parse_qs(url.fragment)['connect'][0]
        base = urllib.parse.urlunsplit((url.scheme, url.netloc, '', '', ''))
        endpoint = json.loads((home / 'endpoint.json').read_text())
        for path in ('/', '/app.mjs', '/core.mjs', '/json-view.mjs', '/metric-help.mjs', '/style.css'):
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
        cli('stop')
        print('Compiled dashboard: one-use CLI link, embedded assets, CSP/no cookies, origin and authority guards, bootstrap/replay passed; browser rendering untested')
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
