import hashlib,importlib.util,json,os,pathlib,re,sys,time,shutil,platform,subprocess
sys.dont_write_bytecode=True
root=pathlib.Path(__file__).resolve().parents[1]
run=root/'.ci-evidence/generic-current'
run.mkdir(mode=0o700,parents=True,exist_ok=False)
(run/'dispatch.json').write_text(json.dumps({'pid':os.getpid(),'root':str(root),'commit':subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),'uname':platform.uname()._asdict(),'image':{k:os.environ.get(k) for k in ('ImageOS','ImageVersion','RUNNER_OS','RUNNER_ARCH')}})+'\n')
p=root/'scripts/formal_process.py';spec=importlib.util.spec_from_file_location('generic_current_native_commands',p);module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
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
env.update(GOMAXPROCS='1',GOCACHE=str(root/'.cache/generic-current-go-build'),GOPROXY='off',GOSUMDB='off',GOTOOLCHAIN='local',GOENV='off',GOWORK='off',GOFLAGS='',CC=cc,CGO_ENABLED='1',ARM_WAITER_NATIVE='1',RUSTUP_AUTO_INSTALL='0')
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
assert not (root/'internal/leaseclock').exists(), 'clock dependency outside compatibility scope'
if re.search(r'\bClock\s+\*', (root/'internal/model/model.go').read_text()):raise ValueError('lease Clock model outside compatibility scope')
before=sources();assert_committed(before);(run/'sources-before.json').write_text(json.dumps(before,sort_keys=True,indent=2)+'\n')
expected={
 'github.com/inferal-oss/arm/internal/syncworker':{
  'TestManagerOwnedSyncPairOverAuthenticatedSSH',
  'TestManagerPairTeardownRequiresCompleteJoinedProtocol',
  'TestNativeManagerSyncWorkerCancellation',
  'TestNativeManagerSyncWorkerDirections',
 },
 'github.com/inferal-oss/arm/internal/manager':{
  'TestCargoCancelAfterCompileGrantBeforeStartedReceiptWaitsForGroup',
  'TestCargoExpiryAfterAuthorizationBeforeStartedReceiptWaitsForVerifiedGroupAbsence',
  'TestCargoOriginalCancellationPrecedesIPCClose',
  'TestSSHCommittedProcessWitnessesTransactionBeforeExec',
  'TestSSHCommittedSealBlocksActualDelayedExec',
  'TestSSHEndingJournalFailureCancelsOriginalGroup',
  'TestPolicyMemoryLowerBoundAndUnknownMetric',
  'TestSSHFailedNativeGateKeepsChargeUntilOriginalInputJoin',
  'TestSSHManagerCancellationAndExpiryEndOwnedProcess',
  'TestSSHManagerParentCompletionReapsOrdinaryDescendants',
  'TestSSHManagerRestartNeverReexecutesRedactedRequest',
  'TestSSHManagerSettledProofAndCorruptRelationships',
  'TestSSHNativeExitKeepsChargeUntilOriginalInputJoin',
  'TestSSHRealCanceledSessionRetainsRunningNativeRuntimeCleanup',
  'TestSSHRealFailedLaunchJoinsUnpublishedRuntime',
  'TestSSHRealSessionNormalExitJoinsInputCopyWithoutClientEOF',
  'TestSSHReconcileCannotBypassHeldOrFailedSettlement',
  'TestSSHRecoveredDescriptorCannotSettleWithoutOriginalJoin',
  'TestSSHStagingRejectsChangedNativeBinding',
 },
 'github.com/inferal-oss/arm/internal/database':{
  'TestSSHCompletionProofNestedUnknownFieldsStayClosed',
  'TestSSHCompletionProofRejectsForgedTerminalAliases',
  'TestSSHNamespaceRetirementAndPendingProofAreReadOnly',
  'TestSSHNamespaceStrictMirrorCannotHideUnknown',
  'TestSSHProofProducerShapesRequireIndependentReview',
  'TestSSHProofLegacyUsageWithoutProportionalResidentFields',
  'TestSSHSettledProofSeparatesAuthorityClocks',
  'TestSyncResetProofRetainsUsedOrPartialNamespace',
  'TestWorkspaceReviewResetSchemaVersionAndLocatorRetention',
 },
 'github.com/inferal-oss/arm/internal/sshservice':{
  'TestOriginalExecutionTransportClosesStalledReadAndCannotBeBorrowed',
  'TestSSHServeJoinsCancellationAtExecutionCuts',
  'TestSSHStartTimerJoinsAlreadyRunningCloseCallback',
 },
}

