#!/usr/bin/env python3
"""Aligned-credit CPU protection: finite safety, actual Go paths and scoped proof.

Uses the existing native owner and actual Manager/physical-SQL cuts. Native
collector accuracy has separate acceptance. Current managed candidate inputs
are controlled; real original job/lease ownership and decisions are observed.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import stat

ROOT = Path(__file__).resolve().parents[1]
MODEL = ROOT / 'formal/cpu-admission'

def load(name,path):
    spec=importlib.util.spec_from_file_location(name,path)
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    return module

def tla_value(value,base):
    if type(value) is list:
        return '<<'+', '.join(tla_value(v,base) for v in value)+'>>'
    if type(value) is dict:
        return '['+', '.join(k+' |-> '+tla_value(v,base) for k,v in value.items())+']'
    return base.tla_value(value)

def traces(gate,base):
    projection=load('cpu_actual_projection',MODEL/'export_actual.py')
    negative=load('cpu_actual_controls',MODEL/'controls_actual.py')
    name='TestCPUAdmissionActualTrace'
    legacy_argv=[gate.args.go,'test','-json','-race','-tags','arm_devtrace','-count=1','-timeout=90s','-run','^'+name+'$','./internal/manager']
    argv=legacy_argv[:2]+['-p=1']+legacy_argv[2:]
    if gate.args.actual_go_log:
        path=Path(gate.args.actual_go_log).absolute();info=path.lstat()
        receipt=projection.parse(path.with_name(path.stem+'-owner.json').read_bytes())
        owner=receipt['owner'];capture=receipt['capture']
        projection.require(stat.S_ISREG(info.st_mode) and stat.S_IMODE(info.st_mode)==0o600 and info.st_size<=16<<20 and
            receipt['source_sha256']=='221acd10f39f979c0ece89055361fcd860ab1cdd7e4ddeb816c36abbfc758b76' and receipt['argv'] in (argv,legacy_argv) and receipt['cwd']==str(ROOT) and receipt['timeout_seconds']==120 and
            receipt['actual_exit']==receipt['expected_exit']==0 and receipt['status']=='passed_native_command_only' and owner['reaped'] and owner['retired'] and not owner['authority'] and
            owner['native_wait_calls']==1 and owner['unknown'] is None and owner['signals']==[] and owner['post_wait_absence']=={'pid':'absent','group':'absent'} and
            receipt['cleanup_error'] is None and capture['path_matches_open_descriptor'] and
            (info.st_dev,info.st_ino,info.st_uid,info.st_nlink,info.st_size)==tuple(capture[k] for k in ('device','inode','uid','links','bytes')),'retained actual Go owner/capture mismatch')
        log=path.read_text()
        gate.summary['retained_actual_go']=str(path)
    else:
        env=dict(os.environ,GOCACHE=str(ROOT/'.cache/go-build'),GOMAXPROCS='1',GOTOOLCHAIN='local',GOPROXY='off',GOSUMDB='off')
        log=gate.command(argv,'actual-go',env=env,timeout=120)
    expected={name}|{name+'/'+s for s in projection.SCENARIOS};ran=[];passed=[];outputs={};packages=0
    for line in log.splitlines():
        e=projection.parse(line)
        projection.require(e.get('Package')=='github.com/yrashk/arm/internal/manager' and e.get('Action') not in ('fail','skip','build-fail'),'actual Go package/failure')
        if 'Test' in e: projection.require(e['Test'] in expected,'foreign Go test')
        if e['Action']=='run':ran.append(e['Test'])
        if e['Action']=='pass':
            if 'Test' in e:passed.append(e['Test'])
            else:packages+=1
        if 'Output' in e and 'Test' in e:outputs[e['Test']]=outputs.get(e['Test'],'')+e['Output']
    projection.require(set(ran)==set(passed)==expected and len(ran)==len(passed)==len(expected) and packages==1,'all actual named tests required')
    histories={};counts=[]
    for i,scenario in enumerate(projection.SCENARIOS,1):
        rows=[]
        for line in outputs[name+'/'+scenario].splitlines():
            if 'CPU_ACTUAL ' in line:
                match=re.fullmatch(r'\s+cpu_trace_dev_test.go:\d+: CPU_ACTUAL (.+)',line)
                projection.require(match is not None,'actual trace source prefix')
                rows.append(projection.parse(match[1]))
        histories[scenario]=rows;value=projection.project(rows,scenario);states=value['states']
        (gate.run/(scenario+'-raw.json')).write_text(json.dumps(rows,indent=2)+'\n')
        (gate.run/(scenario+'-projection.json')).write_text(json.dumps(value,indent=2)+'\n')
        module='Actual'+str(i)
        (gate.run/(module+'.tla')).write_text('---- MODULE '+module+' ----\nEXTENDS CPUAdmission, Sequences\nVARIABLE cursor\nObserved == <<'+',\n'.join(tla_value(s,base) for s in states)+'>>\nTraceInit == cursor=1 /\\ Init /\\ state=Observed[1]\nTraceNext == cursor<Len(Observed) /\\ cursor\'=cursor+1 /\\ [Next]_vars /\\ state\'=Observed[cursor+1]\nTraceSpec == TraceInit /\\ [][TraceNext]_<<state,cursor>>\nNotFinal == cursor#Len(Observed)\n====\n')
        config=gate.run/(module+'.cfg');config.write_text('SPECIFICATION TraceSpec\nINVARIANT Invariant\nINVARIANT NotFinal\nPROPERTY Protection\nCHECK_DEADLOCK FALSE\n')
        witness=gate.run/(module+'-witness.json')
        text=gate.command([gate.args.java,'-Djava.io.tmpdir='+str(gate.java_tmp),'-XX:+UseSerialGC','-Xmx256m','-cp',gate.args.tlc_jar,'tlc2.TLC','-workers','1','-fp','0','-metadir',gate.run/(module+'-states'),'-noGenerateSpecTE','-config',config,'-dumpTrace','json',witness,gate.run/(module+'.tla')],module,expected=12,timeout=120)
        projection.require('Invariant NotFinal is violated.' in text,'complete final witness absent')
        actual=base.tlc_states(witness,variables=('state','cursor'),validator=lambda s:s)
        projection.require(len(actual)==len(states) and all(s['cursor']==j+1 and s['state']==states[j] for j,s in enumerate(actual)),'complete state/Next/final correspondence')
        counts.append({'scenario':scenario,'states':len(states),'edges':len(states)-1,'raw_rows':len(rows)})
    return {'actual_paths':counts,'rejected_alterations':negative.controls(histories,projection)}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--mode', choices=('check', 'traces', 'prove', 'all'), default='all')
    p.add_argument('--go',default='go')
    p.add_argument('--actual-go-log')
    p.add_argument('--java', default='java')
    p.add_argument('--tlc-jar', default=str(ROOT / '.tla/tools/tlc/tla2tools.jar'))
    p.add_argument('--tlapm', default=str(ROOT / '.tla/tools/tlapm/tlapm/bin/tlapm'))
    p.add_argument('--work-dir', default=str(ROOT / '.tla/cpu-admission'))
    args = p.parse_args()
    args.targets = []
    os.umask(0o077)
    spec = importlib.util.spec_from_file_location('cpu_formal_base', ROOT / 'scripts/test-formal.py')
    base = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(base)
    base.MODEL = MODEL
    gate = base.Gate(args)
    gate.runner_sources[str(Path(__file__).resolve())] = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    gate.summary['runner_sources'] = dict(gate.runner_sources)
    gate.summary['scope'] = 'Owned aligned-credit CPU admission; controlled native inputs, not native counter accuracy'
    for source in (MODEL/'export_actual.py',MODEL/'controls_actual.py'):
        gate.runner_sources[str(source)]=hashlib.sha256(source.read_bytes()).hexdigest()
    gate.summary['runner_sources']=dict(gate.runner_sources)
    try:
        # All children use the original formal_process.Command owner. One TLC
        # worker/256MiB, one proof worker and explicit 120s command bounds.
        if args.mode in ('check', 'traces', 'all'):
            gate.tool('java', args.java)
            gate.tool('tlc', args.tlc_jar)
        if args.mode in ('check','all'):
            log = gate.command([args.java, '-Djava.io.tmpdir=' + str(gate.java_tmp),
                '-XX:+UseSerialGC', '-Xmx256m', '-cp', args.tlc_jar, 'tlc2.TLC',
                '-workers', '1', '-fp', '0', '-metadir', gate.run / 'safety-states',
                '-noGenerateSpecTE', '-config', gate.run / 'Safety.cfg',
                gate.run / 'CPUAdmission.tla'], 'safety', timeout=120)
            counts = re.findall(r'(\d+) distinct states found, (\d+) states left on queue', log)
            if 'Model checking completed. No error has been found.' not in log or not counts or int(counts[-1][0]) == 0 or int(counts[-1][1]) != 0:
                raise RuntimeError('CPU finite exploration incomplete')
            gate.summary['gates'].append({'finite_distinct': int(counts[-1][0]), 'queue': 0})
        if args.mode in ('traces','all'):
            gate.summary['gates'].append(traces(gate,base))
        if args.mode in ('prove', 'all'):
            gate.tool('tlapm', args.tlapm)
            roster = base.validate_module_source(gate.run / 'CPUAdmissionProof.tla')
            if len(roster) != 17:
                raise RuntimeError('CPU proof theorem roster changed')
            log = gate.command([args.tlapm, '--strict', '--nofp', '--threads', '1',
                '--stretch', '2', '--cache-dir', gate.run / 'proof-cache',
                '-I', gate.run, gate.run / 'CPUAdmissionProof.tla'], 'proof', timeout=120)
            counts = re.findall(r'All (\d+) obligations proved', log)
            if not counts or int(counts[-1]) == 0 or '[ERROR]' in log:
                raise RuntimeError('CPU proof obligations incomplete')
            gate.summary['gates'].append({'proof_obligations': int(counts[-1]), 'theorems': roster})
        gate.check_captured_sources()
    except BaseException as e:
        gate.summary['error'] = str(e)
        raise
    finally:
        (gate.run / 'summary.json').write_text(json.dumps(gate.summary, indent=2) + '\n')
        print(gate.run, flush=True)


if __name__ == '__main__':
    main()
