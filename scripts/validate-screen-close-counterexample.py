#!/usr/bin/env python3
"""Reproduce one original-TCP-close fault; this is not feature acceptance."""
import hashlib
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / '.cache/screen-close-counterexample'
OUT.mkdir(parents=True, exist_ok=True)
commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
assert commit == os.environ['GITHUB_SHA']
files = subprocess.check_output(['git', 'ls-files', '-z'], cwd=ROOT).split(b'\0')
manifest = {os.fsdecode(p): hashlib.sha256((ROOT / os.fsdecode(p)).read_bytes()).hexdigest()
            for p in files if p and (ROOT / os.fsdecode(p)).is_file()}
(OUT / 'source.json').write_text(json.dumps({'source': commit, 'files': manifest}, indent=2) + '\n')
with (OUT / 'counterexample.log').open('w') as log:
    result = subprocess.run(['go', 'test', '-race', '-json', '-count=1', '-timeout=45s',
        './internal/server', '-run', '^TestScreenResourceUnknownNativeCloseRetainsCharge$'],
        cwd=ROOT, stdout=log, stderr=subprocess.STDOUT)
events = []
for line in (OUT / 'counterexample.log').read_text().splitlines():
    try:
        events.append(json.loads(line))
    except ValueError:
        pass
failed = [e['Test'] for e in events if e.get('Action') == 'fail' and 'Test' in e]
marker = any('COUNTEREXAMPLE: original native TCP close remains unconfirmed and descriptor open'
             in e.get('Output', '') for e in events)
record = {'source': commit, 'exit': result.returncode, 'failed_roots': failed,
          'counterexample_observed': marker,
          'scope': 'One injected original TCP Close error in actual browser handler with real admitted .05CPU/8MiB and duplex traffic. Expected defect reproduction is not normal close failure or feature acceptance.'}
(OUT / 'result.json').write_text(json.dumps(record, indent=2) + '\n')
assert result.returncode == 1 and failed == ['TestScreenResourceUnknownNativeCloseRetainsCharge'] and marker, record
print('Expected native-close counterexample reproduced. This is not product acceptance.')
