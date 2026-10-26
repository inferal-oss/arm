#!/usr/bin/env python3
"""Fail-closed source scope and strict decoder controls for archive verification."""
import importlib.util
from pathlib import Path
import tempfile

ROOT=Path(__file__).resolve().parents[1]
def load(name,path):
 spec=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
base=load('history_runner_base',ROOT/'scripts/test-formal.py')
projection=load('history_runner_actual',ROOT/'formal/history-cleanup/export_actual.py')
runner=load('history_runner_gate',ROOT/'scripts/test-history-cleanup-formal.py')
proof=(ROOT/'formal/history-cleanup/HistoryCleanupProof.tla').read_text()
expected=base.validate_module_source(ROOT/'formal/history-cleanup/HistoryCleanupProof.tla')
assert len(expected)==19 and expected[-1]=='FullScopedSafety'
with tempfile.TemporaryDirectory(prefix='history-runner-') as directory:
 path=Path(directory)/'HistoryCleanupProof.tla'
 variants={
  'early-terminator':proof.replace('THEOREM EveryStep','====\nTHEOREM EveryStep'),
  'trailing-theorem':proof+'\nTHEOREM Ignored == TRUE\nBY OBVIOUS\n',
  'duplicate-theorem':proof.replace('THEOREM StutterSafety','THEOREM InitSafety'),
  'missing-original-theorem':proof.replace('THEOREM FullScopedSafety','THEOREM DifferentSafety'),
 }
 for name,text in variants.items():
  path.write_text(text)
  try:base.validate_module_source(path,expected)
  except ValueError:pass
  else:raise AssertionError('accepted '+name)
 for name,text in {
  'weakened-invariant':proof.replace('Spec => []Invariant /\\ Protection','Spec => TRUE'),
  'all-skipped-proof':'\n'.join([line+'\n OMITTED' if line.startswith('THEOREM ') else line for line in proof.splitlines() if line.startswith(('---- MODULE','EXTENDS ','ASSUME ','THEOREM ','===='))])+'\n',
 }.items():
  path.write_text(text)
  try:runner.guard_source(path)
  except ValueError:pass
  else:raise AssertionError('accepted '+name)
for raw in ('{"a":1,"a":2}' ,'{"a":{"b":false,"b":0}}'):
 try:projection.parse(raw)
 except ValueError:pass
 else:raise AssertionError('accepted duplicate JSON')
print('history runner: four module-scope, two proof-byte and two duplicate-field controls passed')
