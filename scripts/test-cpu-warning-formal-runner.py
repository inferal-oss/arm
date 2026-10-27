#!/usr/bin/env python3
"""Fail-closed original-module, proof-roster, strict-value and witness controls."""
import copy
import importlib.util
from pathlib import Path
import tempfile

ROOT=Path(__file__).resolve().parents[1]
def load(n,p):
 s=importlib.util.spec_from_file_location(n,p);m=importlib.util.module_from_spec(s);s.loader.exec_module(m);return m
runner=load('warning_guard_runner',ROOT/'scripts/test-cpu-warning-formal.py');base=runner.base;p=runner.projection
for name in runner.PINNED:runner.guard_source(runner.MODEL/name)
proof=(runner.MODEL/'CPUWarningProof.tla').read_text()
assert base.validate_module_source(runner.MODEL/'CPUWarningProof.tla')==runner.THEOREMS and len(runner.THEOREMS)==31
(ROOT/'.tla').mkdir(exist_ok=True)
with tempfile.TemporaryDirectory(prefix='warning-runner-',dir=ROOT/'.tla') as d:
 path=Path(d)/'CPUWarningProof.tla'
 variants={
  'early-module-end':proof.replace('THEOREM EveryStateStep','====\nTHEOREM EveryStateStep'),
  'trailing-theorem':proof+'\nTHEOREM Ignored == TRUE\nBY OBVIOUS\n',
  'duplicate-theorem':proof.replace('THEOREM StutterSafety','THEOREM InitSafety'),
  'missing-original-theorem':proof.replace('THEOREM FullConfidenceSafety','THEOREM DifferentSafety'),
 }
 for label,text in variants.items():
  path.write_text(text)
  try:base.validate_module_source(path,runner.THEOREMS)
  except ValueError:pass
  else:raise AssertionError('accepted '+label)
 for label,text in {
  'weakened-final-goal':proof.replace('Spec => []Invariant /\\ Protection','Spec => TRUE'),
  'all-skipped-proof':'\n'.join(' OMITTED' if line.lstrip().startswith(('BY ','<1>')) else line for line in proof.splitlines())+'\n',
  'comment-prefix-proof':proof.replace('THEOREM EveryStateStep','(* theorem prefix cannot replace the full module *)\nTHEOREM EveryStateStep'),
 }.items():
  path.write_text(text)
  try:runner.guard_source(path)
  except ValueError:pass
  else:raise AssertionError('accepted '+label)
 model=Path(d)/'CPUWarning.tla';model.write_text((runner.MODEL/'CPUWarning.tla').read_text().replace('Usable /\\ state.frame /\\ ExactP','Usable /\\ ExactP'))
 try:runner.guard_source(model)
 except ValueError:pass
 else:raise AssertionError('accepted weakened measured-use fence')
 config=Path(d)/'Safety.cfg';config.write_text('CONSTANT MaxEpoch=0\nSPECIFICATION Spec\nCHECK_DEADLOCK FALSE\n')
 try:runner.guard_source(config)
 except ValueError:pass
 else:raise AssertionError('accepted empty/unguarded finite model')
for text in ('{"a":1,"a":2}','{"a":{"b":false,"b":0}}','{"a":NaN}','{"a":Infinity}'):
 try:p.parse(text)
 except ValueError:pass
 else:raise AssertionError('accepted non-closed JSON')
# A closed witness has exact primitive types as well as matching field names.
states=[{'phase':'ready'},{'phase':'done'}]
raw=[[i+1,{'state':s,'cursor':i+1}]for i,s in enumerate(states)]
doc={'vars':['cursor','state'],'counterexample':{'state':raw,'action':[[raw[0],{'name':'TraceNext','location':{'beginLine':1,'beginColumn':1,'endLine':1,'endColumn':2,'module':'Actual'}},raw[1]]]}}
runner.witness(doc,states,'Actual')
for label,mutate in (
 ('boolean-index',lambda d:d['counterexample']['state'][0].__setitem__(0,True)),
 ('boolean-cursor',lambda d:d['counterexample']['state'][0][1].__setitem__('cursor',True)),
 ('boolean-location',lambda d:d['counterexample']['action'][0][1]['location'].__setitem__('beginLine',True)),
 ('foreign-module',lambda d:d['counterexample']['action'][0][1]['location'].__setitem__('module','Other')),
 ('missing-edge',lambda d:d['counterexample']['action'].pop())):
 bad=copy.deepcopy(doc);mutate(bad)
 try:runner.witness(bad,states,'Actual')
 except ValueError:pass
 else:raise AssertionError('accepted '+label)
all_names=sorted(runner.command_roster('all'))
assert len(all_names)==57
runner.require_owner_roster(all_names,'all')
for label,names,mode in (
 ('missing-proof-owner',[n for n in all_names if n!='proof'],'all'),
 ('extra-owner',all_names+['unplanned-command'],'all'),
 ('duplicate-owner',all_names+[all_names[0]],'all'),
 ('wrong-mode',all_names,'prove')):
 try:runner.require_owner_roster(names,mode)
 except ValueError:pass
 else:raise AssertionError('accepted '+label)
identity={'pid':0,'pgid':0,'uid':'','start':'','namespace':''}
member={'BeforeUser':0,'BeforeSystem':0,'AfterUser':0,'AfterSystem':0,'Identity':identity,'Cookie':'','ParentCookie':'','ParentPID':0,'User':0,'System':0}
term={'Owner':{'Token':'','Root':identity,'Cookie':''},'RawDelta':0,'Ticks':0,'MatchedProcesses':0,'Members':[member]}
for value in (None,[],[term]):p.native_terms(value)
for label,mutate in (
 ('boolean-term-list',lambda v:True),
 ('boolean-term',lambda v:[True]),
 ('boolean-native-delta',lambda v:[dict(v[0],RawDelta=True)]),
 ('boolean-member-counter',lambda v:[dict(v[0],Members=[dict(v[0]['Members'][0],BeforeUser=True)])])):
 bad=mutate(copy.deepcopy([term]))
 try:p.native_terms(bad)
 except ValueError:pass
 else:raise AssertionError('accepted '+label)
for value in ('2026-02-30T00:00:00Z','2026-13-01T00:00:00Z','2026-01-00T00:00:00Z','2026-01-01T24:00:00Z','2026-01-01T00:60:00Z','2026-01-01T00:00:60Z','2026-01-01T00:00:00+24:00','2026-01-01T00:00:00+01:60'):
 try:p.stamp(value)
 except ValueError:pass
 else:raise AssertionError('accepted impossible Go timestamp '+value)
print('warning runner:9 source/module,4 closed-JSON,5 typed-witness,4 native-roster,4 native-term and8 calendar controls passed')
