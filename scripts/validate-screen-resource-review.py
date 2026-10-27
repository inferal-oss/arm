#!/usr/bin/env python3
"""Portable, scoped screen resource-hold validation on disposable CI runners.

Evidence contains technical source identities/results only. Native CPU admission
is unchanged. No desktop service is configured and no installed ARM is used.
"""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / '.cache/screen-resource-ci'
OUT.mkdir(parents=True, exist_ok=True)
commands = []


def run(name, argv):
    started = time.monotonic()
    with (OUT / (name + '.log')).open('w') as log:
        result = subprocess.run(argv, cwd=ROOT, stdout=log, stderr=subprocess.STDOUT)
    commands.append({'check': name, 'exit': result.returncode,
                     'seconds': time.monotonic() - started})
    (OUT / 'commands.json').write_text(json.dumps(commands, indent=2) + '\n')
    if result.returncode:
        raise RuntimeError(f'{name} failed: exit {result.returncode}')


def main():
    commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    assert commit == os.environ['GITHUB_SHA']
    files = subprocess.check_output(['git', 'ls-files', '-z'], cwd=ROOT).split(b'\0')
    manifest = {os.fsdecode(p): hashlib.sha256((ROOT / os.fsdecode(p)).read_bytes()).hexdigest()
                for p in files if p and (ROOT / os.fsdecode(p)).is_file()}
    (OUT / 'source.json').write_text(json.dumps({'commit': commit,
        'tree': subprocess.check_output(['git', 'rev-parse', 'HEAD^{tree}'], cwd=ROOT, text=True).strip(),
        'files': manifest}, indent=2) + '\n')
    run('go-version', ['go', 'version'])
    run('go-platform', ['go', 'env', 'GOOS', 'GOARCH', 'CGO_ENABLED'])
    run('screen-races', ['go', 'test', '-race', '-json', '-count=1', '-timeout=120s',
                        './internal/server', '-run', '^Test(Screen|BrowserScreen)'])
    run('original-transit-races', ['go', 'test', '-race', '-json', '-count=1', '-timeout=120s',
                                  './internal/manager', '-run', '^TestRelayTransit'])
    run('affected-vet', ['go', 'vet', './internal/server', './internal/manager'])
    binary = OUT / 'arm-review'
    run('composed-command-build', ['go', 'build', '-o', str(binary), './cmd/arm'])
    (OUT / 'binary.json').write_text(json.dumps({'sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
                                                'size': binary.stat().st_size}, indent=2) + '\n')
    binary.unlink()
    run('source-unchanged', ['git', 'diff', '--exit-code'])
    (OUT / 'ACCEPTED.json').write_text(json.dumps({'source': commit, 'commands': commands,
        'scope': 'Cooperative endpoint transport accounting and original worker lifecycle; no native desktop, suspend, High Performance, or complete screen-feature acceptance.'}, indent=2) + '\n')


if __name__ == '__main__':
    main()
