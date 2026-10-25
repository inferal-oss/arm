#!/usr/bin/env python3
"""PROPOSED harmless actual-PTY controls, not rendered Terminal acceptance.

Requires reviewed --native-pty and a NEW private --output-dir. No ARM/backend,
network, Terminal app/window, user settings, arbitrary command or provider work.
The selected 'arm' image is visibly a fixed self-alarming Python test child.
All directories/evidence retained; failfast prevents another case after failure.
"""
from pathlib import Path
import argparse,errno,fcntl,hashlib,importlib.util,json,os,pty,selectors,signal,stat,struct,subprocess,sys,tempfile,termios,time,unittest
SCRIPTS=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('pty_frozen_owner',SCRIPTS/'mesh_reconnect_terminal.py');terminal=importlib.util.module_from_spec(spec);spec.loader.exec_module(terminal);binding=terminal.binding;state=terminal.state
ACTIVE=4;PARENT_ACTIVE=14;CHILD_ALARM=6;OWNER_ALARM=18;CAP=5<<20
CHILD='''import os,signal,sys,tty,fcntl,termios,struct
from pathlib import Path
root=Path(__file__).resolve().parent
if sys.argv!=[str(root/'arm'),'--home',str(root/'source'),'dashboard','terminal']:raise SystemExit(91)
signal.alarm(6)
tty.setraw(0)
def resize(*_):
 rows,cols,_,_=struct.unpack('HHHH',fcntl.ioctl(0,termios.TIOCGWINSZ,b'\\0'*8));os.write(1,('SIZE=%dx%d|'%(rows,cols)).encode())
def cancel(*_):
 os.write(1,b'CANCEL|');raise SystemExit(0)
signal.signal(signal.SIGWINCH,resize);signal.signal(signal.SIGTERM,cancel)
payload=b'BEGIN|'+b'x'*131072+b'TAIL|READY|'
while payload:
 count=os.write(1,payload);payload=payload[count:]
if os.read(0,1)!=b'q':raise SystemExit(92)
os.write(1,b'NORMAL|')
'''

def put(path,value):
 raw=(json.dumps(value,sort_keys=True,separators=(',',':'))+'\n').encode();fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'wb') as stream:stream.write(raw);stream.flush();os.fsync(stream.fileno())
 return hashlib.sha256(raw).hexdigest()

def fixture(root,rendered):
 root.mkdir(mode=0o700);rendered.mkdir(mode=0o700)
 for name in ('source','source/mesh','source/mesh-lifecycle','destination','destination/mesh'):(root/name).mkdir(mode=0o700)
 # Fixed pinned interpreter shebang, not a shell or caller-selected program.
 image=('#!'+sys.executable+'\n'+CHILD).encode();(root/'arm').write_bytes(image);(root/'arm').chmod(0o700);digest=hashlib.sha256(image).hexdigest();target='b'*32
 def principal(host):return {'host':host,'owner':str(os.getuid()),'owner_username':'synthetic-pty-files','name':'fixture','address':'127.0.0.1:23456','certificate':'synthetic file-only certificate','spki_sha256':'e'*64,'fingerprint':'f'*64,'expires_at':'2027-01-01T00:00:00Z'}
 left=principal('a'*32);right=principal(target)
 put(root/'ui-ready.json',{'mode':'terminal','dashboard_url':'http://127.0.0.1:12345/#connect='+'c'*64,'source_home':str(root/'source'),'arm':str(root/'arm'),'host':target,'pending_key':'lost-disconnect','outcome_file':str(root/'ui-outcome.json'),'required':binding.REQUIRED_TEXT})
 endpoint={'url':'ws://127.0.0.1:12345/v1/ws','pid':123,'token':'d'*64,'backend_generation':'e'*32};put(root/'source/endpoint.json',endpoint)
 put(root/'source/mesh/config.json',{'identity':left,'peers':[{'identity':right,'state':'revoked'}]});put(root/'destination/mesh/config.json',{'identity':right})
 op={'schema':1,'key':'lost-disconnect','kind':'disconnect','revision':1,'phase':'incomplete','approved':True,'targets':['host:'+target],'request':{'key':'lost-disconnect','kind':'disconnect','disconnect':{'host':target}},'review':{'exact':'synthetic file-only review'},'review_sha256':'a'*64}
 put(root/'source/mesh-lifecycle'/('coordinator-operation-'+hashlib.sha256(op['key'].encode()).hexdigest()+'.json'),op)
 summary={'key':op['key'],'revision':1,'recovery_sha256':'c'*64,'phase':'incomplete','kind':'disconnect'};put(root/'terminal-reviewed-operation.json',summary)
 chosen=state.reserve(root,hashlib.sha256((root/'source/endpoint.json').read_bytes()).hexdigest(),digest,summary);put(root/'terminal-reservation.json',chosen)
 saved=binding.begin(root,digest,rendered);path=root/'terminal-binding.json';want=binding.exclusive(path,saved);return chosen,path,want

