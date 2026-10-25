#!/usr/bin/env python3
"""PROPOSED pure/private-file controls. No PTY/process/window/backend execution.

Synthetic declared child facts validate the schema/state relation only, never
kernel closure or real rendering. Actual native PTY controls remain separate.
"""
from pathlib import Path
import copy,hashlib,importlib.util,json,os,subprocess,tempfile,unittest
SCRIPTS=Path(__file__).resolve().parent
def load(name,path):
 s=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(s);s.loader.exec_module(m);return m
state=load('private_terminal_states',SCRIPTS/'mesh_reconnect_terminal_state.py')
terminal=load('private_terminal_owner',SCRIPTS/'mesh_reconnect_terminal.py')
b=load('private_terminal_binding',SCRIPTS/'mesh_reconnect_terminal_binding.py')
webtests=load('private_existing_web_files',SCRIPTS/'test-mesh-reconnect-ui-binding.py')
fixture=load('private_terminal_fixture',SCRIPTS/'test-mesh-reconnect.py')

def synthetic_receipt(chosen,owner,child,**overrides):
 root=Path(chosen['root']);receipt={'schema':1,'nonce':chosen['nonce'],'owner':owner,'child':child,'launch_attempted':True,'native_no_start':False,'exit':0,'wait_joined':True,'pty_drained':True,'group_absence':True,'terminal_restored':True,'status':'passed','error':None,'transcripts':{x:{'bytes':0,'sha256':hashlib.sha256(b'').hexdigest()} for x in ['input','output']},'argv':[str(root/'arm'),'--home',str(root/'source'),'dashboard','terminal'],'signals':[],'signal_errors':[],'group_observation_errno':None,'descriptors_closed':True,'transcripts_synced':True};receipt.update(overrides);return receipt

