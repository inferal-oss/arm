#!/usr/bin/env python3
"""Focused managed-uninstall model/actual/proof gate, original Gate/owner."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import stat
import tempfile

ROOT=Path(__file__).resolve().parents[1]
MODEL=ROOT/'formal/uninstall'
OWNER='221acd10f39f979c0ece89055361fcd860ab1cdd7e4ddeb816c36abbfc758b76'


def load(name,path):
    spec=importlib.util.spec_from_file_location(name,path);module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module


base=load('uninstall_formal_base',ROOT/'scripts/test-formal.py')
projection=load('uninstall_projection',MODEL/'export_actual.py')
negative=load('uninstall_actual_controls',MODEL/'controls_actual.py')


def sha(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def go_log(gate):
    name='TestUninstallActualDevTrace'
    argv=[gate.args.go,'test','-json','-race','-tags','arm_devtrace','-count=1','-timeout=120s','-run','^'+name+'$','./internal/server']
    if not gate.args.actual_go_log:
        env=dict(os.environ,GOMAXPROCS='2',GOTOOLCHAIN='local',GOPROXY='off',GOSUMDB='off')
        return gate.command(argv,'actual-go',env=env,timeout=150)
    path=Path(gate.args.actual_go_log).absolute();st=path.lstat()
    receipt=projection.parse(path.with_name(path.stem+'-owner.json').read_bytes());owner=receipt['owner'];capture=receipt['capture']
    projection.require(stat.S_ISREG(st.st_mode) and stat.S_IMODE(st.st_mode)==0o600 and st.st_size<=16<<20 and
        receipt['schema']=='arm.formal-native-owner/v1' and receipt['source_sha256']==OWNER and
        receipt['argv']==argv and receipt['cwd']==str(ROOT) and receipt['timeout_seconds']==150 and
        receipt['actual_exit']==receipt['expected_exit']==0 and receipt['status']=='passed_native_command_only' and
        owner['reaped'] and owner['retired'] and not owner['authority'] and owner['native_wait_calls']==1 and owner['observed_exit']==0 and
        owner['unknown'] is None and owner['signals']==[] and owner['post_wait_absence']=={'pid':'absent','group':'absent'} and
        all(receipt[k] is None for k in ('cleanup_error','cancellation_received','cancellation_pending')) and capture['path_matches_open_descriptor'] and
        (st.st_dev,st.st_ino,st.st_uid,st.st_nlink,st.st_size)==tuple(capture[k]for k in ('device','inode','uid','links','bytes')),'retained original Go receipt/log mismatch')
    # The source-owning caller retained exact tested files before/after. Reusing
    # Go requires all those Go/module/helper bytes remain equal; Python/model
    # reader corrections do not require repeating already-passed test bodies.
    prefix=path.parent.parent
    before=projection.parse((prefix/'before.json').read_bytes())['selected_source']
    after=projection.parse((prefix/'after.json').read_bytes())['selected_source']
    projection.require(before==after and all(sha(ROOT/n)==h for n,h in before.items()if n.endswith('.go')or n in ('go.mod','go.sum','scripts/formal_process.py')),'tested Go source changed')
    gate.summary['retained_actual_go']={'path':str(path),'sha256':sha(path),'original_owner':str(path.with_name(path.stem+'-owner.json'))}
    return path.read_text()


def observed_module(states,name):
    return ('---- MODULE '+name+' ----\nEXTENDS Uninstall, Sequences\nVARIABLE cursor\nObserved == <<'+
      ',\n'.join(base.tla_value(s)for s in states)+'>>\n'
      'TraceInit == cursor=1 /\\ Init /\\ state=Observed[1]\n'
      "TraceNext == cursor<Len(Observed) /\\ cursor'=cursor+1 /\\ [Next]_vars /\\ state'=Observed[cursor+1]\n"
      'TraceSpec == TraceInit /\\ [][TraceNext]_<<state,cursor>>\n'
      'NotFinal == cursor#Len(Observed)\n====\n')


def traces(gate):
    log=go_log(gate);expected={'TestUninstallActualDevTrace'}|{'TestUninstallActualDevTrace/'+n for n in projection.SCENARIOS}
    ran=[];passed=[];outputs={};packages=0
    for line in log.splitlines():
        e=projection.parse(line)
        projection.require(e.get('Package')=='github.com/yrashk/arm/internal/server' and e.get('Action')not in ('fail','skip','build-fail'),'actual Go package/failure')
        if 'Test'in e:projection.require(e['Test']in expected,'foreign Go test')
        if e['Action']=='run':ran.append(e['Test'])
        if e['Action']=='pass':
            if 'Test'in e:passed.append(e['Test'])
            else:packages+=1
        if 'Output'in e and'Test'in e:outputs[e['Test']]=outputs.get(e['Test'],'')+e['Output']
    projection.require(set(ran)==set(passed)==expected and len(ran)==len(passed)==len(expected)and packages==1,'all six actual named outcomes required')
    histories={};counts=[]
    for index,scenario in enumerate(projection.SCENARIOS,1):
        rows=[]
        for line in outputs['TestUninstallActualDevTrace/'+scenario].splitlines():
            if 'UNINSTALL_ACTUAL 'in line:
                match=re.fullmatch(r'\s+uninstall_trace_dev_test.go:\d+: UNINSTALL_ACTUAL (.+)',line)
                projection.require(match is not None,'raw actual source prefix')
                rows.append(projection.parse(match[1]))
        histories[scenario]=rows;value=projection.project(rows,scenario)
        (gate.run/(scenario+'-raw.json')).write_text(json.dumps(rows,indent=2)+'\n')
        (gate.run/(scenario+'-projection.json')).write_text(json.dumps(value,indent=2)+'\n')
        name='Actual'+str(index);module=gate.run/(name+'.tla');module.write_text(observed_module(value['states'],name))
        config=gate.run/(name+'.cfg');config.write_text('SPECIFICATION TraceSpec\nINVARIANT Invariant\nINVARIANT NotFinal\nCHECK_DEADLOCK FALSE\n')
        witness=gate.run/(name+'-witness.json')
        text=gate.tlc(name+'.tla',config,name,('-dumpTrace','json',witness),expected=12)
        projection.require('Invariant NotFinal is violated.'in text,'complete actual final witness absent')
        states=base.tlc_states(witness,variables=('state','cursor'),validator=lambda x:x)
        projection.require(len(states)==len(value['states'])and all(s['cursor']==i+1 and s['state']==value['states'][i]for i,s in enumerate(states)),'full state/edge/final mismatch')
        counts.append({'scenario':scenario,'states':len(states),'edges':len(states)-1})
    return {'actual_paths':counts,'rejected_alterations':negative.controls(histories,projection)}


def main():
    p=argparse.ArgumentParser();p.add_argument('--mode',choices=('check','traces','prove','all'),default='all')
    p.add_argument('--go',default='go');p.add_argument('--java',default='java');p.add_argument('--tlc-jar',default=str(ROOT/'.tla/tools/tlc/tla2tools.jar'))
    p.add_argument('--tlapm',default=str(ROOT/'.tla/tools/tlapm/tlapm/bin/tlapm'));p.add_argument('--actual-go-log');p.add_argument('--work-dir',default=str(ROOT/'.tla/uninstall'))
    args=p.parse_args();args.targets=[];os.umask(0o077)
    work=Path(args.work_dir).absolute();projection.require(work.is_relative_to(ROOT/'.tla'),'private .tla work required')
    current=ROOT
    for part in work.relative_to(ROOT).parts:
        current/=part;projection.require(not current.is_symlink(),'work symlink');current.mkdir(mode=0o700,exist_ok=True)
    gate=base.Gate.__new__(base.Gate);gate.args=args;gate.run=Path(tempfile.mkdtemp(prefix='run-',dir=work));gate.java_tmp=gate.run/'java-tmp';gate.java_tmp.mkdir(mode=0o700)
    gate.summary={'scope':'Private uninstall component, actual real Stop/native callback, separately scoped original-publication/helper-entry premises; no SSH/UI acceptance','tools':{},'gates':[]}
    gate.capture_runner_sources()
    for path in (Path(__file__).resolve(),MODEL/'export_actual.py',MODEL/'controls_actual.py'):gate.runner_sources[str(path)]=sha(path)
    gate.summary['runner_sources']=dict(gate.runner_sources)
    projection.require(sha(ROOT/'scripts/formal_process.py')==OWNER,'original owner changed')
    for path in MODEL.iterdir():
        if path.suffix in ('.tla','.cfg'):shutil.copyfile(path,gate.run/path.name)
    try:
        gate.tools()
        if args.mode in ('check','all'):
            log=gate.tlc('Uninstall.tla',gate.run/'Safety.cfg','safety')
            found=re.findall(r'(\d+) distinct states found, (\d+) states left on queue',log)
            projection.require('Model checking completed. No error has been found.'in log and found and int(found[-1][0])>0 and int(found[-1][1])==0,'finite check incomplete')
            gate.summary['gates'].append({'finite_distinct':int(found[-1][0])})
        if args.mode in ('traces','all'):gate.summary['gates'].append(traces(gate))
        if args.mode in ('prove','all'):
            log=gate.command([args.tlapm,'--strict','--nofp','--threads','2','--stretch','10','--cache-dir',gate.run/'proof-cache',gate.run/'UninstallProof.tla'],'proof',timeout=300)
            found=re.findall(r'All (\d+) obligations proved',log);projection.require(found and '[ERROR]'not in log,'proof incomplete')
            gate.summary['gates'].append({'proof_obligations':int(found[-1])})
    except BaseException as e:gate.summary['error']=str(e);raise
    finally:(gate.run/'summary.json').write_text(json.dumps(gate.summary,indent=2)+'\n');print(gate.run,flush=True)


if __name__=='__main__':main()
