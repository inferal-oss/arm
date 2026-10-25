#!/usr/bin/env python3
"""Owned PTY relay for ONE reviewed TUI in a NEW actual Terminal window.

No app/window automation, backend launch, RPC, socket or saved-PID recovery.
No child poll/reaper: sole Wait retires group signalling before possible reap.
"""
from pathlib import Path
import atexit,argparse,errno,fcntl,hashlib,importlib.util,json,os,pty,selectors,signal,struct,subprocess,sys,termios,threading,time,tty
ROOT=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('terminal_owner_binding',ROOT/'mesh_reconnect_terminal_binding.py');binding=importlib.util.module_from_spec(spec);spec.loader.exec_module(binding);state=binding.state

# Strong references prevent Popen.__del__/_active from acquiring a second native
# wait on an ambiguous owned child after the caller's stack unwinds. This module
# owns one actual child per owner invocation; test callers are bounded/failfast.
_NATIVE_WAIT_QUARANTINE={}
def _retain_native_wait(child):
 if isinstance(child.process,subprocess.Popen):_NATIVE_WAIT_QUARANTINE[id(child)]=child
def _release_native_wait(child):_NATIVE_WAIT_QUARANTINE.pop(id(child),None)
def _unknown_native_wait():return bool(_NATIVE_WAIT_QUARANTINE)
def _exit_unknown_native_wait():
 # Normal receipt/unknown/report/descriptor attempts run before interpreter
 # shutdown. Exit125 bypasses Python finalizers only when native Wait is unknown.
 if _unknown_native_wait():os._exit(125)
atexit.register(_exit_unknown_native_wait)

class RetainedChild:
 def __init__(self,process):self.process=process;self.authority=True;self.joined=False;self.signals=[];self.errors=[];self.group_errno=None
 def send(self,sig):
  if not self.authority or self.process.returncode is not None:return False
  if int(sig) in self.signals:return False
  if len(self.signals)>=2:raise ValueError('terminal signal roster exhausted')
  self.signals.append(int(sig))
  try:os.killpg(self.process.pid,sig)
  except ProcessLookupError:pass
  except OSError as e:self.errors.append({'signal':int(sig),'errno':e.errno})
  return True
 def wait(self,seconds):
  if not 0<seconds<=5:raise ValueError('terminal native Wait budget must be <=5s')
  if self.joined:return self.process.returncode
  if not self.authority:raise RuntimeError('terminal native Wait ownership is unknown; retry forbidden')
  deadline=time.monotonic()+seconds
  while True:
   # Retire BEFORE every potentially reaping syscall. Popen.wait/_try_wait is
   # deliberately unused: it can turn ECHILD into a synthetic exit0.
   _retain_native_wait(self)
   self.authority=False
   try:pid,status=os.waitpid(self.process.pid,os.WNOHANG)
   except BaseException:
    # ECHILD/interrupt/error is unknown even if a foreign reap made the group
    # absent. Neither returncode nor absence can stand in for our exact Wait.
    self.authority=False;raise
   if pid==self.process.pid:
    if not (os.WIFEXITED(status) or os.WIFSIGNALED(status)):raise RuntimeError('nonterminal native Wait status')
    result=os.waitstatus_to_exitcode(status)
    self.process.returncode=result;self.joined=True;_release_native_wait(self);return result
   if pid!=0:raise RuntimeError('native Wait returned another child identity')
   # Only this explicit nonreaping0 preserves the owned unreaped anchor.
   if time.monotonic()>=deadline:
    self.authority=True;_release_native_wait(self);raise subprocess.TimeoutExpired('owned terminal child',seconds)
   try:time.sleep(min(.01,max(0,deadline-time.monotonic())))
   except BaseException:
    self.authority=False;raise
 def absent(self):
  try:os.killpg(self.process.pid,0)
  except ProcessLookupError:return True
  except OSError as e:self.group_errno=e.errno
  return False

def publish_receipt(chosen,owner,receipt):
 current=state.observe(chosen);state.check_receipt(receipt,current)
 state.exclusive(Path(chosen['root'])/'terminal-child.json',receipt)
 return state.complete(chosen,owner,receipt)

# Darwin XNU f6217f891ac0bb64f3d375211650a4c1ff8ca1ea:
# bsd/sys/fcntl.h136 FWASWRITTEN is kernel write history, not FCNTLFLAGS;
# bsd/sys/termios.h PENDIN is pending-input processing state, not configuration.
_DARWIN_FWASWRITTEN=0x00010000
_DARWIN_PENDIN=0x20000000

