"""Private closeable admission for one terminal fixture. No process/PTY operations.

The fixed lock serializes launch intent and cleanup admission, never native
Start/Wait/signals. Cooperative owned-fixture scope, not same-account isolation.
"""
from contextlib import contextmanager
from pathlib import Path
import fcntl,hashlib,json,os,re,stat,time,uuid
MAX=16384
STATES={'open','start_may_begin','running','unknown','closed_no_launch','closed_positive'}
FIELDS={'schema','nonce','revision','state','closing','binding','lock_native','owner','child','receipt'}
BINDING={'root','root_native','endpoint_sha256','arm_sha256','pending_key','pending_revision','recovery_sha256'}

def sha(raw):return hashlib.sha256(raw).hexdigest()
def native(s):return {'device':s.st_dev,'inode':s.st_ino,'uid':s.st_uid,'mode':stat.S_IMODE(s.st_mode)}
def directory(path):
 s=path.lstat()
 if not stat.S_ISDIR(s.st_mode) or s.st_uid!=os.getuid() or stat.S_IMODE(s.st_mode)!=0o700:raise ValueError('private owned real terminal directory required')
 return native(s)
def pairs(rows):
 out={}
 for k,v in rows:
  if k in out:raise ValueError('duplicate terminal field')
  out[k]=v
 return out
def decode(raw):
 if len(raw)>MAX:raise ValueError('terminal state exceeds16KiB')
 def bad(_):raise ValueError('non-JSON terminal constant')
 def integer(v):
  if len(v)>20:raise ValueError('bounded terminal integer required')
  return int(v)
 value=json.loads(raw.decode('utf-8'),object_pairs_hook=pairs,parse_constant=bad,parse_int=integer)
 def walk(v,depth=0):
  if depth>12:raise ValueError('terminal depth exceeds12')
  if isinstance(v,str) and ('\0' in v or any(0xd800<=ord(c)<=0xdfff for c in v)):raise ValueError('valid terminal text required')
  if type(v) is dict:
   if len(v)>64:raise ValueError('bounded terminal fields required')
   for k,item in v.items():walk(k,depth+1);walk(item,depth+1)
  elif type(v) is list:
   if len(v)>64:raise ValueError('bounded terminal list required')
   for item in v:walk(item,depth+1)
 walk(value);return value

def read(path):
 before=path.lstat()
 if not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or stat.S_IMODE(before.st_mode)!=0o600 or before.st_nlink!=1 or before.st_size>MAX:raise ValueError('private single-link terminal file required')
 fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
 try:
  current=os.fstat(fd)
  version=lambda x:(native(x),x.st_size,x.st_mtime_ns,x.st_ctime_ns,x.st_nlink)
  if version(current)!=version(before):raise ValueError('terminal file changed at open')
  with os.fdopen(fd,'rb',closefd=False) as stream:raw=stream.read(MAX+1)
  if len(raw)!=current.st_size or version(os.fstat(fd))!=version(current) or version(path.lstat())!=version(current):raise ValueError('terminal file changed while reading')
 finally:os.close(fd)
 return decode(raw),sha(raw)

def exclusive(path,value):
 raw=(json.dumps(value,separators=(',',':'),ensure_ascii=False)+'\n').encode()
 if len(raw)>MAX:raise ValueError('terminal state exceeds16KiB')
 fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'wb') as f:f.write(raw);f.flush();os.fsync(f.fileno())
 return sha(raw)

def digest(value):
 if type(value) is not str or re.fullmatch('[a-f0-9]{64}',value) is None:raise ValueError('terminal SHA256 required')
 return value

def binding(value):
 if type(value) is not dict or set(value)!=BINDING:raise ValueError('closed terminal generation binding required')
 path=Path(value['root'])
 if not path.is_absolute() or '..' in path.parts or value['root_native']!=directory(path):raise ValueError('terminal root generation changed')
 for k in ('endpoint_sha256','arm_sha256','recovery_sha256'):digest(value[k])
 if type(value['pending_key']) is not str or not 1<=len(value['pending_key'].encode())<=128 or any(ord(c)<32 or ord(c)>126 for c in value['pending_key']):raise ValueError('bounded exact terminal key required')
 if type(value['pending_revision']) is not int or value['pending_revision']<1:raise ValueError('positive terminal operation revision required')
 return path

