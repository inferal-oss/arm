#!/usr/bin/env python3
"""Bounded diagnostic-worker lifecycle/capacity models and actual Go observations.

Owned process cleanup is inherited from the accepted preparation runner. This
suite has a closed model/theorem roster, exact imported-Next replays (no hidden
stuttering), guarded Go evidence, and pre/post source/tool closure checks. The
six-key capacity instance is explicit; this is not full provisioning/reset proof.
"""
import argparse,copy,hashlib,importlib.util,json,os,re,shutil,signal,subprocess,sys,tempfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
MODEL=ROOT/'formal/provider-worker'
def load(name,path):
 spec=importlib.util.spec_from_file_location(name,path);module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module
base=load('accepted_preparation_runner',ROOT/'scripts/test-formal.py')
TARGETS=('success','cancel_before_grant','cancel_during_start','wait_pending','group_pending','invalid_output','pin_replaced','written_temp')
MODEL_FILES={'ProviderWorker.tla','ProviderWorker.cfg','ProviderWorkerProof.tla','WorkerCapacity.tla','WorkerCapacity.cfg','WorkerCapacityProof.tla','export_trace.py','export_capacity.py'}
PROOFS={'ProviderWorkerProof.tla': ('InitEstablishesInvariant', 'PreserveAdmitWorker', 'PreserveAdmitTemp', 'PreserveReserve', 'PreserveMarkStart', 'PreserveRetain', 'PreserveStart', 'PreserveStartReturned', 'PreserveKnownNoStart', 'PreserveHello', 'PreserveReady', 'PreserveDispatch', 'PreserveGrant', 'PreserveCancel', 'PreserveTerm', 'PreserveKill', 'PreserveReap', 'PreserveObserveClosed', 'PreserveObserve', 'PreserveUnknown', 'PreserveLoseOwner', 'PreserveCompensate', 'PreserveCleanTemp', 'PreserveReleaseWorker', 'PreserveSettle', 'PreserveRetire', 'EveryStepPreservesInvariant', 'StutterPreserves', 'SafetyFollows', 'WriteOnceAdmitWorker', 'WriteOnceAdmitTemp', 'WriteOnceReserve', 'WriteOnceMarkStart', 'WriteOnceRetain', 'WriteOnceStart', 'WriteOnceStartReturned', 'WriteOnceKnownNoStart', 'WriteOnceHello', 'WriteOnceReady', 'WriteOnceDispatch', 'WriteOnceGrant', 'WriteOnceCancel', 'WriteOnceTerm', 'WriteOnceKill', 'WriteOnceReap', 'WriteOnceObserveClosed', 'WriteOnceObserve', 'WriteOnceUnknown', 'WriteOnceLoseOwner', 'WriteOnceCompensate', 'WriteOnceCleanTemp', 'WriteOnceReleaseWorker', 'WriteOnceSettle', 'WriteOnceRetire', 'WriteOncePreserved', 'NeverSignalAfterReap', 'NeverSignalRecoveredIdentity', 'StrengthenedSafety', 'WorkerSafety'), 'WorkerCapacityProof.tla': ('TypeProjection', 'SampleBounds', 'LimitNumbers', 'ReserveScalar', 'RegistrationCases', 'CountNumbers', 'ChargeNumbers', 'ParameterNumbers', 'TotalNumbers', 'NativePhaseFields', 'NativeSettleFields', 'BitNumbers', 'SumReplace1', 'SumReplace2', 'SumReplace3', 'SumReplace4', 'SumReplace5', 'SumReplace6', 'ReserveCounts', 'SettleCounts', 'StableCounts', 'StableCharges', 'StableTotals', 'ReserveCharge1', 'ReserveTotal1', 'ReserveRegTotal1', 'SettleCharge1', 'SettleTotal1', 'SettleRegTotal1', 'ReserveCapacity1', 'SettleCapacity1', 'ReserveCharge2', 'ReserveTotal2', 'ReserveRegTotal2', 'SettleCharge2', 'SettleTotal2', 'SettleRegTotal2', 'ReserveCapacity2', 'SettleCapacity2', 'ReserveCharge3', 'ReserveTotal3', 'ReserveRegTotal3', 'SettleCharge3', 'SettleTotal3', 'SettleRegTotal3', 'ReserveCapacity3', 'SettleCapacity3', 'ReserveCharge4', 'ReserveTotal4', 'ReserveRegTotal4', 'SettleCharge4', 'SettleTotal4', 'SettleRegTotal4', 'ReserveCapacity4', 'SettleCapacity4', 'ReserveCharge5', 'ReserveTotal5', 'ReserveRegTotal5', 'SettleCharge5', 'SettleTotal5', 'SettleRegTotal5', 'ReserveCapacity5', 'SettleCapacity5', 'ReserveCharge6', 'ReserveTotal6', 'ReserveRegTotal6', 'SettleCharge6', 'SettleTotal6', 'SettleRegTotal6', 'ReserveCapacity6', 'SettleCapacity6', 'InitialType', 'InitialCapacity', 'ReserveType', 'UnknownType', 'EligibleType', 'SettleType', 'ReserveCapacity', 'SettleCapacity', 'UnknownCapacity', 'EligibleCapacity', 'NoReplayStep', 'UnknownRetainsSlot', 'SettledDoesNotMutate', 'EveryStep', 'Stutter', 'CapacitySafety')}

