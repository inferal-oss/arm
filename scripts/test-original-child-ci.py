import hashlib,importlib.util,json,os,pathlib,re,sys,time,shutil,platform,subprocess
sys.dont_write_bytecode=True
root=pathlib.Path(__file__).resolve().parents[1]
run=root/'.ci-evidence/original-child'
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
env.update(GOMAXPROCS='1',GOCACHE=str(root/'.cache/original-child-go-build'),GOPROXY='off',GOSUMDB='off',GOTOOLCHAIN='local',GOENV='off',GOWORK='off',GOFLAGS='',CC=cc,CGO_ENABLED='1',ARM_WAITER_NATIVE='1',RUSTUP_AUTO_INSTALL='0')
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
expected={
 'github.com/yrashk/arm/internal/childowner':{'TestUnknownObservationRetiresWithoutSignalsOrWait','TestRetirementPrecedesSoleWaitAndDeniedSignalStaysFailure','TestNativeOriginalExitAndDescendantTeardown','TestNativeConcurrentCancellationCannotSignalAfterWait','TestRepeatedEINTRCannotBypassCancellationBound','TestNativeDeniedSignalCannotBecomeSuccessfulCleanupAfterReap'},
 'github.com/yrashk/arm/internal/manager':{'TestOriginalWaiterNativeAdmissionAndExpiry','TestSupervisedExpiryAndDescendants','TestExpiryQueueAndRenewal','TestSchedulingDeadlineJournalFailureCannotGrant'}}
for file in (root/'internal/childowner').glob('*test.go'):
 if file.name.endswith('_darwin_test.go') and sys.platform!='darwin':continue
 if file.name.endswith('_linux_test.go') and not sys.platform.startswith('linux'):continue
 expected['github.com/yrashk/arm/internal/childowner'].update(re.findall(r'^func (Test[A-Za-z0-9_]+)\(t \*testing.T\)',file.read_text(),re.M))
for file in (root/'internal/manager').glob('ssh*test.go'):
 expected['github.com/yrashk/arm/internal/manager'].update(re.findall(r'^func (Test[A-Za-z0-9_]+)\(t \*testing.T\)',file.read_text(),re.M))

try:
 resolved=json.loads(command.command([go,'env','-json','GOVERSION','GOROOT','GOOS','GOARCH','GOFLAGS','GOWORK','GOENV','GOMOD','CC','GOMODCACHE'],'resolved-go-env',env=env,timeout=30))
 if resolved['GOVERSION']!='go'+re.search(r'^go (\S+)$',(root/'go.mod').read_text(),re.M)[1] or resolved['GOARCH']!='arm64' or resolved['GOFLAGS']!='' or resolved['GOWORK']!='off' or resolved['GOENV']!='' or resolved['GOMOD']!=str(root/'go.mod'):raise ValueError('unexpected Go source selection')
 resource=command.command([env['CC'],'-print-resource-dir'],'cc-resource',env=env,timeout=30).strip()
 linker=command.command([env['CC'],'-print-prog-name=ld'],'cc-linker',env=env,timeout=30).strip()
 if not os.path.isabs(linker):linker=shutil.which(linker)
 closure_paths=[resolved['GOROOT'],resolved['GOMODCACHE'],env['CC'],resource,linker]
 if sdk:closure_paths.extend([str(pathlib.Path(sdk)/'usr/include'),str(pathlib.Path(sdk)/'usr/lib')])
 (run/'selected-cgo.json').write_text(json.dumps({k:env.get(k) for k in ('CC','SDKROOT','CGO_CFLAGS','CGO_LDFLAGS')},indent=2)+'\n')
 selected=closure(closure_paths);(run/'tool-dependency-closure-before.json').write_text(json.dumps(selected,sort_keys=True,indent=2)+'\n')
 pattern='^('+ '|'.join(sorted(set.union(*expected.values()))) +')$'
 raw=command.command([go,'test','-p=1','-json','-race','-count=1','-timeout=180s','-run',pattern,'./internal/childowner','./internal/manager'],'native-race',env=env,timeout=360)
 ran={k:[] for k in expected};passed={k:[] for k in expected};packages=[]
 for line in raw.splitlines():
  e=json.loads(line);pkg=e['Package'];action=e['Action']
  if pkg not in expected or action in ('fail','skip','build-fail'):raise ValueError('failed/skipped/foreign native test')
  if 'Test' in e:
   if e['Test'].split('/')[0] not in expected[pkg]:raise ValueError('unexpected native test')
   if action=='run' and '/' not in e['Test']:ran[pkg].append(e['Test'])
   if action=='pass' and '/' not in e['Test']:passed[pkg].append(e['Test'])
  elif action=='pass':packages.append(pkg)
 for pkg,names in expected.items():
  if set(ran[pkg])!=names or set(passed[pkg])!=names or len(ran[pkg])!=len(names) or len(passed[pkg])!=len(names) or packages.count(pkg)!=1:raise ValueError('incomplete exact native roster')
 command.command([go,'vet','-p=1','./internal/childowner','./internal/manager'],'vet',env=env,timeout=120)
 after=sources();assert_committed(after)
 (run/'sources-after.json').write_text(json.dumps(after,sort_keys=True,indent=2)+'\n')
 final_selected=closure(closure_paths);(run/'tool-dependency-closure-after.json').write_text(json.dumps(final_selected,sort_keys=True,indent=2)+'\n')
 if selected!=final_selected:raise ValueError('selected Go/compiler/dependency closure changed')
 if before!=after:raise ValueError('source changed during native commands')
 (run/'result.json').write_text(json.dumps({'status':'passed_scoped_native_and_vet','roster':passed,'go_env':resolved,'sources_sha256':hashlib.sha256(json.dumps(before,sort_keys=True).encode()).hexdigest()},indent=2)+'\n')
 if any(hashlib.sha256(pathlib.Path(p).read_bytes()).hexdigest()!=h for p,h in tools.items()):raise ValueError('selected tool changed during commands')
 print('passed exact native roster and vet',flush=True)
except BaseException as e:
 (run/'result.json').write_text(json.dumps({'status':'failed','error':str(e),'error_type':type(e).__name__})+'\n')
 raise
