#!/usr/bin/env python3
"""Strict bounded lease-clock safety, physical Go/SQL replay and TLAPM gate.

Controlled clock inputs do not prove native clock accuracy. The one-bare-memory
lease abstraction does not prove SQL atomicity, process/provider joins, pressure
controls or the separate incomplete workspace/provider clock producers.
"""
import argparse
import copy
import datetime
import hashlib
import importlib.util
import json
import os
import platform
import shutil
import stat
import tempfile
from pathlib import Path
import re
import sys

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[1]
MODEL = ROOT / 'formal/lease-clock'
SCENARIOS = {
 'wall-rollback': ('initial','grant','poll','renew','renew-refused','renew-refused'),
 'wall-forward': ('initial','grant','poll','renew','renew-refused','renew-refused'),
 'suspend-admission': ('initial','grant','admit','renew-refused'),
 'restart-live': ('initial','grant','restart'),
 'restart-expired': ('initial','grant','restart'),
 'restart-other-boot': ('initial','grant','restart'),
 'restart-regressed': ('initial','grant','restart'),
 'malformed-deadline': ('initial','grant','corrupt','poll','renew-refused'),
 'sticky-read-failure': ('initial','grant','clock-refused','sticky-refused'),
 'journal-refusal': ('initial','grant','journal-refused','poll'),
}
THEOREMS = ('InitialInvariant','StepInvariant','StepProtection','AlwaysInvariant','AlwaysProtection')
MODEL_FILES = {'LeaseClock.tla','LeaseClock.cfg','LeaseClockProof.tla'}
TEST = 'TestLeaseClockActualTrace'
PREFIX = 'LEASE_CLOCK_ACTUAL '
MODULE_LINE = (ROOT / 'go.mod').read_text().splitlines()[0]
require_module = re.fullmatch(r'module ([A-Za-z0-9._/-]+)', MODULE_LINE)
if require_module is None: raise ValueError('exact captured Go module declaration required')
GO_PACKAGE = require_module[1] + '/internal/manager'
NS = 10**9
ORIGIN = 3600 * NS
WALL_ORIGIN = int(datetime.datetime(2026,10,26,12,tzinfo=datetime.timezone.utc).timestamp()) * NS
FIELDS = {'now','wall','boot','phase','anchor','ends','leaseBoot','valid','known','charged','revision','checkpoint','checkpointBoot','nextGranted','action'}
LEASE_FIELDS = {'elapsed_deadline','cargo','pool_binding','pool_error','budget_binding','budget_policy','budget_error','queue_info','id','host','owner','owner_username','request','state','outcome','recovery_reason','failure','created_at','started_at','expires_at','ended_at','revision','pid','managed_dir','exit_code','capabilities','artifact','ports','process_identity','usage','workload'}


def require(value, message):
 if not value: raise ValueError(message)


def unique(pairs):
 out = {}
 for k,v in pairs:
  require(k not in out,'duplicate JSON key: '+k)
  out[k] = v
 return out


def parse(raw):
 return json.loads(raw, object_pairs_hook=unique, parse_constant=lambda x: (_ for _ in ()).throw(ValueError('nonfinite JSON '+x)))


def load(name,path):
 path = Path(path)
 require(path.is_file() and not path.is_symlink(),'regular captured Python source required')
 module = importlib.util.module_from_spec(importlib.util.spec_from_loader(name,loader=None))
 module.__file__ = str(path)
 exec(compile(path.read_bytes(),str(path),'exec'),module.__dict__)
 return module


base = load('lease_clock_formal_base',ROOT/'scripts/test-formal.py')
base.MODEL = MODEL


def inventory():
 excluded = {'.git','.cache','.tla','.tools','bin','dist','__pycache__'}
 result = {}
 def visit(directory):
  for p in sorted(directory.iterdir()):
   if p.name in excluded: continue
   require(not p.is_symlink(),'source symlink refused: '+str(p))
   if p.is_dir(): visit(p)
   elif p.is_file(): result[str(p.relative_to(ROOT))] = hashlib.sha256(p.read_bytes()).hexdigest()
   else: raise ValueError('nonregular source: '+str(p))
 visit(ROOT)
 return result


def timestamp(value):
 require(type(value) is str,'timestamp string required')
 match = re.fullmatch(r'(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?Z',value)
 require(match is not None,'exact UTC timestamp required')
 sec = int(datetime.datetime.strptime(match[1],'%Y-%m-%dT%H:%M:%S').replace(tzinfo=datetime.timezone.utc).timestamp())
 return sec*NS+int((match[2] or '').ljust(9,'0') or '0')


def unit(value):
 require(type(value) is int and (value-ORIGIN)%NS == 0,'exact controlled integral clock input required')
 n = (value-ORIGIN)//NS
 require(0<=n<=9,'clock outside bounded trace domain')
 return n


