#!/usr/bin/env python3
"""Finite published-agent safety, actual SQL/runtime refinement and full proof.

The accepted preparation runner is imported unchanged for owned process-group
cleanup and exact tool receipts. This suite has its own closed state schema,
source/proof roster, witnesses, strict imported Next replays and coverage.
"""
import argparse
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys

ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('accepted_formal_runner',ROOT/'scripts/test-formal.py')
base=importlib.util.module_from_spec(spec);spec.loader.exec_module(base)
MODEL=ROOT/'formal/published-agents'
TARGETS=('accepted','acknowledged','stale','replacement','original_reply','replacement_reply',
         'reply_refused','lost_ack_restart','reconciled','duplicate','retry','native_refused','withdrawn','late_accept','late_superseded')
STRINGS={'schema','action','pub1','pub2','source','result1','result2'}
INTS={'contact','bound1','bound2','pub_rows','journal_rows','dispatches','generation1','generation2',
      'reply_from','reply_to','native_calls','implicit_replies'}
BOOLS={'source_live','message1','inbox1','link1','message2','inbox2','link2','local_ack1',
       'routed_ack1','local_ack2','routed_ack2','budget_ok','poll_only','identities_ok'}
FIELDS={**dict.fromkeys(STRINGS,str),**dict.fromkeys(INTS,int),**dict.fromkeys(BOOLS,bool)}
def validate_states(states):
    if type(states) is not list or not 2<=len(states)<=128:raise ValueError('complete bounded nonempty trace required')
    for s in states:
        if type(s) is not dict or set(s)!=set(FIELDS):raise ValueError('closed canonical published state required')
        for k,kind in FIELDS.items():
            if type(s[k]) is not kind:raise ValueError('wrong scalar type: '+k)
            if kind is str and (not s[k].isascii() or len(s[k])>128):raise ValueError('bounded ASCII state required')
            if kind is int and not 0<=s[k]<=10000:raise ValueError('bounded state integer required')
        if s['schema']!='arm.published-agents/v1':raise ValueError('unknown publication trace schema')
    return states
MODEL_FILES={'PublishedAgents.tla','PublishedAgentsProof.tla','Safety.cfg','Witness.tla'}
ACTIONS=('Publish1','Publish2','Withdraw1','Withdraw2','ChangeContact','Prepare','Begin','Accept1','Stale1','Accept2','Unknown','Crash','Restart','Complete','Retry','Duplicate1','Duplicate2','Ack1','Ack2','Reply1','Reply2','RefusedReply1','NativeRefused','Discover')
PROOF_THEOREMS=tuple('Preserve'+action for action in ACTIONS)+(
    'InitEstablishesInvariant','EveryStepPreservesInvariant','StutterPreserves','Safety',
    'RowCases','Envelope','PairBounds','PhaseBounds','ScalarCapacitySafety','CurrentRowCases')

def model_roster(path):
    files={p.name for p in path.iterdir() if p.is_file() and p.suffix in ('.tla','.cfg')}
    if files!=MODEL_FILES:raise ValueError('exact publication model and complete proof roster required')
    for name in MODEL_FILES:
        if (path/name).is_symlink():raise ValueError('model source symlink refused')
    return sorted(files)

def proof_source_hashes(directory):
    result={}
    for name in sorted(MODEL_FILES):
        path=directory/name
        if path.is_symlink():raise ValueError('proof source symlink refused')
        raw=path.read_bytes()
        if len(raw)>1<<20:raise ValueError('proof source exceeds 1MiB')
        result[name]=hashlib.sha256(raw).hexdigest()
    return result

def synthetic_negatives(actual):
    def trace(target):return copy.deepcopy(actual[target])
    out={}
    rows=trace('accepted');out['omitted-begin']=[s for s in rows if s['action']!='begin']
    rows=trace('accepted');rows.append(copy.deepcopy(rows[-1]));out['implicit-stutter']=rows
    rows=trace('accepted');rows[-1]['dispatches']=2;out['repeated-dispatch']=rows
    rows=trace('replacement_reply');rows[-1]['reply_to']=2;out['reply-recipient-retarget']=rows
    rows=trace('lost_ack_restart');out['erased-unknown']=[s for s in rows if s['action']!='unknown']
    rows=trace('accepted');rows[-1]['generation1']=2;out['generation-retarget']=rows
    rows=trace('acknowledged');rows[-1],rows[-2]=rows[-2],rows[-1];out['reordered-acknowledgement']=rows
    rows=trace('accepted');rows[-1]['action']='duplicate1';out['false-duplicate-label']=rows
    origins={'omitted-begin':'accepted','implicit-stutter':'accepted','repeated-dispatch':'accepted','reply-recipient-retarget':'replacement_reply','erased-unknown':'lost_ack_restart','generation-retarget':'accepted','reordered-acknowledgement':'acknowledged','false-duplicate-label':'accepted'}
    for name,rows in out.items():
        if rows==actual[origins[name]]:raise ValueError('negative did not change an actual transition: '+name)
        validate_states(rows)
        if rows[0]!=actual['accepted'][0]:raise ValueError('negative initial observation changed: '+name)
    return out

