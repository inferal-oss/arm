#!/usr/bin/env python3
"""Verify private publication safety and actual Go/TLC trace conformance.

Reuse only accepted bounded process control and tool provenance. Publication
normalization, source roster and exact imported Next checks are independent.
"""
import argparse
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
import tempfile

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('preparation_gate', ROOT/'scripts/test-formal.py')
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
TARGETS = ('retired','mismatch','verify_intent_unknown','verify_start_unknown',
           'published_unknown','retiring_unknown','retired_unknown','ending',
           'early_cancelled','duplicate','verify_rollback','crash_unknown','reconciled',
           'restart_unknown','restart_rollback_unknown','published_restart','retirement_decision_recovery','retained_pair')
SCALARS = {key:str for key in ('schema','action','phase','worker','scope','cache_phase',
    'boundary','lease','handle','comparison','end_decision','outcome')}
SCALARS.update({key:int for key in ('revision','slots','cache_revision','evidence')})
SCALARS.update({key:bool for key in ('original','intent','published','retired','charged',
    'pin','absence','stop','identity_ok','original_ok','intent_ok')})

PROOF_ACTIONS = ('Admit','Start','NativeBegin','Compare','NativeJoin','SkipNative',
    'Prepared','DropSupervisor','VerifyIntent','Publish','Stop','EndUnjoined',
    'Finish','RetireIntent','RetireComplete','Crash','Restart','ObserveAbsent',
    'RetainEvidence','Read','LookupUnavailable','CommitUnavailable')
# This is the reviewed, closed source/import/qualified-theorem roster. Each
# proof module is invoked independently: importing a theorem does not prove it.
ACTION_SUFFIXES = ('HasBaseFrame','HasFrame','Normalizes','Types','NativeFacts',
                   'ImmutableEvidence','PhaseEvidence','Safety')
ACTION_MODULES = tuple('SharePublication'+a+'Proof' for a in PROOF_ACTIONS)
PROOF_THEOREMS = {
    'JournalCapacityProof': ('InitValid','StepValid','Safety','ProductMonotonic',
        'ReservedDistribution','FinalizationScalarBounds','RequiredFinalizationFits'),
    'SharePublicationFieldsProof': ('Characterize','CurrentFields','CurrentArithmetic','EmptyNativeTyped'),
    'SharePublicationNativeProof': ('NativeEqualityProjects',)+tuple(
        action+scope+field for action in ('NativeBegin','NativeJoin','SkipNative','Crash')
        for scope in ('preparation','verification')
        for field in ('record','status','count','joined','skipped')),
    'SharePublicationInitProof': ('InitHasBaseFrame','NativeZeroFields','ZeroNativeScopes',
        'ZeroNativeProjections','InitHasFrame','InitTypes','InitNativeFacts',
        'InitImmutableEvidence','InitPhaseEvidence','InitSemantics','InitSafety'),
    **{module:tuple(action+suffix for suffix in ACTION_SUFFIXES)
       for action,module in zip(PROOF_ACTIONS,ACTION_MODULES)},
    'SharePublicationProof': ('StutterSafety','NextSafety','Safety')}
MODULE_IMPORTS = {
    'JournalCapacity': ('Naturals',),
    'JournalCapacityProof': ('JournalCapacity','TLAPS'),
    'SharePublication': ('Naturals','Sequences'),
    'Witness': ('SharePublication',),
    'SharePublicationDefinitions': ('SharePublication','TLAPS'),
    'SharePublicationFieldsProof': ('SharePublicationDefinitions',),
    'SharePublicationNativeProof': ('SharePublicationDefinitions',),
    'SharePublicationInitProof': ('SharePublicationFieldsProof',),
    **{module:('SharePublicationFieldsProof','SharePublicationNativeProof')
       for module in ACTION_MODULES},
    'SharePublicationProof': ('SharePublicationInitProof',*ACTION_MODULES)}
# Independent roles detect a silently dropped invocation/roster entry.
PROOF_MODULES = ('JournalCapacityProof','SharePublicationFieldsProof',
    'SharePublicationNativeProof','SharePublicationInitProof',*ACTION_MODULES,
    'SharePublicationProof')
