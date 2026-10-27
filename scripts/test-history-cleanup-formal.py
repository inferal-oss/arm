#!/usr/bin/env python3
"""Archive cleanup safety, actual Go/SQL/native correspondence and full proof."""
import argparse
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import sys

ROOT=Path(__file__).resolve().parents[1]
MODEL=ROOT/'formal/history-cleanup'
def load(name,path):
    spec=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
base=load('history_formal_base',ROOT/'scripts/test-formal.py')
projection=load('history_actual',MODEL/'export_actual.py')
PINNED={'Safety.cfg': '538d8c2cb6d8738fc0cee7ce9aed6f9a7ff86eb28ffe2e7b9fbda78af7253bf3', 'HistoryCleanupProof.tla': '01609543cc8c48d040b408b683e761956a438c20c074adbe5522786f2ba2732c', 'HistoryCleanup.tla': '7457eef6a4e3f2de2c554693fa6e065f98a9619635f6464908e3d656153c0dfb'}
def guard_source(path):
    path=Path(path)
    projection.require(path.name in PINNED and not path.is_symlink() and path.is_file() and hashlib.sha256(path.read_bytes()).hexdigest()==PINNED[path.name],'original complete model/proof/config bytes required')
    if path.suffix=='.tla':base.validate_module_source(path)


def sources():
    files=[ROOT/'go.mod',ROOT/'go.sum',*ROOT.glob('internal/sshservice/*.go'),*ROOT.glob('internal/historypack/*.go'),*ROOT.glob('internal/database/*.go')]
    return {str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(files)}

def typed(value):return json.dumps(value,sort_keys=True,allow_nan=False)

def validate_witness(doc,states,module):
    projection.require(type(doc) is dict and set(doc)=={'vars','counterexample'} and type(doc['vars']) is list and len(doc['vars'])==2 and all(type(v) is str for v in doc['vars']) and set(doc['vars'])=={'state','cursor'},'exact TLC variable envelope')
    ce=doc['counterexample'];projection.require(type(ce) is dict and set(ce)=={'state','action'} and type(ce['state']) is list and type(ce['action']) is list and len(ce['state'])==len(states) and len(ce['action'])==len(states)-1,'complete TLC state/edge envelope')
    raw=ce['state']
    for j,item in enumerate(raw):projection.require(typed(item)==typed([j+1,{'state':states[j],'cursor':j+1}]),'exact TLC full state position/types')
    for j,edge in enumerate(ce['action']):
        projection.require(type(edge) is list and len(edge)==3 and typed(edge[0])==typed(raw[j]) and typed(edge[2])==typed(raw[j+1]) and type(edge[1]) is dict and set(edge[1])=={'name','location'} and edge[1]['name']=='TraceNext','exact TLC adjacent edge/types')
        location=edge[1]['location'];projection.require(type(location) is dict and set(location)=={'beginLine','beginColumn','endLine','endColumn','module'} and location['module']==module and all(type(location[k]) is int and location[k]>0 for k in ('beginLine','beginColumn','endLine','endColumn')),'exact TLC action source binding')

