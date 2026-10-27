#!/usr/bin/env python3
"""Scoped composed VNC gates; real admission is never replaced or overridden."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import time

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / '.cache/screen-resource-ci'
OUT.mkdir(parents=True, exist_ok=True)
commands = []


def run(name, argv, tolerate=False):
    started = time.monotonic()
    with (OUT / (name + '.log')).open('w') as log:
        result = subprocess.run(argv, cwd=ROOT, stdout=log, stderr=subprocess.STDOUT)
    commands.append({'check': name, 'exit': result.returncode,
                     'seconds': time.monotonic() - started})
    (OUT / 'commands.json').write_text(json.dumps(commands, indent=2) + '\n')
    if result.returncode and not tolerate:
        raise RuntimeError(f'{name} failed: exit {result.returncode}')
    return result.returncode


def events(path):
    rows = []
    for line in path.read_text().splitlines():
        try:
            rows.append(json.loads(line))
        except ValueError:
            pass
    return rows


def diagnose_refusals(rows):
    failed = {r['Test'].split('/')[0] for r in rows
              if r.get('Action') == 'fail' and 'Test' in r}
    refused = {r['Test'].split('/')[0] for r in rows
               if 'Test' in r and 'insufficient_capacity' in r.get('Output', '')}
    selected = sorted(failed & refused)
    if not selected:
        return
    source = (ROOT / 'internal/server/screen_resources.go').read_text()
    source = source.replace('"context"', '"context"\n "encoding/json"', 1)
    before = '\thold, err := s.Manager.AcquireRelayTransit(ctx, model.Request{'
    after = '\t}, joined)\n\tif err != nil {'
    assert source.count(before) == 1 and source.count(after) == 1
    source = source.replace(before, '\tscreenBudgetDiagnostic(s, "before-normal-acquire")\n' + before, 1)
    source = source.replace(after, '\t}, joined)\n\tscreenBudgetDiagnostic(s, "after-normal-acquire")\n\tif err != nil {', 1)
    source += '''
func screenBudgetDiagnostic(s *Server, phase string) {
 status,se:=s.Manager.Status();policy,pe:=s.Manager.BudgetInspect("self")
 data,_:=json.Marshal(map[string]any{"phase":phase,"cpu_budget":status.CPU,"memory_budget":status.Memory,"disk_budget":status.Disk,"capacity":status.Capacity,"reserved":status.Reserved,"draining":status.Draining,"policy_limits":policy.Budget.Limits,"policy_available":policy.Available,"policy_charged":policy.Charged,"policy_deficit":policy.Deficit,"status_read_ok":se==nil,"policy_read_ok":pe==nil})
 log.Printf("ARM screen admission diagnostic: %s",data)
}
'''
    replacement = OUT / 'diagnostic-overlay.go'
    replacement.write_text(source)
    mapping = OUT / 'diagnostic-overlay.json'
    mapping.write_text(json.dumps({'Replace': {str(ROOT / 'internal/server/screen_resources.go'): str(replacement)}}, indent=2) + '\n')
    selector = '^(' + '|'.join(re.escape(name) for name in selected) + ')$'
    code = run('admission-refusal-diagnostic', ['go', 'test', '-overlay=' + str(mapping),
        '-race', '-json', '-count=1', '-timeout=120s', './internal/server', '-run', selector], tolerate=True)
    (OUT / 'DIAGNOSTIC_ONLY.json').write_text(json.dumps({'failed_resource_roots': selected,
        'exit': code, 'overlay_sha256': hashlib.sha256(replacement.read_bytes()).hexdigest(),
        'scope': 'Separate later attempt: actual native CPU/memory/disk/policy reads bracket unchanged normal .05CPU/8MiB Acquire. No predicate/request override. It cannot reconstruct the earlier missing atomic failure snapshot or replace the failed pristine gate.',
        'acceptance': False}, indent=2) + '\n')


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
    # Collect independent remaining gates even after an original screen failure;
    # no failing command is relabelled and no acceptance record is written then.
    failed = []
    if run('screen-races', ['go', 'test', '-race', '-json', '-count=1', '-timeout=120s',
                           './internal/server', '-run', '^Test(Screen|BrowserScreen)'], tolerate=True):
        failed.append('screen-races')
    if run('original-transit-races', ['go', 'test', '-race', '-json', '-count=1', '-timeout=120s',
                                    './internal/manager', '-run', '^TestRelay(Transit|Cleanup)'], tolerate=True):
        failed.append('original-transit-races')
    if run('affected-vet', ['go', 'vet', './internal/server', './internal/manager'], tolerate=True):
        failed.append('affected-vet')
    binary = OUT / 'arm-review'
    if run('composed-command-build', ['go', 'build', '-o', str(binary), './cmd/arm'], tolerate=True):
        failed.append('composed-command-build')
    if binary.exists():
        (OUT / 'binary.json').write_text(json.dumps({'sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
                                                    'size': binary.stat().st_size}, indent=2) + '\n')
        binary.unlink()
    if 'screen-races' in failed:
        diagnose_refusals(events(OUT / 'screen-races.log'))
    run('source-unchanged', ['git', 'diff', '--exit-code'])
    result = {'source': commit, 'failed_checks': failed, 'commands': commands,
              'scope': 'Root-accepted API baseline plus VNC caller: original close retention, real .05CPU/8MiB, bounded Manager close/restart and normal worker lifecycle. No desktop, native clock/suspend, High Performance or complete screen-feature acceptance.'}
    (OUT / ('FAILED.json' if failed else 'ACCEPTED.json')).write_text(json.dumps(result, indent=2) + '\n')
    if failed:
        raise RuntimeError('Pristine gates failed: ' + ', '.join(failed))


if __name__ == '__main__':
    main()
