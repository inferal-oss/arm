#!/usr/bin/env python3
"""Scoped reconnect TLC, actual native Store/journal refinement and complete proof.

This runner reuses the accepted tool-group/cancellation helpers unchanged.
A complete proof result still requires independent actual backend/heap invocation
provenance before integration; PATH/version-only receipts do not establish that.
"""
import argparse
import base64
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import sys

ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('accepted_formal_runner',ROOT/'scripts/test-formal.py')
base=importlib.util.module_from_spec(spec);spec.loader.exec_module(base)
MODEL=ROOT/'formal/mesh-reconnect'
ACTIONS=('Blocked','Handoff','Resolve','Dry','Review','Approve','Decline','RemotePair','LocalPair','SourceActivate','TargetActivate','Complete','Unknown','ChildReview','Lookup','OldCleanup','IdentityRefused','RevisionRefused')
TARGETS=('blocked','handoff','resolve','dry','review','approve','decline','remote_pair','local_pair','source_activate','target_activate','complete','unknown','child_review','lookup','old_cleanup','identity_refused','revision_refused','recovered','unknown_lookup','complete_lookup','child_decline','recovered_lookup','child_approve')
STRINGS=set('schema action old_coord phase local_pair remote_pair local_state remote_state local_grant remote_grant active_review active_approval'.split())
BOOLS=set('old_obligation resolved intent review approved child history_unknown identity_ok binding_ok active_accepted'.split())
INTS={'pair_calls','ensures'}
FIELDS={**dict.fromkeys(STRINGS,str),**dict.fromkeys(BOOLS,bool),**dict.fromkeys(INTS,int)}
MODEL_FILES={'Reconnect.tla','ReconnectProof.tla','Safety.cfg','Witness.tla'}
THEOREMS=('InitEstablishesInvariant',)+tuple('Preserve'+name for name in ACTIONS)+('LookupDoesNotRedispatch','HandoffRetainsNativeObligation','DelayedCleanupPreservesReplacement','ChildReviewHasNoBorrowedApproval','NativeRequiresActiveConsent','EveryStepPreservesInvariant','StutterPreserves','Safety')

def validate_states(rows):
    if type(rows) is not list or not 2<=len(rows)<=128:
        raise ValueError('bounded complete observed trace required')
    for row in rows:
        if type(row) is not dict or set(row)!=set(FIELDS):raise ValueError('closed reconnect state required')
        for key,kind in FIELDS.items():
            value=row[key]
            if type(value) is not kind:raise ValueError('wrong exact scalar type: '+key)
            if kind is str and (not value or len(value)>128 or not value.isascii() or any(ord(x)<32 or ord(x)>126 for x in value)):raise ValueError('bounded ASCII scalar: '+key)
            if kind is int and not 0<=value<=4:raise ValueError('bounded integer: '+key)
        if row['schema']!='arm.mesh-reconnect/v1':raise ValueError('unsupported reconnect trace schema')
    return rows

def read_bounded(path,cap=4<<20):
    with Path(path).open('rb') as stream:raw=stream.read(cap+1)
    if len(raw)>cap:raise ValueError('bounded evidence file required')
    return json.loads(raw,object_pairs_hook=base.strict_object)

def model_hashes(path):
    actual={p.name for p in path.iterdir() if p.suffix in ('.tla','.cfg') and p.is_file()}
    if not MODEL_FILES<=actual:raise ValueError('complete model/proof roster required')
    out={}
    for name in sorted(MODEL_FILES):
        item=path/name
        if item.is_symlink():raise ValueError('model symlink refused')
        raw=item.read_bytes()
        if len(raw)>1<<20:raise ValueError('model source bound')
        out[name]=hashlib.sha256(raw).hexdigest()
    return out

def source_hashes():
    paths=[ROOT/'go.mod',ROOT/'go.sum',Path(__file__),ROOT/'scripts/test-formal.py']
    for folder in ('internal','cmd','scripts'):
        paths.extend((ROOT/folder).rglob('*.go'))
    return {str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(set(paths))}

def principal(value):
    return tuple(value[k] for k in ('host','owner','certificate','spki_sha256','fingerprint','expires_at'))