def traces(gate):
    before=sources();name='TestHistoryCleanupActualTrace'
    env=dict(os.environ,GOMAXPROCS='1',GOCACHE=str(ROOT/'.tla/go-cache'),GOTOOLCHAIN='local',GOPROXY='off',GOSUMDB='off')
    log=gate.command([gate.args.go,'test','-p=1','-json','-race','-tags','arm_devtrace','-count=1','-timeout=180s','-run','^'+name+'$','./internal/sshservice'],'actual-go',env=env,timeout=210)
    projection.require(before==sources(),'actual Go sources changed');gate.summary['actual_sources']=before
    expected={name}|{name+'/'+s for s in projection.SCENARIOS};ran=[];passed=[];outputs={};packages=0
    for line in log.splitlines():
        e=projection.parse(line);projection.require(e.get('Package')=='getarm.org/internal/sshservice' and e.get('Action') not in ('fail','skip','build-fail'),'actual Go package/failure')
        if 'Test' in e:projection.require(e['Test'] in expected,'foreign actual test')
        if e['Action']=='run':ran.append(e['Test'])
        if e['Action']=='pass':
            if 'Test' in e:passed.append(e['Test'])
            else:packages+=1
        if 'Test' in e and 'Output' in e:outputs[e['Test']]=outputs.get(e['Test'],'')+e['Output']
    projection.require(set(ran)==set(passed)==expected and len(ran)==len(passed)==len(expected) and packages==1,'all actual named tests must pass exactly once')
    histories={};counts=[]
    for i,scenario in enumerate(projection.SCENARIOS,1):
        rows=[]
        for line in outputs[name+'/'+scenario].splitlines():
            if 'HISTORY_ACTUAL ' in line:
                match=re.fullmatch(r'\s+history_trace_dev_test.go:\d+: HISTORY_ACTUAL (.+)',line);projection.require(match is not None,'actual source prefix');rows.append(projection.parse(match[1]))
        histories[scenario]=rows;value=projection.project(rows,scenario);states=value['states']
        (gate.run/(scenario+'-raw.json')).write_text(json.dumps(rows,indent=2)+'\n')
        (gate.run/(scenario+'-projection.json')).write_text(json.dumps(value,indent=2)+'\n')
        module='Actual'+str(i)
        (gate.run/(module+'.tla')).write_text('---- MODULE '+module+' ----\nEXTENDS HistoryCleanup, Sequences\nVARIABLE cursor\nObserved == <<'+',\n'.join(base.tla_value(s) for s in states)+'>>\nTraceInit == cursor=1 /\\ Init /\\ state=Observed[1]\nTraceNext == cursor<Len(Observed) /\\ cursor\'=cursor+1 /\\ [Next]_vars /\\ state\'=Observed[cursor+1]\nTraceSpec == TraceInit /\\ [][TraceNext]_<<state,cursor>>\nNotFinal == cursor#Len(Observed)\n====\n')
        config=gate.run/(module+'.cfg');config.write_text('CONSTANT Members='+str(value['members'])+'\nCONSTANT Batch=4\nSPECIFICATION TraceSpec\nINVARIANT Invariant\nINVARIANT NotFinal\nPROPERTY Protection\nCHECK_DEADLOCK FALSE\n')
        witness=gate.run/(module+'-witness.json');text=gate.tlc(module+'.tla',config,module,('-dumpTrace','json',witness),expected=12)
        projection.require('Invariant NotFinal is violated.' in text,'complete actual final witness absent')
        # The shared decoder deliberately bounds traces at128 states. The64
        # native scenario exceeds that bound; validate the complete original
        # JSON state and adjacent-edge roster directly without truncation.
        doc=base.read_json(witness);validate_witness(doc,states,module)
        if i==1:
            for label,mutate in (
                ('boolean-state-index',lambda d:d['counterexample']['state'][0].__setitem__(0,True)),
                ('boolean-edge-cursor',lambda d:d['counterexample']['action'][0][0][1].__setitem__('cursor',True)),
                ('missing-edge',lambda d:d['counterexample']['action'].pop()),
                ('foreign-action-module',lambda d:d['counterexample']['action'][0][1]['location'].__setitem__('module','Other'))):
                bad=copy.deepcopy(doc);mutate(bad)
                try:validate_witness(bad,states,module)
                except ValueError:continue
                raise ValueError('accepted altered TLC witness: '+label)
        counts.append(dict(scenario=scenario,states=len(states),edges=len(states)-1,raw_rows=len(rows),windows=value['windows']))
    controls=[]
    def reject_path(label,states,n):
        module='Negative'+str(len(controls)+1)
        (gate.run/(module+'.tla')).write_text('---- MODULE '+module+' ----\nEXTENDS HistoryCleanup, Sequences\nVARIABLE cursor\nObserved == <<'+',\n'.join(base.tla_value(x) for x in states)+'>>\nTraceInit == cursor=1 /\\ Init /\\ state=Observed[1]\nTraceNext == cursor<Len(Observed) /\\ cursor\'=cursor+1 /\\ [Next]_vars /\\ state\'=Observed[cursor+1]\nTraceSpec == TraceInit /\\ [][TraceNext]_<<state,cursor>>\nNotFinal == cursor#Len(Observed)\n====\n')
        config=gate.run/(module+'.cfg');config.write_text('CONSTANT Members='+str(n)+'\nCONSTANT Batch=4\nSPECIFICATION TraceSpec\nINVARIANT Invariant\nINVARIANT NotFinal\nPROPERTY Protection\nCHECK_DEADLOCK FALSE\n')
        log=gate.tlc(module+'.tla',config,module)
        found=re.findall(r'(\d+) distinct states found, (\d+) states left on queue',log)
        projection.require('Model checking completed. No error has been found.' in log and found and int(found[-1][0])<len(states) and found[-1][1]=='0','unsafe altered path reached its final state: '+label)
        controls.append(label)
    for scenario,stage,label,change in (
        ('same-revision','replace','changed-full-predecessor',lambda x:x.update(window=True)),
        ('false-completed','replace','false-completed-fast-path',lambda x:x.update(certified=True)),
        ('reappear','reappear','checkpointed-reappearance',lambda x:x.update(window=True)),
        ('worker','worker','replaced-worker-continuation',lambda x:x.update(window=True)),
        ('cancel','ack','cancelled-original-context',lambda x:x.update(window=True)),
        ('lost-after','stop','partial-committed-reset',lambda x:x.update(reset=True)),
        ('false-completed','stop','false-completed-partial-reset',lambda x:x.update(reset=True))):
        raw=histories[scenario];value=projection.project(raw,scenario);index=next(i for i,r in enumerate(raw) if r['observation']['stage']==stage)
        states=copy.deepcopy(value['states'][:index+1]);bad=copy.deepcopy(states[-1]);change(bad);states.append(bad);reject_path(label,states,value['members'])
    # The independent reset implementation uses its own parser and native
    # evidence, not the cleanup projector or the abstraction's Reset action.
    root='TestSSHSourceHistoryIndependentProofRefusesMissingForgedOrBusy';positive='TestSSHSourceHistoryIndependentSettledResetRetainsWholeCatalog'
    selected={root,positive}|{root+'/'+n for n in ('op-committed-partial','op-committed-all','op-completed-partial')}
    resetlog=gate.command([gate.args.go,'test','-p=1','-json','-race','-count=1','-timeout=180s','-run','^('+root+'|'+positive+')$/^op-(committed-partial|committed-all|completed-partial)$','./internal/database'],'independent-reset',env=env,timeout=210)
    ran=[];passed=[];packages=0
    for line in resetlog.splitlines():
        e=projection.parse(line);projection.require(e.get('Package')=='getarm.org/internal/database' and e.get('Action') not in ('fail','skip','build-fail'),'independent reset package/failure')
        if 'Test' in e:projection.require(e['Test'] in selected,'foreign independent reset case')
        if e['Action']=='run':ran.append(e['Test'])
        if e['Action']=='pass':
            if 'Test' in e:passed.append(e['Test'])
            else:packages+=1
    projection.require(set(ran)==set(passed)==selected and len(ran)==len(passed)==len(selected) and packages==1 and before==sources(),'complete exact independent reset cases/source binding')
    return dict(actual_paths=counts,rejected_alterations=projection.controls(histories),rejected_model_paths=controls,rejected_witnesses=['boolean-state-index','boolean-edge-cursor','missing-edge','foreign-action-module'],independent_reset=sorted(selected))