def _flag_word(value):return type(value) is int and 0<=value<(1<<64)
def _platform_name(value):return type(value) is str and 1<=len(value)<=32 and all('a'<=c<='z' or '0'<=c<='9' or c=='_' for c in value)
def fd_flags_restored(before,after,platform):
 if not _platform_name(platform) or not _flag_word(before) or not _flag_word(after):return False
 if platform!='darwin':return before==after
 # Preserve every other bit (including unknown/access/settable bits), and do
 # not accept loss of sticky write history. Never change actual native flags.
 return ((before^after)&~_DARWIN_FWASWRITTEN)==0 and not (before&_DARWIN_FWASWRITTEN and not after&_DARWIN_FWASWRITTEN)

def _termios_shape(value):
 if type(value) is not list or len(value)!=7 or not all(_flag_word(item) for item in value[:6]):return False
 chars=value[6]
 return type(chars) is list and 1<=len(chars)<=64 and all((type(item) is bytes and len(item)==1) or (type(item) is int and 0<=item<=255) for item in chars)

def termios_restored(before,after,platform):
 if not _platform_name(platform) or not _termios_shape(before) or not _termios_shape(after):return False
 if any(before[index]!=after[index] for index in (0,1,2,4,5,6)):return False
 return ((before[3]^after[3])&~_DARWIN_PENDIN)==0 if platform=='darwin' else before[3]==after[3]

def restoration_evidence(before,after,flags_before,flags_after,platform):
 if not _platform_name(platform):raise ValueError('bounded native platform name required')
 if type(flags_before) is not dict or type(flags_after) is not dict or set(flags_before)!=set(flags_after) or len(flags_before)>3 or any(type(fd) is not int or not 0<=fd<=1048576 for fd in flags_before):raise ValueError('bounded exact restoration FD roster required')
 if before is None or after is None:
  terms_ok=before is None and after is None and not flags_before
 else:terms_ok=termios_restored(before,after,platform)
 def packed(value):
  if value is None:return None
  if not _termios_shape(value):raise ValueError('bounded native termios snapshot required')
  return value[:6]+[[{'hex':item.hex()} if type(item) is bytes else {'int':item} for item in value[6]]]
 records={}
 for fd in sorted(flags_before):
  first,last=flags_before[fd],flags_after[fd]
  if not _flag_word(first) or not _flag_word(last):raise ValueError('native FD status word required')
  records[str(fd)]={'before':first,'requested':first,'after':last,'configuration_equal':fd_flags_restored(first,last,platform),'darwin_was_written_before':bool(first&_DARWIN_FWASWRITTEN) if platform=='darwin' else None,'darwin_was_written_after':bool(last&_DARWIN_FWASWRITTEN) if platform=='darwin' else None}
 return {'schema':1,'platform':platform,'termios_before':packed(before),'termios_after':packed(after),'termios_configuration_equal':terms_ok,'darwin_pendin_before':bool(before[3]&_DARWIN_PENDIN) if platform=='darwin' and before is not None else None,'darwin_pendin_after':bool(after[3]&_DARWIN_PENDIN) if platform=='darwin' and after is not None else None,'fd_status':records,'configuration_restored':terms_ok and all(item['configuration_equal'] for item in records.values())}


def controlling_terminal():
 # Sole-thread fixture pre-exec only. The accepted native executable is fixed;
 # this is session ownership, not containment of a hostile escaping workload.
 os.setsid();fcntl.ioctl(0,termios.TIOCSCTTY,0)

