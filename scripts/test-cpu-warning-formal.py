#!/usr/bin/env python3
"""Scoped CPU warning confidence: complete finite safety, actual Go, strict proof."""
import argparse
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import sys
import stat

ROOT=Path(__file__).resolve().parents[1]
MODEL=ROOT/'formal/cpu-warning'
def load(name,path):
    spec=importlib.util.spec_from_file_location(name,path);module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module
base=load('warning_formal_base',ROOT/'scripts/test-formal.py')
projection=load('warning_actual',MODEL/'export_actual.py')
# Complete reviewed modules/config, not a prefix, theorem fingerprint or
# runtime-generated replacement. Update only with reviewed source/proof changes.
PINNED={'CPUWarning.tla': '8cf01fc827e5d44943cc84ecc4cf0e7daf391a9c7e60f039f6d7d638d4f22528', 'CPUWarningProof.tla': 'f93919037bacd6a53a14b9ef2b922f454636d3f82e2c210e9660dd86f34b8451', 'Safety.cfg': 'f100ba704aea8aebdf6aecf5481ecebb51588d87b5bb62100c8ea54ef49e9df3'}
THEOREMS=['InitSafety', 'EvaluationReadOnly', 'SnapshotReadOnly', 'BareReadOnly', 'SameComputations', 'ScalarType', 'NumericType', 'UnknownNotHealthy', 'RecoveryUsesLower', 'ShortfallFence', 'MeasuredOwnerFence', 'PossibleOwnerFence', 'PhysicalRecovery', 'ClassifierConfidence', 'EvaluateType', 'EvaluateOutputs', 'EvaluateBinding', 'EvaluateConfidence', 'EvaluationSafety', 'FaultSafety', 'BareSafety', 'BackgroundSafety', 'CaptureSafety', 'AgeSafety', 'RestartSafety', 'StutterSafety', 'EveryStateStep', 'EveryProtectionStep', 'BareNeverSuppliesCoverage', 'StateSafety', 'FullConfidenceSafety']
OBLIGATIONS=125