def digest(path):
 h=hashlib.sha256()
 with Path(path).open('rb') as stream:
  for chunk in iter(lambda:stream.read(1<<20),b''):h.update(chunk)
 return h.hexdigest()

def model_roster(directory):
 actual={p.name for p in directory.iterdir() if p.is_file() and p.suffix in ('.tla','.cfg','.py')}
 if actual!=MODEL_FILES:raise ValueError('exact worker model/export/proof roster required')
 for name in MODEL_FILES:
  p=directory/name
  if p.is_symlink() or not p.is_file() or p.stat().st_size>1<<20:raise ValueError('bounded real model source required')
 return sorted(actual)

def literal(v):
 if type(v) is bool:return 'TRUE' if v else 'FALSE'
 if type(v) is int:return str(v)
 if type(v) is str:return json.dumps(v)
 if type(v) is list:
  if len(v)!=6:raise ValueError('six-key function observation required')
  return r'[k \in 1..6 |-> CASE '+' [] '.join('k='+str(i+1)+' -> '+literal(x) for i,x in enumerate(v))+']'
 if type(v) is dict:
  if set(v)==set(map(str,range(8))):return r'[i \in 0..7 |-> CASE '+' [] '.join('i='+k+' -> '+literal(x) for k,x in v.items())+']'
  return '['+', '.join(k+' |-> '+literal(x) for k,x in v.items())+']'
 raise ValueError('unsupported typed observation')

