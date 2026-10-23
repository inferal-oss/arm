#!/usr/bin/env python3
"""Real offline worker-runner guards; fake tools never validate a theorem."""
import importlib.util,json,os,shutil,signal,subprocess,sys,tempfile,time,unittest
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
SCRIPT=ROOT/'scripts/test-provider-worker-formal.py'

def load(path):
 spec=importlib.util.spec_from_file_location('worker_runner_'+str(time.time_ns()),path);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m

def running(pid):
 if sys.platform.startswith('linux'):
  try:return Path('/proc',str(pid),'stat').read_text().split(') ',1)[1].split()[0] not in ('Z','X')
  except (FileNotFoundError,ProcessLookupError):return False
 result=subprocess.run(['ps','-o','stat=','-p',str(pid)],capture_output=True,text=True)
 return result.returncode==0 and bool(result.stdout.strip()) and not result.stdout.lstrip().startswith('Z')

class Guards(unittest.TestCase):
 def setUp(self):
  (ROOT/'.tla').mkdir(exist_ok=True)
  self.temp=tempfile.TemporaryDirectory(prefix='worker-runner-guard-',dir=ROOT/'.tla')
  self.path=Path(self.temp.name);self.addCleanup(self.temp.cleanup)
 def fixture(self):
  """Tiny complete tool closure; only the wrapper/cleanup behavior is tested."""
  bindir=self.path/'bin';bindir.mkdir();stdlib=self.path/'stdlib';stdlib.mkdir()
  (stdlib/'TLAPS.tla').write_text('fixture')
  home=self.path/'isabelle';ml=home/'poly';heap=home/'heaps';ml.mkdir(parents=True);heap.mkdir()
  for name in ('poly','libgmp.10.dylib','libsha1.so'):(ml/name).write_text('fixture')
  for name in ('Pure','TLA+','Options'):(heap/name).write_text('fixture')
  for name in ('z3','zenon','ptl_to_trp','ls4'):
   p=bindir/name;p.write_text('#!'+sys.executable+'\nprint("fixture tool")\n');p.chmod(0o700)
  config="library_path == "+json.dumps(str(stdlib))+"\nSMT == "+json.dumps("PATH='"+str(bindir)+"'; z3")+"\nIsabelle == "+json.dumps("export ISABELLE_HOME='"+str(home)+"'; export ML_HOME='"+str(ml)+"'")+"\n"
  return config
 def args(self,m,tool):
  import argparse
  return argparse.Namespace(mode='prove',work_dir=str(self.path/'work'),targets=list(m.TARGETS),java='java',tlc_jar='unused',tlapm=str(tool),go='go')
 def fake_tool(self,body):
  tool=self.path/'tlapm';tool.write_text('#!'+sys.executable+'\nimport sys,os,json,time,subprocess\n'+body);tool.chmod(0o700);return tool
 def test_duplicate_targets_refused_before_work(self):
  result=subprocess.run([sys.executable,str(SCRIPT),'--targets','success','success','--work-dir',str(self.path/'never')],capture_output=True,text=True,timeout=10)
  self.assertNotEqual(result.returncode,0);self.assertIn('duplicate targets',result.stderr);self.assertFalse((self.path/'never').exists())
 def test_nonterminal_module_prefix_refused_before_prover(self):
  m=load(SCRIPT);marker=self.path/'invoked';tool=self.fake_tool("open("+repr(str(marker))+",'w').write('invoked')\n")
  gate=m.Gate(self.args(m,tool));proof=gate.run/'ProviderWorkerProof.tla'
  text=proof.read_text();proof.write_text(text[:text.index('THEOREM EveryStepPreservesInvariant')]+'\n====\n'+text[text.index('THEOREM EveryStepPreservesInvariant'):])
  gate.summary['sources']['ProviderWorkerProof.tla']=m.digest(proof) # simulate malformed captured source, not a mutation bypass claim
  with self.assertRaises(ValueError):gate.prove()
  self.assertFalse(marker.exists())
 def test_changed_captured_proof_refused_before_prover(self):
  m=load(SCRIPT);marker=self.path/'invoked';tool=self.fake_tool("open("+repr(str(marker))+",'w').write('invoked')\n")
  gate=m.Gate(self.args(m,tool));p=gate.run/'ProviderWorkerProof.tla';p.write_text(p.read_text()+'\n\\* changed\n')
  with self.assertRaisesRegex(ValueError,'snapshot changed'):gate.prove()
  self.assertFalse(marker.exists())
 def test_duplicate_success_counts_are_refused(self):
  for counts in ((5,5),(5,6)):
   with self.subTest(counts=counts):
    m=load(SCRIPT);tool=self.fake_tool("if '--summary' in sys.argv: print('obligations_count = 5')\nelse:\n print('All "+str(counts[0])+" obligations proved.')\n print('All "+str(counts[1])+" obligations proved.')\n")
    gate=m.Gate(self.args(m,tool))
    with self.assertRaisesRegex(ValueError,'success count'):gate.prove()
 def test_changed_original_source_refused_before_prover(self):
  for kind in ('model','go'):
   with self.subTest(kind=kind):
    clone=self.path/('clone-'+kind)
    (clone/'scripts').mkdir(parents=True);(clone/'formal/provider-worker').mkdir(parents=True);(clone/'internal').mkdir()
    for name in ('go.mod','go.sum'):shutil.copyfile(ROOT/name,clone/name)
    for name in ('test-formal.py','test-provider-worker-formal.py'):shutil.copyfile(ROOT/'scripts'/name,clone/'scripts'/name)
    original_module=load(SCRIPT)
    for name in original_module.MODEL_FILES:shutil.copyfile(ROOT/'formal/provider-worker'/name,clone/'formal/provider-worker'/name)
    actual_go=clone/'internal/owned.go';actual_go.write_text('package owned\n')
    m=load(clone/'scripts/test-provider-worker-formal.py');marker=self.path/'invoked';tool=self.fake_tool("open("+repr(str(marker))+",'w').write('invoked')\n")
    args=self.args(m,tool);args.work_dir=str(clone/'.tla/work');gate=m.Gate(args)
    original=clone/'formal/provider-worker/ProviderWorker.tla' if kind=='model' else actual_go
    original.write_text(original.read_text()+('\n\\* changed original\n' if kind=='model' else '\n// changed original\n'))
    with self.assertRaisesRegex(ValueError,'input/source snapshot changed'):gate.prove()
    self.assertFalse(marker.exists())
 def test_changed_tool_closure_refused_before_prover(self):
  config=self.fixture();marker=self.path/'invoked'
  tool=self.fake_tool("if '--version' in sys.argv: print('fixture')\nelif '--config' in sys.argv: print("+repr(config)+")\nelse: open("+repr(str(marker))+",'w').write('invoked')\n")
  m=load(SCRIPT);gate=m.Gate(self.args(m,tool));gate.tools()
  (self.path/'isabelle/heaps/TLA+').write_text('changed after tools captured')
  with self.assertRaisesRegex(ValueError,'tool/module closure changed'):gate.prove()
  self.assertFalse(marker.exists())
 def signal_case(self,number):
  config=self.fixture();ready=self.path/'ready.json'
  tool=self.fake_tool("if '--version' in sys.argv: print('fixture version')\nelif '--config' in sys.argv: print("+repr(config)+")\nelif '--summary' in sys.argv: print('obligations_count = 5')\nelse:\n child=subprocess.Popen([sys.executable,'-c','import time;time.sleep(120)'])\n open("+repr(str(ready))+",'w').write(json.dumps({'leader':os.getpid(),'child':child.pid,'pgid':os.getpgrp()}))\n time.sleep(120)\n")
  process=subprocess.Popen([sys.executable,str(SCRIPT),'--mode','prove','--tlapm',str(tool),'--work-dir',str(self.path/'signal-work')],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
  metadata=None
  try:
   until=time.monotonic()+10
   while not ready.exists() and time.monotonic()<until and process.poll() is None:time.sleep(.02)
   if not ready.exists():
    out,err=process.communicate(timeout=3);self.fail('owned fake prover did not start: '+out+err)
   metadata=json.loads(ready.read_text());self.assertTrue(running(metadata['leader']));self.assertTrue(running(metadata['child']))
   os.kill(process.pid,number);out,err=process.communicate(timeout=10)
   self.assertEqual(process.returncode,128+number,(out,err));self.assertFalse(running(metadata['leader']));self.assertFalse(running(metadata['child']))
   reports=list((self.path/'signal-work').glob('run-*/summary.json'));self.assertEqual(len(reports),1)
   report=json.loads(reports[0].read_text());self.assertEqual(report['result'],'failed')
   self.assertTrue(list(reports[0].parent.glob('prove-*.log')))
  finally:
   if process.poll() is None:
    os.kill(process.pid,signal.SIGTERM)
    try:process.communicate(timeout=3)
    except subprocess.TimeoutExpired:os.killpg(process.pid,signal.SIGKILL);process.communicate(timeout=3)
   if metadata and any(running(metadata[k]) for k in ('leader','child')):
    try:os.killpg(metadata['pgid'],signal.SIGKILL)
    except ProcessLookupError:pass
 def test_actual_cli_sigterm_cleans_descendants_and_reports(self):self.signal_case(signal.SIGTERM)
 def test_actual_cli_sigint_cleans_descendants_and_reports(self):self.signal_case(signal.SIGINT)

if __name__=='__main__':unittest.main()