def selection(value):return {'root':value['binding']['root'],'nonce':value['nonce'],'binding':value['binding'],'lock_native':value['lock_native']}
def validate(value,chosen=None):
 if type(value) is not dict or set(value)!=FIELDS or (type(value['schema']) is not int or value['schema']!=1) or type(value['revision']) is not int or not 1<=value['revision']<=8 or (type(value['state']) is not str or value['state'] not in STATES) or type(value['closing']) is not bool or re.fullmatch('[0-9a-f]{32}',value['nonce'] or '') is None:raise ValueError('closed bounded terminal ledger required')
 binding(value['binding'])
 if chosen is not None and selection(value)!=chosen:raise ValueError('terminal admission generation changed')
 if value['state']=='open' and (value['closing'] or value['owner'] is not None or value['child'] is not None or value['receipt'] is not None):raise ValueError('open terminal cannot own native facts')
 if value['state']=='closed_no_launch' and (not value['closing'] or any(value[x] is not None for x in ('owner','child','receipt'))):raise ValueError('no-launch terminal cannot own native facts')
 if value['state'] not in ('open','closed_no_launch'):
  owner=value['owner']
  if type(owner) is not dict or set(owner)!={'pid','launch','binding_sha256'} or type(owner['pid']) is not int or owner['pid']<=0 or re.fullmatch('[0-9a-f]{32}',owner['launch'] or '') is None:raise ValueError('exact terminal owner required')
 if value['owner'] is not None:digest(value['owner']['binding_sha256'])
 if value['child'] is not None:
  c=value['child']
  if type(c) is not dict or set(c)!={'pid','group'} or type(c['pid']) is not int or c['pid']<=0 or type(c['group']) is not int or c['group']!=c['pid']:raise ValueError('exact sessioned terminal child required')
 if value['state']=='start_may_begin' and value['child'] is not None:raise ValueError('unreturned terminal Start cannot claim a child')
 if value['state']=='running' and value['child'] is None:raise ValueError('running terminal requires its actual returned child')
 if value['state']=='closed_positive':check_receipt(value['receipt'],value)
 elif value['receipt'] is not None:raise ValueError('receipt requires positive terminal closure')
 return value

def reserve(root,endpoint_sha,arm_sha,operation):
 root=Path(root);b={'root':str(root),'root_native':directory(root),'endpoint_sha256':endpoint_sha,'arm_sha256':arm_sha,'pending_key':operation['key'],'pending_revision':operation['revision'],'recovery_sha256':operation['recovery_sha256']};binding(b)
 lock=root/'terminal-lifecycle.lock';fd=os.open(lock,os.O_RDWR|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
 try:info=os.fstat(fd);os.fsync(fd)
 finally:os.close(fd)
 value={'schema':1,'nonce':uuid.uuid4().hex,'revision':1,'state':'open','closing':False,'binding':b,'lock_native':native(info),'owner':None,'child':None,'receipt':None};validate(value);exclusive(root/'terminal-lifecycle.json',value);return selection(value)

@contextmanager
def locked(chosen):
 root=Path(chosen['root']);directory(root);path=root/'terminal-lifecycle.lock';before=path.lstat()
 if not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or stat.S_IMODE(before.st_mode)!=0o600 or before.st_nlink!=1 or before.st_size!=0 or native(before)!=chosen['lock_native']:raise ValueError('fixed terminal lock changed')
 fd=os.open(path,os.O_RDWR|os.O_NOFOLLOW|os.O_NONBLOCK);acquired=False
 try:
  if native(os.fstat(fd))!=native(before):raise ValueError('terminal lock changed at open')
  deadline=time.monotonic()+1
  while True:
   try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB);acquired=True;break
   except BlockingIOError:
    if time.monotonic()>=deadline:raise TimeoutError('terminal ledger contention; retain obligation')
    time.sleep(.005)
  if native(path.lstat())!=chosen['lock_native'] or path.lstat().st_nlink!=1:raise ValueError('terminal lock changed while acquiring')
  value,preimage=read(root/'terminal-lifecycle.json');validate(value,chosen);yield root,value,preimage
 finally:
  if acquired:fcntl.flock(fd,fcntl.LOCK_UN)
  os.close(fd)

def observe(chosen):
 with locked(chosen) as (_,value,_):return value

