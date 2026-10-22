#!/usr/bin/env python3
"""Real private PTY hierarchy editor/partition accounting acceptance."""
import argparse, errno, fcntl, json, os, pty, select, shutil, signal, struct, subprocess, tempfile, termios, time
from pathlib import Path
import importlib.util
spec=importlib.util.spec_from_file_location('pool_pty',Path(__file__).with_name('test-terminal-pools.py'))
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
TerminalScreen=module.TerminalScreen

def main():
 parser=argparse.ArgumentParser();parser.add_argument('--arm',default='bin/arm');binary=str(Path(parser.parse_args().arm).resolve())
 root=Path(tempfile.mkdtemp(prefix='arm-subpool-pty-'));prefix=[binary,'--home',str(root/'state')];process=None;master=slave=None
 def cli(*args):
  p=subprocess.run(prefix+list(args),capture_output=True,text=True,timeout=20);assert p.returncode==0,(args,p.stderr);return json.loads(p.stdout)
 try:
  cli('start','--cpu','2','--memory','1GiB','--disk','1GiB')
  parent=cli('pool','create','Build slots','--scope','global','--unit','slots','--capacity','12','--reason','Private hierarchy PTY')['after']
  master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',46,160,0,0))
  env=dict(os.environ,TERM='xterm-256color',COLORTERM='truecolor');env.pop('NO_COLOR',None)
  process=subprocess.Popen(prefix+['dashboard','terminal'],stdin=slave,stdout=slave,stderr=slave,env=env,start_new_session=True)
  screen=TerminalScreen(160,46);raw=bytearray()
  def drain(seconds=.1):
   end=time.monotonic()+seconds
   while time.monotonic()<end:
    if select.select([master],[],[],min(.03,max(0,end-time.monotonic())))[0]:
     try:data=os.read(master,65536)
     except OSError as e:
      if e.errno==errno.EIO:break
      raise
     raw.extend(data);screen.feed(data)
  def wait(text):
   end=time.monotonic()+8
   while time.monotonic()<end:
    drain()
    if text in screen.text():return
    assert process.poll() is None,'TUI exited'
   raise AssertionError((text,screen.text()))
  def send(text):os.write(master,text.encode())
  def field(value,nextlabel):send('\x15'+value+'\r');wait(nextlabel)
  wait('connected');send('2');wait('RESOURCES');send('p');wait('CUSTOM POOLS');send('\r');wait('CURRENT CONSUMERS');send('a');wait('Create subpool');send('\r');wait('Pool name')
  field('Compiler workers','Capacity (0..9223372036854775807)');field('6','Description (optional)');field('Private child','Required reason');field('Carve compiler capacity','REVIEW POOL')
  assert len(cli('pool','list'))==1,'form mutated before confirmation'
  send('n');drain(.2);assert len(cli('pool','list'))==1,'decline mutated'
  send('a');wait('Create subpool');send('\r');wait('Pool name');field('Compiler workers','Capacity (0..9223372036854775807)');field('6','Description (optional)');field('Private child','Required reason');field('Carve compiler capacity','REVIEW POOL');send('y');wait('Recorded journal #')
  views=cli('pool','list');assert len(views)==2;child=next(v['pool'] for v in views if v['pool'].get('parent')==parent['id']);assert child['unit']=='slots' and child['scope']=='global'
  nested=cli('pool','create','Release workers','--parent',child['id'],'--parent-revision',str(child['revision']),'--capacity','2','--reason','Nested release capacity')['after']
  lease=cli('acquire','--pool',child['id']+'=3','--ttl','5m','--reason','Compile intermediates')
  cli('acquire','--pool',nested['id']+'=2','--ttl','5m','--reason','Link release executable')
  send('\x1b');wait('CUSTOM POOLS · LIST');send('r');drain(.3);wait('Compiler workers')
  # Root is first, followed by its child. Move to the child's exact row.
  send('\x1b[B\r');wait('PARTITION BACKING');wait('Backing for children: 2')
  send('a');wait('Create subpool');send('\x1b[B\r');wait('Capacity (0..9223372036854775807)');field('2','Required reason');field('Reduce while retaining promises','REVIEW POOL');wait('Deficit');send('y');wait('Recorded journal #')
  v=cli('pool','inspect',child['id']);assert v['partition']['committed']==5 and v['partition']['held_backing']==5 and v['partition']['pending_return']==3 and v['deficit']==3
  assert cli('inspect',lease['id'])['state']=='active'
  send('\x1b');wait('CUSTOM POOLS · LIST');send('\r');wait('PARTITION BACKING');send('c');wait('SUBTREE');wait('Compile intermediates');wait('Link release executable')
  # Mouse an actual visible consumer row; the details must belong to that lease.
  lines=screen.text().splitlines();row=next(i for i,line in enumerate(lines) if lease['id'] in line)
  send('\x1b[<0;10;'+str(row+1)+'M\x1b[<0;10;'+str(row+1)+'m');drain(.2);send('\r');wait('Why: Compile intermediates')
  fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',18,55,0,0));os.kill(process.pid,signal.SIGWINCH);drain(.4)
  send('q');process.wait(timeout=6);assert process.returncode==0
  assert b'38;2;' in raw or b'38:2:' in raw,'truecolor terminal was not styled'
  print('Subpool PTY passed: inherited child creation/default-no, nested backing/retained deficit, subtree reasons, mouse consumer, truecolor and narrow resize')
 finally:
  if process and process.poll() is None:process.kill();process.wait()
  for fd in (master,slave):
   if fd is not None:os.close(fd)
  stopped=subprocess.run(prefix+['stop','--force'],capture_output=True,timeout=20)
  if stopped.returncode==0:shutil.rmtree(root)
  else:print('Private fixture retained:',root)
if __name__=='__main__':main()