def sample(value):
 require(type(value) is dict and set(value)=={'boot','tick_ns','wall'},'closed native clock sample required')
 require(value['boot'] in ('a','b'),'controlled boot scope required')
 tick = unit(value['tick_ns'])
 wall_ns = timestamp(value['wall'])-WALL_ORIGIN
 require(wall_ns%(3600*NS)==0 and 0<=wall_ns//(3600*NS)<=2,'controlled wall input required')
 return tick,wall_ns//(3600*NS),value['boot']


INPUTS = {
 'wall-rollback': ((1,1,'a'),(1,1,'a'),(2,0,'a'),(2,0,'a'),(5,0,'a'),(5,0,'a')),
 'wall-forward': ((1,1,'a'),(1,1,'a'),(2,2,'a'),(2,2,'a'),(5,2,'a'),(5,2,'a')),
 'suspend-admission': ((1,1,'a'),(1,1,'a'),(3,1,'a'),(3,1,'a')),
 'restart-live': ((1,1,'a'),(1,1,'a'),(2,0,'a')),
 'restart-expired': ((1,1,'a'),(1,1,'a'),(3,1,'a')),
 'restart-other-boot': ((1,1,'a'),(1,1,'a'),(1,1,'b')),
 'restart-regressed': ((1,1,'a'),(1,1,'a'),(0,1,'a')),
 'malformed-deadline': ((1,1,'a'),(1,1,'a'),(1,1,'a'),(2,1,'a'),(2,1,'a')),
 'sticky-read-failure': ((1,1,'a'),(1,1,'a'),(1,1,'a'),(2,1,'a')),
 'journal-refusal': ((1,1,'a'),(1,1,'a'),(3,1,'a'),(3,1,'a')),
}


def event_delta(previous,row,before,after):
 delta=row['events'][len(previous):]
 expected=[]
 op=row['operation'];lid=row['lease_id']
 if op=='grant':expected=[('lease.created',lid)]
 elif op=='renew':expected=[('lease.renewed',lid)]
 elif before=='active' and after=='terminal':expected=[('lease.expired',lid)]
 elif before=='active' and after=='needs_attention':expected=[('lease.clock_uncertain',lid)]
 if op=='admit':expected.append(('lease.created',row['next_id']))
 require([(e['type'],e['entity']) for e in delta]==expected,'exact action-specific physical lifetime journal delta required')
 return delta


def project(rows,scenario):
 require(type(rows) is list and len(rows)==len(SCENARIOS[scenario]),'complete actual scenario row count required')
 states=[];previous_events=[];selected='';identity=None
 for index,(row,op) in enumerate(zip(rows,SCENARIOS[scenario])):
  require(type(row) is dict and set(row)=={'schema','scenario','index','operation','input','input_error','clock_error','returned_error','lease_id','next_id','leases','events','checkpoint'},'closed physical trace row required')
  require(type(row['schema']) is int and row['schema']==1 and row['scenario']==scenario and type(row['index']) is int and row['index']==index and row['operation']==op,'exact actual scenario/order required')
  require(type(row['input_error']) is bool and row['clock_error'] in ('','lease_clock_unavailable'),'closed clock failure domain required')
  require(row['returned_error'] in ('','lease_clock_unavailable','lease_inactive','database_error','insufficient_capacity'),'closed API error domain required')
  now,wall,boot=sample(row['input']);cp,_,cb=sample(row['checkpoint'])
  require((now,wall,boot)==INPUTS[scenario][index],'named actual clock inputs changed')
  require(row['input_error']==(scenario=='sticky-read-failure' and index==2),'named native read failure semantics changed')
  require(bool(row['clock_error'])==((scenario=='sticky-read-failure' and index>=2) or (scenario=='restart-regressed' and index==2)),'named sticky/restart failure semantics changed')
  require(type(row['leases']) is list and len(row['leases'])<=2 and type(row['events']) is list and len(row['events'])<=32,'bounded complete SQL roster required')
  require(row['events'][:len(previous_events)]==previous_events,'durable event prefix changed')
  seq=0
  for event in row['events']:
   require(type(event) is dict and set(event)=={'sequence','type','entity'} and type(event['sequence']) is int and event['sequence']>seq and type(event['type']) is str and event['type'].startswith('lease.') and re.fullmatch('[0-9a-f]{32}',event['entity'] or '') is not None,'ordered physical lifetime event required')
   seq=event['sequence']
  leases={};charged=0
  for lease in row['leases']:
   require(type(lease) is dict and set(lease)<=LEASE_FIELDS and {'id','host','owner','request','state','created_at','revision','capabilities'}<=set(lease),'closed physical lease body required')
   lid=lease['id'];require(type(lid) is str and re.fullmatch('[0-9a-f]{32}',lid) is not None and lid not in leases,'distinct exact physical lease ids required')
   request=lease['request'];require(type(request) is dict and set(request)=={'budget','host','reason','resources','ttl_ns','idempotency_key','queue'},'bare controlled request scope required')
   require(type(request['resources']) is dict and set(request['resources'])=={'cpu','memory_bytes','disk_bytes'} and type(request['resources']['cpu']) in (int,float) and request['resources']['cpu']==0 and type(request['resources']['memory_bytes']) is int and request['resources']['memory_bytes']==1 and type(request['resources']['disk_bytes']) is int and request['resources']['disk_bytes']==0 and type(request['ttl_ns']) is int and request['ttl_ns']==2*NS and request['queue'] is False,'exact original memory request required')
   require(lease['state'] in ('active','needs_attention','terminal') and type(lease['revision']) is int and 1<=lease['revision']<=4,'bounded durable lifecycle required')
   require(type(lease['host']) is str and type(lease['owner']) is str,'exact host/owner strings required')
   owner_budget='budget_owner_'+hashlib.sha256(json.dumps([lease['host'],'local_uid',lease['owner']],ensure_ascii=False,separators=(',',':')).encode()).hexdigest()
   binding=lease.get('budget_binding')
   require(request['budget']==owner_budget and type(binding) is dict and set(binding)=={'authority','owner','selected','path','bound_at'} and binding['authority']==lease['host'] and binding['owner']==lease['owner'] and binding['selected']==owner_budget and type(binding['path']) is list and binding['path'] and binding['path'][-1]==owner_budget,'exact backend-resolved owner budget binding required')
   require(not ({'pid','process_identity','artifact','ports','cargo','workload','managed_dir','exit_code'}&set(lease)),'native/provider authority is outside this trace scope')
   leases[lid]=lease
   if lease['state'] in ('active','needs_attention'): charged+=1
  require(charged<=1,'actual capacity-one memory promises exceeded')
  state={'now':now,'wall':wall,'boot':boot,'phase':'absent','anchor':0,'ends':0,'leaseBoot':'none','valid':True,'known':not row['clock_error'],'charged':False,'revision':0,'checkpoint':cp,'checkpointBoot':cb,'nextGranted':False,'action':op}
  if index==0:
   require(row['lease_id']==row['next_id']=='' and leases=={} and row['events']==[] and row['returned_error']==row['clock_error']=='' and not row['input_error'],'exact empty initial SQL required')
  else:
   require(re.fullmatch('[0-9a-f]{32}',row['lease_id'] or '') is not None,'selected exact lease required')
   if not selected: selected=row['lease_id']
   require(selected==row['lease_id'] and selected in leases,'selected lease cannot be crossed/replaced')
   lease=leases[selected]
   request=lease['request']
   require(request['idempotency_key']=='clock-first','selected key changed')
   immutable=(lease['host'],lease['owner'],lease['created_at'],request)
   if identity is None: identity=copy.deepcopy(immutable)
   require(immutable==identity,'selected original lease intent/identity changed')
   require({'elapsed_deadline','started_at','expires_at'}<=set(lease),'persisted deadline required')
   clock=lease['elapsed_deadline']
   require(type(clock) is dict and set(clock)=={'version','boot','anchor_ns','end_ns','wall_expires'} and type(clock['version']) is int and clock['version'] in (1,2) and clock['boot'] in ('a','b'),'closed controlled deadline required')
   anchor,end=unit(clock['anchor_ns']),unit(clock['end_ns'])
   valid=(clock['version']==1 and 0<=clock['anchor_ns']<clock['end_ns'] and clock['end_ns']-clock['anchor_ns']<=7*24*3600*NS and timestamp(clock['wall_expires'])==timestamp(lease['expires_at']))
   phase=lease['state']
   if phase=='terminal':require(lease.get('outcome')=='expired' and 'ended_at' in lease,'only witnessed expiry terminal is in this scope')
   else:require('ended_at' not in lease,'retained obligation cannot have terminal timestamp')
   if phase=='needs_attention':require(lease.get('recovery_reason')=='lease_clock_uncertain','clock uncertainty must remain visible')
   state.update(phase=phase,anchor=anchor,ends=end,leaseBoot=clock['boot'],valid=valid,charged=phase in ('active','needs_attention'),revision=lease['revision'])
   if row['next_id']:
    require(row['next_id'] in leases and row['next_id']!=selected and len(leases)==2,'exact sibling lease required')
    sibling=leases[row['next_id']]
    require(sibling['request']['idempotency_key']=='clock-next' and sibling['state']=='active','original admitted sibling required')
    state['nextGranted']=True
   else:require(len(leases)==1,'unattributed additional lease refused')
   if op in ('grant','poll','renew','corrupt','restart','admit'):require(row['returned_error']=='','successful selected operation required')
   elif op=='renew-refused':require(row['returned_error']=='lease_inactive','late/inactive renewal must refuse')
   elif op in ('clock-refused','sticky-refused'):require(row['returned_error']==row['clock_error']=='lease_clock_unavailable','sticky native-clock refusal required')
   elif op=='journal-refused':require(row['returned_error']=='database_error' and row['clock_error']=='','durable checkpoint refusal must stay distinct from native-clock failure')
   if op=='admit':
    expired=[e['sequence'] for e in row['events'] if e['entity']==selected and e['type']=='lease.expired']
    created=[e['sequence'] for e in row['events'] if e['entity']==row['next_id'] and e['type']=='lease.created']
    require(len(expired)==len(created)==1 and expired[0]<created[0],'overdue original expiry must be durable before sibling admission')
  require(set(state)==FIELDS,'complete model projection required')
  event_delta(previous_events,row,states[-1]['phase'] if states else 'absent',state['phase'])
  previous_events=copy.deepcopy(row['events'])
  states.append(state)
 return states


def read_go(log):
 require(type(log) is str and log.endswith('\n') and len(log.encode())<=16<<20,'bounded complete Go JSON transcript required')
 expected={TEST}|{TEST+'/'+s for s in SCENARIOS}
 ran=[];passed=[];outputs={};packages=0
 for line in log.splitlines():
  e=parse(line)
  require(e.get('Package')==GO_PACKAGE and e.get('Action') not in ('fail','skip','build-fail'),'actual producer failure/foreign package')
  if 'Test' in e:require(e['Test'] in expected,'unexpected named producer')
  if e['Action']=='run':ran.append(e['Test'])
  if e['Action']=='pass':
   if 'Test' in e:passed.append(e['Test'])
   else:packages+=1
  if 'Output' in e and 'Test' in e:outputs[e['Test']]=outputs.get(e['Test'],'')+e['Output']
 require(set(ran)==set(passed)==expected and len(ran)==len(passed)==len(expected) and packages==1,'exact complete actual Go roster required')
 histories={}
 for scenario in SCENARIOS:
  rows=[]
  for line in outputs[TEST+'/'+scenario].splitlines():
   if PREFIX in line:
    match=re.fullmatch(r'\s+lease_clock_trace_dev_test.go:\d+: '+PREFIX+r'(.+)',line)
    require(match is not None,'actual source prefix required')
    rows.append(parse(match[1]))
  project(rows,scenario);histories[scenario]=rows
 return histories


def altered(histories):
 variants={}
 def variant(name,scenario,index,change):
  rows=copy.deepcopy(histories[scenario]);change(rows[index]);require(rows!=histories[scenario],'control must alter actual history');variants[name]=(scenario,rows)
 variant('wall-rebases-native-end','wall-rollback',2,lambda r:r['leases'][0]['elapsed_deadline'].__setitem__('end_ns',r['leases'][0]['elapsed_deadline']['end_ns']+NS))
 variant('late-renewal-revives','wall-forward',4,lambda r:r['leases'][0].update(state='active',outcome=''))
 variant('restart-rebases-other-boot','restart-other-boot',2,lambda r:r['leases'][0]['elapsed_deadline'].__setitem__('boot','b'))
 variant('uncertain-capacity-freed','malformed-deadline',3,lambda r:r['leases'][0].update(state='terminal',outcome='expired',ended_at=r['input']['wall']))
 variant('sticky-error-cleared','sticky-read-failure',3,lambda r:r.update(clock_error='',returned_error='lease_inactive'))
 variant('checkpoint-failure-authorizes-renew','journal-refusal',2,lambda r:r['leases'][0]['elapsed_deadline'].__setitem__('end_ns',r['leases'][0]['elapsed_deadline']['end_ns']+NS))
 variant('extra-unattributed-lease','suspend-admission',2,lambda r:r['leases'].append(copy.deepcopy(r['leases'][0])))
 variant('admission-before-expiry-event','suspend-admission',2,lambda r:r['events'].reverse())
 variant('unknown-record-field','wall-rollback',2,lambda r:r['leases'][0].__setitem__('unreviewed_authority',True))
 for label,scenario,index,kind in (('renew','wall-rollback',3,'lease.renewed'),('expiry','wall-forward',4,'lease.expired')):
  for change in ('delete','rename'):
   rows=copy.deepcopy(histories[scenario]);events=rows[index]['events']
   selected=[e for e in events if e['type']==kind and e['entity']==rows[index]['lease_id']]
   require(len(selected)==1,'exact original journal mutation target required')
   sequence=selected[0]['sequence']
   for row in rows[index:]:
    if change=='delete':row['events']=[e for e in row['events'] if e['sequence']!=sequence]
    else:
     for event in row['events']:
      if event['sequence']==sequence:event['type']='lease.wrong_lifetime_action'
   variants[label+'-journal-'+change]=(scenario,rows)
 variant('clock-error-without-failed-read','sticky-read-failure',2,lambda r:r.__setitem__('input_error',False))
 variant('successful-poll-claims-read-error','wall-forward',2,lambda r:r.__setitem__('input_error',True))
 return variants


TLC_SERIAL_WARNING = ('Warning: Please run the Java VM, which executes TLC with a throughput optimized garbage collector, by passing the "-XX:+UseParallelGC" property.\n'
 '(Use the -nowarning option to disable this warning.)\n')


def validate_tlc_diagnostics(log):
 count=log.count(TLC_SERIAL_WARNING)
 require(count<=1,'duplicate TLC startup warning refuses acceptance')
 if count:
  require(log.startswith('TLC2 Version ') and log.partition('\n')[2].startswith(TLC_SERIAL_WARNING),'known TLC warning must occur immediately after the version banner')
  log=log.replace(TLC_SERIAL_WARNING,'',1)
 require(not re.search(r'(?mi)(warning:|OutOfMemoryError|Exception in thread)',log),'unexpected TLC warning/exception refuses acceptance')


def tlc_diagnostic_controls():
 banner='TLC2 Version 2026.10.20.183001 (rev: d2e1472)\n'
 validate_tlc_diagnostics(banner+TLC_SERIAL_WARNING+'Running breadth-first search\n')
 validate_tlc_diagnostics(banner+'Running breadth-first search\n')
 rejected=[]
 for name,log in (
  ('unknown-warning',banner+TLC_SERIAL_WARNING+'Warning: unrecognized diagnostic\n'),
  ('duplicate-startup-warning',banner+TLC_SERIAL_WARNING*2),
  ('partial-startup-warning',banner+TLC_SERIAL_WARNING.splitlines()[0]+'\n'),
  ('late-startup-warning',banner+'Running breadth-first search\n'+TLC_SERIAL_WARNING),
  ('out-of-memory',banner+TLC_SERIAL_WARNING+'OutOfMemoryError\n'),
  ('thread-exception',banner+TLC_SERIAL_WARNING+'Exception in thread main\n')):
  try:validate_tlc_diagnostics(log)
  except ValueError:rejected.append(name)
  else:raise ValueError('TLC diagnostic control accepted: '+name)
 return {'known_serial_gc_startup':'passed','warning_free':'passed','rejected':rejected}


def terminal_count(log,witness=False,expected=None):
 errors=re.findall(r'^Error: (.*)$',log,re.M)
 permitted=['Invariant NotFinal is violated.','The behavior up to this point is:'] if witness else []
 require(errors==permitted,'unexpected TLC diagnostic')
 validate_tlc_diagnostics(log)
 if not witness:require('Model checking completed. No error has been found.' in log,'complete TLC success required')
 counts=re.findall(r'(\d+) distinct states found, (\d+) states left on queue',log)
 require(len(counts)==1 and int(counts[0][0])>0,'one positive terminal TLC statistic required')
 count,queued=map(int,counts[0])
 if not witness:require(queued==0,'TLC queue must be empty')
 if expected is not None:require(count==expected,'complete exact observed trace required')
 return count


class ToolClosure:
 def __init__(self):self.receipts={};self.optional_receipts={}
 @staticmethod
 def resolve(requested):
  value=str(requested)
  require(Path(value).is_absolute() or '/' not in value,'relative directory tool selectors are refused before dispatch')
  return Path(shutil.which(value) or value).resolve(strict=True)
 @staticmethod
 def identity(path):
  info=path.stat()
  require(stat.S_ISREG(info.st_mode),'regular tool file required')
  return {'device':info.st_dev,'inode':info.st_ino,'uid':info.st_uid,'mode':stat.S_IMODE(info.st_mode),'bytes':info.st_size,'links':info.st_nlink,'mtime_ns':info.st_mtime_ns,'sha256':hashlib.sha256(path.read_bytes()).hexdigest()}
 def capture(self,name,requested):
  path=self.resolve(requested)
  receipt={'requested':str(requested),'path':str(path),**self.identity(path)}
  require(name not in self.receipts,'tool name cannot be rebound')
  self.receipts[name]=receipt
  return dict(receipt)
 def capture_optional(self,name,requested):
  requested=Path(requested)
  require(requested.is_absolute(),'optional tool/config source requires absolute identity')
  path=requested.resolve(strict=False)
  receipt={'requested':str(requested),'path':str(path),'present':path.exists(),'identity':self.identity(path) if path.exists() else None}
  require(name not in self.receipts and name not in self.optional_receipts,'tool/config name cannot be rebound')
  self.optional_receipts[name]=receipt
  return copy.deepcopy(receipt)
 def canonical_argv(self,argv):
  result=[str(a) for a in argv]
  candidates=[r for r in self.receipts.values() if result[0] in (r['requested'],r['path'])]
  require(candidates and len({r['path'] for r in candidates})==1,'native executable must bind one captured absolute tool')
  result[0]=candidates[0]['path']
  for index,value in enumerate(result[:-1]):
   if value in ('-cp','-classpath'):
    jars=[r for r in self.receipts.values() if result[index+1] in (r['requested'],r['path'])]
    require(jars and len({r['path'] for r in jars})==1,'Java classpath must bind the captured absolute TLC JAR')
    result[index+1]=jars[0]['path']
  return result
 def verify(self):
  for name,receipt in self.receipts.items():
   path=self.resolve(receipt['requested'])
   require(str(path)==receipt['path'],'tool requested path retargeted: '+name)
   require(self.identity(path)=={k:v for k,v in receipt.items() if k not in ('requested','path')},'tool bytes/identity changed: '+name)
  for name,receipt in self.optional_receipts.items():
   path=Path(receipt['requested']).resolve(strict=False)
   require(str(path)==receipt['path'] and path.exists()==receipt['present'],'tool/config source presence or target changed: '+name)
   require((self.identity(path) if path.exists() else None)==receipt['identity'],'tool/config source bytes/identity changed: '+name)


def sanitized_go_env():
 # Compile selectors are a closed environment, not caller-controlled flags.
 env=dict(os.environ)
 saved_cache=env.get('GOCACHE',str(ROOT/'.cache/go-build'))
 saved_gopath=env.get('GOPATH')
 for key in tuple(env):
  if key.startswith('GO') or key.startswith('CGO_') or key in ('CC','CXX','GCCGO','PKG_CONFIG'):env.pop(key)
 native_os={'darwin':'darwin','linux':'linux'}.get(sys.platform)
 native_arch={'arm64':'arm64','aarch64':'arm64','x86_64':'amd64'}.get(platform.machine())
 require(native_os is not None and native_arch is not None,'supported native Go host required')
 env.update(GOMAXPROCS='1',GOPROXY='off',GOSUMDB='off',GOTOOLCHAIN='local',GOENV='off',GOWORK='off',GOFLAGS='',GOOS=native_os,GOARCH=native_arch,CGO_ENABLED='1',GOCACHE=saved_cache)
 if saved_gopath:env['GOPATH']=saved_gopath
 for name in ('CC','CXX'):
  command='cc' if name=='CC' else 'c++'
  path=shutil.which(command)
  require(path is not None,'native C toolchain required for race tests')
  env[name]=str(Path(path).resolve(strict=True))
 validate_go_env(env)
 return env


def validate_go_env(env):
 require(env.get('GOFLAGS')=='' and env.get('GOWORK')=='off' and env.get('GOENV')=='off','Go overlay/modfile/toolexec/workspace/environment substitution refused')
 require(env.get('GOTOOLCHAIN')=='local' and env.get('GOPROXY')=='off' and env.get('GOSUMDB')=='off' and env.get('GOMAXPROCS')=='1','pinned offline bounded Go environment required')
 require(not any(k in env for k in ('GOEXPERIMENT','GCCGO','PKG_CONFIG','CGO_CFLAGS','CGO_CPPFLAGS','CGO_CXXFLAGS','CGO_LDFLAGS')),'inherited Go/CGo compile selectors refused')


def validate_goenv_report(env,reported):
 # Pinned Go1.27.3 cfg.EnvFile returns an empty name for GOENV=off;
 # envcmd reports that empty name while the process selector remains off.
 require(env.get('GOENV')=='off' and reported.get('GOENV')=='','resolved persisted Go environment must be disabled')
 require(reported.get('GOEXPERIMENT','')=='','unreviewed Go root experiment selector refused')


FORMAL_SELECTORS = ('JAVA_TOOL_OPTIONS','_JAVA_OPTIONS','JDK_JAVA_OPTIONS','CLASSPATH','TLA_LIBRARY','TLA_LIBRARY_PATH','TLAPM_LIBRARY_PATH','TLA_PATH','TLC_CLASSPATH')

def formal_env(env):
 result=dict(os.environ if env is None else env)
 for key in FORMAL_SELECTORS:result.pop(key,None)
 validate_formal_env(result)
 return result


def validate_formal_env(env):
 require(not any(key in env for key in FORMAL_SELECTORS),'Java/TLA launcher or library substitution selector refused')


def closure_controls(directory):
 directory.mkdir(mode=0o700)
 a=directory/'tool-a';b=directory/'tool-b';link=directory/'requested'
 a.write_bytes(b'original-a');b.write_bytes(b'original-b');link.symlink_to(a)
 closure=ToolClosure();closure.capture('fixture',link);closure.verify()
 rejected=[]
 link.unlink();link.symlink_to(b)
 try:closure.verify()
 except ValueError:rejected.append('requested-symlink-retarget')
 else:raise ValueError('tool retarget control accepted')
 link.unlink();link.symlink_to(a);closure.verify()
 original=a.read_bytes();a.write_bytes(b'modified-a')
 try:closure.verify()
 except ValueError:rejected.append('resolved-tool-byte-replacement')
 else:raise ValueError('tool byte control accepted')
 # Re-capture a fresh approved fixture to test final checks after an apparent
 # successful command; original receipts never regain authority by recapture.
 fresh=ToolClosure();fresh.capture('fixture',b);fresh.verify();b.write_bytes(b'changed-after-last-command')
 try:fresh.verify()
 except ValueError:rejected.append('final-tool-closure-change')
 else:raise ValueError('final tool closure control accepted')
 caller=directory/'caller';command=directory/'command'
 for target,data in ((caller,b'caller-image'),(command,b'command-image')):
  (target/'bin').mkdir(parents=True);(target/'bin/tool').write_bytes(data)
 previous_cwd=Path.cwd()
 try:
  os.chdir(caller)
  relative=ToolClosure()
  try:relative.capture('relative','bin/tool')
  except ValueError:rejected.append('differing-cwd-relative-tool-selector')
  else:raise ValueError('relative directory selector accepted across command cwd')
  absolute=ToolClosure();receipt=absolute.capture('absolute',command/'bin/tool')
  require(absolute.canonical_argv([command/'bin/tool','--version'])[0]==receipt['path'],'absolute execution/hash binding changed')
 finally:os.chdir(previous_cwd)
 config=directory/'go.env';config.write_text('GOFLAGS=\n')
 root_config=ToolClosure();root_config.capture_optional('go-root-environment',config);root_config.verify();config.write_text('GOFLAGS=-overlay=/tmp/foreign.json\n')
 try:root_config.verify()
 except ValueError:rejected.append('late-goroot-go-env-mutation')
 else:raise ValueError('late Go root environment mutation accepted')
 absent=directory/'absent-go.env';absence=ToolClosure();absence.capture_optional('go-root-environment',absent);absence.verify();absent.write_text('GOFLAGS=-modfile=/tmp/foreign.mod\n')
 try:absence.verify()
 except ValueError:rejected.append('late-goroot-go-env-appearance')
 else:raise ValueError('unverified Go root environment appearance accepted')
 env={'GOFLAGS':'','GOWORK':'off','GOENV':'off','GOTOOLCHAIN':'local','GOPROXY':'off','GOSUMDB':'off','GOMAXPROCS':'1'}
 validate_go_env(env)
 validate_goenv_report(env,{'GOENV':''})
 for bad in ('off','/tmp/foreign.env'):
  try:validate_goenv_report(env,{'GOENV':bad})
  except ValueError:rejected.append('resolved-goenv-'+('off-label' if bad=='off' else 'file')+'-substitution')
  else:raise ValueError('resolved GOENV report control accepted')
 try:validate_goenv_report(env,{'GOENV':'','GOEXPERIMENT':'unreviewed'})
 except ValueError:rejected.append('resolved-go-root-experiment-substitution')
 else:raise ValueError('unreviewed Go experiment accepted')
 clean_formal=formal_env({})
 for key in FORMAL_SELECTORS:
  changed=dict(clean_formal);changed[key]='unreviewed-selector'
  try:validate_formal_env(changed)
  except ValueError:rejected.append('formal-'+key+'-substitution')
  else:raise ValueError('formal source substitution control accepted: '+key)
 for name,key,value in (('overlay','GOFLAGS','-overlay=/tmp/foreign.json'),('modfile','GOFLAGS','-modfile=/tmp/foreign.mod'),('toolexec','GOFLAGS','-toolexec=/tmp/wrapper'),('workspace','GOWORK','/tmp/foreign.work'),('goenv','GOENV','/tmp/foreign.env')):
  changed=dict(env);changed[key]=value
  try:validate_go_env(changed)
  except ValueError:rejected.append('go-'+name+'-substitution')
  else:raise ValueError('Go source substitution control accepted: '+name)
 return rejected


class ClockGate(base.Gate):
 def __init__(self,args):
  require({p.name for p in MODEL.iterdir() if p.suffix in ('.tla','.cfg')}==MODEL_FILES,'exact model/proof source roster required')
  for module in ('LeaseClock.tla','LeaseClockProof.tla'):
   roster=base.validate_module_source(MODEL/module,THEOREMS if module=='LeaseClockProof.tla' else ())
   require(tuple(roster)==(THEOREMS if module=='LeaseClockProof.tla' else ()), 'complete exact model/proof theorem roster required')
  self.project_sources=inventory()
  self.tool_closure=ToolClosure()
  self.generated_closure=ToolClosure()
  self.java_home=None
  super().__init__(args)
  self.runner_sources[str(Path(__file__).resolve())]=hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
  self.summary['runner_sources']=dict(self.runner_sources)
  self.summary.update(project_sources=self.project_sources,scope='one bare-memory lease API-boundary elapsed entitlement; controlled inputs; not native clock/SQL atomicity/process/provider/full workspace verification')
 def tool(self,name,path,version_args=None):
  receipt=self.tool_closure.capture(name,path)
  self.summary['tools'][name]=receipt
  if version_args is not None:receipt['version']=self.command([path,*version_args],name+'-version',timeout=30).strip()
  return receipt
 def command(self,argv,name,expected=0,env=None,timeout=180):
  self.tool_closure.verify()
  self.verify_sources()
  actual_env=formal_env(env)
  if self.java_home is not None:actual_env['JAVA_HOME']=self.java_home
  validate_formal_env(actual_env)
  failure=None;result=None
  actual_argv=self.tool_closure.canonical_argv(argv)
  self.summary.setdefault('command_argv_bindings',[]).append({'command':name,'requested':[str(a) for a in argv],'actual':actual_argv})
  try:result=super().command(actual_argv,name,expected=expected,env=actual_env,timeout=timeout)
  except BaseException as error:failure=error
  try:self.tool_closure.verify();self.verify_sources()
  except BaseException as closure:
   self.summary.setdefault('command_closure_failures',[]).append({'command':name,'error':str(closure)})
   if failure is None:failure=closure
  if failure is not None:raise failure.with_traceback(failure.__traceback__)
  return result
 def tools(self):
  super().tools()
  if self.args.mode in ('check','traces','all'):
   settings=self.command([self.args.java,'-XshowSettings:properties','-version'],'java-selected-runtime',timeout=30)
   homes=re.findall(r'^\s*java.home = (.+)$',settings,re.M)
   require(len(homes)==1,'exact selected Java runtime home required')
   self.java_home=str(Path(homes[0]).resolve(strict=True))
   self.summary['tools']['java']['selected_home']=self.java_home
   self.tool('java-runtime',Path(self.java_home)/'bin/java')
   library=Path(self.java_home)/'lib/server'/('libjvm.dylib' if sys.platform=='darwin' else 'libjvm.so')
   self.tool('java-vm',library)
  if self.args.mode in ('prove','all'):
   self.tool('tlapm-TLAPS-source',Path(self.summary['tools']['tlapm']['path']).parent.parent/'lib/tlapm/stdlib/TLAPS.tla')
  self.summary['gates'].append({'strict_closure_controls':closure_controls(self.run/'closure-controls')})
  self.summary['gates'].append({'tlc_diagnostic_controls':tlc_diagnostic_controls()})
 def capture_generated(self,name,path):
  require(path.parent==self.run and not path.is_symlink() and path.stat().st_nlink==1,'owned regular generated source/artifact required')
  receipt=self.generated_closure.capture(name,path)
  self.summary.setdefault('generated_inputs',{})[name]=receipt
 def verify_sources(self):
  require(inventory()==self.project_sources,'exact project input source changed')
  self.check_captured_sources()
  self.generated_closure.verify()
 def tlc_run(self,module,config,name,expected=0,extra=()):
  return self.command([self.args.java,'-Djava.io.tmpdir='+str(self.java_tmp),'-XX:+UseSerialGC','-Xmx256m','-cp',self.args.tlc_jar,'tlc2.TLC','-workers','1','-fp','0','-metadir',self.run/(name+'-states'),'-noGenerateSpecTE','-config',config,*extra,self.run/module],name,expected=expected,timeout=120)
 def check(self):
  self.verify_sources()
  self.view_controls()
  log=self.tlc_run('LeaseClock.tla',self.run/'LeaseClock.cfg','lease-clock-safety')
  self.summary['gates'].append({'finite_safety':'passed','distinct_states':terminal_count(log),'view':'only wall/action; raw invariant and action edges checked'})
 def view_controls(self):
  source=(self.run/'LeaseClock.tla').read_text();expected='StateView == [state EXCEPT !.wall = 0, !.action = "initial"]'
  require(expected in source.splitlines() and re.findall(r'^StateView ==[^\n]*$',base.module_code(source),re.M)==[base.module_code(expected)],'exact decorative-field VIEW required')
  require((self.run/'LeaseClock.cfg').read_text().splitlines()==['SPECIFICATION Spec','INVARIANT Invariant','PROPERTY Protection','PROPERTY RawInvariant','VIEW StateView','CHECK_DEADLOCK FALSE'],'exact full safety configuration required')
  results=[]
  # All targets share the initial state's VIEW. The two negative controls must
  # fail on the raw edge even though TLC has already seen that fingerprint.
  for name,wall,action,property_name in (('decorative-edge',2,'poll',None),('action-edge',2,'renew','Protection'),('raw-shape-edge',3,'poll','RawInvariant')):
   module='ViewControl'+re.sub('[^A-Za-z0-9]','',name)
   source=self.run/(module+'.tla');cfg=self.run/(module+'.cfg')
   source.write_text('---- MODULE '+module+' ----\nEXTENDS LeaseClock\nControlNext == state\'= [state EXCEPT !.wall = '+str(wall)+', !.action = "'+action+'"]\nControlSpec == Init /\\ [][ControlNext]_vars\n====\n')
   cfg.write_text('SPECIFICATION ControlSpec\nINVARIANT Invariant\nPROPERTY Protection\nPROPERTY RawInvariant\nVIEW StateView\nCHECK_DEADLOCK FALSE\n')
   self.capture_generated(name+'-view-module',source);self.capture_generated(name+'-view-config',cfg)
   log=self.tlc_run(module+'.tla',cfg,'view-control-'+name,13 if property_name else 0)
   if property_name:
    require(re.findall(r'^Error: (.*)$',log,re.M)==['Action property '+property_name+' is violated.','The behavior up to this point is:'],'exact raw-edge property violation required')
    validate_tlc_diagnostics(log)
    require(re.findall(r'(\d+) distinct states found, (\d+) states left on queue',log)==[('1','0')],'negative control must hit an already visited VIEW fingerprint')
   else:require(terminal_count(log)==1,'decorative changes must collapse to one exact VIEW state')
   results.append({'control':name,'status':'rejected' if property_name else 'passed','distinct_states':1,'property':property_name})
  self.summary['gates'].append({'view_controls':results})
 def replay(self,states,name,positive):
  module='Replay'+re.sub('[^A-Za-z0-9]','',name)
  literals=',\n'.join(base.tla_value(s) for s in states)
  (self.run/(module+'.tla')).write_text('---- MODULE '+module+' ----\nEXTENDS LeaseClock, Sequences\nVARIABLE cursor\nObserved == <<'+literals+'>>\nTraceInit == cursor=1 /\\ Init /\\ state=Observed[1]\nTraceNext == cursor<Len(Observed) /\\ cursor\'=cursor+1 /\\ Next /\\ state\'=Observed[cursor+1]\nTraceSpec == TraceInit /\\ [][TraceNext]_<<state,cursor>>\nNotFinal == cursor#Len(Observed)\n====\n')
  cfg=self.run/(module+'.cfg');cfg.write_text('SPECIFICATION TraceSpec\nINVARIANT Invariant\nPROPERTY Protection\nCHECK_DEADLOCK FALSE\n'+('INVARIANT NotFinal\n' if positive else ''))
  self.capture_generated(name+'-module',self.run/(module+'.tla'))
  self.capture_generated(name+'-config',cfg)
  witness=self.run/(module+'-witness.json')
  log=self.tlc_run(module+'.tla',cfg,name,12 if positive else 0,('-dumpTrace','json',witness) if positive else ())
  count=terminal_count(log,positive,len(states) if positive else None)
  if positive:
   self.capture_generated(name+'-witness',witness)
   actual=base.tlc_states(witness,variables=('state','cursor'),validator=lambda s:s)
   require(len(actual)==len(states) and all(type(row['cursor']) is int and row['cursor']==n and json.dumps(row['state'],sort_keys=True,allow_nan=False)==json.dumps(state,sort_keys=True,allow_nan=False) for n,(row,state) in enumerate(zip(actual,states),1)),'every complete actual state/cursor must match TLC witness')
  else:require(count<len(states),'altered actual history reached its full terminal observation')
  return count
 def traces(self):
  self.verify_sources()
  env=sanitized_go_env()
  for name in ('CC','CXX'):self.tool('go-'+name.lower(),env[name])
  inferred_goroot=Path(self.summary['tools']['go']['path']).parent.parent
  self.summary['tools']['go-root-environment']=self.tool_closure.capture_optional('go-root-environment',inferred_goroot/'go.env')
  names=('GOENV','GOFLAGS','GOWORK','GOMOD','GOROOT','GOPATH','GOCACHE','GOMODCACHE','GOPROXY','GOSUMDB','GOTOOLCHAIN','GOOS','GOARCH','CGO_ENABLED','GOTOOLDIR','GOVERSION','CC','CXX','GOEXPERIMENT')
  resolved=parse(self.command([self.args.go,'env','-json',*names],'actual-go-resolved-environment',env=env,timeout=30))
  require(type(resolved) is dict and set(resolved)==set(names),'exact resolved Go environment roster required')
  for key in ('GOFLAGS','GOWORK','GOPROXY','GOSUMDB','GOTOOLCHAIN','GOOS','GOARCH','CGO_ENABLED','CC','CXX'):require(resolved[key]==env[key],'resolved Go environment differs: '+key)
  validate_goenv_report(env,resolved)
  require(resolved['GOVERSION']=='go1.27.3','pinned Go1.27.3 environment-report semantics required')
  require(Path(resolved['GOMOD']).resolve(strict=True)==ROOT/'go.mod','resolved Go module source differs')
  goroot=Path(resolved['GOROOT']).resolve(strict=True);toolroot=Path(resolved['GOTOOLDIR']).resolve(strict=True)
  require(goroot==inferred_goroot,'resolved Go root differs from pinned captured tool distribution')
  require(toolroot.is_relative_to(goroot/'pkg/tool'),'resolved native Go compiler tools escaped captured GOROOT')
  for name in ('compile','link','asm','cgo','vet'):self.tool('go-'+name,toolroot/name)
  self.summary['go_resolved_environment']=resolved
  self.summary['go_compile_environment']={k:env[k] for k in sorted(env) if k.startswith('GO') or k.startswith('CGO_') or k in ('CC','CXX')}
  validate_go_env(env)
  log=self.command([self.args.go,'test','-p=1','-buildvcs=false','-race','-json','-tags','arm_devtrace','-run','^'+TEST+'$','-count=1','-timeout=90s','./internal/manager'],'actual-lease-clock-go',env=env,timeout=120)
  histories=read_go(log);accepted=[];rejected=[]
  for scenario,rows in histories.items():
   states=project(rows,scenario)
   (self.run/(scenario+'-raw.json')).write_text(json.dumps(rows,indent=2)+'\n')
   (self.run/(scenario+'-projection.json')).write_text(json.dumps(states,indent=2)+'\n')
   self.capture_generated(scenario+'-raw',self.run/(scenario+'-raw.json'))
   self.capture_generated(scenario+'-projection',self.run/(scenario+'-projection.json'))
   accepted.append({'scenario':scenario,'rows':len(rows),'witness_states':self.replay(states,'actual-'+scenario,True)})
  for name,(scenario,rows) in altered(histories).items():
   (self.run/(name+'-altered.json')).write_text(json.dumps(rows,indent=2)+'\n')
   self.capture_generated(name+'-altered',self.run/(name+'-altered.json'))
   try:states=project(rows,scenario)
   except ValueError as error:rejected.append({'name':name,'rejected_by':'closed reader','error':str(error)});continue
   rejected.append({'name':name,'rejected_by':'model relation','reachable_prefix':self.replay(states,'negative-'+name,False)})
  self.summary['gates'].append({'actual_go_traces':accepted,'altered_histories_rejected':rejected})
 def prove(self):
  self.verify_sources()
  path=self.run/'LeaseClockProof.tla'
  roster=base.validate_module_source(path,THEOREMS);require(tuple(roster)==THEOREMS,'exact complete theorem roster required')
  code=base.module_code(path.read_text())
  require(not re.search(r'\b(?:OMITTED|OBVIOUS|AXIOM|ASSUME)\b',code),'unproved/admitted proof source refused')
  scope=self.command([self.args.tlapm,'--summary','--cache-dir',self.run/'parse','-I',self.run,path],'lease-clock-proof-scope',timeout=60)
  modules=re.findall(r'^---- summary of module "([^"]+)" ----$',scope,re.M)
  counts=re.findall(r'^\s*obligations_count\s*=\s*(\d+)\s*$',scope,re.M)
  require(modules==['LeaseClockProof'] and len(counts)==1 and int(counts[0])>0,'one exact complete parsed proof scope required')
  log=self.command([self.args.tlapm,'--strict','--nofp','--threads','1','--stretch','10','--cache-dir',self.run/'proofs','-I',self.run,path],'lease-clock-proof',timeout=120)
  proved=re.findall(r'All (\d+) obligations proved\.',log)
  require(proved==counts and '[ERROR]' not in log and 'unproved' not in log.lower(),'all original parsed obligations must be strictly proved')
  self.summary['gates'].append({'strict_proofs':'passed','theorems':roster,'obligations':int(counts[0])})


def main():
 p=argparse.ArgumentParser(description=__doc__)
 p.add_argument('--mode',choices=('check','traces','prove','all'),default='all')
 p.add_argument('--go',default='go')
 p.add_argument('--java',default='java')
 p.add_argument('--tlc-jar',default=str(ROOT/'.tla/tools/tlc/tla2tools.jar'))
 p.add_argument('--tlapm',default=str(ROOT/'.tla/tools/tlapm/tlapm/bin/tlapm'))
 p.add_argument('--work-dir',default=str(ROOT/'.tla/lease-clock'))
 args=p.parse_args();args.targets=[];os.umask(0o077)
 gate=ClockGate(args);failure=None
 try:
  gate.tools()
  if args.mode in ('check','all'):gate.check()
  if args.mode in ('traces','all'):gate.traces()
  if args.mode in ('prove','all'):gate.prove()
 except BaseException as error:failure=error
 finally:
  for label,verify in (('source_closure',gate.verify_sources),('tool_closure',gate.tool_closure.verify)):
   try:
    verify();gate.summary[label]={'status':'passed'}
    if label=='tool_closure':gate.summary[label]['tools']=list(gate.tool_closure.receipts)+list(gate.tool_closure.optional_receipts)
   except BaseException as closure:
    gate.summary[label]={'status':'failed','error':str(closure)}
    if failure is None:failure=closure
  gate.summary['status']='failed' if failure else 'passed';gate.finish()
 if failure is not None:raise failure.with_traceback(failure.__traceback__)


if __name__=='__main__':main()
