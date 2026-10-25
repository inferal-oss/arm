#!/usr/bin/env python3
"""PROPOSED bounded actual-child Wait controls; explicit --native required.

No PTY, window, backend, socket, agent or ARM invocation. Each fixed Python
child is its own session, has no descendants and self-alarms after three seconds.
Do not execute before the parent reviews/authorizes this separate native gate.
"""
from pathlib import Path
import argparse,errno,importlib.util,json,os,selectors,signal,subprocess,sys,tempfile,time,unittest
SCRIPTS=Path(__file__).resolve().parent
def load(name,path):
 spec=importlib.util.spec_from_file_location(name,path);module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module
terminal=load('native_terminal_owner',SCRIPTS/'mesh_reconnect_terminal.py')
state=terminal.state
# The gate's only executable command; no caller-supplied script or argv.
CHILD='import os,signal,sys;signal.alarm(3);os.write(1,b"R");data=os.read(0,1);sys.exit(7 if data==b"7" else 0)'

def ready(process):
 with selectors.DefaultSelector() as selector:
  selector.register(process.stdout,selectors.EVENT_READ)
  if not selector.select(1):raise TimeoutError('fixed child readiness deadline')
  if os.read(process.stdout.fileno(),1)!=b'R':raise ValueError('fixed child readiness differs')

def start():
 process=subprocess.Popen([sys.executable,'-I','-B','-c',CHILD],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,close_fds=True,start_new_session=True,env={'PATH':'/usr/bin:/bin','LANG':'C','LC_ALL':'C'})
 return process,terminal.RetainedChild(process)

def release(process,value=b''):
 if value:process.stdin.write(value);process.stdin.flush()
 process.stdin.close()

def cleanup(process,child):
 # The sole retained owner can signal ONLY following a positively nonreaping
 # timeout. A generic wait error/ECHILD never restores that authority.
 try:
  if not process.stdin.closed:process.stdin.close()
  if not child.joined:
   if not child.authority:raise RuntimeError('native fixture cleanup unconfirmed: retained Wait ownership unknown')
   try:child.wait(1)
   except subprocess.TimeoutExpired:
    child.send(signal.SIGKILL);child.wait(1)
  if not child.joined or not child.absent():raise RuntimeError('native fixture cleanup lacks positive Wait/group absence')
 finally:
  process.stdout.close()

def external_wait_exact(pid):
 deadline=time.monotonic()+2
 while True:
  got,status=os.waitpid(pid,os.WNOHANG)
  if got==pid:
   if not os.WIFEXITED(status):raise ValueError('external control child did not exit normally')
   return status
  if got!=0:raise ValueError('external wait returned different child')
  if time.monotonic()>=deadline:raise TimeoutError('external control Wait deadline')
  time.sleep(.005)

def unknown_exit_child(directory):
 # Private isolated probe: the exact fixed child is externally reaped first,
 # leaving no live descendant. Deliberately do NOT perform external teardown;
 # prove the unknown native owner stays strongly held through exit125.
 signal.alarm(6);directory=Path(directory);directory.mkdir(mode=0o700)
 original_type=subprocess.Popen;original_wait=os.waitpid
 class LoggedPopen(original_type):
  def __del__(self):
   with (directory/'finalizer-called').open('a') as stream:stream.write('unexpected Popen finalizer\n')
   super().__del__()
 process=LoggedPopen([sys.executable,'-I','-B','-c',CHILD],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,close_fds=True,start_new_session=True,env={'PATH':'/usr/bin:/bin','LANG':'C','LC_ALL':'C'});child=terminal.RetainedChild(process)
 ready(process);release(process);status=external_wait_exact(process.pid)
 if os.waitstatus_to_exitcode(status)!=0 or not child.absent():raise RuntimeError('isolated external cleanup not positively observed')
 def recorded_wait(pid,options):
  with (directory/'native-wait-calls').open('a') as stream:stream.write(str(pid)+'\n');stream.flush();os.fsync(stream.fileno())
  return original_wait(pid,options)
 os.waitpid=recorded_wait
 try:child.wait(1)
 except ChildProcessError as error:
  if error.errno!=errno.ECHILD:raise
 else:raise RuntimeError('actual ECHILD missing')
 process.stdin.close();process.stdout.close()
 evidence={'child_pid':process.pid,'external_exit':os.waitstatus_to_exitcode(status),'external_group_absent':child.absent(),'retained_joined':child.joined,'retained_authority':child.authority,'returncode_unset':process.returncode is None,'quarantined':terminal._NATIVE_WAIT_QUARANTINE.get(id(child)) is child}
 with (directory/'unknown-evidence.json').open('x') as stream:json.dump(evidence,stream);stream.flush();os.fsync(stream.fileno())
 return 1 # atexit125 must run BEFORE Popen.__del__, with no second waitpid.