def go_json_digest(value):
    # These typed fixture requests contain no fractional/exponent JSON numbers;
    # retain producer field order and Go's HTML/line-separator string escaping.
    raw=json.dumps(value,ensure_ascii=False,separators=(',',':'))
    for char,escaped in (('<',r'\u003c'),('>',r'\u003e'),('&',r'\u0026'),('\u2028',r'\u2028'),('\u2029',r'\u2029')):
        raw=raw.replace(char,escaped)
    return hashlib.sha256(raw.encode()).hexdigest()

def exact_json(left,right):
    if type(left) is not type(right):return False
    if type(left) is dict:return set(left)==set(right) and all(exact_json(left[k],right[k]) for k in left)
    if type(left) is list:return len(left)==len(right) and all(exact_json(a,b) for a,b in zip(left,right))
    return left==right

def validate_confirmation_inputs(inputs):
    if type(inputs) is not dict or len(inputs)>2:raise ValueError('bounded detached confirmation input roster required')
    for key,row in inputs.items():
        if type(key) is not str or not 1<=len(key)<=128 or not key.isascii():raise ValueError('bounded input attempt key required')
        if type(row) is not dict or set(row)!={'attempt','plan','sha256','review_revision','accepted'}:raise ValueError('closed actual confirmation input required')
        if type(row['attempt']) is not str or not 1<=len(row['attempt'])<=128 or not row['attempt'].isascii():raise ValueError('bounded actual input attempt required')
        if type(row['plan']) is not dict or len(json.dumps(row['plan']).encode())>256<<10:raise ValueError('bounded captured Plan required')
        if type(row['sha256']) is not str or not re.fullmatch('[0-9a-f]{64}',row['sha256']) or go_json_digest(row['plan'])!=row['sha256']:raise ValueError('actual callback Plan/hash differs')
        if type(row['review_revision']) is not int or not 1<=row['review_revision']<=1000000 or type(row['accepted']) is not bool:raise ValueError('actual callback stamp/decision required')
    return inputs

def active_consent(op,inputs):
    if op is None:return ('absent','absent',False)
    approval='approved' if op.get('approved') is True else 'unapproved' if op.get('approved') is False else 'absent'
    captured=inputs.get(op['key']);bound=captured is not None and captured['attempt']==op['key']
    accepted=bool(bound and captured['accepted'])
    if op.get('review') is None:return ('absent',approval,accepted)
    exact=(bound and captured['review_revision']==op.get('review_revision') and 0<captured['review_revision']<=op['revision']
        and op.get('review_sha256')==captured['sha256'] and exact_json(op['review'],captured['plan']))
    return ('exact' if exact else 'changed',approval,accepted)

def read_hashed_case(path,digest):
    # Hash and decode precisely this one bounded immutable byte value. A changed
    # pathname after close cannot substitute unverified bytes through a reopen.
    with path.open('rb') as stream:raw=stream.read((4<<20)+1)
    if len(raw)>4<<20 or hashlib.sha256(raw).hexdigest()!=digest:raise ValueError('bounded case artifact/hash required')
    return json.loads(raw,object_pairs_hook=base.strict_object)

def read_case_manifest(path,required):
    """Yield one hash-bound states/facts pair; retain only manifest metadata."""
    required=frozenset(required)
    if not 1<=len(required)<=16 or any(type(name) is not str for name in required):raise ValueError('bounded required case roster')
    manifest=read_bounded(path,65536)
    if type(manifest) is not dict or set(manifest)!={'schema','cases'} or manifest['schema']!='arm.mesh-reconnect-evidence/v1' or type(manifest['cases']) is not list or not 1<=len(manifest['cases'])<=16:raise ValueError('closed bounded evidence manifest required')
    owner=Path(path).resolve().parent
    seen=set();rows=[]
    # Validate the entire closed roster BEFORE yielding or opening case data.
    for row in manifest['cases']:
        if type(row) is not dict or set(row)!={'name','states','facts','states_sha256','facts_sha256'}:raise ValueError('closed exact evidence case roster required')
        name=row['name']
        if type(name) is not str or name in seen or name not in required:raise ValueError('closed exact evidence case roster required')
        seen.add(name);targets=[]
        for field in ('states','facts'):
            value=row[field];digest=row[field+'_sha256']
            if type(value) is not str or not 1<=len(value)<=4096 or '\x00' in value:raise ValueError('bounded case artifact path required')
            target=Path(value)
            if not target.is_absolute() or target.parent!=owner or target.is_symlink():raise ValueError('owned case artifact path required')
            if type(digest) is not str or not re.fullmatch('[0-9a-f]{64}',digest):raise ValueError('exact case artifact SHA required')
            targets.append((target,digest))
        rows.append((name,targets))
    if seen!=required:raise ValueError('complete exact evidence cases required')
    for name,targets in rows:
        # No decoded case map accumulates in this generator. The consumer must
        # finish/release each pair before requesting the next; callers choosing
        # to list() it deliberately retain their own objects.
        yield (name,read_hashed_case(*targets[0]),read_hashed_case(*targets[1]))