# The negative input changes only the exact reviewed ending-transaction hunk.
# Production sources stay committed and are never rewritten by this phase.
negative_fixed_hunk = """	if j := m.jobs[id]; j != nil {
		// This exact process-local owner must receive cancellation even when
		// recording ending fails. Request original group control before closing
		// SSH IO: EOF/SIGPIPE must not win the original parent's signal race.
		// A refused transaction leaves the prior durable charge untouched.
		defer func() {
			_ = j.signalGroup(syscall.SIGTERM)
			if j.sshRuntime != nil {
				j.sshRuntime.cancel()
			}
			m.wg.Go(func() {
				select {
				case <-j.done:
					return
				case <-time.After(2 * time.Second):
				}
				m.mu.Lock()
				defer m.mu.Unlock()
				if m.jobs[id] == j {
					_ = j.signalGroup(syscall.SIGKILL)
				}
			})
		}()
		l.State = "ending"
		if e = m.put(l, "lease.ending"); e != nil {
			return l, e
		}
		return l, nil
	}
"""
negative_previous_hunk = """	if j := m.jobs[id]; j != nil {
		l.State = "ending"
		if e = m.put(l, "lease.ending"); e != nil {
			return l, e
		}
		if j.sshRuntime != nil {
			j.sshRuntime.cancel()
		}
		_ = j.signalGroup(syscall.SIGTERM)
		m.wg.Go(func() {
			select {
			case <-j.done:
				return
			case <-time.After(2 * time.Second):
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.jobs[id] == j {
				_ = j.signalGroup(syscall.SIGKILL)
			}
		})
		return l, nil
	}
"""
negative_previous_sha256 = '8aee5059451fbbe50b6fcb6fa578952044fb156d453ee504da0ac7275f79406b'
def negative_ending_gate():
 original=(root/'internal/manager/manager.go').read_bytes()
 fixed=negative_fixed_hunk.encode();previous=negative_previous_hunk.encode()
 if original.count(fixed)!=1:raise ValueError('reviewed ending hunk changed')
 generated=original.replace(fixed,previous,1)
 if hashlib.sha256(generated).hexdigest()!=negative_previous_sha256:raise ValueError('negative input differs beyond pre-fix ending hunk')
 inputs=run/'negative-inputs';inputs.mkdir(mode=0o700)
 source=inputs/'manager.go';source.write_bytes(generated)
 overlay=inputs/'overlay.json';overlay.write_text(json.dumps({'Replace':{str(root/'internal/manager/manager.go'):str(source)}},sort_keys=True)+'\n')
 source_hash=hashlib.sha256(generated).hexdigest();overlay_hash=hashlib.sha256(overlay.read_bytes()).hexdigest()
 initial=sources();assert_committed(initial)
 (run/'sources-before-negative.json').write_text(json.dumps(initial,sort_keys=True,indent=2)+'\n')
 name='TestSSHEndingJournalFailureCancelsOriginalGroup';pkg='github.com/inferal-oss/arm/internal/manager'
 raw=command.command([go,'test','-overlay',str(overlay),'-p=1','-json','-race','-count=1','-timeout=60s','-run','^'+name+'$/^end$','./internal/manager'],'ending-negative',expected=1,env=env,timeout=360)
 runs=[];fails=[];package_fails=0;diagnostics=[]
 for line in raw.splitlines():
  event=json.loads(line);action=event['Action'];test=event.get('Test')
  if event['Package']!=pkg or action in ('pass','skip','build-fail'):raise ValueError('negative phase had foreign/passed/skipped/build-failed event')
  if test is not None:
   if test not in (name,name+'/end'):raise ValueError('negative phase ran a foreign test')
   if action=='run':runs.append(test)
   if action=='fail':fails.append(test)
  elif action=='fail':package_fails+=1
  if action=='output':
   output=event.get('Output','')
   if re.search(r'\.go:\d+:',output):diagnostics.append((test,output.strip()))
   if re.search(r'panic:|fatal error:|WARNING: DATA RACE|test timed out',output):raise ValueError('negative phase failed outside causal oracle')
 if sorted(runs)!=[name,name+'/end'] or sorted(fails)!=[name,name+'/end'] or package_fails!=1:raise ValueError('negative phase exact failure roster differs')
 if len(diagnostics)!=1 or diagnostics[0][0]!=name+'/end' or not re.fullmatch(r'[^ :]+\.go:\d+: ending refusal did not cancel original SSH IO',diagnostics[0][1]):raise ValueError('negative phase lacked sole expected cancellation failure')
 receipt=json.loads((run/'ending-negative-owner.json').read_text());owner=receipt['owner']
 if receipt['actual_exit']!=1 or receipt['cleanup_error'] is not None or receipt['cancellation_received'] is not None or not owner['retired'] or not owner['reaped'] or owner['authority'] or owner['unknown'] is not None or owner['native_wait_calls']!=1 or owner['signals'] or owner['post_wait_absence']!={'pid':'absent','group':'absent'}:raise ValueError('negative Go command did not join cleanly through its sole original Wait')
 final=sources();assert_committed(final)
 (run/'sources-after-negative.json').write_text(json.dumps(final,sort_keys=True,indent=2)+'\n')
 if initial!=final or original!=(root/'internal/manager/manager.go').read_bytes() or source_hash!=hashlib.sha256(source.read_bytes()).hexdigest() or overlay_hash!=hashlib.sha256(overlay.read_bytes()).hexdigest():raise ValueError('negative phase mutated original or generated source')
 (run/'ending-negative-result.json').write_text(json.dumps({'status':'passed_expected_causal_failure','expected_go_exit':1,'sole_failure':'ending refusal did not cancel original SSH IO','original_manager_sha256':hashlib.sha256(original).hexdigest(),'generated_manager_sha256':source_hash,'overlay_sha256':overlay_hash,'only_change':'exact pre-fix Manager.end job cancellation hunk','tracked_sources_unchanged':True,'native_wait_calls':1,'post_wait_absence':owner['post_wait_absence']},sort_keys=True,indent=2)+'\n')