class ActualNativeWait(unittest.TestCase):
 output=None
 def test_exact_native_exit_status_is_owned_positive_wait(self):
  process,child=start()
  try:
   ready(process);release(process,b'7');self.assertEqual(child.wait(2),7)
   self.assertTrue(child.joined);self.assertFalse(child.authority);self.assertTrue(child.absent());self.assertFalse(child.send(signal.SIGTERM))
  finally:cleanup(process,child)
 def test_actual_nonreaping_timeout_keeps_anchor_until_release_and_wait(self):
  process,child=start()
  try:
   ready(process)
   with self.assertRaises(subprocess.TimeoutExpired):child.wait(.01)
   self.assertTrue(child.authority);self.assertFalse(child.joined);self.assertIsNone(process.returncode)
   release(process);self.assertEqual(child.wait(2),0);self.assertTrue(child.joined);self.assertFalse(child.authority);self.assertTrue(child.absent())
  finally:cleanup(process,child)
 def test_isolated_unknown_exit125_prevents_finalizer_native_retry(self):
  directory=self.output/'unknown-exit125'
  process=subprocess.Popen([sys.executable,'-I','-B',str(Path(__file__).resolve()),'--unknown-exit-child',str(directory)],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,close_fds=True,start_new_session=True)
  child=terminal.RetainedChild(process)
  try:
   self.assertEqual(child.wait(5),125);self.assertTrue(child.joined);self.assertTrue(child.absent())
   evidence=json.loads((directory/'unknown-evidence.json').read_bytes());self.assertEqual({k:v for k,v in evidence.items() if k!='child_pid'},{'external_exit':0,'external_group_absent':True,'retained_joined':False,'retained_authority':False,'returncode_unset':True,'quarantined':True})
   self.assertEqual((directory/'native-wait-calls').read_text().splitlines(),[str(evidence['child_pid'])]);self.assertFalse((directory/'finalizer-called').exists())
   print(json.dumps({'exit125_probe':evidence,'native_wait_calls':1,'finalizer_called':False,'parent_wait_joined':child.joined,'parent_group_absent':child.absent()}),flush=True)
  finally:
   if not child.joined:
    if not child.authority:raise RuntimeError('exit125 probe owner unknown; no saved signal permitted')
    child.send(signal.SIGKILL);child.wait(2)
   if not child.joined or not child.absent():raise RuntimeError('exit125 probe cleanup unconfirmed')
 def test_actual_foreign_reap_echild_never_becomes_receipt_or_closure(self):
  process,child=start();external_status=None;external_closure=False
  try:
   ready(process);release(process);external_status=external_wait_exact(process.pid)
   self.assertEqual(os.waitstatus_to_exitcode(external_status),0)
   # Deliberately leave Popen.returncode unset: this is the precise Python
   # _try_wait ECHILD-synthetic-zero hazard, without using that implementation.
   self.assertIsNone(process.returncode)
   with self.assertRaises(ChildProcessError) as caught:child.wait(1)
   self.assertEqual(caught.exception.errno,errno.ECHILD);self.assertFalse(child.joined);self.assertFalse(child.authority);self.assertIsNone(process.returncode)
   self.assertFalse(child.send(signal.SIGTERM));self.assertFalse(child.send(signal.SIGKILL));self.assertEqual(child.signals,[])
   self.assertTrue(child.absent());external_closure=True
   with tempfile.TemporaryDirectory(prefix='arm-terminal-native-echild-') as name:
    root=Path(name);root.chmod(0o700);chosen=state.reserve(root,'a'*64,'b'*64,{'key':'lost-disconnect','revision':8,'recovery_sha256':'c'*64});owner=state.start(chosen,'d'*64);actual={'pid':process.pid,'group':process.pid};state.running(chosen,owner,actual)
    receipt={'schema':1,'nonce':chosen['nonce'],'owner':owner,'child':actual,'launch_attempted':True,'native_no_start':False,'exit':None,'wait_joined':child.joined,'pty_drained':True,'group_absence':child.absent(),'terminal_restored':True,'status':'failed','error':'external reap: no owned terminal status','transcripts':{x:{'bytes':0,'sha256':'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'} for x in ('input','output')},'argv':[str(root/'arm'),'--home',str(root/'source'),'dashboard','terminal'],'signals':[],'signal_errors':[],'group_observation_errno':None,'descriptors_closed':True,'transcripts_synced':True}
    with self.assertRaises(ValueError):terminal.publish_receipt(chosen,owner,receipt)
    self.assertFalse((root/'terminal-child.json').exists());self.assertEqual(state.observe(chosen)['state'],'running')
    with self.assertRaises(TimeoutError):state.close_and_wait(chosen,.001)
  finally:
   # Suppress Popen destructor's unrelated reaper only from the separately
   # captured external status; this NEVER changes child.joined or the ledger.
   if external_status is not None:process.returncode=os.waitstatus_to_exitcode(external_status)
   if external_closure:
    # TEST ONLY: real exact external status and group absence above make this
    # Popen finalizer harmless. This never promotes retained ownership/ledger.
    terminal._release_native_wait(child)
    process.stdout.close()
    print(json.dumps({'native_fixture_cleanup':'external exact status plus group absence','pid':process.pid,'external_exit':os.waitstatus_to_exitcode(external_status),'retained_wait_joined':child.joined,'retained_authority':child.authority,'retained_ledger_closure':False}),flush=True)
   elif external_status is not None:
    process.stdout.close();raise RuntimeError('foreign-reap fixture cleanup unconfirmed; remaining cases forbidden')
   else:cleanup(process,child)

if __name__=='__main__':
 parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--native',action='store_true');parser.add_argument('--unknown-exit-child',type=Path);parser.add_argument('--evidence-dir',type=Path);args=parser.parse_args()
 if args.unknown_exit_child:
  if args.native or args.evidence_dir:parser.error('isolated probe arguments required')
  raise SystemExit(unknown_exit_child(args.unknown_exit_child))
 if not args.native or args.evidence_dir is None:parser.error('reviewed --native --evidence-dir required')
 if not args.evidence_dir.is_absolute() or '..' in args.evidence_dir.parts:parser.error('absolute exclusive private evidence directory required')
 args.evidence_dir.mkdir(mode=0o700);ActualNativeWait.output=args.evidence_dir
 unittest.main(argv=[sys.argv[0]],verbosity=2,failfast=True)
