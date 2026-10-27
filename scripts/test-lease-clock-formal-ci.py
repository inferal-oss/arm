import hashlib,importlib.util,json,os,pathlib,re,sys,time,shutil,platform,subprocess
sys.dont_write_bytecode=True
root=pathlib.Path(__file__).resolve().parents[1]
run=root/'.tla/clock-formal-wrapper'
run.mkdir(mode=0o700,parents=True,exist_ok=False)
(run/'dispatch.json').write_text(json.dumps({'pid':os.getpid(),'root':str(root),'commit':subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),'uname':platform.uname()._asdict(),'image':{k:os.environ.get(k) for k in ('ImageOS','ImageVersion','RUNNER_OS','RUNNER_ARCH')}})+'\n')
p=root/'scripts/formal_process.py';spec=importlib.util.spec_from_file_location('wait_owner_native_commands',p);module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
command=module.Command(run,root)
go=str(pathlib.Path(shutil.which('go')).resolve())
env=dict(os.environ)
for k in ('GOFLAGS','GOWORK','GOENV','GOTOOLCHAIN','GOEXPERIMENT','GOOS','GOARCH','GODEBUG','CGO_CFLAGS','CGO_CPPFLAGS','CGO_CXXFLAGS','CGO_LDFLAGS','GOTMPDIR','GOVCS','GO111MODULE','GOROOT','GOTOOLDIR','AR','CC','CXX'):
 env.pop(k,None)
cc=shutil.which('clang') or shutil.which('cc')
sdk=None
if sys.platform=='darwin':
 cc=subprocess.check_output(['xcrun','--find','clang'],text=True).strip()
 sdk=subprocess.check_output(['xcrun','--show-sdk-path'],text=True).strip()
env.update(GOMAXPROCS='1',GOCACHE=str(root/'.cache/original-child-go-build'),GOPROXY='off',GOSUMDB='off',GOTOOLCHAIN='local',GOENV='off',GOWORK='off',GOFLAGS='',CC=cc,CGO_ENABLED='1',ARM_WAITER_NATIVE='1')
if sdk:env.update(SDKROOT=sdk,CGO_CFLAGS='-isysroot '+sdk,CGO_LDFLAGS='-isysroot '+sdk)
def sources():
 result={}
 for p in root.rglob('*'):
  if p.relative_to(root).parts[0] in ('.git','.cache','.tools','.tla','.ci-evidence','bin','dist'):continue
  if p.is_symlink():data=os.readlink(p).encode()
  elif p.is_file():data=p.read_bytes()
  else:continue
  result[str(p.relative_to(root))]=hashlib.sha256(data).hexdigest()
 return result
def assert_committed(source):
 subprocess.run(['git','diff','--quiet','HEAD','--'],check=True)
 head=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()
 if head!=os.environ['GITHUB_SHA']:raise ValueError('checkout differs from dispatched commit')
 for name,digest in source.items():
  blob=subprocess.check_output(['git','show',head+':'+name])
  if hashlib.sha256(blob).hexdigest()!=digest:raise ValueError('source differs from commit: '+name)
def closure(paths):
 result={}
 for value in paths:
  base=pathlib.Path(value)
  for p in ([base]+sorted(base.rglob('*')) if base.is_dir() else [base]):
   if p.is_symlink():result[str(p)+':link']=os.readlink(p)
   if p.is_file():result[str(p)]=hashlib.sha256(p.read_bytes()).hexdigest()
 return result
tools={go:hashlib.sha256(pathlib.Path(go).read_bytes()).hexdigest(),env['CC']:hashlib.sha256(pathlib.Path(env['CC']).read_bytes()).hexdigest()}
(run/'tools.json').write_text(json.dumps(tools,indent=2)+'\n')
before=sources();assert_committed(before);(run/'sources-before.json').write_text(json.dumps(before,sort_keys=True,indent=2)+'\n')
try:
 resolved=json.loads(command.command([go,'env','-json','GOROOT','GOMODCACHE'],'resolved-go-env',env=env,timeout=30))
 resource=command.command([env['CC'],'-print-resource-dir'],'cc-resource',env=env,timeout=30).strip()
 linker=command.command([env['CC'],'-print-prog-name=ld'],'cc-linker',env=env,timeout=30).strip()
 if not os.path.isabs(linker):linker=shutil.which(linker)
 inner_cc=str(pathlib.Path(shutil.which('cc')).resolve())
 inner_cxx=str(pathlib.Path(shutil.which('c++')).resolve())
 inner_resource=command.command([inner_cc,'-print-resource-dir'],'inner-cc-resource',env=env,timeout=30).strip()
 inner_linker=command.command([inner_cc,'-print-prog-name=ld'],'inner-cc-linker',env=env,timeout=30).strip()
 if not os.path.isabs(inner_linker):inner_linker=shutil.which(inner_linker)
 paths=[resolved['GOROOT'],resolved['GOMODCACHE'],env['CC'],resource,linker,inner_cc,inner_cxx,inner_resource,inner_linker]
 if sdk:paths.append(subprocess.check_output(['xcrun','--find','clang++'],text=True).strip())
 if sdk:paths.extend([str(pathlib.Path(sdk)/'usr/include'),str(pathlib.Path(sdk)/'usr/lib')])
 selected=closure(paths);(run/'closure-before.json').write_text(json.dumps(selected,sort_keys=True,indent=2)+'\n')
 java=str(pathlib.Path(shutil.which('java')).resolve())
 for mode in ('check','traces'):
  command.command([sys.executable,str(root/'scripts/test-lease-clock-formal.py'),'--mode',mode,'--go',go,'--java',java,'--tlc-jar',str(root/'.tla/tools/tlc/tla2tools.jar'),'--work-dir',str(root/'.tla/lease-clock')],'formal-'+mode,env=env,timeout=900)
 summaries=list((root/'.tla/lease-clock').glob('run-*/summary.json'))
 if len(summaries)!=2:raise ValueError('exact check and trace summaries required')
 summaries=[json.loads(p.read_text()) for p in summaries]
 if {v['mode'] for v in summaries}!={'check','traces'} or any(v['status']!='passed' for v in summaries):raise ValueError('incomplete formal component modes')
 summary=next(v for v in summaries if v['mode']=='traces')
 if summary['go_compile_environment']['CC']!=inner_cc or summary['go_compile_environment']['CXX']!=inner_cxx:raise ValueError('inner formal compiler selection differs')
 after=sources();assert_committed(after)
 (run/'sources-after.json').write_text(json.dumps(after,sort_keys=True,indent=2)+'\n')
 final=closure(paths);(run/'closure-after.json').write_text(json.dumps(final,sort_keys=True,indent=2)+'\n')
 if before!=after or selected!=final:raise ValueError('source or selected dependency closure changed')
 (run/'result.json').write_text(json.dumps({'status':'passed_tlc_and_go_components_strict_proof_pending','commit':os.environ['GITHUB_SHA']})+'\n')
except BaseException as e:
 (run/'result.json').write_text(json.dumps({'status':'failed','error':str(e),'type':type(e).__name__})+'\n')
 raise