base.MODEL=MODEL
base.validate_states=validate_states
class PublishedGate(base.Gate):
    def __init__(self,args):
        model_roster(MODEL)
        super().__init__(args)
        self.summary['proof_roster']=['PublishedAgentsProof.tla']
        self.summary['scope']='one source lifecycle; second destination key has a fixed externally admitted source'
        paths=[ROOT/'go.mod',ROOT/'go.sum',Path(__file__),ROOT/'scripts/test-formal.py']
        paths.extend(sorted((ROOT/'internal').rglob('*.go')))
        self.summary['go_source_sha256']={str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}
    def check(self):
        log=self.tlc('PublishedAgents.tla',self.run/'Safety.cfg','safety')
        counts=re.findall(r'(\d+) distinct states found, (\d+) states left on queue',log)
        if 'Model checking completed. No error has been found.' not in log or not counts or int(counts[-1][0])==0 or int(counts[-1][1])!=0:raise RuntimeError('nonempty complete exhaustive safety check required')
        self.summary['gates'].append({'safety':'passed','statistics':re.findall(r'.*(?:states generated|depth of the complete).*',log)})
    def witnesses(self):
        paths,manifest=super().witnesses()
        for row in paths:row['facts']=str(self.run/(row['target']+'-sql-bytes.json'))
        manifest.write_text(json.dumps(paths,indent=2)+'\n')
        return paths,manifest
    def replay(self,states,name,accepted=True):
        validate_states(states)
        if not accepted and (self.initial_state is None or states[0]!=self.initial_state):raise ValueError('negative must preserve validated actual initial state')
        module='Replay'+re.sub('[^A-Za-z0-9]','',name)
        text='---- MODULE '+module+' ----\nEXTENDS PublishedAgents, Sequences\nVARIABLE cursor\n'
        text+='Observed == <<'+',\n'.join(base.tla_value(s) for s in states)+'>>\n'
        text+='ReplayInit == Init /\\ cursor=1 /\\ state=Observed[1]\n'
        text+="ReplayNext == /\\ cursor<Len(Observed) /\\ state'=Observed[cursor+1] /\\ cursor'=cursor+1 /\\ Next\n"
        text+='NotEnd == cursor<Len(Observed)\n====\n'
        (self.run/(module+'.tla')).write_text(text)
        config=self.run/(module+'.cfg');config.write_text('INIT ReplayInit\nNEXT ReplayNext\nCHECK_DEADLOCK FALSE\nINVARIANT InductiveInvariant\nINVARIANT NotEnd\n')
        raw=self.run/(name+'-import.json');raw.write_text(json.dumps(base.envelope(states))+'\n')
        log=self.tlc(module+'.tla',config,name+'-reverse',('-loadTrace','json',raw),expected=12 if accepted else 0)
        if accepted:
            if 'Invariant NotEnd is violated.' not in log or not re.search(r'\b'+str(len(states))+r' distinct states found',log):raise RuntimeError('complete actual trace did not satisfy Init and each imported Next')
            if self.initial_state is not None and states[0]!=self.initial_state:raise ValueError('positive traces disagree on exact initial observation')
            self.initial_state=json.loads(json.dumps(states[0]))
        elif 'Invariant NotEnd is violated.' in log:raise RuntimeError('invalid observed trace reached its end')
        counts=re.findall(r'(\d+) distinct states found',log)
        if not counts or int(counts[-1])<1:raise RuntimeError('replay did not explore its valid initial state')
    def traces(self):
        self.check();paths,manifest=self.witnesses()
        tmp=self.run/'tmp';tmp.mkdir(mode=0o700)
        negative_output=self.run/'actual-negatives.json'
        env=dict(os.environ,ARM_PUBLISHED_FORMAL_WITNESSES=str(manifest),ARM_PUBLISHED_FORMAL_NEGATIVES=str(negative_output),ARM_PUBLISHED_FORMAL_DIR=str(self.run),TMPDIR=str(tmp))
        env.setdefault('GOPATH',str(ROOT/'.cache/gopath'));env.setdefault('GOCACHE',str(ROOT/'.cache/go-build'))
        self.command([self.args.go,'test','-race','-tags','arm_devtrace','./internal/database','-run','^TestPublishedFormal(TLCWitnesses|ObservationNegatives|InputGuards|RawKeyGuards)$','-count=1','-v','-timeout=240s'],'go-witnesses',env=env,timeout=270)
        observed=[];actual_by_target={}
        for row in paths:
            expected=validate_states(base.read_json(row['path']));actual=validate_states(base.read_json(row['output']))
            continuation=['restart','complete'] if row['target'] in ('late_accept','late_superseded') else []
            if actual[:len(expected)]!=expected or len(actual)!=len(expected)+len(continuation) or [s['action'] for s in actual[len(expected):]]!=continuation:raise RuntimeError('actual SQL/runtime state differs from TLC prefix/bounded continuation: '+row['target'])
            facts=base.read_json(row['facts'])
            if type(facts) is not list or len(facts)!=len(actual):raise ValueError('complete actual escaped-byte evidence required')
            for f in facts:
                if type(f) is not dict or set(f)!={'publication_raw','publication_reserve','routing_raw','routing_reserve','future_events'} or any(type(v) is not int or v<0 for v in f.values()):raise ValueError('closed actual capacity observation required')
                if f['publication_raw']+f['publication_reserve']>16<<20 or sum(f[k] for k in ('publication_raw','publication_reserve','routing_raw','routing_reserve'))>32<<20 or f['future_events']>2048:raise RuntimeError('actual future-finalization budget violated')
            self.replay(actual,row['target']);observed.extend(actual);actual_by_target[row['target']]=actual
        actual_negatives=base.read_json(negative_output)
        required={'actual-partial-inbox','actual-native-entry','actual-native-promotion','actual-unwithdrawn-contact','actual-erased-local-ack','actual-retargeted-reply','actual-escaped-row-overflow'}
        if type(actual_negatives) is not dict or set(actual_negatives)!=required:raise ValueError('complete actual SQL/runtime negative inventory required')
        for name,states in actual_negatives.items():self.replay(states,name,accepted=False)
        synthetic=synthetic_negatives(actual_by_target) if set(actual_by_target)==set(TARGETS) else {}
        for name,states in synthetic.items():self.replay(states,name,accepted=False)
        self.summary['gates'].append({'traces':'passed','witnesses':len(paths),'observed_rows':len(observed),'coverage_complete':set(self.args.targets)==set(TARGETS),'actual_negative_traces_rejected':sorted(required),'synthetic_negative_traces_rejected':sorted(synthetic),'negative_coverage_complete':set(self.args.targets)==set(TARGETS) and len(synthetic)==8})
    def prove(self):
        path=self.run/'PublishedAgentsProof.tla'
        if not path.exists():raise RuntimeError('complete explicit publication proof is missing')
        sources=proof_source_hashes(self.run)
        if self.summary.get('sources')!=sources:raise RuntimeError('proof source differs from initial gate snapshot')
        covered=base.validate_module_source(path,PROOF_THEOREMS)
        self.summary['proof_theorems']=covered
        parsed=self.command([self.args.tlapm,'--summary','--cache-dir',self.run/'parse-scope','-I',self.run,path],'tlapm-scope',timeout=120)
        if proof_source_hashes(self.run)!=sources:raise RuntimeError('proof source changed during parser summary')
        counts=re.findall(r'^\s*obligations_count\s*=\s*(\d+)\s*$',parsed,re.M)
        modules=re.findall(r'^---- summary of module "([^"]+)" ----$',parsed,re.M)
        if modules!=['PublishedAgentsProof'] or len(counts)!=1 or int(counts[0])==0:
            raise RuntimeError('complete parsed publication proof scope required')
        expected=int(counts[0]);self.summary['parsed_proof_obligations']=expected
        log=self.command([self.args.tlapm,'--strict','--nofp','--threads','2','--stretch','10','--cache-dir',self.run/'proofs','-I',self.run,path],'tlapm',timeout=3600)
        if proof_source_hashes(self.run)!=sources:raise RuntimeError('proof source changed during complete proof')
        results=re.findall(r'All (\d+) obligations proved\.',log)
        if len(results)!=1 or int(results[0])!=expected or '[ERROR]' in log:raise RuntimeError('exactly one complete positive proof result required')
        self.summary['gates'].append({'proofs':'passed','result':'All '+results[0]+' obligations proved.','source_sha256':sources})