def owner_child(path,want,fault):
 # A separate fixed harness process supplies a genuine outer tty. It executes
 # unchanged owner(), including actual binding validation; no API monkeypatch.
 signal.alarm(OWNER_ALARM);before=termios.tcgetattr(0);flags={fd:fcntl.fcntl(fd,fcntl.F_GETFL) for fd in (0,1)};observed={'fault':fault,'entered':False,'real_selector_closed':False};root=path.parent
 original_publish=terminal.publish_receipt
 def capture_receipt(chosen,cap,receipt):
  observed['attempted_receipt']=receipt
  return original_publish(chosen,cap,receipt)
 terminal.publish_receipt=capture_receipt
 if fault=='fsync':
  original=os.fsync
  def fsync(fd):
   selected=root/'terminal-output.ansi'
   if selected.exists() and (os.fstat(fd).st_dev,os.fstat(fd).st_ino)==(selected.stat().st_dev,selected.stat().st_ino):observed['entered']=True;raise OSError(errno.EIO,'fixed test transcript sync fault')
   return original(fd)
  os.fsync=fsync
 elif fault=='close':
  original=terminal.selectors.DefaultSelector
  class ClosedThenError:
   def __init__(self):self.inner=original()
   def __getattr__(self,name):return getattr(self.inner,name)
   def close(self):
    self.inner.close();observed['real_selector_closed']=True;observed['entered']=True;raise OSError(errno.EIO,'fixed test selector close fault')
  terminal.selectors.DefaultSelector=ClosedThenError
 elif fault!='none':raise ValueError('unknown fixed fault selection')
 code=terminal.owner(path,want,ACTIVE)
 raw=terminal.restoration_evidence(before,termios.tcgetattr(0),flags,{fd:fcntl.fcntl(fd,fcntl.F_GETFL) for fd in flags},sys.platform)
 observed.update(exit=code,termios_restored=raw['termios_configuration_equal'],fd_flags_restored=all(item['configuration_equal'] for item in raw['fd_status'].values()),raw_restoration=raw)
 put(root/'owner-observation.json',observed);return code