try:
 resolved=json.loads(command.command([go,'env','-json','GOVERSION','GOROOT','GOOS','GOARCH','GOFLAGS','GOWORK','GOENV','GOMOD','CC','GOMODCACHE'],'resolved-go-env',env=env,timeout=30))
 if resolved['GOVERSION']!='go'+re.search(r'^go (\S+)$',(root/'go.mod').read_text(),re.M)[1] or resolved['GOARCH']!='arm64' or resolved['GOFLAGS']!='' or resolved['GOWORK']!='off' or resolved['GOENV']!='' or resolved['GOMOD']!=str(root/'go.mod'):raise ValueError('unexpected Go source selection')
 resource=command.command([env['CC'],'-print-resource-dir'],'cc-resource',env=env,timeout=30).strip()
 linker=command.command([env['CC'],'-print-prog-name=ld'],'cc-linker',env=env,timeout=30).strip()
 if not os.path.isabs(linker):linker=shutil.which(linker)
 closure_paths=[resolved['GOROOT'],resolved['GOMODCACHE'],env['CC'],resource,linker]
 if sdk:closure_paths.extend([str(pathlib.Path(sdk)/'usr/include'),str(pathlib.Path(sdk)/'usr/lib')])
 rustup=shutil.which('rustup')
 if not rustup:raise ValueError('preinstalled Rust toolchain required; no installation permitted')
 cargo=pathlib.Path(command.command([rustup,'which','cargo'],'native-cargo-path',env=env,timeout=30).strip()).resolve(strict=True)
 rustc=pathlib.Path(command.command([rustup,'which','rustc'],'native-rustc-path',env=env,timeout=30).strip()).resolve(strict=True)
 rustdoc=pathlib.Path(command.command([rustup,'which','rustdoc'],'native-rustdoc-path',env=env,timeout=30).strip()).resolve(strict=True)
 if cargo.parent!=rustc.parent or cargo.parent!=rustdoc.parent:raise ValueError('Rust tools must share one preinstalled toolchain')
 toolchain=cargo.parents[1]
 env['ARM_TEST_TOOLCHAIN_BIN']=str(cargo.parent)
 env['ARM_TEST_TOOL_LIBRARY_PATH']=str(toolchain/'lib')
 closure_paths.extend([rustup,str(toolchain)])
 (run/'selected-rust.json').write_text(json.dumps({'cargo':str(cargo),'rustc':str(rustc),'rustdoc':str(rustdoc),'toolchain':str(toolchain),'auto_install':'0'},indent=2)+'\n')
 (run/'selected-cgo.json').write_text(json.dumps({k:env.get(k) for k in ('CC','SDKROOT','CGO_CFLAGS','CGO_LDFLAGS')},indent=2)+'\n')
 selected=closure(closure_paths);(run/'tool-dependency-closure-before.json').write_text(json.dumps(selected,sort_keys=True,indent=2)+'\n')
 negative_ending_gate()
 pattern='^('+ '|'.join(sorted(set.union(*expected.values()))) +')$'
 raw=command.command([go,'test','-p=1','-json','-race','-count=1','-timeout=300s','-run',pattern,*['./internal/'+pkg.rsplit('/',1)[1] for pkg in expected]],'native-race',env=env,timeout=720)
 ran={k:[] for k in expected};passed={k:[] for k in expected};packages=[];observed_run={};observed_pass={}
 for line in raw.splitlines():
  e=json.loads(line);pkg=e['Package'];action=e['Action']
  if pkg not in expected or action in ('fail','skip','build-fail'):raise ValueError('failed/skipped/foreign native test')
  if 'Test' in e:
   if e['Test'].split('/')[0] not in expected[pkg]:raise ValueError('unexpected native test')
   if action=='run':observed_run[(pkg,e['Test'])]=observed_run.get((pkg,e['Test']),0)+1
   if action=='pass':observed_pass[(pkg,e['Test'])]=observed_pass.get((pkg,e['Test']),0)+1
   if action=='run' and '/' not in e['Test']:ran[pkg].append(e['Test'])
   if action=='pass' and '/' not in e['Test']:passed[pkg].append(e['Test'])
  elif action=='pass':packages.append(pkg)
 if observed_run!=observed_pass or any(n!=1 for n in observed_run.values()):raise ValueError('observed test/subtest completion differs')
 for subtest in ('end','close'):
  if observed_run.get(('github.com/inferal-oss/arm/internal/manager','TestSSHEndingJournalFailureCancelsOriginalGroup/'+subtest))!=1:raise ValueError('positive ending failure regression omitted required subtest')
 for pkg,names in expected.items():
  if set(ran[pkg])!=names or set(passed[pkg])!=names or len(ran[pkg])!=len(names) or len(passed[pkg])!=len(names) or packages.count(pkg)!=1:raise ValueError('incomplete exact native roster')
 command.command([go,'vet','-p=1',*['./internal/'+pkg.rsplit('/',1)[1] for pkg in expected]],'vet',env=env,timeout=240)
 after=sources();assert_committed(after)
 (run/'sources-after.json').write_text(json.dumps(after,sort_keys=True,indent=2)+'\n')
 final_selected=closure(closure_paths);(run/'tool-dependency-closure-after.json').write_text(json.dumps(final_selected,sort_keys=True,indent=2)+'\n')
 if selected!=final_selected:raise ValueError('selected Go/compiler/dependency closure changed')
 if before!=after:raise ValueError('source changed during native commands')
 (run/'result.json').write_text(json.dumps({'status':'passed_focused_current_compatibility_and_vet','roster':passed,'go_env':resolved,'sources_sha256':hashlib.sha256(json.dumps(before,sort_keys=True).encode()).hexdigest()},indent=2)+'\n')
 if any(hashlib.sha256(pathlib.Path(p).read_bytes()).hexdigest()!=h for p,h in tools.items()):raise ValueError('selected tool changed during commands')
 print('passed exact current compatibility roster and vet',flush=True)
except BaseException as e:
 (run/'result.json').write_text(json.dumps({'status':'failed','error':str(e),'error_type':type(e).__name__})+'\n')
 raise