def main():
    p=argparse.ArgumentParser();p.add_argument('--mode',choices=('check','traces','prove','all'),default='all');p.add_argument('--go',default='go');p.add_argument('--java',default='java');p.add_argument('--tlc-jar',default=str(ROOT/'.tla/tools/tlc/tla2tools.jar'));p.add_argument('--tlapm',default=str(ROOT/'.tla/tools/tlapm/tlapm/bin/tlapm'));p.add_argument('--work-dir',default=str(ROOT/'.tla/history-cleanup'));args=p.parse_args();args.targets=[];os.umask(0o077)
    base.MODEL=MODEL;gate=base.Gate(args)
    for path in (Path(__file__).resolve(),MODEL/'export_actual.py',ROOT/'scripts/test-history-cleanup-formal-runner.py'):gate.runner_sources[str(path)]=hashlib.sha256(path.read_bytes()).hexdigest()
    gate.summary['runner_sources']=dict(gate.runner_sources);gate.summary['scope']='Cooperative private source-history metadata cleanup; exact row/native observations at guarded cuts, finite safety and parameterized protocol proof; not arbitrary same-UID exclusion or whole-program refinement'
    try:
        projection.require(set(gate.summary['sources'])==set(PINNED),'exact original source file roster')
        for name in PINNED:guard_source(gate.run/name)
        gate.tools();gate.check_captured_sources()
        gate.command([sys.executable,ROOT/'scripts/test-history-cleanup-formal-runner.py'],'runner-controls',env=dict(os.environ,PYTHONDONTWRITEBYTECODE='1'),timeout=30)
        if args.mode in ('check','all'):
            log=gate.tlc('HistoryCleanup.tla',gate.run/'Safety.cfg','safety');found=re.findall(r'(\d+) distinct states found, (\d+) states left on queue',log)
            projection.require('Model checking completed. No error has been found.' in log and found and int(found[-1][0])>0 and found[-1][1]=='0','complete finite safety check required')
            gate.summary['gates'].append(dict(finite_distinct=int(found[-1][0]),members=5,batch=4))
        if args.mode in ('traces','all'):gate.summary['gates'].append(traces(gate))
        if args.mode in ('prove','all'):
            names=base.validate_module_source(gate.run/'HistoryCleanupProof.tla');projection.require(names==['InitSafety']+['Preserve'+n for n in ('Enter','Unlink','Sync','Checkpoint','Ack','Stop','Retry','Reconcile','LoseWorker','Cancel','LoseCold','Reappear','Replace','Reset')]+['StutterSafety','EveryStep','StateSafety','FullScopedSafety'],'full original theorem roster required')
            summary=gate.command([args.tlapm,'--summary','--cache-dir',gate.run/'proof-scope','-I',gate.run,gate.run/'HistoryCleanupProof.tla'],'proof-scope',timeout=120)
            counts=re.findall(r'^\s*obligations_count\s*=\s*(\d+)\s*$',summary,re.M);modules=re.findall(r'^---- summary of module "([^"]+)" ----$',summary,re.M)
            projection.require(modules==['HistoryCleanupProof'] and counts==['80'],'full exact parser obligation roster')
            log=gate.command([args.tlapm,'--strict','--nofp','--threads','1','--stretch','2','--cache-dir',gate.run/'proof-cache','-I',gate.run,gate.run/'HistoryCleanupProof.tla'],'proof',timeout=180)
            projection.require(re.findall(r'All (\d+) obligations proved\.',log)==counts and '[ERROR]' not in log,'all original obligations must be proved')
            gate.summary['gates'].append(dict(proof_obligations=int(counts[0]),theorems=names))
        gate.check_captured_sources()
        for name in PINNED:guard_source(MODEL/name)
        projection.require(gate.runner_sources=={path:hashlib.sha256(Path(path).read_bytes()).hexdigest() for path in gate.runner_sources},'original runner/decoder/helper bytes changed')
        for receipt in gate.summary['tools'].values():projection.require(hashlib.sha256(Path(receipt['path']).read_bytes()).hexdigest()==receipt['sha256'],'captured executable/backend bytes changed')
    except BaseException as e:gate.summary['error']=str(e);raise
    finally:gate.finish()
if __name__=='__main__':main()