def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mode',choices=('check','traces','prove','all'),default='all')
    parser.add_argument('--java',default='java')
    parser.add_argument('--tlc-jar',default=str(ROOT/'.tla/tools/tlc/tla2tools.jar'))
    parser.add_argument('--tlapm',default=str(ROOT/'.tla/tools/tlapm/tlapm/bin/tlapm'))
    parser.add_argument('--go',default='go')
    parser.add_argument('--work-dir',default=str(ROOT/'.tla/published-agents'))
    parser.add_argument('--targets',nargs='+',choices=TARGETS,default=list(TARGETS))
    args=parser.parse_args()
    if len(set(args.targets))!=len(args.targets):
        parser.error('duplicate targets')
    if args.mode!='prove' and not Path(args.tlc_jar).is_file():
        parser.error('TLC missing: provision with make tla-tools')
    if args.mode in ('prove','all') and not Path(args.tlapm).is_file():
        parser.error('TLAPM missing: provision with make tla-tools')
    gate=PublishedGate(args)
    gate.summary['suite']='published-agents'
    gate.tools()
    if args.mode=='check':gate.check()
    if args.mode in ('traces','all'):gate.traces()
    if args.mode in ('prove','all'):gate.prove()
    gate.finish()

if __name__=='__main__':
    def terminate(signum,_frame):
        raise SystemExit(128+signum)
    signal.signal(signal.SIGTERM,terminate)
    try:
        main()
    except KeyboardInterrupt:
        print('publication formal verification interrupted',file=sys.stderr)
        sys.exit(130)
    except (ValueError,RuntimeError,subprocess.TimeoutExpired,OSError) as error:
        print('publication formal verification failed: '+str(error),file=sys.stderr)
        sys.exit(1)