class EndpointBinding(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory(prefix='arm-terminal-binding-schema-');self.addCleanup(self.tmp.cleanup)
  parent=Path(self.tmp.name);self.root=parent/'fixture';self.rendered=parent/'rendered'
  self.chosen,self.path,self.want=fixture(self.root,self.rendered)
  self.endpoint=json.loads((self.root/'source/endpoint.json').read_text());self.arm_sha=hashlib.sha256((self.root/'arm').read_bytes()).hexdigest()
 def replace_endpoint(self,value):
  path=self.root/'source/endpoint.json';path.write_text(json.dumps(value));path.chmod(0o600)
 def test_current_endpoint_generation_bound_before_child(self):
  saved,_=binding.selected_binding(self.path,self.want)
  self.assertEqual(saved['endpoint_generation'],'e'*32)
  self.assertEqual(state.observe(self.chosen)['state'],'open')
 def test_missing_malformed_or_extra_generation_refused(self):
  variants=[{k:v for k,v in self.endpoint.items() if k!='backend_generation'},dict(self.endpoint,extra=True)]
  variants += [dict(self.endpoint,backend_generation=value) for value in ('', 'e'*31, 'E'*32, None, True, [])]
  for value in variants:
   self.replace_endpoint(value)
   with self.subTest(value=value),self.assertRaises(ValueError):binding.context(self.root,self.arm_sha)
  self.assertEqual(state.observe(self.chosen)['state'],'open')
 def test_generation_change_refuses_original_selected_binding(self):
  self.replace_endpoint(dict(self.endpoint,backend_generation='f'*32))
  with self.assertRaises(ValueError):binding.selected_binding(self.path,self.want)
  self.assertEqual(state.observe(self.chosen)['state'],'open')

class PTY(unittest.TestCase):
 output=None
 def run_case(self,name,action,fault='none'):
  root=self.output/name;chosen,path,want=fixture(root,self.output/(name+'-rendered'));master=slave=None;process=None;retained=None;closed=False;raw=bytearray();native_errors=[];released=False;resized=False;cancelled=False;case_passed=False
  try:
   master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',36,120,0,0));before=termios.tcgetattr(slave);flags=fcntl.fcntl(slave,fcntl.F_GETFL);os.set_blocking(master,False)
   process=subprocess.Popen([sys.executable,'-I','-B',str(Path(__file__).resolve()),'--owner-child',str(path),'--binding-sha256',want,'--fault',fault],stdin=slave,stdout=slave,stderr=slave,close_fds=True,start_new_session=True,env={'PATH':'/usr/bin:/bin','LANG':'C','LC_ALL':'C'})
   retained=terminal.RetainedChild(process);deadline=time.monotonic()+PARENT_ACTIVE
   def read_once(seconds):
    with selectors.DefaultSelector() as select:
     select.register(master,selectors.EVENT_READ)
     if not select.select(seconds):return False
     try:data=os.read(master,16384)
     except BlockingIOError:return False
     except OSError as error:
      if error.errno!=errno.EIO:raise
      data=b''
     if len(raw)+len(data)>CAP:raise ValueError('outer PTY artifact cap exceeded')
     raw.extend(data);return not data
   while time.monotonic()<deadline:
    read_once(.01)
    if b'READY|' in raw:
     if action=='resize' and not resized:
      fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',40,140,0,0))
      if not retained.authority or process.returncode is not None:raise RuntimeError('resize lacks retained unreaped owner')
      os.killpg(process.pid,signal.SIGWINCH);resized=True
     if action=='cancel' and not cancelled:os.write(master,b'\x1d');cancelled=True
     elif action in ('normal','resize') and not released and (action=='normal' or b'SIZE=40x140|' in raw):os.write(master,b'q');released=True
    try:retained.wait(.001)
    except subprocess.TimeoutExpired:continue
    break
   if not retained.joined:raise TimeoutError('bounded outer owner did not positively Wait')
   self.assertTrue(retained.absent());parent_restoration=terminal.restoration_evidence(before,termios.tcgetattr(slave),{slave:flags},{slave:fcntl.fcntl(slave,fcntl.F_GETFL)},sys.platform);self.assertTrue(parent_restoration['configuration_restored']);put(root/'parent-restoration.json',parent_restoration)
   os.close(slave);slave=None;drain=time.monotonic()+2
   while time.monotonic()<drain:
    if read_once(.02):closed=True;break
   self.assertTrue(closed,'outer PTY EOF/drain missing');self.assertIn(b'BEGIN|'+b'x'*131072+b'TAIL|READY|',raw)
   observation=binding.read_json(root/'owner-observation.json',16384)[0];self.assertTrue(observation['termios_restored']);self.assertTrue(observation['fd_flags_restored']);restoration=binding.read_json(root/'terminal-restoration.json',16384)[0];self.assertTrue(restoration['restored']);self.assertTrue(restoration['setters_succeeded']);ledger=state.observe(chosen)
   transcript=(root/'terminal-output.ansi').read_bytes();self.assertLessEqual(len(transcript),4<<20);self.assertIn(b'BEGIN|'+b'x'*131072+b'TAIL|READY|',transcript)
   if action=='resize':self.assertIn(b'SIZE=40x140|',transcript)
   self.assertIn(b'CANCEL|' if action=='cancel' else b'NORMAL|',transcript)
   if fault=='none':
    self.assertEqual(process.returncode,1 if action=='cancel' else 0);self.assertEqual(ledger['state'],'closed_positive');receipt=ledger['receipt'];self.assertEqual(receipt['status'],'failed' if action=='cancel' else 'passed');self.assertEqual(receipt['error'],'operator Control-] abort' if action=='cancel' else None);self.assertEqual(binding.read_json(root/'terminal-child.json',16384)[0],receipt)
    for key in ('wait_joined','pty_drained','group_absence','terminal_restored','descriptors_closed','transcripts_synced'):self.assertTrue(receipt[key],key)
    self.assertEqual(receipt['transcripts']['output'],{'bytes':len(transcript),'sha256':hashlib.sha256(transcript).hexdigest()});self.assertEqual(receipt['transcripts']['input'],{'bytes':len((root/'terminal-input.bin').read_bytes()),'sha256':hashlib.sha256((root/'terminal-input.bin').read_bytes()).hexdigest()})
   else:
    self.assertEqual(process.returncode,1);self.assertTrue(observation['entered']);self.assertEqual(ledger['state'],'unknown');self.assertFalse((root/'terminal-child.json').exists())
    attempted=observation['attempted_receipt']
    self.assertFalse(attempted['transcripts_synced'] if fault=='fsync' else attempted['descriptors_closed'])
    for field in ('wait_joined','pty_drained','group_absence','terminal_restored'):self.assertTrue(attempted[field],field)
    if fault=='close':self.assertTrue(observation['real_selector_closed'])
    with self.assertRaises(TimeoutError):state.close_and_wait(chosen,.001)
   put(root/'case-result.json',{'status':'passed','action':action,'fault':fault,'owner_pid':process.pid,'owner_exit':process.returncode,'owner_wait_joined':retained.joined,'owner_group_absent':retained.absent(),'outer_drained':closed,'outer_termios_restored':True,'outer_fd_flags_restored':True,'ledger_state':ledger['state'],'native_signal_errors':retained.errors,'inner_closure':'positive receipt' if fault=='none' else 'retained unknown; no underlying fixture reclaimed'})
   case_passed=True
  finally:
   # Always attempt all diagnostic writes/FD closes even if cancellation or
   # native ownership becomes unknown. No signal follows any reaping Wait.
   first_failure=sys.exc_info()[1];cleanup_errors=[]
   try:
    if retained is not None and not retained.joined:
     if not retained.authority:raise RuntimeError('outer owner Wait unknown; signalling forbidden')
     retained.send(signal.SIGTERM)
     try:retained.wait(1)
     except subprocess.TimeoutExpired:retained.send(signal.SIGKILL);retained.wait(2)
    if retained is not None and (not retained.joined or not retained.absent()):raise RuntimeError('outer owner closure unconfirmed')
   except BaseException as error:cleanup_errors.append(type(error).__name__+': '+str(error))
   for fd in (master,slave):
    if fd is not None:
     try:os.close(fd)
     except BaseException as error:cleanup_errors.append(type(error).__name__+': '+str(error))
   try:
    (root/'outer-output.bin').write_bytes(raw);(root/'outer-output.bin').chmod(0o600)
   except BaseException as error:cleanup_errors.append(type(error).__name__+': '+str(error))
   try:
    put(root/'parent-closure.json',{'wait_joined':retained.joined if retained else False,'exit':process.returncode if process else None,'authority':retained.authority if retained else False,'group_absent':retained.absent() if retained and retained.joined else False,'signals':retained.signals if retained else [],'resize_signal_sent':resized,'signal_errors':retained.errors if retained else [],'outer_drained':closed,'original_assertions_passed':case_passed,'cleanup_errors':cleanup_errors,'first_failure':(type(first_failure).__name__+': '+str(first_failure))[:2048] if first_failure is not None else None,'note':'No saved inner PID signalling; all files retained even success. Unknown inner ledger is not reclaimed.'})
   except BaseException as error:cleanup_errors.append(type(error).__name__+': '+str(error))
   if cleanup_errors and first_failure is None:raise RuntimeError('PTY fixture cleanup retained: '+repr(cleanup_errors))
   if cleanup_errors and first_failure is not None:print('Additional retained cleanup errors: '+repr(cleanup_errors),file=sys.stderr,flush=True)
 def test_normal_drain_resize_and_restoration(self):self.run_case('normal-resize','resize')
 def test_active_cancellation_drain_and_restoration(self):self.run_case('active-cancel','cancel')
 def test_transcript_sync_failure_retains_unknown(self):self.run_case('sync-fault','normal','fsync')
 def test_descriptor_close_failure_retains_unknown(self):self.run_case('close-fault','normal','close')

if __name__=='__main__':
 parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--binding-only',action='store_true');parser.add_argument('--native-pty',action='store_true');parser.add_argument('--output-dir',type=Path);parser.add_argument('--owner-child',type=Path);parser.add_argument('--binding-sha256');parser.add_argument('--fault',choices=('none','fsync','close'),default='none');args=parser.parse_args()
 if args.binding_only:
  if args.native_pty or args.output_dir or args.owner_child or args.binding_sha256 or args.fault!='none':parser.error('binding-only accepts no native/output/owner arguments')
  unittest.main(argv=[sys.argv[0],'EndpointBinding'],verbosity=2,failfast=True)
  raise SystemExit(0)
 if args.owner_child:
  if args.native_pty or args.output_dir or not args.binding_sha256:parser.error('closed owner-child arguments required')
  raise SystemExit(owner_child(args.owner_child,args.binding_sha256,args.fault))
 if not args.native_pty or args.output_dir is None or args.binding_sha256 or args.fault!='none':parser.error('reviewed --native-pty --output-dir required')
 output=args.output_dir
 if not output.is_absolute() or '..' in output.parts:parser.error('exact absolute output path required')
 output.mkdir(mode=0o700)
 if binding.directory(output)!=binding.native(output.lstat()):raise ValueError('private output selection changed')
 PTY.output=output
 unittest.main(argv=[sys.argv[0],'PTY'],verbosity=2,failfast=True)