FORMAL_FILES = frozenset(m+'.tla' for m in MODULE_IMPORTS)|{'capacity.cfg','safety.cfg'}
CODE_FILES = ('scripts/test-formal-publication.py','scripts/test-formal.py')


def file_digest(path):
    digest=hashlib.sha256()
    with Path(path).open('rb') as stream:
        for chunk in iter(lambda:stream.read(1<<20),b''):digest.update(chunk)
    return digest.hexdigest()


def file_receipt(path):
    path=Path(path);actual=path.resolve(strict=True)
    if not actual.is_file():raise ValueError('regular verification input required: '+str(path))
    return {'configured':str(path),'resolved':str(actual),'size':actual.stat().st_size,
            'sha256':file_digest(actual)}


def validate_sources(directory):
    directory=Path(directory)
    actual={p.name for p in directory.iterdir() if p.suffix in ('.tla','.cfg')}
    if actual!=FORMAL_FILES:raise ValueError('complete exact formal source inventory required')
    if tuple(PROOF_THEOREMS)!=PROOF_MODULES:
        raise ValueError('every reviewed proof module must be independently invoked')
    # No unknown dependency or cycle, and each project proof dependency must precede use.
    completed=set()
    def dependencies(module,seen=()):
        if module in seen:raise ValueError('cyclic formal imports')
        for dep in MODULE_IMPORTS[module]:
            if dep in MODULE_IMPORTS:
                yield dep
                yield from dependencies(dep,(*seen,module))
            elif dep not in ('Naturals','Sequences','TLAPS'):
                raise ValueError('unreviewed external formal import: '+dep)
    for module in PROOF_MODULES:
        if any(d in PROOF_MODULES and d not in completed for d in dependencies(module)):
            raise ValueError('proof dependency is not independently verified before use: '+module)
        completed.add(module)
    qualified=[]
    for module,imports in MODULE_IMPORTS.items():
        path=directory/(module+'.tla')
        if path.is_symlink():raise ValueError('formal source symlink refused')
        required=PROOF_THEOREMS.get(module,())
        actual=base.validate_module_source(path,required)
        if tuple(actual)!=required:raise ValueError('exact reviewed theorem roster required: '+module)
        code=base.module_code(path.read_text())
        matches=re.findall(r'\bEXTENDS[ \t]+([A-Za-z_][A-Za-z0-9_]*(?:\s*,\s*[A-Za-z_][A-Za-z0-9_]*)*)',code)
        if len(matches)!=1 or tuple(re.split(r'\s*,\s*',matches[0]))!=imports:
            raise ValueError('exact reviewed imports required: '+module)
        if re.search(r'\b(?:INSTANCE|AXIOM|ASSUMPTION|OMITTED|LEMMA|COROLLARY|PROPOSITION)\b|^\s*ASSUME\b',code,re.M):
            raise ValueError('unreviewed formal declaration: '+module)
        qualified.extend(module+'!'+name for name in required)
    if len(qualified)!=len(set(qualified)):raise ValueError('duplicate qualified theorem')
    return {name:file_digest(directory/name) for name in sorted(FORMAL_FILES)},qualified


def states(rows):
    if type(rows) is not list or not 2 <= len(rows) <= 128:
        raise ValueError('bounded complete publication trace required')
    for row in rows:
        if type(row) is not dict or set(row) != set(SCALARS)|{'native'}:
            raise ValueError('noncanonical publication state fields')
        for key,kind in SCALARS.items():
            value=row[key]
            if type(value) is not kind:
                raise ValueError('wrong publication scalar type: '+key)
            if kind is int and not 0 <= value <= 10000:
                raise ValueError('unbounded publication scalar')
            if kind is str and (len(value)>128 or not value.isascii()):
                raise ValueError('invalid publication scalar text')
        if row['schema']!='arm.share-publication/v1':
            raise ValueError('unsupported publication schema')
        if type(row['native']) is not dict or set(row['native'])!={'preparation','verification'}:
            raise ValueError('exact native scope roster required')
        for native in row['native'].values():
            if type(native) is not dict or set(native)!={'status','count','joined','skipped'}:
                raise ValueError('noncanonical native record')
            if type(native['status']) is not str or native['status'] not in ('never','running','joined','lost','skipped') or type(native['count']) is not int or not 0<=native['count']<=10000 or type(native['joined']) is not bool or type(native['skipped']) is not bool:
                raise ValueError('invalid native scalar')
    return rows