class Ledger(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory(prefix='arm-terminal-ledger-files-');self.addCleanup(self.tmp.cleanup);self.root=Path(self.tmp.name);self.root.chmod(0o700)
  self.chosen=state.reserve(self.root,'a'*64,'b'*64,{'key':'lost-disconnect','revision':8,'recovery_sha256':'c'*64})
 def test_close_before_start_and_repeated_closes_do_not_grow_revision(self):
  closed=state.request_close(self.chosen);self.assertEqual((closed['state'],closed['revision']),('closed_no_launch',2))
  for _ in range(100):self.assertEqual(state.request_close(self.chosen),closed)
  with self.assertRaises(ValueError):state.start(self.chosen,'d'*64)
 def test_all_allowed_transitions_with_repeated_close_fit_bound8(self):
  owner=state.start(self.chosen,'d'*64);before=state.observe(self.chosen);self.assertEqual(before['state'],'start_may_begin');self.assertIsNone(before['child'])
  closing=state.request_close(self.chosen)
  for _ in range(20):self.assertEqual(state.request_close(self.chosen),closing)
  child={'pid':43210,'group':43210};running=state.running(self.chosen,owner,child);self.assertTrue(running['closing'])
  for _ in range(20):self.assertEqual(state.running(self.chosen,owner,child),running)
  unknown=state.unknown(self.chosen,owner)
  for _ in range(20):self.assertEqual(state.unknown(self.chosen,owner),unknown);self.assertEqual(state.request_close(self.chosen),unknown)
  receipt=synthetic_receipt(self.chosen,owner,child,status='cancelled',error='synthetic cancellation');closed=state.complete(self.chosen,owner,receipt);self.assertEqual(closed['revision'],6)
  for _ in range(20):self.assertEqual(state.complete(self.chosen,owner,receipt),closed);self.assertEqual(state.request_close(self.chosen),closed)
 def test_unknown_does_not_authorize_fixture_shutdown(self):
  owner=state.start(self.chosen,'d'*64);state.unknown(self.chosen,owner)
  with self.assertRaises(TimeoutError):state.close_and_wait(self.chosen,.001)
  self.assertEqual(state.observe(self.chosen)['state'],'unknown')
 def test_missing_wait_drain_group_restore_cannot_complete(self):
  owner=state.start(self.chosen,'d'*64);child={'pid':43210,'group':43210};state.running(self.chosen,owner,child)
  for name in ['wait_joined','pty_drained','group_absence','terminal_restored','descriptors_closed','transcripts_synced']:
   with self.subTest(name=name),self.assertRaises(ValueError):state.complete(self.chosen,owner,synthetic_receipt(self.chosen,owner,child,**{name:False}))
  self.assertEqual(state.observe(self.chosen)['state'],'running')
 def test_wrong_owner_child_argv_and_unstarted_claim_refused(self):
  owner=state.start(self.chosen,'d'*64);child={'pid':43210,'group':43210};state.running(self.chosen,owner,child)
  values=[{'owner':dict(owner,launch='0'*32)},{'child':{'pid':43211,'group':43211}},{'argv':['arm','dashboard','terminal']},{'native_no_start':True}]
  for change in values:
   receipt=synthetic_receipt(self.chosen,owner,child);receipt.update(change)
   with self.subTest(change=change),self.assertRaises(ValueError):state.complete(self.chosen,owner,receipt)
 def test_hardlinked_or_replaced_lock_refused(self):
  lock=self.root/'terminal-lifecycle.lock';os.link(lock,self.root/'lock-alias')
  with self.assertRaises(ValueError):state.observe(self.chosen)
  (self.root/'lock-alias').unlink();lock.rename(self.root/'retained-lock');(self.root/'terminal-lifecycle.lock').touch(mode=0o600)
  with self.assertRaises(ValueError):state.observe(self.chosen)
 def test_closed_schema_utf8_duplicates_nul_and_caps_refuse(self):
  path=self.root/'terminal-lifecycle.json';raw=path.read_bytes()
  for malformed in [b'{"schema":1,"schema":1}',b'{"x":"\xff"}',b'{"x":"\\u0000"}',b'{"x":"\\ud800"}',b'x'*16385]:
   path.write_bytes(malformed)
   with self.subTest(raw=malformed[:24]),self.assertRaises((ValueError,UnicodeError)):state.observe(self.chosen)
  path.write_bytes(raw);v=json.loads(raw);v['extra']=True;path.write_text(json.dumps(v))
  with self.assertRaises(ValueError):state.observe(self.chosen)
 def test_producer_state_child_combinations_are_closed(self):
  owner=state.start(self.chosen,'d'*64);starting=state.observe(self.chosen);child={'pid':43210,'group':43210}
  wrong=copy.deepcopy(starting);wrong['child']=child
  with self.assertRaises(ValueError):state.validate(wrong,self.chosen)
  state.running(self.chosen,owner,child);running=state.observe(self.chosen);wrong=copy.deepcopy(running);wrong['child']=None
  with self.assertRaises(ValueError):state.validate(wrong,self.chosen)
  for selected in [None,child]:
   unknown=copy.deepcopy(running);unknown['state']='unknown';unknown['child']=selected
   self.assertEqual(state.validate(unknown,self.chosen),unknown)
 def test_positive_known_no_start_requires_no_native_attempt(self):
  owner=state.start(self.chosen,'d'*64);receipt=synthetic_receipt(self.chosen,owner,None,native_no_start=True,launch_attempted=False,wait_joined=False,exit=None,status='failed',error='before native entry')
  self.assertEqual(state.complete(self.chosen,owner,receipt)['state'],'closed_positive')

class Ownership(unittest.TestCase):
 def test_wait_interruption_never_revives_group_signal_authority(self):
  class Fake:pid=43210;returncode=None
  child=terminal.RetainedChild(Fake());original=terminal.os.waitpid
  def interrupted(pid,options):raise InterruptedError('synthetic native-reap/returncode gap')
  terminal.os.waitpid=interrupted
  try:
   with self.assertRaises(InterruptedError):child.wait(1)
   self.assertFalse(child.authority);self.assertFalse(child.joined);self.assertFalse(child.send(15));self.assertEqual(child.signals,[])
  finally:terminal.os.waitpid=original
 def test_explicit_nonreaping_timeout_restores_but_exact_success_retires(self):
  class Fake:pid=43210;returncode=None
  child=terminal.RetainedChild(Fake());original=terminal.os.waitpid;calls=[]
  def pending(pid,options):calls.append((pid,options));return 0,0
  terminal.os.waitpid=pending
  try:
   with self.assertRaises(subprocess.TimeoutExpired):child.wait(.001)
   self.assertTrue(child.authority);self.assertFalse(child.joined)
   terminal.os.waitpid=lambda pid,options:(pid,0)
   self.assertEqual(child.wait(1),0);self.assertFalse(child.send(15));self.assertTrue(child.joined);self.assertEqual(child.process.returncode,0)
   self.assertTrue(all(x==(43210,os.WNOHANG) for x in calls))
  finally:terminal.os.waitpid=original
 def test_native_fixture_cleanup_reports_unknown_instead_of_silent_success(self):
  native=load('proposed_native_wait_cleanup_only',SCRIPTS/'test-mesh-reconnect-terminal-wait-native.py');calls=[]
  class Stream:
   closed=True
   def close(self):calls.append('close')
  class Process:stdin=Stream();stdout=Stream()
  class Child:
   joined=False;authority=False
   def wait(self,*a):calls.append('wait');raise AssertionError('unknown cleanup must not retry Wait')
   def send(self,*a):calls.append('signal');raise AssertionError('unknown cleanup must not signal')
  with self.assertRaisesRegex(RuntimeError,'cleanup unconfirmed'):native.cleanup(Process(),Child())
  self.assertEqual(calls,['close'])
 def test_native_registry_stays_unknown_and_exit_guard_fails_closed(self):
  class SyntheticPopen:pid=43210;returncode=None
  original_type=terminal.subprocess.Popen;original_wait=terminal.os.waitpid;original_exit=terminal.os._exit;calls=[]
  terminal.subprocess.Popen=SyntheticPopen;child=terminal.RetainedChild(SyntheticPopen())
  def echild(pid,options):calls.append('wait');raise ChildProcessError('synthetic native ECHILD')
  def exit125(code):calls.append(('exit',code));raise SystemExit(code)
  terminal.os.waitpid=echild;terminal.os._exit=exit125
  try:
   with self.assertRaises(ChildProcessError):child.wait(1)
   self.assertIs(terminal._NATIVE_WAIT_QUARANTINE[id(child)],child);self.assertTrue(terminal._unknown_native_wait())
   with self.assertRaisesRegex(RuntimeError,'retry forbidden'):child.wait(1)
   with self.assertRaises(SystemExit) as caught:terminal._exit_unknown_native_wait()
   self.assertEqual(caught.exception.code,125);self.assertEqual(calls,['wait',('exit',125)])
  finally:
   terminal._release_native_wait(child);terminal.subprocess.Popen=original_type;terminal.os.waitpid=original_wait;terminal.os._exit=original_exit
 def test_native_registry_releases_only_exact_status_or_nonreaping_timeout(self):
  class SyntheticPopen:pid=43210;returncode=None
  original_type=terminal.subprocess.Popen;original_wait=terminal.os.waitpid;terminal.subprocess.Popen=SyntheticPopen
  child=terminal.RetainedChild(SyntheticPopen())
  def pending(pid,options):
   self.assertIs(terminal._NATIVE_WAIT_QUARANTINE[id(child)],child);self.assertFalse(child.authority);return 0,0
  terminal.os.waitpid=pending
  try:
   with self.assertRaises(subprocess.TimeoutExpired):child.wait(.001)
   self.assertTrue(child.authority);self.assertNotIn(id(child),terminal._NATIVE_WAIT_QUARANTINE)
   terminal.os.waitpid=lambda pid,options:(pid,7<<8)
   self.assertEqual(child.wait(1),7);self.assertTrue(child.joined);self.assertFalse(child.authority);self.assertNotIn(id(child),terminal._NATIVE_WAIT_QUARANTINE)
  finally:
   terminal._release_native_wait(child);terminal.subprocess.Popen=original_type;terminal.os.waitpid=original_wait
 def test_unknown_wait_is_sticky_and_never_reenters_native_wait(self):
  class Fake:pid=43210;returncode=None
  child=terminal.RetainedChild(Fake());original=terminal.os.waitpid;calls=[]
  def echild(pid,options):calls.append((pid,options));raise ChildProcessError('synthetic ECHILD')
  terminal.os.waitpid=echild
  try:
   with self.assertRaises(ChildProcessError):child.wait(1)
   with self.assertRaisesRegex(RuntimeError,'retry forbidden'):child.wait(1)
   self.assertEqual(calls,[(43210,os.WNOHANG)]);self.assertFalse(child.authority);self.assertFalse(child.joined);self.assertFalse(child.send(15));self.assertEqual(child.signals,[])
  finally:terminal.os.waitpid=original
 def test_echild_cannot_publish_receipt_or_positive_ledger_closure(self):
  class Fake:pid=43210;returncode=None
  temp=tempfile.TemporaryDirectory(prefix='arm-terminal-echild-files-');self.addCleanup(temp.cleanup);root=Path(temp.name);root.chmod(0o700)
  chosen=state.reserve(root,'a'*64,'b'*64,{'key':'lost-disconnect','revision':8,'recovery_sha256':'c'*64});owner=state.start(chosen,'d'*64);actual={'pid':43210,'group':43210};state.running(chosen,owner,actual)
  child=terminal.RetainedChild(Fake());original=terminal.os.waitpid
  def echild(pid,options):raise ChildProcessError('synthetic ECHILD, no owned terminal status')
  terminal.os.waitpid=echild
  try:
   with self.assertRaises(ChildProcessError):child.wait(1)
  finally:terminal.os.waitpid=original
  self.assertFalse(child.authority);self.assertFalse(child.joined);self.assertIsNone(child.process.returncode);self.assertFalse(child.send(15))
  receipt=synthetic_receipt(chosen,owner,actual,wait_joined=child.joined,exit=child.process.returncode)
  with self.assertRaises(ValueError):terminal.publish_receipt(chosen,owner,receipt)
  self.assertFalse((root/'terminal-child.json').exists());self.assertEqual(state.observe(chosen)['state'],'running')
  with self.assertRaises(TimeoutError):state.close_and_wait(chosen,.001)


class Restoration(unittest.TestCase):
 def snapshot(self):return [11010,3,19200,1483,9600,9600,[b'\x04',b'\x7f',b'\x01',b'\x00']]
 def test_darwin_fd_only_monotonic_write_history_is_allowed(self):
  for before,after,want in [(2,65538,True),(65538,65538,True),(2,2,True),(65538,2,False),(2,65542,False)]:
   with self.subTest(before=before,after=after):self.assertIs(terminal.fd_flags_restored(before,after,'darwin'),want)
  for bit in range(64):
   if bit==16:continue
   with self.subTest(bit=bit):self.assertFalse(terminal.fd_flags_restored(2,2^(1<<bit),'darwin'))
 def test_non_darwin_fd_every_difference_and_bad_words_refused(self):
  for platform in ('linux','freebsd','unknown'):
   self.assertTrue(terminal.fd_flags_restored(2,2,platform));self.assertFalse(terminal.fd_flags_restored(2,65538,platform))
  for value in (-1,1<<64,True,None,'2'):
   self.assertFalse(terminal.fd_flags_restored(value,value,'darwin'))
 def test_darwin_termios_only_pendin_is_observational_state(self):
  before=self.snapshot();after=copy.deepcopy(before);after[3]^=0x20000000
  self.assertTrue(terminal.termios_restored(before,after,'darwin'));self.assertTrue(terminal.termios_restored(after,before,'darwin'));self.assertEqual(before,self.snapshot())
  for index in range(6):
   for bit in range(64):
    if index==3 and bit==29:continue
    altered=copy.deepcopy(before);altered[index]^=1<<bit
    with self.subTest(index=index,bit=bit):self.assertFalse(terminal.termios_restored(before,altered,'darwin'))
 def test_all_control_characters_shapes_and_other_platforms_exact(self):
  before=self.snapshot()
  for index in range(len(before[6])):
   after=copy.deepcopy(before);after[6][index]=b'\xff'
   self.assertFalse(terminal.termios_restored(before,after,'darwin'))
  for bad in (None,[],tuple(before),before[:-1],before[:6]+[[True]],before[:6]+[[b'two']],before[:6]+[[1.0]],before[:6]+[[None]],before[:6]+[[b'']]):self.assertFalse(terminal.termios_restored(bad,bad,'darwin'))
  after=copy.deepcopy(before);after[3]^=0x20000000
  for platform in ('linux','freebsd','unknown'):
   self.assertTrue(terminal.termios_restored(before,before,platform));self.assertFalse(terminal.termios_restored(before,after,platform))
 def test_raw_evidence_retains_exact_state_and_rejects_changed_fd_roster(self):
  before=self.snapshot();after=copy.deepcopy(before);after[3]|=0x20000000
  proof=terminal.restoration_evidence(before,after,{0:2,1:2},{0:65538,1:65538},'darwin')
  self.assertTrue(proof['configuration_restored']);self.assertEqual(proof['termios_before'][3],1483);self.assertEqual(proof['termios_after'][3],536872395);self.assertEqual(proof['fd_status']['0']['before'],2);self.assertEqual(proof['fd_status']['0']['after'],65538)
  self.assertFalse(proof['darwin_pendin_before']);self.assertTrue(proof['darwin_pendin_after']);self.assertEqual(before,self.snapshot())
  with self.assertRaises(ValueError):terminal.restoration_evidence(before,after,{0:2},{1:2},'darwin')
  for platform in (None,True,'','x'*33,'darwin\x00','dárwin'):
   self.assertFalse(terminal.fd_flags_restored(2,2,platform));self.assertFalse(terminal.termios_restored(before,before,platform))
   with self.assertRaises(ValueError):terminal.restoration_evidence(before,after,{0:2},{0:2},platform)
  for first,last in (([],{}),({True:2},{True:2}),({0:'2'},{0:'2'})):
   with self.assertRaises(ValueError):terminal.restoration_evidence(before,after,first,last,'darwin')
  self.assertTrue(terminal.restoration_evidence(None,None,{}, {},'darwin')['configuration_restored']);self.assertFalse(terminal.restoration_evidence(None,None,{0:2},{0:2},'darwin')['configuration_restored'])
  before[6][0]=b'\x00';self.assertEqual(proof['termios_before'][6][0],{'hex':'04'})

class Binding(unittest.TestCase):
 def setUp(self):
  self.files=webtests.Binding(methodName='runTest');self.files.setUp();self.addCleanup(self.files.doCleanups);self.root=self.files.root;self.files.ready['mode']='terminal';self.files.put('ui-ready.json',self.files.ready)
  operation={'key':'lost-disconnect','revision':1,'recovery_sha256':'d'*64,'phase':'incomplete','kind':'disconnect'}
  self.files.put('terminal-reviewed-operation.json',operation);endpoint=hashlib.sha256((self.root/'source/endpoint.json').read_bytes()).hexdigest();self.chosen=state.reserve(self.root,endpoint,self.files.sha,operation);self.files.put('terminal-reservation.json',self.chosen)
  self.rendered_tmp=tempfile.TemporaryDirectory(prefix='arm-terminal-rendered-files-');self.addCleanup(self.rendered_tmp.cleanup);self.rendered=Path(self.rendered_tmp.name);self.rendered.chmod(0o700)
 def begun(self):
  value=b.begin(self.root,self.files.sha,self.rendered);path=self.root/'terminal-binding.json';digest=b.exclusive(path,value);return path,digest,value
 def proof(self,path,digest):
  owner=state.start(self.chosen,digest);child={'pid':43210,'group':43210};state.running(self.chosen,owner,child)
  for name in ['terminal-input.bin','terminal-output.ansi']:(self.root/name).write_bytes(b'');(self.root/name).chmod(0o600)
  receipt=synthetic_receipt(self.chosen,owner,child);state.exclusive(self.root/'terminal-child.json',receipt);state.complete(self.chosen,owner,receipt)
  actions=[]
  for index,stage in enumerate(sorted(b.STAGES)):
   filename=str(index)+'.png';raw=b'\x89PNG\r\n\x1a\nsynthetic metadata control, NOT real rendering';p=self.rendered/filename;p.write_bytes(raw);p.chmod(0o600);actions.append({'stage':stage,'file':filename,'sha256':hashlib.sha256(raw).hexdigest()})
  manifest=self.rendered/'actions.json';h=b.exclusive(manifest,{'schema':1,'source':'root-cua-terminal-window','window_id':'synthetic-only','new_window':True,'actions':actions});return manifest,h
 def test_web_mode_cannot_be_used_for_terminal_begin(self):
  self.files.put('ui-ready.json',dict(self.files.ready,mode='web'))
  with self.assertRaises(ValueError):b.begin(self.root,self.files.sha,self.rendered)
 def test_file_only_positive_requires_child_default_and_all_stages(self):
  path,digest,_=self.begun();default,value=b.check_default(path,digest);b.exclusive(default,value);self.files.complete();manifest,h=self.proof(path,digest)
  target,outcome,evidence=b.finish(path,digest,'declined','approved',manifest,h);self.assertEqual(target,self.root/'ui-outcome.json');self.assertFalse(target.exists());self.assertEqual(outcome['handoff_key'],'lost-disconnect');self.assertIn('terminal_child',evidence)
 def test_running_child_blocks_finish_without_outcome(self):
  path,digest,_=self.begun();self.files.complete()
  with self.assertRaises(ValueError):b.finish(path,digest,'declined','approved',self.rendered/'actions.json','0'*64)
  self.assertFalse((self.root/'ui-outcome.json').exists())
 def test_recovery_summary_endpoint_or_mode_changes_refuse(self):
  path,digest,_=self.begun();self.files.put('source/endpoint.json',dict(self.files.endpoint,pid=124))
  with self.assertRaises(ValueError):b.selected_binding(path,digest)
  self.assertFalse((self.root/'ui-outcome.json').exists())
 def test_changed_default_receipt_refuses_observation(self):
  path,digest,_=self.begun();self.files.complete()
  with self.assertRaises(ValueError):b.check_default(path,digest)

class CleanupOrder(unittest.TestCase):
 def test_unknown_terminal_retains_every_underlying_fixture_obligation(self):
  f=object.__new__(fixture.Fixture);f.terminal_selection={'synthetic':True};f.evidence={};calls=[];f.observe_backend_endpoints=lambda:calls.append('backend-read')
  original=fixture.terminal_state.close_and_wait
  def unknown(*a):calls.append('terminal-close');raise TimeoutError('synthetic unknown')
  fixture.terminal_state.close_and_wait=unknown
  try:self.assertTrue(f.cleanup());self.assertEqual(calls,['terminal-close'])
  finally:fixture.terminal_state.close_and_wait=original
 def test_legacy_nonterminal_minimal_fixture_does_not_require_new_attribute(self):
  f=object.__new__(fixture.Fixture);f.evidence={};f.server=None;f.server_log=None;f.homes=[];f.backend_pids=set();calls=[];f.observe_backend_endpoints=lambda:calls.append('backend-read')
  self.assertEqual(f.cleanup(),[]);self.assertEqual(calls,['backend-read'])
 def test_positive_close_precedes_all_underlying_cleanup(self):
  f=object.__new__(fixture.Fixture);f.terminal_selection={'synthetic':True};f.evidence={};f.server=None;f.server_log=None;f.homes=[];f.backend_pids=set();calls=[];f.observe_backend_endpoints=lambda:calls.append('backend-read')
  original=fixture.terminal_state.close_and_wait
  def closed(*a):calls.append('terminal-close');return {'state':'closed_no_launch'}
  fixture.terminal_state.close_and_wait=closed
  try:self.assertEqual(f.cleanup(),[]);self.assertEqual(calls,['terminal-close','backend-read'])
  finally:fixture.terminal_state.close_and_wait=original
if __name__=='__main__':unittest.main(verbosity=2)