def digest(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()
def guard_source(path):
    path=Path(path);projection.require(path.name in PINNED and path.is_file() and not path.is_symlink() and digest(path)==PINNED[path.name],'original complete warning model/proof/config bytes required')
    if path.suffix=='.tla':base.validate_module_source(path)

def sources():
    files=[ROOT/'go.mod',ROOT/'go.sum']
    for part in ('manager','model','observe','usage','pressure','syncservice','syncfs'):files.extend((ROOT/'internal'/part).glob('*.go'))
    return {str(p.relative_to(ROOT)):digest(p) for p in sorted(files)}

def witness(doc,states,module):
    projection.require(type(doc) is dict and set(doc)=={'vars','counterexample'} and type(doc['vars']) is list and len(doc['vars'])==2 and all(type(v) is str for v in doc['vars']) and set(doc['vars'])=={'state','cursor'},'closed TLC variable roster')
    ce=doc['counterexample'];projection.require(type(ce) is dict and set(ce)=={'state','action'} and type(ce['state']) is list and type(ce['action']) is list and len(ce['state'])==len(states) and len(ce['action'])==len(states)-1,'complete exact TLC state/edge roster')
    for index,item in enumerate(ce['state']):projection.require(projection.typed(item)==projection.typed([index+1,{'state':states[index],'cursor':index+1}]),'exact TLC full-state ordinal/types')
    for index,edge in enumerate(ce['action']):
        projection.require(type(edge) is list and len(edge)==3 and projection.typed(edge[0])==projection.typed(ce['state'][index]) and projection.typed(edge[2])==projection.typed(ce['state'][index+1]) and type(edge[1]) is dict and set(edge[1])=={'name','location'} and edge[1]['name']=='TraceNext','exact adjacent TLC edge/source')
        loc=edge[1]['location'];projection.require(type(loc) is dict and set(loc)=={'beginLine','beginColumn','endLine','endColumn','module'} and loc['module']==module and all(type(loc[k]) is int and loc[k]>0 for k in ('beginLine','beginColumn','endLine','endColumn')),'original TLC action location')

def path_module(gate,module,states):
    (gate.run/(module+'.tla')).write_text('---- MODULE '+module+' ----\nEXTENDS CPUWarning, Sequences\nVARIABLE cursor\nObserved == <<'+',\n'.join(base.tla_value(s) for s in states)+'>>\nTraceInit == cursor=1 /\\ Init /\\ state=Observed[1]\nTraceNext == cursor<Len(Observed) /\\ cursor\'=cursor+1 /\\ [Next]_vars /\\ state\'=Observed[cursor+1]\nTraceSpec == TraceInit /\\ [][TraceNext]_<<state,cursor>>\nNotFinal == cursor#Len(Observed)\n====\n')
    config=gate.run/(module+'.cfg');config.write_text('CONSTANT MaxEpoch=3\nSPECIFICATION TraceSpec\nINVARIANT Invariant\nINVARIANT NotFinal\nPROPERTY Protection\nCHECK_DEADLOCK FALSE\n');return config

def traces(gate):
    before=sources();name='TestCPUWarningActualTrace'
    cache=Path(gate.args.go_cache).absolute();projection.require(cache.is_relative_to(ROOT/'.tla') and not any(p.is_symlink() for p in (cache,*cache.parents) if p.is_relative_to(ROOT)),'Go cache must stay under owned ignored .tla without symlinks')
    env=dict(os.environ,GOCACHE=str(cache),GOMAXPROCS='1',GOTOOLCHAIN='local',GOPROXY='off',GOSUMDB='off',PYTHONDONTWRITEBYTECODE='1')
    argv=[gate.args.go,'test','-p=1','-json','-race','-tags','arm_devtrace','-count=1','-timeout=180s','-run','^'+name+'$','./internal/manager']
    text=gate.command(argv,'actual-go',env=env,timeout=300)
    projection.require(sources()==before,'actual Go source closure changed during command');gate.summary['actual_sources']=before
    expected={name}|{name+'/'+s for s in projection.SCENARIOS};ran=[];passed=[];outputs={};packages=0
    for line in text.splitlines():
        e=projection.parse(line);projection.require(e.get('Package')=='github.com/yrashk/arm/internal/manager' and e.get('Action') not in ('fail','skip','build-fail'),'actual package/failure/skip')
        if 'Test'in e:projection.require(e['Test'] in expected,'foreign actual test')
        if e['Action']=='run':ran.append(e['Test'])
        if e['Action']=='pass':
            if 'Test'in e:passed.append(e['Test'])
            else:packages+=1
        if 'Test'in e and 'Output'in e:outputs[e['Test']]=outputs.get(e['Test'],'')+e['Output']
    projection.require(set(ran)==set(passed)==expected and len(ran)==len(passed)==len(expected) and packages==1,'all exact actual test roots/scenarios must pass once')
    histories={};counts=[];witness_controls=[]
    for number,scenario in enumerate(projection.SCENARIOS,1):
        rows=[]
        for line in outputs[name+'/'+scenario].splitlines():
            if 'WARNING_ACTUAL 'in line:
                m=re.fullmatch(r'\s+cpu_warning_trace_dev_test.go:\d+: WARNING_ACTUAL (.+)',line);projection.require(m is not None,'original actual Go source prefix');rows.append(projection.parse(m[1]))
        histories[scenario]=rows;value=projection.project(rows,scenario);states=value['states']
        (gate.run/(scenario+'-raw.json')).write_text(json.dumps(rows,indent=2)+'\n');(gate.run/(scenario+'-projection.json')).write_text(json.dumps(value,indent=2)+'\n')
        module='Actual'+str(number);config=path_module(gate,module,states);path=gate.run/(module+'-witness.json')
        log=gate.tlc(module+'.tla',config,module,('-dumpTrace','json',path),expected=12)
        projection.require('Invariant NotFinal is violated.'in log,'complete actual final witness absent');doc=base.read_json(path);witness(doc,states,module)
        if number==1:
            for label,mutate in (
                ('boolean-state-index',lambda d:d['counterexample']['state'][0].__setitem__(0,True)),
                ('boolean-edge-cursor',lambda d:d['counterexample']['action'][0][0][1].__setitem__('cursor',True)),
                ('missing-final-state',lambda d:d['counterexample']['state'].pop()),
                ('missing-edge',lambda d:d['counterexample']['action'].pop()),
                ('foreign-action-module',lambda d:d['counterexample']['action'][0][1]['location'].__setitem__('module','Other'))):
                bad=copy.deepcopy(doc);mutate(bad)
                try:witness(bad,states,module)
                except ValueError:witness_controls.append(label);continue
                raise ValueError('accepted malformed TLC witness: '+label)
        counts.append(dict(scenario=scenario,calls=value['calls'],states=len(states),edges=len(states)-1,raw_rows=len(rows)))
    negative=[]
    for scenario,call,label,change in (
        ('bare',1,'bare-is-health',dict(result='recovery',known=True,minimum=1000)),
        ('inline-full',0,'possible-inline-is-measured',dict(result='recovery',known=True,credit=250,minimum=250)),
        ('process-frame',1,'ineligible-frame-credit',dict(result='recovery',known=True,credit=999,minimum=999)),
        ('process-retained',1,'lost-owner-shortfall',dict(result='shortfall',known=True,maximum=0)),
        ('physical-idle',2,'lost-owner-poisons-physical-idle',dict(result='uncertain',known=False)),
        ('fractional',0,'fractional-recovery-rounds-down',dict(result='recovery',known=True)),
        ('sum17',0,'possible-sum-rounds-inward',dict(result='shortfall',known=True,possible=750,maximum=750)),
        ('snapshot',1,'background-replaces-frozen-observation',dict(result='shortfall',known=True,minimum=0,maximum=0)),
        ('inline-after-snapshot',1,'new-owner-inherits-old-snapshot',dict(possible=250,maximum=1000)),
        ('inline-volume',1,'changed-volume-retains-use',dict(possible=250,maximum=1000)),
        ('process-cap-bare',0,'borrow-idle-bare-promise',dict(credit=2999,minimum=1000)),
        ('process-credit',0,'warning-mints-authority',dict(admission=True,signal=True,cleanup=True))):
        states=projection.project(histories[scenario],scenario)['states'][:2*call+2];states=copy.deepcopy(states);states[-1].update(change)
        module='Negative'+str(len(negative)+1);config=path_module(gate,module,states);log=gate.tlc(module+'.tla',config,module)
        complete=re.findall(r'(\d+) distinct states found, (\d+) states left on queue',log)
        projection.require('Model checking completed. No error has been found.'in log and complete and 0<int(complete[-1][0])<len(states) and complete[-1][1]=='0','altered model path reached final state: '+label);negative.append(label)
    projection.require(sources()==before,'actual Go source closure changed before trace gate completed')
    return dict(actual_paths=counts,rejected_raw=projection.controls(histories),rejected_model_paths=negative,rejected_witnesses=witness_controls)

def validate_tools(gate):
    lock=projection.parse((ROOT/'formal/tools.lock.json').read_text())
    if 'tlc'in gate.summary['tools']:projection.require(gate.summary['tools']['tlc']['sha256']==lock['tlc']['artifact']['sha256'],'pinned TLC release required')
    if 'tlapm'in gate.summary['tools']:
        provision=load('warning_provision',ROOT/'scripts/provision-tla.py');package=Path(gate.args.tlapm).resolve().parents[2];receipt=package/'.arm-tool.json'
        projection.require(receipt.is_file() and not receipt.is_symlink() and receipt.stat().st_size<=128<<10,'original provisioned prover receipt required')
        r=projection.parse(receipt.read_text());platform=provision.platform.system().lower()+'-'+{'aarch64':'arm64','arm64':'arm64','x86_64':'amd64','AMD64':'amd64'}.get(provision.platform.machine(),provision.platform.machine());artifact=lock['tlapm']['artifacts'][platform]
        projection.require(r['schema']==1 and r['version']==lock['tlapm']['version'] and r['archive_sha256']==artifact['sha256'] and r['files']==provision.inventory(package),'complete original prover/backend/library bundle required')
        projection.require(all(Path(r['path']).is_relative_to(package) for name,r in gate.summary['tools'].items() if name.startswith('tlapm-')),'actual backend belongs to verified bundle')
        gate.summary['prover_bundle']=dict(root=str(package),receipt_sha256=digest(receipt),archive_sha256=r['archive_sha256'])

def command_roster(mode):
    projection.require(mode in ('check','traces','prove','all'),'closed formal command mode')
    names=['runner-controls']
    if mode in ('check','traces','all'):names+=['java-version','tlc-version']
    if mode in ('traces','all'):names+=['go-version','actual-go']+['Actual'+str(n) for n in range(1,len(projection.SCENARIOS)+1)]+['Negative'+str(n) for n in range(1,13)]
    if mode in ('check','all'):names+=['safety']
    if mode in ('prove','all'):names+=['tlapm-version','tlapm-config','tlapm-z3-version','proof-scope','proof']
    return set(names)

def require_owner_roster(names,mode):
    expected=command_roster(mode)
    projection.require(len(names)==len(expected) and set(names)==expected,'complete exact mode-specific native command owner roster required')

def owners(gate):
    paths=list(gate.run.glob('*-owner.json'))
    require_owner_roster([p.name[:-len('-owner.json')] for p in paths],gate.args.mode)
    count=0
    for path in paths:
        r=projection.parse(path.read_text());o=r['owner']
        projection.require(r['schema']=='arm.formal-native-owner/v1' and r['source_sha256']==digest(ROOT/'scripts/formal_process.py') and r['status']=='passed_native_command_only' and r['actual_exit']==r['expected_exit'] and r['cleanup_error'] is None and o['retired'] and o['reaped'] and not o['authority'] and type(o['native_wait_calls']) is int and o['native_wait_calls']==1 and o['unknown'] is None and o['signals']==[] and o['post_wait_absence']=={'pid':'absent','group':'absent'},'original formal native owner incomplete')
        capture=r['capture'];log=gate.run/(path.name[:-len('-owner.json')]+'.log');info=log.lstat()
        projection.require(stat.S_ISREG(info.st_mode) and stat.S_IMODE(info.st_mode)==0o600 and all(type(capture[k]) is int for k in ('device','inode','uid','links','bytes')) and capture['path_matches_open_descriptor'] and (info.st_dev,info.st_ino,info.st_uid,info.st_nlink,info.st_size)==tuple(capture[k] for k in ('device','inode','uid','links','bytes')),'original native output capture binding')
        count+=1
    projection.require(count>0,'native command owner receipts absent');return count

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--mode',choices=('check','traces','prove','all'),default='all');parser.add_argument('--go',default='go');parser.add_argument('--java',default='java');parser.add_argument('--tlc-jar',default=str(ROOT/'.tla/tools/tlc/tla2tools.jar'));parser.add_argument('--tlapm',default=str(ROOT/'.tla/tools/tlapm/tlapm/bin/tlapm'));parser.add_argument('--work-dir',default=str(ROOT/'.tla/cpu-warning'));parser.add_argument('--go-cache',default=str(ROOT/'.tla/go-cache'));args=parser.parse_args();args.targets=[];os.umask(0o077)
    base.MODEL=MODEL;gate=base.Gate(args)
    for p in (Path(__file__).resolve(),MODEL/'export_actual.py',ROOT/'scripts/test-cpu-warning-formal-runner.py',ROOT/'scripts/provision-tla.py',ROOT/'formal/tools.lock.json'):gate.runner_sources[str(p)]=digest(p)
    gate.summary['runner_sources']=dict(gate.runner_sources);gate.summary['scope']='CPU warning confidence only: checked controlled numeric/native-input interface and original-owner/SQL cuts; not native/IEEE completeness, admission, cleanup, signal, grace/outbox/pause or whole-program refinement'
    try:
        projection.require(set(gate.summary['sources'])==set(PINNED),'complete original module/config roster')
        for name in PINNED:guard_source(gate.run/name)
        gate.tools();validate_tools(gate)
        gate.command([sys.executable,ROOT/'scripts/test-cpu-warning-formal-runner.py'],'runner-controls',env=dict(os.environ,PYTHONDONTWRITEBYTECODE='1'),timeout=30)
        if args.mode in ('check','all'):
            log=gate.tlc('CPUWarning.tla',gate.run/'Safety.cfg','safety');found=re.findall(r'(\d+) distinct states found, (\d+) states left on queue',log);generated=re.findall(r'(\d+) states generated, (\d+) distinct states found, (\d+) states left on queue',log)
            projection.require('Model checking completed. No error has been found.'in log and found and int(found[-1][0])>0 and found[-1][1]=='0' and generated,'complete finite safety exploration required')
            gate.summary['gates'].append(dict(finite_distinct=int(found[-1][0]),generated=int(generated[-1][0]),queue=0,snapshot_epochs=1))
        if args.mode in ('traces','all'):gate.summary['gates'].append(traces(gate))
        if args.mode in ('prove','all'):
            names=base.validate_module_source(gate.run/'CPUWarningProof.tla');projection.require(names==THEOREMS,'full original theorem roster')
            scope=gate.command([args.tlapm,'--summary','--cache-dir',gate.run/'proof-scope','-I',gate.run,gate.run/'CPUWarningProof.tla'],'proof-scope',timeout=120)
            counts=re.findall(r'^\s*obligations_count\s*=\s*(\d+)\s*$',scope,re.M);modules=re.findall(r'^---- summary of module "([^"]+)" ----$',scope,re.M)
            projection.require(modules==['CPUWarningProof'] and counts==[str(OBLIGATIONS)],'full parser/module obligation roster')
            log=gate.command([args.tlapm,'--strict','--nofp','--threads','1','--stretch','2','--cache-dir',gate.run/'proof-cache','-I',gate.run,gate.run/'CPUWarningProof.tla'],'proof',timeout=240)
            projection.require(re.findall(r'All (\d+) obligations proved\.',log)==counts and '[ERROR]'not in log,'every original proof obligation must be proved')
            gate.summary['gates'].append(dict(proof_obligations=int(counts[0]),theorems=names))
        gate.check_captured_sources()
        for name in PINNED:guard_source(MODEL/name)
        projection.require(gate.runner_sources=={p:digest(p) for p in gate.runner_sources},'captured runner/helper/reader sources changed')
        for r in gate.summary['tools'].values():projection.require(digest(r['path'])==r['sha256'],'captured executable/backend changed')
        validate_tools(gate);gate.summary['native_owners']=owners(gate)
    except BaseException as e:gate.summary['error']=str(e);raise
    finally:gate.finish()
if __name__=='__main__':main()