def owner(binding_path,binding_sha,seconds=420):
 if not 0<seconds<=420:raise ValueError('terminal active budget must be <=420s')
 if not os.isatty(0) or not os.isatty(1) or threading.active_count()!=1:raise ValueError('dedicated single-thread actual Terminal required')
 saved,_=binding.selected_binding(binding_path,binding_sha);chosen=saved['terminal_selection'];root=Path(saved['root']);cap=state.start(chosen,binding_sha)
 process=None;child=None;launch_attempted=False;original=None;flags={};master=slave=None;selector=None;streams={};counts={'input':0,'output':0};hashes={k:hashlib.sha256() for k in counts};outgoing=bytearray();incoming=bytearray();eof=False;restored=False;descriptors_closed=True;transcripts_synced=True;failure=None;cancelled=None;cancel_at=None;killed=False;resize=True;old_handlers={};started=time.monotonic()
 argv=[str(root/'arm'),'--home',str(root/'source'),'dashboard','terminal']
 def interrupted(sig,_):
  nonlocal cancelled,resize
  if sig==signal.SIGWINCH:resize=True
  else:cancelled=cancelled or signal.Signals(sig).name
 def record_error(error):
  nonlocal failure
  if failure is None:failure=(type(error).__name__+': '+str(error))[:2048]
 def cancel(reason):
  nonlocal failure,cancel_at
  if failure is None:failure=reason
  if cancel_at is None:
   cancel_at=time.monotonic()
   if child is not None:child.send(signal.SIGTERM)
 def geometry():
  raw=fcntl.ioctl(0,termios.TIOCGWINSZ,b'\0'*8);rows,cols,x,y=struct.unpack('HHHH',raw)
  if not 36<=rows<=160 or not 120<=cols<=400:raise ValueError('bounded real Terminal geometry required')
  fcntl.ioctl(master,termios.TIOCSWINSZ,raw)
 def pump(wait):
  nonlocal eof,transcripts_synced
  # Read pressure is bounded by each64KiB queue. No unbounded transcript cache.
  for fd in list(selector.get_map()):selector.unregister(fd)
  if not eof:
   event=(selectors.EVENT_READ if len(outgoing)<=49152 else 0)|(selectors.EVENT_WRITE if incoming else 0)
   if event:selector.register(master,event,'pty')
   if len(incoming)<=49152:selector.register(0,selectors.EVENT_READ,'input')
  if outgoing:selector.register(1,selectors.EVENT_WRITE,'output')
  for key,events in selector.select(wait):
   if key.data=='input':
    raw=os.read(0,16384)
    if not raw:cancel('outer Terminal input closed');continue
    if counts['input']+len(raw)>1<<20:cancel('terminal input transcript cap exceeded');continue
    try:streams['input'].write(raw)
    except BaseException:transcripts_synced=False;raise
    hashes['input'].update(raw);counts['input']+=len(raw)
    if b'\x1d' in raw:cancel('operator Control-] abort');continue
    incoming.extend(raw)
   elif key.data=='output':
    try:n=os.write(1,outgoing[:16384]);del outgoing[:n]
    except BlockingIOError:pass
   elif key.data=='pty':
    if events&selectors.EVENT_WRITE and incoming:
     try:n=os.write(master,incoming[:16384]);del incoming[:n]
     except BlockingIOError:pass
     except OSError as e:
      if e.errno in (errno.EIO,errno.EPIPE):incoming.clear()
      else:raise
    if events&selectors.EVENT_READ:
     try:raw=os.read(master,16384)
     except BlockingIOError:continue
     except OSError as e:
      if e.errno!=errno.EIO:raise
      raw=b''
     if not raw:eof=True;incoming.clear();continue
     if counts['output']+len(raw)>4<<20:cancel('terminal output transcript cap exceeded');continue
     try:streams['output'].write(raw)
     except BaseException:transcripts_synced=False;raise
     hashes['output'].update(raw);counts['output']+=len(raw);outgoing.extend(raw)
 try:
  original=termios.tcgetattr(0)
  for fd in (0,1):flags[fd]=fcntl.fcntl(fd,fcntl.F_GETFL)
  for sig in (signal.SIGHUP,signal.SIGTERM,signal.SIGINT,signal.SIGWINCH):old_handlers[sig]=signal.getsignal(sig);signal.signal(sig,interrupted)
  for label,filename in [('input','terminal-input.bin'),('output','terminal-output.ansi')]:streams[label]=os.fdopen(os.open(root/filename,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600),'wb')
  for name in ('terminal-home','terminal-tmp'):(root/name).mkdir(mode=0o700)
  master,slave=pty.openpty();geometry();tty.setraw(0)
  for fd in (0,1,master):os.set_blocking(fd,False)
  env={'HOME':str(root/'terminal-home'),'TMPDIR':str(root/'terminal-tmp'),'PATH':'/usr/bin:/bin:/usr/sbin:/sbin','TERM':'xterm-256color','COLORTERM':'truecolor','LANG':'en_US.UTF-8','LC_ALL':'en_US.UTF-8'}
  # No ledger lock surrounds native entry. Even an interrupted Start leaves the
  # prior durable start_may_begin obligation; no handle means unknown ownership.
  launch_attempted=True
  process=subprocess.Popen(argv,stdin=slave,stdout=slave,stderr=slave,cwd=root,env=env,close_fds=True,preexec_fn=controlling_terminal)
  child=RetainedChild(process);os.close(slave);slave=None;state.running(chosen,cap,{'pid':process.pid,'group':process.pid});selector=selectors.DefaultSelector()
  while not (eof and not outgoing):
   if cancelled:cancel('terminal owner interrupted: '+cancelled)
   if state.observe(chosen)['closing']:cancel('fixture terminal admission closed')
   if time.monotonic()-started>=seconds:cancel('terminal active budget exhausted')
   if resize:geometry();resize=False
   if cancel_at is not None and time.monotonic()-cancel_at>=.5 and not killed:child.send(signal.SIGKILL);killed=True
   if cancel_at is not None and time.monotonic()-cancel_at>=6:break
   pump(.02)
  if failure:
   if not killed:child.send(signal.SIGKILL);killed=True
   child.wait(1)
  else:
   try:child.wait(1)
   except subprocess.TimeoutExpired:
    cancel('terminal pipe EOF without positive child Wait');time.sleep(.5);child.send(signal.SIGKILL);killed=True;child.wait(1)
 except BaseException as error:
  record_error(error)
  if child is not None and child.authority and process.returncode is None:
   child.send(signal.SIGTERM)
   if not killed:child.send(signal.SIGKILL);killed=True
   try:
    until=time.monotonic()+1
    while selector is not None and not (eof and not outgoing) and time.monotonic()<until:pump(.02)
    child.wait(1)
   except BaseException as terminal_error:record_error(terminal_error)
 finally:
  if selector is not None:
   try:selector.close()
   except BaseException as error:descriptors_closed=False;record_error(error)
  for fd in (master,slave):
   if fd is not None:
    try:os.close(fd)
    except OSError as error:descriptors_closed=False;record_error(error)
  for label,stream in streams.items():
   try:
    stream.flush();os.fsync(stream.fileno())
    if os.fstat(stream.fileno()).st_size!=counts[label]:raise ValueError('terminal transcript length differs')
   except BaseException as error:transcripts_synced=False;record_error(error)
   finally:
    try:stream.close()
    except BaseException as error:descriptors_closed=False;record_error(error)
  setters_succeeded=True
  try:
   if original is not None:termios.tcsetattr(0,termios.TCSANOW,original)
   for fd,value in flags.items():fcntl.fcntl(fd,fcntl.F_SETFL,value)
  except BaseException as error:setters_succeeded=False;record_error(error)
  try:
   proof=restoration_evidence(original,termios.tcgetattr(0) if original is not None else None,flags,{fd:fcntl.fcntl(fd,fcntl.F_GETFL) for fd in flags},sys.platform)
   proof['setters_succeeded']=setters_succeeded;proof['restored']=setters_succeeded and proof['configuration_restored']
   state.exclusive(root/'terminal-restoration.json',proof);restored=proof['restored']
  except BaseException as error:restored=False;record_error(error)
  for sig,handler in old_handlers.items():signal.signal(sig,handler)
  absent=child.absent() if child is not None else not launch_attempted
  no_start=not launch_attempted;drained=(eof and not outgoing) if child is not None else no_start
  exit_code=process.returncode if process is not None else None
  if child is not None and exit_code!=0 and failure is None:failure='actual TUI child exited '+str(exit_code)
  receipt={'schema':1,'nonce':chosen['nonce'],'owner':cap,'child':{'pid':process.pid,'group':process.pid} if process is not None else None,'launch_attempted':launch_attempted,'native_no_start':no_start,'exit':exit_code,'wait_joined':child.joined if child is not None else False,'pty_drained':drained,'group_absence':absent,'terminal_restored':restored,'status':'passed' if failure is None and child is not None and exit_code==0 else 'cancelled' if cancelled else 'failed','error':failure,'transcripts':{k:{'sha256':hashes[k].hexdigest(),'bytes':counts[k]} for k in counts},'argv':argv,'signals':child.signals if child is not None else [],'signal_errors':child.errors if child is not None else [],'group_observation_errno':child.group_errno if child is not None else None,'descriptors_closed':descriptors_closed,'transcripts_synced':transcripts_synced}
  try:
   publish_receipt(chosen,cap,receipt)
  except BaseException as error:
   record_error(error)
   try:state.unknown(chosen,cap)
   except BaseException:pass
  # Console output is outside child raw transcript. No additional child launch
  # can follow either this terminal state or a retained unknown obligation.
  print('\nTerminal TUI owner finished; inspect exact child receipt and ledger. '+('SUCCESS' if failure is None else 'RETAINED FAILURE: '+failure),flush=True)
 return 0 if failure is None else 1

def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--binding',type=Path,required=True);p.add_argument('--binding-sha256',required=True);p.add_argument('--active-seconds',type=float,default=420);a=p.parse_args();return owner(a.binding,a.binding_sha256,a.active_seconds)
if __name__=='__main__':sys.exit(main())