def validate_facts(facts,states):
    if type(facts) is not list or len(facts)!=len(states):raise ValueError('one complete raw-native fact per observed state required')
    initial=None
    for fact,state in zip(facts,states):
        required_fields={'key','artifacts','action','source','destination','old_coord','old_lifecycle','pair_calls','ensures','confirmation_inputs'}
        optional_fields={'root','active','resolved','last_remote_inspection'}
        if type(fact) is not dict or not required_fields<=set(fact) or not set(fact)<=required_fields|optional_fields:raise ValueError('closed native observation required')
        if fact['action']!=state['action']:raise ValueError('native observation/action binding required')
        if type(fact['key']) is not str or not 1<=len(fact['key'])<=128 or not fact['key'].isascii():raise ValueError('bounded native key required')
        for counter in ('pair_calls','ensures'):
            if type(fact[counter]) is not int or not 0<=fact[counter]<=4:raise ValueError('bounded native entry counter required')
        inputs=validate_confirmation_inputs(fact['confirmation_inputs'])
        artifacts=fact.get('artifacts')
        required={'source_config','destination_config','old_coord','old_lifecycle'}
        if type(artifacts) is not dict or not required<=set(artifacts) or not set(artifacts)<=(required|{'root','active'}):raise ValueError('closed native artifact roster required')
        decoded={}
        for name,row in artifacts.items():
            if type(row) is not dict or set(row)!={'path','sha256','raw_base64'}:raise ValueError('closed raw native artifact required')
            if type(row['path']) is not str or not 1<=len(row['path'])<=4096 or not Path(row['path']).is_absolute() or '\x00' in row['path']:raise ValueError('bounded absolute native path required')
            if type(row['sha256']) is not str or not re.fullmatch('[0-9a-f]{64}',row['sha256']):raise ValueError('exact native SHA required')
            if type(row['raw_base64']) is not str or not 1<=len(row['raw_base64'])<=4*(((1<<20)+2)//3):raise ValueError('bounded encoded native bytes required')
            raw=base64.b64decode(row['raw_base64'],validate=True)
            if not raw or len(raw)>1<<20 or hashlib.sha256(raw).hexdigest()!=row['sha256']:raise ValueError('actual native bytes/hash mismatch')
            decoded[name]=json.loads(raw,object_pairs_hook=base.strict_object)
        # Both the structured receipt and its raw captured file are bound. This
        # comparison does not claim a cross-node atomic snapshot or native commit
        # from a callback label; the stored raw files are quiescent observations.
        for artifact,field in (('source_config','source'),('destination_config','destination'),('old_coord','old_coord'),('old_lifecycle','old_lifecycle'),('root','root'),('active','active')):
            if artifact in decoded and decoded[artifact]!=fact.get(field):raise ValueError('structured/native bytes mismatch: '+artifact)
        if 'root' in decoded and 'active' not in decoded and fact.get('active')!=decoded['root']:raise ValueError('active observation is not its captured root record')
        if initial is None:initial=decoded
        root=decoded.get('root');active=decoded.get('active',root);resolved=fact.get('resolved')
        key=fact['key'];old=decoded['old_lifecycle'];old_pair=old['pair']
        new_pair=hashlib.sha256(('arm-connect:'+key+':'+old['local_host']+':'+old['remote_host']).encode()).hexdigest()
        peers=[decoded['source_config']['peers'][0],decoded['destination_config']['peers'][0]]
        kinds=['old' if p['pair']==old_pair else 'new' if p['pair']==new_pair else 'other' for p in peers]
        if kinds!=[state['local_pair'],state['remote_pair']]:raise ValueError('actual key-derived enrollment projection mismatch')
        old_grant=initial['source_config']['peers'][0]['grant']
        if initial['destination_config']['peers'][0]['grant']!=old_grant:raise ValueError('fixture old directional grant baseline differs')
        fresh=[resolved['draft']['target_permissions_on_source'],resolved['draft']['source_permissions_on_target']] if resolved else [None,None]
        grants=['old' if p['grant']==old_grant else 'fresh' if p['grant']==g else 'other' for p,g in zip(peers,fresh)]
        if grants!=[state['local_grant'],state['remote_grant']]:raise ValueError('actual full grant projection mismatch')
        bound=True
        if resolved:
            binding=resolved['binding']
            bound=(binding['previous_enrollment']['pair']==old_pair
                and principal(binding['local_identity'])==principal(old['local_identity'])
                and principal(binding['previous_enrollment']['identity'])==principal(old['remote_identity']))
        if root:
            expected={'key':key,'kind':'reconnect','reconnect':resolved}
            bound=(bound and root['key']==key and root['request_sha256']==go_json_digest(resolved)
                and go_json_digest(root['request'])==go_json_digest(expected))
            if active['key']!=key:
                bound=(bound and active.get('parent_key')==key and active['request_sha256']==root['request_sha256']
                    and go_json_digest(active['request'])==go_json_digest(expected))
        identity=fact.get('last_remote_inspection',{}).get('identity',decoded['destination_config']['identity'])
        if state['identity_ok'] != (principal(identity)==principal(old['remote_identity'])):raise ValueError('actual identity projection mismatch')
        if state['binding_ok']!=bool(bound):raise ValueError('actual original intent/binding projection mismatch')
        projection={'resolved':resolved is not None,'intent':root is not None,
            'phase':active['phase'] if active else 'absent','review':bool(root and active_consent(root,inputs)[0]=='exact'),
            'approved':bool(root and root.get('approved') is True),
            'child':bool(active and active['key']!=key and active.get('parent_key')==key),
            'history_unknown':bool(root and root['phase']=='incomplete')}
        projection.update(zip(('active_review','active_approval','active_accepted'),active_consent(active,inputs)))
        if any(state[k]!=v for k,v in projection.items()):raise ValueError('actual saved coordinator projection mismatch')
        if fact.get('pair_calls')!=state['pair_calls'] or fact.get('ensures')!=state['ensures']:raise ValueError('actual native entry counter mismatch')
        peers=[decoded['source_config']['peers'][0],decoded['destination_config']['peers'][0]]
        if peers[0]['state']!=state['local_state'] or peers[1]['state']!=state['remote_state']:raise ValueError('native peer state mismatch')
        if state['old_obligation'] != (decoded['old_lifecycle']['phase']=='remote-pending' and decoded['old_coord']['result']['remote_pending']):raise ValueError('old native obligation projection mismatch')
        if state['old_coord'] != ('handed_off' if decoded['old_coord']['phase']=='handed_off' else 'pending'):raise ValueError('workflow exclusion projection mismatch')


def altered_negatives(actual):
    origin=actual['complete'];out={}
    row=copy.deepcopy(origin);row[-1]['remote_grant']='old';out['inherited-old-grants']=row
    row=copy.deepcopy(origin);row[-1]['local_pair']='old';out['replacement-erased']=row
    row=copy.deepcopy(origin);row[-1]['pair_calls']+=1;out['unobserved-extra-native-entry']=row
    row=copy.deepcopy(origin);out['omitted-review']=[s for s in row if s['action']!='review']
    row=copy.deepcopy(actual['recovered']);out['omitted-unknown']=[s for s in row if s['action']!='unknown']
    row=copy.deepcopy(actual['handoff']);row[-1]['old_obligation']=False;out['handoff-erased-obligation']=row
    for rows in out.values():
        validate_states(rows)
        if rows[0]!=origin[0]:raise ValueError('negative initial observation changed')
    return out

base.MODEL=MODEL
base.validate_states=validate_states
class ReconnectGate(base.Gate):
    def __init__(self,args):
        if {p.name for p in MODEL.iterdir() if p.suffix in ('.tla','.cfg')}!=MODEL_FILES:raise ValueError('exact reconnect model roster required')
        super().__init__(args)
        self.summary['go_source_sha256']=source_hashes()
        self.summary['scope']='two owned enrollment generations + one competing local replacement; initial native old revokes committed/source ACK unknown; one explicit reconnect child; target-pair ACK loss cut'
        self.summary['backend_provenance']='independent actual backend executable/config/heap invocation closure still required for integration'
    def check(self):
        log=self.tlc('Reconnect.tla',self.run/'Safety.cfg','safety')
        found=re.findall(r'(\d+) distinct states found, (\d+) states left on queue',log)
        if 'Model checking completed. No error has been found.' not in log or not found or int(found[-1][0])==0 or int(found[-1][1])!=0:raise RuntimeError('complete nonempty exhaustive safety required')
        self.summary['gates'].append({'safety':'passed','statistics':re.findall(r'.*(?:states generated|depth of the complete).*',log)})
    def witnesses(self):
        rows,manifest=super().witnesses()
        for row in rows:row['facts']=str(self.run/(row['target']+'-native.json'))
        manifest.write_text(json.dumps(rows,indent=2)+'\n')
        return rows,manifest
    def replay(self,states,name,accepted=True):
        validate_states(states)
        if not accepted and (self.initial_state is None or states[0]!=self.initial_state):raise ValueError('negative must preserve validated actual initial observation')
        module='Replay'+re.sub('[^A-Za-z0-9]','',name)
        text='---- MODULE '+module+' ----\nEXTENDS Reconnect, Sequences\nVARIABLE cursor\n'
        text+='Observed == <<'+',\n'.join(base.tla_value(s) for s in states)+'>>\n'
        text+='ReplayInit == Init /\\ cursor=1 /\\ state=Observed[1]\n'
        text+="ReplayNext == /\\ cursor<Len(Observed) /\\ state'=Observed[cursor+1] /\\ cursor'=cursor+1 /\\ Next\n"
        text+='NotEnd == cursor<Len(Observed)\n====\n'
        (self.run/(module+'.tla')).write_text(text)
        config=self.run/(module+'.cfg');config.write_text('INIT ReplayInit\nNEXT ReplayNext\nCHECK_DEADLOCK FALSE\nINVARIANT InductiveInvariant\nINVARIANT NotEnd\n')
        raw=self.run/(name+'-import.json');raw.write_text(json.dumps(base.envelope(states))+'\n')
        log=self.tlc(module+'.tla',config,name+'-reverse',('-loadTrace','json',raw),expected=12 if accepted else 0)
        if accepted:
            if 'Invariant NotEnd is violated.' not in log or not re.search(r'\b'+str(len(states))+r' distinct states found',log):raise RuntimeError('complete imported actual Init/Next replay required')
            if self.initial_state is not None and self.initial_state!=states[0]:raise ValueError('positive actual initial observations disagree')
            self.initial_state=copy.deepcopy(states[0])
        elif 'Invariant NotEnd is violated.' in log:raise RuntimeError('invalid actual trace reached its end')
        counts=re.findall(r'(\d+) distinct states found',log)
        if not counts or int(counts[-1])<1:raise RuntimeError('replay did not explore valid initial observation')
    def traces(self):
        self.check();paths,manifest=self.witnesses()
        negative=self.run/'actual-negatives.json';tmp=self.run/'tmp';tmp.mkdir(mode=0o700)
        before=source_hashes()
        if before!=self.summary['go_source_sha256']:raise RuntimeError('Go source changed before actual witness run')
        env=dict(os.environ,ARM_RECONNECT_FORMAL_MANIFEST=str(manifest),ARM_RECONNECT_FORMAL_NEGATIVES=str(negative),TMPDIR=str(tmp))
        env.setdefault('GOPATH',str(ROOT/'.cache/gopath'));env.setdefault('GOCACHE',str(ROOT/'.cache/go-build'));env.setdefault('GOMAXPROCS','2')
        pattern='^TestReconnect(Formal(TLCWitnesses|ObservationNegatives|InputGuards|OutputBoundIncludesNewline)|ActualActiveChildConsentControls|ActualChildConsentCorruptionControls|ConfirmationInputAttemptAndDetachment)$'
        self.command([self.args.go,'test','-race','-tags','arm_devtrace','./internal/meshops','-run',pattern,'-count=1','-v','-timeout=300s'],'go-witnesses',env=env,timeout=330)
        if source_hashes()!=before:raise RuntimeError('Go source changed during actual witnesses')
        actual={};rows_total=0
        for row in paths:
            expected=validate_states(base.read_json(row['path']));observed=validate_states(base.read_json(row['output']))
            if observed!=expected:raise RuntimeError('actual native state differs from TLC witness: '+row['target'])
            facts=read_bounded(row['facts']);validate_facts(facts,observed);del facts
            self.replay(observed,row['target']);actual[row['target']]=observed;rows_total+=len(observed)
        required={'actual-old-grant-on-new-pair','actual-changed-original-binding','actual-erased-old-obligation'}|{'actual-child-'+name for name in ('missing-review','substituted-review','changed-review-revision','missing-approval','cleared-approval')}
        cases=read_case_manifest(negative,required)
        try:
            while True:
                try:name,states,case_facts=next(cases)
                except StopIteration:break
                validate_facts(case_facts,states);del case_facts
                self.replay(states,name,accepted=False)
                del name,states
        finally:cases.close()
        altered=altered_negatives(actual) if set(actual)==set(TARGETS) else {}
        for name,states in altered.items():self.replay(states,name,accepted=False)
        self.summary['gates'].append({'traces':'passed','witnesses':len(paths),'actual_rows':rows_total,'actual_negatives':sorted(required),'altered_negatives':sorted(altered),'coverage_complete':set(actual)==set(TARGETS)})
    def prove(self):
        before=model_hashes(self.run)
        if before!=self.summary['sources']:raise RuntimeError('proof source differs from initial captured snapshot')
        path=self.run/'ReconnectProof.tla'
        self.summary['proof_theorems']=base.validate_module_source(path,THEOREMS)
        summary=self.command([self.args.tlapm,'--summary','--cache-dir',self.run/'parse-scope','-I',self.run,path],'tlapm-scope',timeout=120)
        if model_hashes(self.run)!=before:raise RuntimeError('proof source changed during summary')
        modules=re.findall(r'^---- summary of module "([^"]+)" ----$',summary,re.M);counts=re.findall(r'^\s*obligations_count\s*=\s*(\d+)\s*$',summary,re.M)
        if modules!=['ReconnectProof'] or len(counts)!=1 or int(counts[0])==0:raise RuntimeError('one complete positive parser scope required')
        count=int(counts[0]);self.summary['parsed_obligations']=count
        log=self.command([self.args.tlapm,'--strict','--nofp','--threads','2','--stretch','10','--cache-dir',self.run/'proof','-I',self.run,path],'tlapm',timeout=3600)
        if model_hashes(self.run)!=before:raise RuntimeError('proof source changed during complete proof')
        results=re.findall(r'All (\d+) obligations proved\.',log)
        if len(results)!=1 or int(results[0])!=count or '[ERROR]' in log:raise RuntimeError('one complete exact positive proof result required')
        self.summary['gates'].append({'proofs':'passed','obligations':count,'sources':before,'actual_backend_closure':'additional independent required receipt'})

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mode',choices=('check','traces','prove','all'),default='all')
    parser.add_argument('--java',default='java');parser.add_argument('--tlc-jar',default=str(ROOT/'.tla/tools/tlc/tla2tools.jar'))
    parser.add_argument('--tlapm',default=str(ROOT/'.tla/tools/tlapm/tlapm/bin/tlapm'));parser.add_argument('--go',default='go')
    parser.add_argument('--work-dir',default=str(ROOT/'.tla/mesh-reconnect'));parser.add_argument('--targets',nargs='+',choices=TARGETS,default=list(TARGETS))
    args=parser.parse_args()
    if len(set(args.targets))!=len(args.targets):parser.error('duplicate targets')
    if args.mode in ('traces','all') and not {'complete','recovered'}<=set(args.targets):parser.error('complete and recovered witnesses required for actual negatives')
    gate=None
    def interrupted(signum,_frame):raise SystemExit(128+signum)
    signal.signal(signal.SIGTERM,interrupted)
    try:
        gate=ReconnectGate(args);gate.tools()
        if args.mode=='check':gate.check()
        elif args.mode=='traces':gate.traces()
        elif args.mode=='prove':gate.prove()
        else:gate.traces();gate.prove()
        gate.summary['status']='passed'
    except BaseException as error:
        if gate is not None:gate.summary.update(status='failed',error=repr(error))
        raise
    finally:
        if gate is not None:
            (gate.run/'summary.json').write_text(json.dumps(gate.summary,indent=2)+'\n')
            print('Reconnect gate receipt:',gate.run/'summary.json',flush=True)
    return 0

if __name__=='__main__':sys.exit(main())
