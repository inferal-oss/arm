#!/usr/bin/env python3
"""Bounded, read-only CI acceptance for the default-off forwarding client.

Only disposable source files are changed for the retained counterexamples;
fixtures use their own private homes. No CPU admission override is installed.
This runner does not claim process-tree cancellation acceptance.
"""
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import time

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / '.cache/forward-client-ci'
OUT.mkdir(parents=True, exist_ok=True)
records = []


def command(name, args, expected=0):
    started = time.monotonic()
    with (OUT / (name + '.log')).open('w') as log:
        result = subprocess.run(args, cwd=ROOT, stdout=log, stderr=subprocess.STDOUT)
    record = {'name': name, 'argv': args, 'exit': result.returncode,
              'seconds': time.monotonic() - started, 'expected_exit': expected}
    records.append(record)
    (OUT / 'commands.json').write_text(json.dumps(records, indent=2) + '\n')
    if expected is not None and result.returncode != expected:
        raise RuntimeError(f'{name}: exit {result.returncode}, expected {expected}')
    return result.returncode


def main():
    commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    assert commit == os.environ['GITHUB_SHA'], 'CI source SHA mismatch'
    files = subprocess.check_output(['git', 'ls-files', '-z'], cwd=ROOT).split(b'\0')
    manifest = {os.fsdecode(p): hashlib.sha256((ROOT / os.fsdecode(p)).read_bytes()).hexdigest()
                for p in files if p and (ROOT / os.fsdecode(p)).is_file()}
    (OUT / 'source.json').write_text(json.dumps({'commit': commit,
        'tree': subprocess.check_output(['git', 'rev-parse', 'HEAD^{tree}'], cwd=ROOT, text=True).strip(),
        'platform': platform.platform(), 'files': manifest}, indent=2) + '\n')
    command('go-version', ['go', 'version'])
    command('go-environment', ['go', 'env', 'GOOS', 'GOARCH', 'CGO_ENABLED', 'GOMOD', 'GOFLAGS'])

    # Retain the actual reviewed predecessor failures. Extract its unchanged
    # admission block into the helper seam so the new held-write/ACK cases can
    # exercise that original code without changing the original dial path.
    http = ROOT / 'internal/cli/mesh_forward_http.go'
    stream = ROOT / 'internal/cli/mesh_forward_stream.go'
    final_http, final_stream = http.read_bytes(), stream.read_bytes()
    try:
        http.write_bytes(subprocess.check_output(['git', 'show', '2f07eb0:internal/cli/mesh_forward_http.go'], cwd=ROOT))
        original = subprocess.check_output(['git', 'show', '2f07eb0:internal/cli/mesh_forward_stream.go'], cwd=ROOT, text=True)
        start = original.index('\tif err = conn.SetDeadline(')
        end = original.index('\taccepted = true', start)
        body = original[start:end].replace('return nil, status,', 'return')
        stream.write_text(original + '\nfunc confirmForwardSource(ctx context.Context, conn net.Conn, token string) (err error) {\n' + body + '\nreturn nil\n}\n')
        command('predecessor-counterexamples', ['go', 'test', '-json', '-count=1', '-timeout=120s',
            './internal/cli', '-run', '^TestForward(HTTPClientInformationalResponses|HTTPClientAlreadyCanceledSendsNothing|SourceAdmissionCancellation|StreamAlreadyCanceledDoesNotReadInput)$'], expected=1)
        events = []
        for line in (OUT / 'predecessor-counterexamples.log').read_text().splitlines():
            try: events.append(json.loads(line))
            except ValueError: pass
        actual = {e['Test'] for e in events if e.get('Action') == 'fail' and 'Test' in e and '/' not in e['Test']}
        expected = {'TestForwardHTTPClientInformationalResponses', 'TestForwardHTTPClientAlreadyCanceledSendsNothing',
                    'TestForwardSourceAdmissionCancellation', 'TestForwardStreamAlreadyCanceledDoesNotReadInput'}
        assert actual == expected, (actual, expected)
    finally:
        http.write_bytes(final_http)
        stream.write_bytes(final_stream)
    command('final-default-races', ['go', 'test', '-race', '-json', '-count=1', '-timeout=120s',
        './internal/cli', '-run', '^Test(Forward|LocalForward)'])
    command('final-development-races', ['go', 'test', '-race', '-json', '-count=1', '-timeout=120s',
        '-tags=arm_forward_review', './internal/cli', '-run', '^Test(Forward|LocalForward)'])
    command('carrier-proxy-races', ['go', 'test', '-race', '-json', '-count=1', '-timeout=120s',
        './internal/meshforward', './internal/meshsshrelay'])
    command('focused-vet', ['go', 'vet', './internal/cli', './internal/meshforward', './internal/meshsshrelay'])
    binary = OUT / 'arm-development'
    command('development-build', ['go', 'build', '-tags=arm_forward_review', '-o', str(binary), './cmd/arm'])
    (OUT / 'binary.json').write_text(json.dumps({'sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
        'size': binary.stat().st_size}, indent=2) + '\n')
    # The fixture's PASS is emitted only after owned daemon/service shutdown
    # and removal of its mode-0700 private root. A capacity refusal stays failed.
    command('compiled-three-node-cli', ['python3', '-B', 'scripts/test-routed-forward.py', '--arm', str(binary)])
    binary.unlink()
    command('restored-source', ['git', 'diff', '--exit-code'])
    (OUT / 'ACCEPTED.json').write_text(json.dumps({'commit': commit,
        'scope': 'default-off client on canonical af7c059 foundation; no production enablement or suspend acceptance',
        'commands': records}, indent=2) + '\n')


if __name__ == '__main__':
    main()
