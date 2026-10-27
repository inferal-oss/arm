import hashlib,importlib.util,json,os,pathlib,re,sys,time,shutil,platform,subprocess
sys.dont_write_bytecode=True
root=pathlib.Path(__file__).resolve().parents[1]
run=root/'.tla/clock-native'
run.mkdir(mode=0o700,parents=True,exist_ok=False)
(run/'dispatch.json').write_text(json.dumps({'pid':os.getpid(),'root':str(root),'commit':subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),'uname':platform.uname()._asdict(),'image':{k:os.environ.get(k) for k in ('ImageOS','ImageVersion','RUNNER_OS','RUNNER_ARCH')}})+'\n')
if sys.platform.startswith('linux'):
 status=dict(line.split(':',1) for line in pathlib.Path('/proc/self/status').read_text().splitlines())
 scope={'pid':os.getpid(),'uid':os.getuid(),'euid':os.geteuid(),'gid':os.getgid(),'egid':os.getegid(),'pid_namespace':os.stat('/proc/self/ns/pid').st_ino,'mount_namespace':os.stat('/proc/self/ns/mnt').st_ino,'init_command':pathlib.Path('/proc/1/comm').read_text().strip(),'capabilities':{k:status[k].strip() for k in ('CapInh','CapPrm','CapEff','CapBnd','CapAmb','NoNewPrivs')}}
 (run/'linux-namespace.json').write_text(json.dumps(scope,indent=2)+'\n')
 if scope['pid']<=1 or scope['init_command']!='bash' or scope['pid_namespace']==int(os.environ['ARM_CI_PARENT_PIDNS']) or scope['mount_namespace']==int(os.environ['ARM_CI_PARENT_MOUNTNS']):raise ValueError('private ephemeral PID/mount fixture required')
 if scope['uid']!=int(os.environ['ARM_CI_EXPECT_UID']) or scope['euid']!=scope['uid'] or scope['gid']!=int(os.environ['ARM_CI_EXPECT_GID']) or scope['egid']!=scope['gid']:raise ValueError('fixture did not restore original user identity')
 if any(int(scope['capabilities'][k],16)!=0 for k in ('CapInh','CapPrm','CapEff','CapBnd','CapAmb')) or scope['capabilities']['NoNewPrivs']!='1':raise ValueError('fixture must drop native privileges')
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
roster_files={'leaseclock': ['clock_test.go'], 'childowner': ['owner_test.go'], 'manager': ['lease_clock_test.go', 'job_control_native_test.go', 'ssh_transport_join_test.go', 'ssh_settlement_test.go', 'sync_preparation_test.go', 'forward_listener_test.go', 'forward_listener_recovery_test.go', 'cargo_lifecycle_test.go', 'cargo_cancel_native_test.go', 'gradle_evidence_test.go', 'lease_reviews_startup_test.go', 'storage_export_test.go', 'relay_transit_test.go', 'relay_cleanup_test.go', 'sync_source_hold_test.go', 'sync_worker_hold_review_test.go'], 'cargoprofile': ['helper_protocol_test.go', 'helper_real_test.go'], 'meshsshrelay': ['forward_join_review_test.go', 'policy_test.go', 'policy_store_test.go', 'request_test.go', 'route_test.go', 'pace_test.go', 'forward_route_test.go', 'afterfunc_test.go', 'binding_test.go', 'pace_pool_test.go', 'reservation_context_test.go', 'carrier_test.go'], 'database': ['proof_workspace_recovery_reconcile_test.go', 'proof_coordination_test.go', 'proof_schema_diagnostic_review_test.go', 'proof_ssh_namespaces_test.go', 'proof_review_test.go', 'proof_subpools_test.go', 'proof_routing_test.go', 'proof_lease_reviews_cursor_test.go', 'proof_workspace_recovery_test.go', 'proof_provision_seed_test.go', 'proof_workspace_keys_test.go', 'proof_published_test.go', 'proof_ssh_fences_test.go', 'proof_pool_review_test.go', 'proof_sync_integration_test.go', 'proof_workspaces_test.go', 'proof_namespaces_test.go', 'proof_routing_probes_test.go', 'proof_reconnect_test.go', 'proof_integration_test.go', 'proof_routing_graph_test.go', 'proof_provision_test.go', 'proof_registry_test.go', 'proof_cargo_shapes_test.go', 'proof_ssh_history_test.go', 'proof_ssh_reset_test.go', 'proof_schema_test.go', 'proof_ssh_shapes_test.go', 'proof_coordination_writer_test.go', 'proof_json_review_test.go', 'proof_json_test.go']}
roster_files['tui']=['lease_clock_renewal_test.go','duration_parser_test.go','dashboard_test.go','lifecycle_test.go']
roster_files['childowner']=[p.name for p in (root/'internal/childowner').glob('*test.go') if not (p.name.endswith('_darwin_test.go') and sys.platform!='darwin') and not (p.name.endswith('_linux_test.go') and not sys.platform.startswith('linux'))]
roster_files['manager']=sorted(set(roster_files['manager'])|{p.name for p in (root/'internal/manager').glob('ssh*test.go')})
if sys.platform.startswith('linux'):roster_files['leaseclock'].append('stop_linux_test.go')
expected={}
for package,names in roster_files.items():
 tests=set()
 for name in names:
  tests.update(re.findall(r'^func (Test[A-Za-z0-9_]+)\(t \*testing.T\)',(root/'internal'/package/name).read_text(),re.M))
 if not tests:raise ValueError('empty declared test roster: '+package)
 expected['github.com/inferal-oss/arm/internal/'+package]=tests