class Gate(base.Gate):
 def __init__(self,args):
  self.args=args;work=Path(os.path.abspath(args.work_dir));allowed=ROOT/'.tla'
  if not work.is_relative_to(allowed):raise ValueError('worker work directory must stay beneath repository .tla')
  current=ROOT
  for part in work.relative_to(ROOT).parts:
   current=current/part
   if current.is_symlink():raise ValueError('worker work path cannot contain symlinks')
   current.mkdir(mode=0o700,exist_ok=True)
  self.run=Path(tempfile.mkdtemp(prefix='run-',dir=work));self.java_tmp=self.run/'java-tmp';self.java_tmp.mkdir(mode=0o700)
  self.summary={'schema':'arm.provider-worker-formal/v1','mode':args.mode,'targets':list(args.targets),'artifacts':str(self.run),'gates':[],'tools':{}}
  for name in model_roster(MODEL):shutil.copyfile(MODEL/name,self.run/name)
  self.summary['sources']={name:digest(self.run/name) for name in sorted(MODEL_FILES)}
  self.summary['inputs']=self.input_hashes()
  self.export=load('worker_observations',self.run/'export_trace.py');self.capacity=load('worker_capacity_observations',self.run/'export_capacity.py')
  self.proof_tools={}
  self.capture_runner_sources()
 def input_hashes(self):
  paths=[ROOT/'go.mod',ROOT/'go.sum',ROOT/'scripts/test-formal.py',ROOT/'scripts/formal_process.py',Path(__file__)]
  for folder in ('internal','cmd'):paths.extend((ROOT/folder).rglob('*.go'))
  paths.extend(MODEL/name for name in MODEL_FILES)
  return {str(p.relative_to(ROOT)):digest(p) for p in sorted(set(paths))}
 def guard(self):
  if self.summary['sources']!={name:digest(self.run/name) for name in sorted(MODEL_FILES)}:raise ValueError('captured worker model snapshot changed')
  if self.summary['inputs']!=self.input_hashes():raise ValueError('captured worker input/source snapshot changed')
 def tools(self):
  super().tools()
  if self.args.mode in ('prove','all'):
   config=self.summary['tools']['tlapm']['config'];paths=re.findall(r"PATH='([^']+)'",config)
   for name in ('ptl_to_trp','ls4'):
    found=shutil.which(name,path=paths[0] if paths else '')
    if not found:raise ValueError('cannot identify actual configured temporal backend '+name)
    self.tool('tlapm-'+name,found)
   match=re.search(r'^library_path == (".*")$',config,re.M)
   if not match:raise ValueError('cannot identify actual TLAPM standard module directory')
   stdlib=Path(json.loads(match.group(1)))
   closure=list(stdlib.glob('*.tla'))+list(stdlib.glob('*.jar'))
   # The exact configured PolyML and heap hierarchy, even when only version
   # startup uses them; no claim every obligation is checked in Isabelle.
   home=re.search(r"ISABELLE_HOME='([^']+)'",config)
   ml=re.search(r"ML_HOME='([^']+)'",config)
   if not home or not ml:raise ValueError('actual configured Isabelle/PolyML closure unavailable')
   closure += [Path(ml.group(1))/'poly',Path(ml.group(1))/'libgmp.10.dylib',Path(ml.group(1))/'libsha1.so']
   closure += [Path(home.group(1))/'heaps'/name for name in ('Pure','TLA+','Options')]
   closure += [Path(item['path']) for item in self.summary['tools'].values() if 'path' in item]
   self.proof_tools={str(p):digest(p) for p in sorted(set(closure))}
   self.summary['proof_tool_module_closure_before']=dict(self.proof_tools)
 def proof_guard(self):
  self.guard()
  if self.proof_tools!={p:digest(p) for p in self.proof_tools}:raise ValueError('proof tool/module closure changed')
 def check(self):
  self.guard()
  for module in ('ProviderWorker','WorkerCapacity'):
   log=self.command([self.args.java,'-XX:+UseParallelGC','-Xmx256m','-cp',self.args.tlc_jar,'tlc2.TLC','-workers','1','-fp','0','-noGenerateSpecTE','-metadir',self.run/(module+'-states'),'-config',self.run/(module+'.cfg'),self.run/(module+'.tla')],'check-'+module,timeout=120)
   if 'Model checking completed. No error has been found.' not in log or '0 states left on queue.' not in log:raise ValueError('incomplete exhaustive worker model check')
  self.guard()
 def replay(self,rows,name,module,positive,initial_sizes=None,samples=None):
  (self.export.validate_states if module=='ProviderWorker' else self.capacity.validate_states)(rows)
  unique='Replay'+re.sub('[^A-Za-z0-9]','',name);variable='state' if module=='ProviderWorker' else 'ledger'
  text='---- MODULE '+unique+' ----\nEXTENDS '+module+', Sequences\nVARIABLE cursor\n'
  if initial_sizes is not None:text+='ActualInitial == '+literal(initial_sizes)+'\n'
  text+='Observed == <<'+',\n'.join(literal(s) for s in rows)+'>>\n'
  conj='/'+chr(92)
  text+=f"ReplayInit == Init {conj} cursor=1 {conj} {variable}=Observed[1]\nReplayNext == {conj} cursor<Len(Observed) {conj} {variable}'=Observed[cursor+1] {conj} cursor'=cursor+1 {conj} Next\nNotEnd == cursor<Len(Observed)\n====\n"
  source=self.run/(unique+'.tla');source.write_text(text)
  config=self.run/(unique+'.cfg');params=''
  if initial_sizes is not None:params='CONSTANTS\nInitialBytes <- ActualInitial\nTerminalLimit=34816\nOverhead=1024\nRowLimit=128\nAuthorityLimit=4194304\nRegistrationLimit=131072\nTerminalSamples={'+','.join(map(str,samples))+'}\n'
  config.write_text(params+'INIT ReplayInit\nNEXT ReplayNext\nCHECK_DEADLOCK FALSE\nINVARIANT NotEnd\n')
  log=self.command([self.args.java,'-XX:+UseParallelGC','-Xmx256m','-cp',self.args.tlc_jar,'tlc2.TLC','-workers','1','-fp','0','-noGenerateSpecTE','-metadir',self.run/(name+'-states'),'-config',config,source],name,expected=12 if positive else 0,timeout=30)
  reached='Invariant NotEnd is violated.' in log
  if reached!=positive:raise ValueError('wrong strict original Next result '+name)
  if positive and not re.search(r'\b'+str(len(rows))+r' distinct states found',log):raise ValueError('incomplete actual observation replay '+name)
  self.summary['gates'].append({'replay':name,'accepted':positive,'rows':len(rows),'source_sha256':digest(source),'config_sha256':digest(config)})
 def traces(self):
  self.guard();trace=self.run/'native-traces';trace.mkdir(mode=0o700)
  env=os.environ.copy();env['ARM_PROVIDER_WORKER_TRACE_DIR']=str(trace);env['ARM_PROVIDER_WORKER_CAPACITY_OUT']=str(self.run/'actual-capacity.json');env['GOMAXPROCS']='2'
  pattern='^TestObservedWorkerOwnershipScenarios$/^('+'|'.join(self.args.targets)+')$'
  self.command([self.args.go,'test','-race','-tags','arm_devtrace','-count=1','-timeout','4m','-run',pattern,'./internal/providerexec'],'actual-worker-traces',env=env,timeout=260)
  self.command([self.args.go,'test','-race','-tags','arm_devtrace','-count=1','-timeout','90s','-run','^(TestObservedDurableAuthorityCapacityAndFinalization|TestWorkerFormalArtifactOutputGuards|TestObservedSQLByteEnvelope.*)$','./internal/providerexec'],'actual-capacity-traces',env=env,timeout=110)
  self.guard();actual={}
  for target in self.args.targets:
   parsed=self.export.extract(trace/(target+'.json'));actual[target]=parsed['rows']
   (self.run/(target+'-observed.json')).write_text(json.dumps(parsed,indent=2)+'\n')
   self.replay(parsed['rows'],'actual-'+target,'ProviderWorker',True)
  if set(self.args.targets)==set(TARGETS):
   negatives={}
   def changed(name,target):
    result=copy.deepcopy(actual[target]);negatives[name]=result;return result
   rows=changed('repeated-grant','success');i=next(i for i,s in enumerate(rows) if s['grant']);rows.insert(i+1,copy.deepcopy(rows[i]))
   rows=changed('erased-marker','success');i=next(i for i,s in enumerate(rows) if s['grant']);rows[i]['marker']=False;rows[i]['stamps']['1']=False
   rows=changed('unknown-slot-dropped','cancel_during_start');i=next(i for i,s in enumerate(rows) if s['unknown']);rows[i]['slot']=False
   rows=changed('premature-settled','written_temp');rows[-1]['settled']=True;rows[-1]['slot']=False;rows[-1]['stamps']['7']=True
   rows=changed('release-before-cleanup','success');i=next(i for i,s in enumerate(rows) if s['released']);rows[i]['cleanup']=False
   rows=changed('grant-after-cancel','cancel_before_grant');i=next(i for i,s in enumerate(rows) if s['cancelled']);rows[i]['grant']=True
   rows=changed('second-native-start','success');i=next(i for i,s in enumerate(rows) if s['starts']);rows[i]['starts']=2
   for name,rows in negatives.items():self.replay(rows,'negative-'+name,'ProviderWorker',False)
  cap=self.capacity.extract(self.run/'actual-capacity.json');rows=cap['states'];samples=sorted({n for s in rows for n in s['terminal'] if n>0})
  self.replay(rows,'actual-capacity','WorkerCapacity',True,cap['initial_bytes'],samples)
  negatives={}
  def changed(name):
   result=copy.deepcopy(rows);negatives[name]=result;return result
  bad=changed('duplicate-reservation');bad.insert(2,copy.deepcopy(bad[1]))
  bad=changed('unknown-erased');i=next(i for i,s in enumerate(bad) if s['phase'][1]=='unknown');bad[i]['phase'][1]='none'
  bad=changed('premature-settlement');i=next(i for i,s in enumerate(bad) if s['phase'][0]=='eligible');bad[i]['phase'][0]='settled';bad[i]['terminal'][0]=rows[-1]['terminal'][0]
  bad=changed('unknown-released');i=next(i for i,s in enumerate(bad) if s['phase'][1]=='unknown');bad[i]['phase'][1]='settled';bad[i]['terminal'][1]=1
  bad=changed('settled-bytes-changed');bad[-1]['terminal'][0]+=1
  bad=changed('fifth-slot');bad[4]['phase'][5]='live'
  bad=changed('old-key-replay');bad[-1]['phase'][0]='live';bad[-1]['terminal'][0]=0
  for name,bad in negatives.items():self.replay(bad,'negative-capacity-'+name,'WorkerCapacity',False,cap['initial_bytes'],samples)
  self.summary['actual_artifacts']={str(p.relative_to(self.run)):digest(p) for p in [*trace.glob('*.json'),self.run/'actual-capacity.json']}
  self.guard()
 def prove(self):
  self.proof_guard()
  for module,names in PROOFS.items():
   self.proof_guard();source=self.run/module;base.validate_module_source(source,names)
   parser=self.command([self.args.tlapm,'--summary','-I',self.run,source],'parser-'+source.stem,timeout=60)
   expected=re.findall(r'obligations_count = (\d+)',parser)
   if len(expected)!=1 or int(expected[0])<=0:raise ValueError('missing unique complete proof parser count')
   self.proof_guard()
   log=self.command([self.args.tlapm,'--strict','--nofp','--threads','1','--stretch','10','--cache-dir',self.run/(source.stem+'-proof'),'-I',self.run,source],'prove-'+source.stem,timeout=900)
   success=re.findall(r'All (\d+) obligations proved\.',log)
   if success!=expected:raise ValueError('full proof success count does not match exact parser scope')
   self.proof_guard();self.summary['gates'].append({'proof':module,'obligations':int(expected[0]),'required_theorems':list(names)})
  self.summary['proof_tool_module_closure_after']={p:digest(p) for p in self.proof_tools}
 def finish(self,result='passed',error=None):
  self.summary['result']=result
  if error is not None:self.summary['error']=str(error)
  (self.run/'summary.json').write_text(json.dumps(self.summary,indent=2)+'\n')
  print(json.dumps(self.summary,indent=2))