def _change(chosen,apply):
 with locked(chosen) as (root,value,preimage):
  # The callback is module-private pure dictionary logic; never caller code.
  candidate=decode(json.dumps(value).encode());apply(candidate)
  if candidate==value:return value
  candidate['revision']=value['revision']+1;validate(candidate,chosen)
  path=root/'terminal-lifecycle.json';stage=root/('terminal-ledger-stage-'+uuid.uuid4().hex);exclusive(stage,candidate)
  if read(path)[1]!=preimage or directory(root)!=chosen['binding']['root_native']:raise ValueError('terminal ledger changed before publication; stage retained')
  os.replace(stage,path)
  directory_fd=os.open(root,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
  try:os.fsync(directory_fd)
  finally:os.close(directory_fd)
  return candidate

def start(chosen,binding_sha):
 digest(binding_sha)
 owner={'pid':os.getpid(),'launch':uuid.uuid4().hex,'binding_sha256':binding_sha}
 def apply(v):
  if v['state']!='open' or v['closing']:raise ValueError('terminal admission closed or already consumed')
  v.update(state='start_may_begin',owner=owner)
 _change(chosen,apply);return owner

def owned(v,owner):
 if v['owner']!=owner:raise ValueError('terminal retained owner differs')

def running(chosen,owner,child):
 def apply(v):
  owned(v,owner)
  if v['state']=='running' and v['child']==child:return
  if v['state']!='start_may_begin':raise ValueError('terminal child cannot replace an invocation')
  v.update(state='running',child=child)
 return _change(chosen,apply)

def unknown(chosen,owner):
 def apply(v):
  owned(v,owner)
  if v['state']=='unknown':return
  if v['state'] not in ('start_may_begin','running'):raise ValueError('closed terminal cannot become unknown')
  v['state']='unknown'
 return _change(chosen,apply)

def request_close(chosen):
 def apply(v):
  if v['state'] in ('closed_no_launch','closed_positive') or v['closing']:return
  v['closing']=True
  if v['state']=='open':v['state']='closed_no_launch'
 return _change(chosen,apply)

def check_receipt(receipt,value):
 keys={'schema','nonce','owner','child','launch_attempted','native_no_start','exit','wait_joined','pty_drained','group_absence','terminal_restored','status','error','transcripts','argv','signals','signal_errors','group_observation_errno','descriptors_closed','transcripts_synced'}
 if type(receipt) is not dict or set(receipt)!=keys or type(receipt['schema']) is not int or receipt['schema']!=1 or receipt['nonce']!=value['nonce'] or receipt['owner']!=value['owner'] or receipt['child']!=value['child']:raise ValueError('exact terminal child receipt required')
 expected=[str(Path(value['binding']['root'])/'arm'),'--home',str(Path(value['binding']['root'])/'source'),'dashboard','terminal']
 if receipt['argv']!=expected:raise ValueError('terminal exact child argv differs')
 if type(receipt['signals']) is not list or len(receipt['signals'])>2 or any(type(x) is not int or x not in (9,15) for x in receipt['signals']):raise ValueError('bounded terminal signals required')
 if type(receipt['signal_errors']) is not list or len(receipt['signal_errors'])>2:raise ValueError('bounded terminal signal errors required')
 for error in receipt['signal_errors']:
  if type(error) is not dict or set(error)!={'signal','errno'} or type(error['signal']) is not int or error['signal'] not in (9,15) or type(error['errno']) is not int:raise ValueError('closed terminal signal error required')
 if receipt['group_observation_errno'] is not None and type(receipt['group_observation_errno']) is not int:raise ValueError('explicit group observation error required')
 if any(receipt[k] is not True for k in ('pty_drained','group_absence','terminal_restored','descriptors_closed','transcripts_synced')):raise ValueError('terminal drain/group/restoration closure missing')
 if receipt['native_no_start'] is True:
  if receipt['launch_attempted'] is not False or receipt['child'] is not None or receipt['wait_joined'] is not False or receipt['exit'] is not None:raise ValueError('known no-start must precede any native launch attempt')
 elif receipt['native_no_start'] is False:
  if receipt['launch_attempted'] is not True or receipt['child'] is None or receipt['wait_joined'] is not True or type(receipt['exit']) is not int or not -128<=receipt['exit']<=255:raise ValueError('positive sole native Wait required')
 else:raise ValueError('explicit native launch knowledge required')
 if receipt['status'] not in ('passed','cancelled','failed') or (receipt['error'] is not None and (type(receipt['error']) is not str or len(receipt['error'].encode())>4096)):raise ValueError('bounded terminal outcome required')
 if receipt['status']=='passed' and (receipt['native_no_start'] or receipt['exit']!=0 or receipt['error'] is not None):raise ValueError('terminal success requires actual clean child exit0')
 t=receipt['transcripts']
 if type(t) is not dict or set(t)!={'input','output'}:raise ValueError('closed terminal transcript proofs required')
 for label,limit in [('input',1<<20),('output',4<<20)]:
  x=t[label]
  if type(x) is not dict or set(x)!={'sha256','bytes'} or type(x['bytes']) is not int or not 0<=x['bytes']<=limit:raise ValueError('bounded terminal transcript proof required')
  digest(x['sha256'])
 return receipt

def complete(chosen,owner,receipt):
 def apply(v):
  owned(v,owner)
  if v['state']=='closed_positive' and v['receipt']==receipt:return
  if v['state'] not in ('start_may_begin','running','unknown'):raise ValueError('terminal cannot replace a settled receipt')
  check_receipt(receipt,v);v.update(state='closed_positive',receipt=receipt)
 return _change(chosen,apply)

def close_and_wait(chosen,seconds=10):
 if not 0<seconds<=10:raise ValueError('terminal close budget must be <=10s')
 v=request_close(chosen);deadline=time.monotonic()+seconds
 while v['state'] not in ('closed_no_launch','closed_positive'):
  if time.monotonic()>=deadline:raise TimeoutError('terminal native/Wait/drain/restoration remains unknown; retain fixture')
  time.sleep(.02);v=observe(chosen)
 return v