def capacity_input_roster():
    """Ordered boundary controls; duplicates are intentional and remain counted.

    Expected controls only: acceptance and resulting accounting are independently
    returned by Go and checked against the imported TLA+ capacity specification.
    """
    max_rows,max_bytes,max_event,max_slots=16384,16777216,8192,8
    before_states=((0,0,0),(max_rows-8,max_bytes-8*max_event,8),
                   (max_rows,max_bytes,0),(0,max_bytes-1,0),
                   (max_rows-1,0,0),(1,max_bytes-8*max_event,8))
    return tuple((rows,byte_count,reserved,event,old,new)
                 for rows,byte_count,reserved in before_states
                 for event in (0,1,max_event,max_event+1)
                 for old in (0,reserved,reserved+1)
                 for new in (0,max(0,old-1),max_slots,max_slots+1))

def capacity_cases(cases):
    expected=capacity_input_roster()
    if type(cases) is not list or len(cases)!=len(expected):
        raise ValueError('complete exact capacity case roster required')
    for index,(c,controls) in enumerate(zip(cases,expected)):
        if type(c) is not dict or set(c)!={'before','event','old','new','accepted','after'} or type(c['accepted']) is not bool:
            raise ValueError('noncanonical capacity result')
        for key in ('event','old','new'):
            if type(c[key]) is not int or not 0<=c[key]<=20000:
                raise ValueError('invalid capacity control scalar')
        for key in ('before','after'):
            if type(c[key]) is not dict or set(c[key])!={'rows','bytes','reserved'} or any(type(v) is not int or not 0<=v<=1<<30 for v in c[key].values()):
                raise ValueError('invalid capacity state')
        actual=tuple(c['before'][key] for key in ('rows','bytes','reserved'))+tuple(c[key] for key in ('event','old','new'))
        if actual!=controls:
            raise ValueError('capacity input roster differs at case '+str(index+1))
    return cases

def envelope(rows):
    states(rows)
    roster=[[i,{'state':s}] for i,s in enumerate(rows,1)]
    return {'vars':['state'],'counterexample':{'state':roster,'action':[
        [roster[i],{'name':rows[i+1]['action']},roster[i+1]] for i in range(len(rows)-1)]}}

def tlc_states(path):
    doc=base.read_json(path)
    if type(doc) is not dict or set(doc)!={'counterexample','vars'} or doc['vars']!=['state']:
        raise ValueError('unexpected publication TLC envelope')
    ce=doc['counterexample']
    if type(ce) is not dict or set(ce)!={'state','action'} or type(ce['state']) is not list or len(ce['state'])>128:
        raise ValueError('unexpected publication TLC roster')
    rows=[]
    for i,row in enumerate(ce['state'],1):
        if type(row) is not list or len(row)!=2 or row[0]!=i or type(row[1]) is not dict or set(row[1])!={'state'}:
            raise ValueError('noncontiguous publication TLC roster')
        rows.append(row[1]['state'])
    if type(ce['action']) is not list or len(ce['action'])!=len(rows)-1:
        raise ValueError('missing publication TLC edges')
    for i,edge in enumerate(ce['action']):
        if type(edge) is not list or len(edge)!=3 or edge[0]!=ce['state'][i] or edge[2]!=ce['state'][i+1] or type(edge[1]) is not dict:
            raise ValueError('nonadjacent publication TLC edges')
    return states(rows)