(run/'expected-roster.json').write_text(json.dumps({k:sorted(v) for k,v in expected.items()},indent=2)+'\n')
try:
 resolved=json.loads(command.command([go,'env','-json','GOVERSION','GOROOT','GOOS','GOARCH','GOFLAGS','GOWORK','GOENV','GOMOD','CC','GOMODCACHE'],'resolved-go-env',env=env,timeout=30))
 if resolved['GOVERSION']!='go'+re.search(r'^go (\S+)$',(root/'go.mod').read_text(),re.M)[1] or resolved['GOARCH']!='arm64' or resolved['GOFLAGS']!='' or resolved['GOWORK']!='off' or resolved['GOENV']!='' or resolved['GOMOD']!=str(root/'go.mod'):raise ValueError('unexpected Go source selection')
 resource=command.command([env['CC'],'-print-resource-dir'],'cc-resource',env=env,timeout=30).strip()
 linker=command.command([env['CC'],'-print-prog-name=ld'],'cc-linker',env=env,timeout=30).strip()
 if not os.path.isabs(linker):linker=shutil.which(linker)
 closure_paths=[resolved['GOROOT'],resolved['GOMODCACHE'],env['CC'],resource,linker]
 if sys.platform.startswith('linux'):closure_paths.extend([shutil.which(v) for v in ('sudo','unshare','setpriv','bash')])
 if sdk:closure_paths.extend([str(pathlib.Path(sdk)/'usr/include'),str(pathlib.Path(sdk)/'usr/lib')])
 (run/'selected-cgo.json').write_text(json.dumps({k:env.get(k) for k in ('CC','SDKROOT','CGO_CFLAGS','CGO_LDFLAGS')},indent=2)+'\n')
 selected=closure(closure_paths);(run/'tool-dependency-closure-before.json').write_text(json.dumps(selected,sort_keys=True,indent=2)+'\n')
 rustup=shutil.which('rustup')
 if not rustup:raise ValueError('preinstalled native Rust toolchain required')
 cargo=command.command([rustup,'which','cargo'],'native-cargo-path',env=env,timeout=30).strip()
 env['ARM_TEST_TOOLCHAIN_BIN']=str(pathlib.Path(cargo).parent)
 closure_paths.append(str(pathlib.Path(cargo).parents[1]))
 selected=closure(closure_paths);(run/'tool-dependency-closure-before.json').write_text(json.dumps(selected,sort_keys=True,indent=2)+'\n')
 command.command([go,'test','-p=1','-race','-run','^$','./internal/manager','./internal/database','./internal/meshsshrelay','./internal/tui'],'affected-compile',env=env,timeout=360)
 pattern='^('+ '|'.join(sorted(set.union(*expected.values()))) +')$'
 raw=command.command([go,'test','-p=1','-json','-race','-count=1','-timeout=600s','-run',pattern,*['./internal/'+k.rsplit('/',1)[1] for k in expected]],'native-race',env=env,timeout=900)
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
 for pkg,names in expected.items():
  if set(ran[pkg])!=names or set(passed[pkg])!=names or len(ran[pkg])!=len(names) or len(passed[pkg])!=len(names) or packages.count(pkg)!=1:raise ValueError('incomplete exact native roster')
 command.command([go,'test','-p=1','-run','^$','./...'],'full-compile',env=env,timeout=600)
 command.command([go,'vet','-p=1',*['./internal/'+k.rsplit('/',1)[1] for k in expected]],'vet',env=env,timeout=180)
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