def main():
 parser=argparse.ArgumentParser(description=__doc__)
 parser.add_argument('--mode',choices=('check','traces','prove','all'),default='all')
 parser.add_argument('--java',default='java');parser.add_argument('--tlc-jar',default=str(ROOT/'.tla/tools/tlc/tla2tools.jar'))
 parser.add_argument('--tlapm',default=str(ROOT/'.tla/tools/tlapm/tlapm/bin/tlapm'));parser.add_argument('--go',default='go')
 parser.add_argument('--work-dir',default=str(ROOT/'.tla/provider-worker'));parser.add_argument('--targets',nargs='+',choices=TARGETS,default=list(TARGETS))
 args=parser.parse_args()
 if len(set(args.targets))!=len(args.targets):parser.error('duplicate targets')
 gate=Gate(args)
 try:
  gate.tools()
  if args.mode in ('check','all'):gate.check()
  if args.mode in ('traces','all'):gate.traces()
  if args.mode in ('prove','all'):gate.prove()
  gate.finish()
 except BaseException as error:
  gate.finish('failed',error);raise

if __name__=='__main__':
 signal.signal(signal.SIGTERM,lambda number,_:(_ for _ in ()).throw(SystemExit(128+number)))
 try:main()
 except KeyboardInterrupt:print('worker formal verification interrupted',file=sys.stderr);sys.exit(130)
 except (ValueError,RuntimeError,subprocess.TimeoutExpired,OSError) as error:print('worker formal verification failed: '+str(error),file=sys.stderr);sys.exit(1)