class PublicationGate(base.Gate):
    def __init__(self,args):
        self.args=args
        work=Path(os.path.abspath(args.work_dir))
        if not work.is_relative_to(ROOT/'.tla'):
            raise ValueError('publication work must stay beneath .tla')
        current=ROOT
        for part in work.relative_to(ROOT).parts:
            current=current/part
            if current.is_symlink():raise ValueError('publication work cannot contain symlinks')
            current.mkdir(mode=0o700,exist_ok=True)
        self.run=Path(tempfile.mkdtemp(prefix='run-',dir=work))
        self.java_tmp=self.run/'java-tmp';self.java_tmp.mkdir(mode=0o700)
        self.summary={'schema':1,'suite':'share-publication','mode':args.mode,'artifacts':str(self.run),'targets':list(args.targets),'gates':[],'tools':{}}
        for source in sorted((ROOT/'formal/share-publication').iterdir()):
            if source.suffix in ('.tla','.cfg'):shutil.copyfile(source,self.run/source.name)
        self.summary['sources']={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(self.run.iterdir()) if p.suffix in ('.tla','.cfg')}
        self.initial_state=None
        self.code_sources={name:file_receipt(ROOT/name) for name in CODE_FILES}
    def check(self):
        log=self.tlc('SharePublication.tla',self.run/'safety.cfg','safety')
        explored=re.findall(r'(\d+) distinct states found, (\d+) states left on queue',log)
        if 'Model checking completed. No error has been found.' not in log or not explored or int(explored[-1][0])==0 or int(explored[-1][1])!=0:
            raise RuntimeError('publication TLC exploration incomplete or empty')
        self.summary['gates'].append({'safety':'passed','statistics':re.findall(r'.*(?:states generated|depth of the complete).*',log)})
        capacity_log=self.tlc('JournalCapacity.tla',self.run/'capacity.cfg','capacity-safety')
        explored=re.findall(r'(\d+) distinct states found, (\d+) states left on queue',capacity_log)
        if 'Model checking completed. No error has been found.' not in capacity_log or not explored or int(explored[-1][0])==0 or int(explored[-1][1])!=0:raise RuntimeError('capacity exploration incomplete')
        self.summary['gates'].append({'capacity_safety':'passed','statistics':re.findall(r'.*(?:states generated|depth of the complete).*',capacity_log)})
    def replay(self,rows,name,accepted=True):
        states(rows)
        if not accepted and (self.initial_state is None or rows[0]!=self.initial_state):
            raise ValueError('negative must preserve validated exact Init')
        module='Replay'+re.sub('[^A-Za-z0-9]','',name)
        source='---- MODULE '+module+' ----\nEXTENDS SharePublication, Sequences\nVARIABLE cursor\nObserved == <<'+',\n'.join(base.tla_value(row) for row in rows)+'>>\n'
        source+='ReplayInit == Init /\\ cursor = 1 /\\ state = Observed[1]\nReplayNext == /\\ cursor < Len(Observed) /\\ state\' = Observed[cursor+1] /\\ cursor\' = cursor+1 /\\ Next\nNotEnd == cursor < Len(Observed)\n====\n'
        (self.run/(module+'.tla')).write_text(source)
        cfg=self.run/(module+'.cfg');cfg.write_text('INIT ReplayInit\nNEXT ReplayNext\nCHECK_DEADLOCK FALSE\nINVARIANT InductiveInvariant\nINVARIANT NotEnd\n')
        raw=self.run/(name+'-replay.json');raw.write_text(json.dumps(envelope(rows))+'\n')
        log=self.tlc(module+'.tla',cfg,name+'-reverse',('-loadTrace','json',raw),expected=12 if accepted else 0)
        count=re.findall(r'(\d+) distinct states found',log)
        if accepted:
            if 'Invariant NotEnd is violated.' not in log or not count or int(count[-1])!=len(rows):raise RuntimeError('publication observations not completely replayed')
            if self.initial_state is not None and self.initial_state!=rows[0]:raise ValueError('positive Init differs')
            self.initial_state=json.loads(json.dumps(rows[0]))
        elif 'Invariant NotEnd is violated.' in log or not count or int(count[-1])<1:
            raise RuntimeError('negative was accepted or did not explore valid Init')
    def traces(self):
        self.check();paths=[]
        for target in self.args.targets:
            cfg=self.run/(target+'.cfg');cfg.write_text('SPECIFICATION Spec\nCHECK_DEADLOCK FALSE\nCONSTANT Target = '+json.dumps(target)+'\nINVARIANT InductiveInvariant\nINVARIANT NotReached\n')
            raw=self.run/(target+'-tlc.json')
            log=self.tlc('Witness.tla',cfg,target+'-witness',('-dumpTrace','json',raw),expected=12)
            if 'Invariant NotReached is violated.' not in log:raise RuntimeError('witness violated safety rather than coverage')
            rows=tlc_states(raw);path=self.run/(target+'-expected.json');path.write_text(json.dumps(rows,indent=2)+'\n')
            paths.append({'target':target,'path':str(path),'output':str(self.run/(target+'-go.json'))})
        manifest=self.run/'witnesses.json';manifest.write_text(json.dumps(paths,indent=2)+'\n')
        negative_output=self.run/'actual-negatives.json'
        capacity_output=self.run/'capacity-go.json'
        env=dict(os.environ,ARM_PUBLICATION_WITNESSES=str(manifest),ARM_PUBLICATION_NEGATIVES=str(negative_output),ARM_PUBLICATION_CAPACITY=str(capacity_output));tmp=self.run/'tmp';tmp.mkdir(mode=0o700);env['TMPDIR']=str(tmp)
        self.command([self.args.go,'test','-race','-tags','arm_devtrace','./internal/manager','-run','^TestSharePublicationFormal(Witnesses|Negatives|Capacity)$','-count=1','-v','-timeout=240s'],'go-witnesses',env=env,timeout=270)
        for item in paths:
            actual=states(base.read_json(item['output']))
            expected=states(base.read_json(item['path']))
            if item['target']=='retirement_decision_recovery':
                if actual[:len(expected)]!=expected or [r['action'] for r in actual[len(expected):]]!=['retired','retry'] or any(r['outcome']!='released' or r['end_decision']!='released' for r in actual[len(expected):]):raise RuntimeError('actual retirement decision suffix differs')
            elif actual!=expected:raise RuntimeError('actual publication observations differ: '+item['target'])
            self.replay(actual,item['target'])
        negatives=base.read_json(negative_output)
        required={'actual-premature-release','actual-premature-publication','actual-original-change','actual-intent-change','actual-duplicate-verifier'}
        if type(negatives) is not dict or set(negatives)!=required:raise ValueError('complete actual publication negative inventory required')
        for name,rows in negatives.items():self.replay(rows,name,accepted=False)
        self.capacity(base.read_json(capacity_output))
        self.summary['gates'].append({'traces':'passed','witnesses':len(paths),'coverage_complete':set(self.args.targets)==set(TARGETS),'actual_negatives_rejected':sorted(required)})
    def capacity(self,cases):
        capacity_cases(cases)
        source='---- MODULE CapacityObservations ----\nEXTENDS JournalCapacity, Sequences\nVARIABLE cursor\nCases == <<'+',\n'.join(base.tla_value(c) for c in cases)+'>>\nCheckInit == /\\ cursor=1 /\\ capacity=[rows |-> 0, bytes |-> 0, reserved |-> 0]\nCheckNext == /\\ cursor<Len(Cases) /\\ cursor\'=cursor+1 /\\ UNCHANGED capacity\nMatches == LET c == Cases[cursor] IN IF CanChange(c.before,c.event,c.old,c.new) THEN c.accepted /\\ c.after=Plan(c.before,c.event,c.old,c.new) ELSE ~c.accepted /\\ c.after=c.before\n====\n'
        (self.run/'CapacityObservations.tla').write_text(source)
        cfg=self.run/'capacity-observations.cfg';cfg.write_text('INIT CheckInit\nNEXT CheckNext\nCHECK_DEADLOCK FALSE\nCONSTANT MaxRows=16384\nCONSTANT MaxBytes=16777216\nCONSTANT MaxEvent=8192\nCONSTANT MaxSlots=8\nINVARIANT Matches\n')
        log=self.tlc('CapacityObservations.tla',cfg,'capacity-observations')
        if 'Model checking completed. No error has been found.' not in log or not re.search(r'\b288 distinct states found, 0 states left on queue',log):raise RuntimeError('actual capacity observations incomplete')
        self.summary['gates'].append({'actual_journal_capacity':'passed','cases':len(cases),'limits':{'rows':16384,'bytes':16777216,'event':8192,'slots':8}})
    def backend_closure(self,name):
        config=self.command([self.args.tlapm,'--config'],name+'-config',timeout=30)
        def one(pattern,label):
            values=re.findall(pattern,config,re.M)
            if len(set(values))!=1:raise ValueError('one configured '+label+' required')
            return values[0]
        library=Path(one(r'^library_path == "([^"]+)"$', 'library path'))
        search=one(r'^search_path == << (.*?) >>$', 'search path')
        if search!='"'+str(library)+'"':raise ValueError('unreviewed TLAPM search path')
        homes={key:Path(one("export "+key+"='([^']+)'",key)) for key in ('ISABELLE_HOME','ML_HOME')}
        paths=[Path(shutil.which(str(self.args.tlapm)) or self.args.tlapm)]
        backend_paths=re.findall(r"PATH='([^']+)'",config)
        if not backend_paths or len(set(backend_paths))!=1:
            raise ValueError('one consistent configured backend PATH required')
        for tool in ('z3','zenon'):
            path=shutil.which(tool,path=backend_paths[0])
            if not path:raise ValueError('configured backend missing: '+tool)
            paths.append(Path(path))
        wrapper=shutil.which('isabelle',path=backend_paths[0])
        if wrapper:paths.append(Path(wrapper))
        # The pinned Isabelle command invokes ML_HOME/poly directly; a PATH
        # wrapper is optional and is never accepted as evidence for those heaps.
        # All configured PolyML files, not merely the PATH Isabelle wrapper.
        if not (homes['ML_HOME']/'poly').is_file():raise ValueError('actual PolyML executable required')
        paths.extend(p for p in homes['ML_HOME'].rglob('*') if p.is_file())
        paths.extend(homes['ISABELLE_HOME']/'heaps'/h for h in ('Pure','TLA+','Options'))
        # Naturals/Sequences are built into TLAPM; capture all installed library
        # files as well, including transitive TLAPS definitions/annotations.
        if not (library/'TLAPS.tla').is_file():raise ValueError('actual TLAPS library required')
        paths.extend(p for p in library.rglob('*') if p.is_file())
        return {'config_sha256':hashlib.sha256(config.encode()).hexdigest(),
                'artifacts':[file_receipt(p) for p in sorted(set(paths))]}

    def prove(self):
        # Check original capture before creating a separate closed proof directory;
        # generated trace modules elsewhere in the run are not proof inputs.
        original=ROOT/'formal/share-publication'
        source_hashes,qualified=validate_sources(original)
        if self.summary.get('sources')!=source_hashes:
            raise RuntimeError('initial captured proof source snapshot changed or missing')
        if not getattr(self,'code_sources',None):raise RuntimeError('initial runner source capture missing')
        proof_dir=self.run/'proof-source';proof_dir.mkdir(mode=0o700)
        for name in sorted(FORMAL_FILES):
            if file_digest(self.run/name)!=source_hashes[name]:
                raise RuntimeError('captured proof source changed: '+name)
            shutil.copyfile(self.run/name,proof_dir/name)
        self.summary['proof_sources']=source_hashes
        self.summary['qualified_theorems']=qualified
        self.summary['proof_progress']=[]
        self.summary['proof_status']='incomplete'
        self.summary['runner_sources']=self.code_sources
        self.summary['closure_checks']=[]
        def sources():
            if any((ROOT/(m+'.tla')).exists() for m in (*MODULE_IMPORTS,'TLAPS','Naturals','Sequences')):
                raise RuntimeError('ambient formal import shadowing refused')
            for directory in (original,proof_dir):
                inventory={p.name for p in directory.iterdir() if p.suffix in ('.tla','.cfg')}
                if inventory!=FORMAL_FILES or any((directory/n).is_symlink() or file_digest(directory/n)!=h for n,h in source_hashes.items()):
                    raise RuntimeError('proof source changed during validation')
            if any(file_digest(self.run/n)!=h for n,h in source_hashes.items()):
                raise RuntimeError('captured proof source changed during validation')
            if any(file_receipt(ROOT/n)!=h for n,h in self.code_sources.items()):
                raise RuntimeError('verification runner source changed during validation')
        if validate_sources(proof_dir)[0]!=source_hashes:raise RuntimeError('proof source copy differs')
        sources()
        backend=self.backend_closure('proof-initial')
        self.summary['backend_closure']=backend
        def boundary(label):
            sources()
            current=self.backend_closure(label)
            sources()
            if current!=backend:raise RuntimeError('actual backend/import closure changed: '+label)
            self.summary['closure_checks'].append(label)
        try:
            for module in PROOF_MODULES:
                path=proof_dir/(module+'.tla')
                boundary(module+'-before-parser')
                parsed=self.command([self.args.tlapm,'--summary','--cache-dir',
                    self.run/(module+'-parser'),'-I',proof_dir,path],module+'-scope',timeout=120)
                boundary(module+'-after-parser')
                counts=re.findall(r'^\s*obligations_count\s*=\s*(\d+)\s*$',parsed,re.M)
                modules=re.findall(r'^---- summary of module "([^"]+)" ----$',parsed,re.M)
                if modules!=[module] or len(counts)!=1 or int(counts[0])<=0 or '[ERROR]' in parsed:
                    raise RuntimeError('complete parsed proof scope required: '+module)
                expected=int(counts[0])
                boundary(module+'-before-proof')
                argv=[self.args.tlapm,'--strict','--nofp','--threads','2','--stretch','10']
                if module=='JournalCapacityProof':argv+=['--smt-logic','AUFNIA']
                argv+=['--cache-dir',self.run/(module+'-proofs'),'-I',proof_dir,path]
                log=self.command(argv,module+'-tlapm',timeout=3600)
                boundary(module+'-after-proof')
                proved=re.findall(r'^(?:\[INFO\]: )?All (\d+) obligations proved\.[ \t]*$',log,re.M)
                if len(proved)!=1 or int(proved[0])!=expected or '[ERROR]' in log:
                    raise RuntimeError('complete parsed proof obligations must pass: '+module)
                self.summary['proof_progress'].append({'module':module,'obligations':expected,
                    'theorems':[module+'!'+n for n in PROOF_THEOREMS[module]]})
            if tuple(r['module'] for r in self.summary['proof_progress'])!=PROOF_MODULES:
                raise RuntimeError('every proof module must pass, in reviewed order')
            boundary('proof-complete')
            self.summary['gates'].append({'proofs':'passed','modules':self.summary['proof_progress']})
            self.summary['proof_status']='passed'
        finally:
            # Cancellation must not launch another isolated subprocess. The accepted
            # command wrapper already kills/reaps its owned tool group and saves logs.
            (self.run/'summary.json').write_text(json.dumps(self.summary,indent=2)+'\n')

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mode',choices=('check','traces','prove','all'),default='all')
    parser.add_argument('--java',default='java')
    parser.add_argument('--tlc-jar',default=str(ROOT/'.tla/tools/tlc/tla2tools.jar'))
    parser.add_argument('--tlapm',default=str(ROOT/'.tla/tools/tlapm/tlapm/bin/tlapm'))
    parser.add_argument('--go',default='go')
    parser.add_argument('--work-dir',default=str(ROOT/'.tla/share-publication'))
    parser.add_argument('--targets',nargs='+',choices=TARGETS,default=list(TARGETS))
    args=parser.parse_args()
    if len(set(args.targets))!=len(args.targets):parser.error('duplicate targets')
    gate=PublicationGate(args)
    try:
        gate.tools()
        if args.mode=='check':gate.check()
        elif args.mode=='traces':gate.traces()
        elif args.mode=='prove':gate.prove()
        else:gate.traces();gate.prove()
        gate.finish()
    except BaseException:
        gate.summary['status']='incomplete'
        (gate.run/'summary.json').write_text(json.dumps(gate.summary,indent=2)+'\n')
        raise
if __name__=='__main__':
    signal.signal(signal.SIGTERM,lambda signum,frame:sys.exit(128+signum))
    try:main()
    except KeyboardInterrupt:sys.exit(130)
    except (ValueError,RuntimeError,subprocess.TimeoutExpired,OSError) as error:
        print('publication verification failed: '+str(error),file=sys.stderr);sys.exit(1)
