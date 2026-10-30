// SOURCE PREPARATION ONLY. Separate review/browser allocation required.
// Replays actual owned Program PTY bytes, never model text as screen data.
import assert from 'node:assert/strict';
import {readFile,writeFile,mkdir,readdir,stat,lstat,realpath} from 'node:fs/promises';
import {writeFileSync} from 'node:fs';
import {resolve,dirname,join,relative,sep} from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
import {chromium} from 'playwright';
import {performance} from 'node:perf_hooks';

const now=()=>performance.now();
const started=now(),workDeadline=started+20000,totalDeadline=started+30000;
const ownSource=fileURLToPath(import.meta.url),here=dirname(ownSource);
const root=resolve(process.argv[2]||''),output=resolve(process.argv[4]||'');
const retainedRoots=[6,8,10,12,14,16,18].map(i=>resolve(process.argv[i]||'')),repo=resolve(here,'../..');
const outsideOf=(parent,path)=>{const rel=relative(parent,path);return rel==='..'||rel.startsWith('..'+sep);};
const normalizeRow=text=>text.replace(/\u0020+$/u,'');
const result={complete:false,failure:null,cases:[],operations:[],cleanup:{},
 retainedBudget:{limitBytes:16777216,scope:'All seven immutable retained input roots plus fresh native capture/output; future wrapper metadata also shares this cap.'},
 rowComparison:{normalization:'Trailing ASCII U+0020 only, applied equally to expected and actual comparison text.',rawExpected:'cases[].expectedRawRows',rawActual:'cases[].actual.text',perRowResults:'cases[].rowMatches',qualification:'Leading/internal spaces and every other Unicode whitespace or character remain significant.'},
 limits:['Pinned xterm emulator of actual owned PTY bytes; not every physical terminal/font/platform.',
 'Original model row/text comparator is separate from actual emulator cells. No OS descendant-reap or production Run startup/auth claim.',
 'Private loopback Playwright CONTROL connection only; its endpoint is never recorded. Page/native-app networking is refused.',
 'Original promise and process-exit observations qualify cleanup; a timeout/forced request is not a returned API or universal OS reap.']};
let stage='output validation',cleanupStarted=false,outputCreated=false,deadlineReceiptAllowed=false;
let server,browser,context,page,originalProcess,exitPromise;
const closes=new Map();
const elapsed=()=>Math.round(now()-started);
const errorName=error=>error instanceof Error?error.name:'UnknownError'; // Never retain endpoint-bearing error text.
const processState=result.cleanup.originalProcess={handleRetained:false,exitObserved:false,observation:'not-created'};
function begin(name,phase,fn,adopt){
 const record={name,phase,startedMS:elapsed(),originalStatus:'pending',waitTimedOut:false};
 result.operations.push(record);
 let original;try{original=fn();}catch(error){original=Promise.reject(error);}
 const promise=Promise.resolve(original).then(value=>{
  record.originalStatus='returned';record.settledMS=elapsed();adopt?.(value);return value;
 },error=>{record.originalStatus='rejected';record.settledMS=elapsed();record.errorName=errorName(error);throw error;});
 promise.catch(()=>{}); // The original late outcome stays recorded after a bounded wait.
 return {record,promise};
}
async function waitFor(op,cap,deadline){
 const ms=Math.min(cap,deadline-now());
 if(ms<=0){op.record.waitTimedOut=true;throw Error('owned deadline');}
 let timer;
 try{return await Promise.race([op.promise,new Promise((_,reject)=>{
  timer=setTimeout(()=>{op.record.waitTimedOut=true;reject(Error('owned operation deadline'));},ms);
 })]);}finally{clearTimeout(timer);}
}
async function work(name,fn,adopt){
 assert(!cleanupStarted&&now()<workDeadline,'owned work deadline');
 const value=await waitFor(begin(name,'work',fn,adopt),workDeadline-now(),workDeadline);
 assert(!cleanupStarted&&now()<workDeadline,'owned work deadline');return value;
}
function closeOnce(name,value,fn,kind='close'){
 if(!value)return undefined;
 if(!closes.has(value))closes.set(value,new Map());
 const actions=closes.get(value);
 if(!actions.has(kind))actions.set(kind,begin(name,'cleanup',fn));
 return actions.get(kind);
}
function lateClose(name,value,fn){if(cleanupStarted)closeOnce(name,value,fn);}
function retainServer(value){
 server=value;originalProcess=value.process();
 assert(originalProcess,'original BrowserServer process required');
 processState.handleRetained=true;processState.observation='pending';
 // Attach to the original handle immediately, before inspecting an already-exited handle.
 exitPromise=new Promise(resolveExit=>{
  const observe=(code,signal,observation)=>{
   if(processState.exitObserved)return;
   Object.assign(processState,{exitObserved:true,exitCode:code,signalCode:signal,observation,observedMS:elapsed()});
   resolveExit();
  };
  originalProcess.once('exit',(code,signal)=>observe(code,signal,'original-exit-event'));
  if(originalProcess.exitCode!==null||originalProcess.signalCode!==null)
   observe(originalProcess.exitCode,originalProcess.signalCode,'already-exited-original-handle');
 });
 if(cleanupStarted){result.cleanup.forced=true;closeOnce('late server kill',server,()=>server.kill(),'kill');}
}
function signalOriginal(signal){
 if(!originalProcess||processState.exitObserved)return;
 result.cleanup.forced=true;
 const request={signal,requestedMS:elapsed()};(processState.signalRequests??=[]).push(request);
 try{request.originalKillReturned=originalProcess.kill(signal);}catch(error){request.errorName=errorName(error);}
}
// This tool's own deadline includes launch/context/page initialization, work and
// cleanup. The outer FIFO remains a second bound, not its sole cleanup action.
const hardStop=setTimeout(()=>{
 result.complete=false;result.failure??='owned replay total deadline';result.cleanup.ownDeadlineExitRequested=true;
 if(server){result.cleanup.forced=true;closeOnce('deadline server kill',server,()=>server.kill(),'kill');}
 signalOriginal('SIGKILL');
 // begin() invokes the retained kill API immediately before process.exit.
 // The pinned launcher's exit callback also requests cleanup of its own launches,
 // including a constructor that never returned a public handle. That is not an
 // observed process exit and is explicitly unknown in this receipt.
 if(outputCreated&&deadlineReceiptAllowed){
  const snapshot={complete:false,failure:result.failure,elapsedMS:elapsed(),cleanup:result.cleanup,sourceLineage:result.sourceLineage??null,rowComparison:result.rowComparison,retainedBudget:result.retainedBudget,
   operations:result.operations.filter(x=>x.phase==='cleanup'||x.name.startsWith('initialize ')),
   qualification:'Deadline snapshot only; unresolved original APIs/process exit remain unknown. No universal reap claim.'};
  const raw=JSON.stringify(snapshot,null,2)+'\n';
  if(Buffer.byteLength(raw)<=64512){try{writeFileSync(join(output,'replay-deadline.json'),raw,{mode:0o600,flag:'wx'});}catch{}}
 }
 process.exit(1);
},Math.max(1,totalDeadline-now()));
const exactGrid=(cols,rows)=>[[72,28]].some(([c,r])=>cols===c&&rows===r);
const digest=b=>createHash('sha256').update(b).digest('hex');
async function retained(path){let bytes=0;for(const e of await readdir(path,{withFileTypes:true})){const p=join(path,e.name);if(e.isSymbolicLink())throw Error('Unexpected artifact link');bytes+=e.isDirectory()?await retained(p):(await stat(p)).size;}return bytes;}
// Budget the native capture, all seven immutable prior runtime containers and this fresh replay.
// Input and output are disjoint; every write below targets output exclusively.
async function retainedTotal(){return await retained(root)+(await Promise.all(retainedRoots.map(retained))).reduce((a,b)=>a+b,0)+await retained(output);}
try{
 assert(process.argv.length===19&&process.argv[3]==='--output'&&[5,7,9,11,13,15,17].every(i=>process.argv[i]==='--retained-input-root'));
 assert(process.argv[2]===root&&process.argv[4]===output&&[6,8,10,12,14,16,18].every((i,j)=>process.argv[i]===retainedRoots[j]));
 const inputs=[root,...retainedRoots];assert(new Set(inputs).size===8);
 assert(inputs.every(p=>outsideOf(repo,p)&&outsideOf(p,output)&&outsideOf(output,p))&&outsideOf(repo,output));
 assert(inputs.every((a,i)=>inputs.every((b,j)=>i===j||(outsideOf(a,b)&&outsideOf(b,a)))));
 for(const p of inputs){const info=await work('private native input',()=>lstat(p));assert(info.isDirectory()&&!info.isSymbolicLink()&&(info.mode&0o077)===0);assert(await work('canonical native input',()=>realpath(p))===p);}
 assert(await work('canonical output parent',()=>realpath(dirname(output)))===dirname(output));
 await work('new replay directory',()=>mkdir(output,{mode:0o700}),()=>{outputCreated=true;});
 assert(await work('receipt reserve',()=>retainedTotal())+131072<16*1024*1024);deadlineReceiptAllowed=true;
 stage='capture receipt';
 const captureRaw=await work('capture receipt',()=>readFile(join(root,'capture-receipt.json')));const receipt=JSON.parse(captureRaw);assert(receipt.complete===true);
 const casesRaw=await work('checkpoint receipt',()=>readFile(join(root,'checkpoints.json')));const cases=JSON.parse(casesRaw);assert(cases.length===8);
 const roster={'72x28':["lease-detail-first", "lease-detail-tail", "lease-record-exact-number", "release-review-first", "release-review-tail", "filter-draft", "leases-filter-empty", "leases-restored"]};
 assert.deepEqual(cases.map(c=>[c.grid,c.name]),Object.entries(roster).flatMap(([grid,names])=>names.map(name=>[grid,name])));
 const buildRaw=await work('original build receipt',()=>readFile(join(root,'build-receipt.json')));const build=JSON.parse(buildRaw);
 assert(typeof build.sourceCommit==='string'&&build.sourceCommit.length===40&&build.testSelector==='^TestUXTerminalModalShadowFixture$'&&Object.keys(build.sourceSHA256).length===63);
 const bindingRaw=await work('modal sibling binding',()=>readFile(join(repo,"docs/validation/ux-terminal-modal-shadow-binding-2026-10-30.json")));
 assert(digest(bindingRaw)==="c6c6858e9b97dd4a71bc54f05ab1c22a1856b5ef730c3c92e7c51019965b532d");const binding=JSON.parse(bindingRaw);
 const classifierRaw=await work('historical classifier',()=>readFile(join(repo,'docs/validation/ux-terminal-source-classification-2026-10-29.json')));
 assert(digest(classifierRaw)==='574c62ed1e2d66828082880beeaa4d706b7690cf4f38b1ff3a229aa1165cae5d');
 const classified=JSON.parse(classifierRaw).sourceEntries;
 assert(Object.keys(classified).length===50&&binding.schema===1);
 const overrides={"internal/tui/presentation.go": "5c06dca64d42f7c8f27905fe2e4b7f2125671eabcc2dbf9a5c895c8697ccc466", "internal/tui/polish_test.go": "04ff15993dbdac1a7d7866aaa429587df6f3364e5730e61d4c9374a204d90a34"};
 assert.deepEqual(binding.sourceOverrides,overrides);assert(Object.keys(overrides).every(f=>Object.hasOwn(classified,f)));
 const effective=Object.fromEntries(Object.entries(classified).map(([f,item])=>[f,overrides[f]??item.sha256]));
 assert.deepEqual(binding.effectiveSourceSHA256,effective);
 const required=[...new Set([...Object.keys(classified),...["docs/validation/ux-terminal-direct-fixture-plan-2026-10-30.json", "docs/validation/ux-terminal-lease-reading-plan-2026-10-30.json", "docs/validation/ux-terminal-loop-fixture-plan-2026-10-30.json", "docs/validation/ux-terminal-loop-replay-correction-plan-2026-10-30.json", "docs/validation/ux-terminal-modal-shadow-binding-2026-10-30.json", "docs/validation/ux-terminal-source-classification-2026-10-29.json", "internal/tui/ux_terminal_direct_fixture_test.go", "internal/tui/ux_terminal_lease_reading_fixture_test.go", "internal/tui/ux_terminal_loop_fixture_test.go", "scripts/dashboard-screenshots/ux-terminal-lease-capture.py", "scripts/dashboard-screenshots/ux-terminal-lease-replay.mjs", "scripts/dashboard-screenshots/ux-terminal-loop-capture.py", "scripts/dashboard-screenshots/ux-terminal-loop-replay.mjs"]])].sort();
 assert(required.length===63&&binding.sourceBinding.requiredEntries===63&&binding.sourceBinding.testSelector==='^TestUXTerminalModalShadowFixture$');
 assert.deepEqual(binding.sourceBinding.members,required);assert.deepEqual(Object.keys(build.sourceSHA256).sort(),required);
 for(const [file,sha] of Object.entries(effective))assert(build.sourceSHA256[file]===sha);
 for(const file of required)assert(digest(await work('fresh63 source member',()=>readFile(join(repo,file))))===build.sourceSHA256[file]);
 assert(digest(await work('historical native plan',()=>readFile(join(repo,'docs/validation/ux-terminal-lease-reading-plan-2026-10-30.json'))))==='10c195a224dd78573bf11320efa32e559f09635a7431a03fa232cc1a2b5746f9');
 const preservedExpected=new Map([["60cf482f0aa12b2b95f6569ffd3608ff60048c57a13bbd86d9ddac632126402d",111],["01283d9b87a645d7c5f443f783ffb21707e8196a583369cd8eb0db3f40dbb3ce",29],["bb963ddd2c5583bc689e5bf3d0916671797b05da9118af78fbb572ef0b12b510",47],["17a72af61c172d6620e77dd42f58b42d0f2c259933ff712a0766c803dcd1da67",15],["2e5a0919fffd1ae3379cc3cd95e1fa180ad88585fb939815d056eef25613e177",23],["ef54319ca16db0bda82c713c04f2687c61538232afe04b5954313dccd988fa6e",22],["fb8ee961d2618708c672bfeb7bb2c96feda582be72f4f2712325135390b13055",62]]);
 const preservedSeen=new Set();
 for(const p of retainedRoots){
  const raw=await work('preserved manifest',()=>readFile(join(p,'artifact-manifest.json')));const h=digest(raw);
  assert(preservedExpected.has(h)&&!preservedSeen.has(h));const entries=JSON.parse(raw);assert(Object.keys(entries).length===preservedExpected.get(h));preservedSeen.add(h);
  async function roster(dir){let names=[];for(const e of await work('preserved artifact roster',()=>readdir(dir,{withFileTypes:true}))){assert(!e.isSymbolicLink());const path=join(dir,e.name);if(e.isDirectory())names.push(...await roster(path));else{assert(e.isFile());names.push(relative(p,path));}}return names;}
  assert.deepEqual((await roster(p)).sort(),[...Object.keys(entries),'artifact-manifest.json'].sort());
  for(const [file,hash] of Object.entries(entries)){const path=resolve(p,file);assert(outsideOf(repo,path)&&relative(p,path)!==''&&!outsideOf(p,path));assert(digest(await work('preserved artifact bytes',()=>readFile(path)))===hash);}
 }
 assert(preservedSeen.size===7);
 const ownSHA=digest(await work('native replay source',()=>readFile(ownSource)));assert(build.sourceSHA256['scripts/dashboard-screenshots/ux-terminal-lease-replay.mjs']===ownSHA);
 assert(receipt.receipts.length===1&&receipt.receipts[0].grid==='72x28');
 for(const entry of receipt.receipts){
  const c=entry.cleanup,child=entry.originalChild;
  const positive=['programStarted','programReturned','sameWrapperReturned','sameModel','restored','quitRequested','initSuppressed','sawExactNumber','sawReview','declined','fakeClosed','contextCanceled'];
  const diagnostic=entry.diagnostics,childDiagnostic=child?.diagnostics;
  assert(diagnostic?.accepted===true&&diagnostic.records<=64&&diagnostic.retainedDiagnosticBytes<=65536&&childDiagnostic?.unknown===false&&childDiagnostic.programResult==='nil');
  const observationRaw=await work('original readiness metadata',()=>readFile(join(root,entry.grid,'readiness.json'))),observation=JSON.parse(observationRaw);
  const actionRaw=await work('original mouse action metadata',()=>readFile(join(root,entry.grid,'mouse-actions.json'))),actions=JSON.parse(actionRaw).records;
  assert(digest(observationRaw)===diagnostic.readinessSHA256&&digest(actionRaw)===diagnostic.actionsSHA256&&!observation.unknown&&!observation.startup.drift&&observation.programResult==='nil');
  assert(observation.records.length<=48&&actions.length<=16&&observationRaw.length<=16384&&actionRaw.length<=8192);
  assert(Object.values(observation.descriptor).every(Boolean));
  const barrier=diagnostic.initialBarrier;assert.deepEqual(observation.frame,barrier.frame);assert(observation.startup.sealed);const range=barrier.frame,stream=await work('original initial frame stream',()=>readFile(join(root,entry.grid+'.pty')));
  assert(range.full&&range.requested===range.returned&&Number.isInteger(range.start)&&Number.isInteger(range.end)&&range.start>=0&&range.end>range.start&&range.end<=stream.length&&range.end-range.start===range.returned);
  const initialBytes=stream.subarray(range.start,range.end);assert(digest(initialBytes)===range.sha256&&barrier.fingerprint===range.fingerprint&&barrier.fingerprint===observation.startup.fingerprint);
  assert(['\x1b[?1049h','\x1b[?1002h','\x1b[?1006h','LEASES','Synthetic lease 1','Inspect'].every(token=>initialBytes.includes(Buffer.from(token))));
  const completedWrites=actions.filter(a=>a.kind==='mouse-press-return'||a.kind==='mouse-release-return');assert(completedWrites.length===4&&completedWrites.every(a=>a.requested===a.returned));
  for(const stage of ['parsed-click','filter-admitted','update-click','callback-entry','cmd-created','cmd-start','cmd-produced','typed-delivery','typed-frame'])assert(observation.counters[stage]===2);
  assert(observation.counters['inspect-complete']===1&&!observation.counters['cmd-nil']);
  assert(entry.error===null&&child&&!child.testFailed&&positive.every(k=>child[k]===true)&&child.activeFakeCalls===0&&!(child.forbidden??[]).length&&child.reads===1&&child.calls.length===1&&child.calls[0].Operation==='inspect'&&child.pastes===1&&child.recordKeys<=64&&child.mouseProduced===2&&child.mouseDelivered===2&&child.mouseActive===0);
  assert(c.childExitCode===0&&c.childWaited&&c.finalDrainAfterChildJoin&&c.termiosRestored&&c.slaveClosed&&c.masterClosed&&(c.finalMasterEOFObserved||c.finalMasterEIOObserved)&&!c.finalDrainTimedOut&&!c.ownedChildTerminationRequested&&!c.cleanupErrors.length&&!c.cleanupOutputTruncated);
  assert(['detail','review'].every(k=>entry.readingCoverage[k].totalLogicalRows===entry.readingCoverage[k].coveredLogicalRows));
 }
 result.sourceLineage={nativeCompiledCapture:{sourceCommit:build.sourceCommit,sourceEntries:63,binarySHA256:build.binarySHA256,buildReceiptSHA256:digest(buildRaw),captureReceiptSHA256:digest(captureRaw),checkpointsSHA256:digest(casesRaw)},nativeReplay:{sourceFile:relative(repo,ownSource),sourceSHA256:ownSHA,role:'Fresh fixed72 modal-shadow native63 compile/capture cut; historical native evidence remains unchanged.'}};
 result.readingCoverage=receipt.receipts.map(entry=>({grid:entry.grid,...entry.readingCoverage}));
 result.retainedBudget.scope='Seven preserved runtime input containers, including both original native failures, plus new native capture/output and future wrapper metadata share16MiB; old allocated binaries remain separate.';
 stage='all eight checkpoint semantics';
 const originalStream=await work('original72 full stream',()=>readFile(join(root,'72x28.pty')));
 let previousPrefix=0;
 for(const c of cases){
  assert(c.grid==='72x28'&&exactGrid(c.cols,c.rows)&&c.stream==='72x28.pty');
  assert(Number.isInteger(c.bytesThrough)&&c.bytesThrough>=previousPrefix&&c.bytesThrough<=originalStream.length&&c.bytesThrough<=4*1024*1024);previousPrefix=c.bytesThrough;
  const prefix=originalStream.subarray(0,c.bytesThrough);assert(digest(prefix)===c.prefixSHA256&&!prefix.includes(Buffer.from('\x1b]52;')));
  assert(c.resizes.every(r=>exactGrid(r.cols,r.rows)&&Number.isInteger(r.offset)&&r.offset>=0&&r.offset<=c.bytesThrough));
  const state=c.state;assert(state.sameModel&&!state.pending&&state.unknownEmpty&&state.defaultLeases&&state.defaultRuns&&state.initSuppressed&&state.readClosures===1&&state.calls.length===1&&state.calls[0].Operation==='inspect'&&state.mouseActive===0&&state.mouseProduced===state.mouseDelivered);
  const modelRows=state.viewContent.replace(/\x1b\[[0-?]*[ -/]*[@-~]/g,'').split('\n');assert(modelRows.length===28);
  if(c.name.startsWith('lease-detail-'))assert(state.detailOpen&&!state.recordOpen&&!state.reviewOpen&&state.viewport.total>state.viewport.height&&state.viewport.offset===(c.name.endsWith('tail')?state.viewport.total-state.viewport.height:0));
  if(c.name==='lease-record-exact-number')assert(state.recordOpen&&state.detailOpen&&state.recordValueType==='json.Number'&&typeof state.recordValueDecimal==='string'&&state.recordValueDecimal==='9007199254740993'&&state.recordPath==='["request","resources","memory_bytes"]'&&modelRows.join('\n').includes('9007199254740993'));
  if(c.name.startsWith('release-review-'))assert(state.reviewOpen&&state.detailOpen&&!state.recordOpen&&state.reviewAction==='release'&&state.reviewID===state.detailID&&state.reviewRevision===state.detailRevision&&state.viewport.total>state.viewport.height&&state.viewport.offset===(c.name.endsWith('tail')?state.viewport.total-state.viewport.height:0));
  if(c.name==='filter-draft')assert(state.inputMode==='filter'&&state.inputValue==='no-synthetic-match'&&!state.reviewOpen&&!state.detailOpen&&!state.recordOpen);
  if(c.name==='leases-filter-empty')assert(state.filter==='no-synthetic-match'&&state.visibleLeaseIDs.length===0&&state.inputMode==='');
  if(c.name==='leases-restored')assert(state.restored&&state.filter===''&&state.inputMode===''&&state.visibleLeaseIDs.length===8);
 }
 result.checkpointSemantics={checked:8,roster:cases.map(c=>c.name),exactRecordTypeAndDecimalObserved:true,qualification:'All eight original interaction/checkpoint states and prefix hashes verified; only the three modal targets below receive new actual emulator-cell and PNG comparison.'};
 const targets=cases.filter(c=>['release-review-first','release-review-tail','filter-draft'].includes(c.name));assert(targets.length===3);
 stage='browser initialization';
 // Public BrowserServer exposes the original ChildProcess for observation and
 // bounded fallback. Never record wsEndpoint() or credentials/error messages.
 await work('initialize BrowserServer',()=>chromium.launchServer({headless:true,host:'127.0.0.1',port:0,timeout:5000}),retainServer);
 await work('initialize browser connection',()=>chromium.connect(server.wsEndpoint(),{timeout:3000}),value=>{browser=value;lateClose('late browser connection close',value,()=>value.close());});
 await work('initialize context',()=>browser.newContext({serviceWorkers:'block',locale:'en-US',viewport:{width:1952,height:1032}}),value=>{context=value;lateClose('late context close',value,()=>value.close());});
 result.externalRequests=[];
 await work('network refusal',()=>context.route('**/*',route=>{result.externalRequests.push('refused');return route.abort();}));
 await work('native path refusal',()=>context.addInitScript(()=>{window.__loopForbidden=[];window.WebSocket=class{constructor(){window.__loopForbidden.push('WebSocket');throw Error('Native socket refused');}};Object.defineProperty(navigator,'clipboard',{value:{readText(){window.__loopForbidden.push('clipboard-read');throw Error('Native clipboard refused');},writeText(){window.__loopForbidden.push('clipboard-write');throw Error('Native clipboard refused');}}});}));
 await work('initialize page',()=>context.newPage(),value=>{page=value;lateClose('late page close',value,()=>value.close({runBeforeUnload:false}));});
 page.setDefaultTimeout(4000);
 for(const [index,c] of targets.entries()){
  assert(exactGrid(c.cols,c.rows)&&c.grid===`${c.cols}x${c.rows}`&&c.stream===c.grid+'.pty');assert(Number.isInteger(c.bytesThrough)&&c.bytesThrough>=0&&c.bytesThrough<=4*1024*1024);
  stage='original stream hash';const full=await work('read original stream',()=>readFile(join(root,c.stream)));const bytes=full.subarray(0,c.bytesThrough);assert.equal(digest(bytes),c.prefixSHA256);assert(!bytes.includes(Buffer.from('\x1b]52;')));
  stage='terminal initialize';
  await work('terminal document',()=>page.setContent('<!doctype html><html lang="en"><meta charset="utf-8"><body style="margin:0;background:#101519"><div id="terminal" style="display:inline-block;padding:16px" aria-label="Owned Program PTY replay"></div></body></html>'));
  await work('terminal style',()=>page.addStyleTag({path:join(here,'node_modules/@xterm/xterm/css/xterm.css')}));
  await work('terminal script',()=>page.addScriptTag({path:join(here,'node_modules/@xterm/xterm/lib/xterm.js')}));
  await work('terminal evaluate',()=>page.evaluate(({cols,rows})=>{window.__terminal?.dispose();window.__terminal=new Terminal({cols,rows,fontFamily:'"DejaVu Sans Mono", monospace',fontSize:14,scrollback:0,cursorBlink:false,allowTransparency:false,theme:{background:'#101519',foreground:'#e1e9ed'}});window.__terminal.open(document.getElementById('terminal'));},{cols:c.cols,rows:c.rows}));
  stage='actual PTY byte replay';let offset=0;
  const write=async data=>{if(data.length)await work('terminal evaluate',()=>page.evaluate(async encoded=>{const b=Uint8Array.from(atob(encoded),v=>v.charCodeAt(0));await new Promise(resolve=>window.__terminal.write(b,resolve));},data.toString('base64')));};
  for(const event of c.resizes){assert(event.offset>=offset&&event.offset<=bytes.length);assert(exactGrid(event.cols,event.rows));await write(bytes.subarray(offset,event.offset));await work('terminal evaluate',()=>page.evaluate(({cols,rows})=>window.__terminal.resize(cols,rows),event));offset=event.offset;}
  await write(bytes.subarray(offset));
  await work('terminal evaluate',()=>page.evaluate(()=>new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('layout deadline')),1000);requestAnimationFrame(()=>requestAnimationFrame(()=>{clearTimeout(timer);resolve();}));})));
  stage='actual cells and model comparator';
  const modal={x:0,y:4+Math.floor((20-(c.state.viewport.height+2))/2),width:72,height:c.state.viewport.height+2};
  assert(Number.isInteger(modal.height)&&modal.height>=9&&modal.height<=20&&modal.y>=4&&modal.y+modal.height<=24);
  const actual=await work('terminal evaluate',()=>page.evaluate(({regions,modal})=>{
   const t=window.__terminal,b=t.buffer.active;
   const cell=(x,y)=>{const c=b.getLine(y)?.getCell(x);return{chars:c?.getChars()??'',width:c?.getWidth()??null};};
   return{cols:t.cols,rows:t.rows,alternate:b.type==='alternate',text:Array.from({length:t.rows},(_,i)=>b.getLine(i)?.translateToString(true)??''),
    modal:{...modal,borderRows:Array.from({length:modal.height},(_,row)=>({row,left:cell(modal.x,modal.y+row),right:cell(modal.x+modal.width-1,modal.y+row)}))},
    ownedRegionRows:regions.filter(r=>['y','n','enter','esc'].includes(r.key)).map(r=>{const visible=r.x>=0&&r.y>=0&&r.width>0&&r.height>0&&r.x+r.width<=t.cols&&r.y+r.height<=t.rows;return{...r,visible,rows:visible?Array.from({length:r.height},(_,i)=>b.getLine(r.y+i)?.translateToString(true,r.x,r.x+r.width)??''):[]};}),forbidden:window.__loopForbidden??[]};
  },{regions:c.state.regions,modal}));

  const expectedRaw=c.state.viewContent.replace(/\x1b\[[0-?]*[ -/]*[@-~]/g,'').split('\n');
  const rowMatches=actual.text.map((s,i)=>i<expectedRaw.length&&normalizeRow(s)===normalizeRow(expectedRaw[i]));
  const visible=actual.text.join('\n'),number=c.name==='lease-record-exact-number',filter=c.name==='filter-draft',empty=c.name==='leases-filter-empty',restored=c.name==='leases-restored';
  const reviewLast=['release-review-fit','release-review-tail'].includes(c.name);
  const keepVisible=actual.ownedRegionRows.some(r=>r.visible&&r.rows.join('\n').includes('Keep lease'));
  const item={grid:c.grid,name:c.name,sourcePrefixSHA256:c.prefixSHA256,bytes:c.bytesThrough,actual,expectedRawRows:expectedRaw,rowMatches,modelRowsMatch:rowMatches.every(Boolean)&&expectedRaw.length===c.rows,typedRecordDecimal:c.state.recordValueDecimal??null,typedRecordType:c.state.recordValueType??null,recordPath:c.state.recordPath??null,exactDecimalVisible:visible.includes('9007199254740993'),filterDraftVisible:visible.includes('no-synthetic-match'),keepVisible,readingState:{detailID:c.state.detailID,detailRevision:c.state.detailRevision,reviewID:c.state.reviewID,reviewRevision:c.state.reviewRevision,reviewAction:c.state.reviewAction,inputMode:c.state.inputMode,filter:c.state.filter,restored:c.state.restored,visibleLeaseIDs:c.state.visibleLeaseIDs,viewport:c.state.viewport},resizeTimeline:c.resizes};
  for(const edge of actual.modal.borderRows){
   const left=edge.row===0?'╔':edge.row===modal.height-1?'╚':'║',right=edge.row===0?'╗':edge.row===modal.height-1?'╝':'║';
   assert(edge.left.chars===left&&edge.right.chars===right&&edge.left.width===1&&edge.right.width===1,'actual modal border/corner clipped');
  }
  const modalControls=actual.ownedRegionRows.filter(r=>r.y>=modal.y&&r.y<modal.y+modal.height);
  if(filter){
   assert(modalControls.length===0&&visible.includes('[Enter] Apply filter')&&visible.includes('[Esc] Back')&&item.filterDraftVisible);
   const controls=actual.ownedRegionRows.filter(r=>r.y===25);assert.deepEqual(controls.map(r=>r.key),['enter','esc']);
   assert(controls.every(r=>r.visible&&r.rows.join('\n').includes('['+r.key+']')));
  }else{
   const lastVisible=c.state.viewport.total-1-c.state.viewport.offset<c.state.viewport.height;
   assert(modalControls.length===(lastVisible?2:0));
   if(lastVisible){
    assert.deepEqual(modalControls.map(r=>r.key),['y','n']);
    for(const r of modalControls)assert(r.visible&&r.height===1&&r.x>=modal.x+1&&r.x+r.width<=modal.x+modal.width-1&&r.y>=modal.y+1&&r.y<modal.y+modal.height-1&&r.rows.join('\n').includes(r.key==='n'?'[n/Esc] Keep lease':'[y] Release lease'));
   }
  }
  item.modalFrameAndControlGeometryAccepted=true;
  stage='native lease PNG';const png=await work('native lease PNG',()=>page.locator('#terminal').screenshot({animations:'disabled'}));const reserve=Buffer.byteLength(JSON.stringify({...result,cases:[...result.cases,item]}))+131072;
  assert(await work('combined retained native bytes',()=>retainedTotal())+png.length+reserve<=16*1024*1024);
  item.png=`${index+1}-${c.grid}-${c.name}.png`;await work('retain native lease PNG',()=>writeFile(join(output,item.png),png,{mode:0o600,flag:'wx'}));result.cases.push(item);
  stage='native lease rows/reading assertions';
  assert(actual.cols===c.cols&&actual.rows===c.rows&&actual.alternate&&item.modelRowsMatch);assert.deepEqual(actual.forbidden,[]);
  if(number)assert(item.typedRecordType==='json.Number'&&typeof item.typedRecordDecimal==='string'&&item.typedRecordDecimal==='9007199254740993'&&item.recordPath==='["request","resources","memory_bytes"]'&&item.exactDecimalVisible);
  if(reviewLast)assert(keepVisible&&c.state.reviewAction==='release'&&c.state.reviewID===c.state.detailID&&c.state.reviewRevision===c.state.detailRevision&&!c.state.pending);
  if(filter)assert(c.state.inputMode==='filter'&&c.state.inputValue==='no-synthetic-match'&&item.filterDraftVisible);
  if(empty)assert(c.state.filter==='no-synthetic-match'&&c.state.visibleLeaseIDs.length===0);
  if(restored)assert(c.state.restored&&c.state.filter===''&&c.state.inputMode===''&&c.state.visibleLeaseIDs.length===8&&c.state.defaultLeases&&c.state.defaultRuns);
  item.terminalDisposeReturned=await work('terminal dispose',()=>page.evaluate(()=>{window.__terminal.dispose();window.__terminal=null;return true;}));
 }
 assert(result.cases.length===3&&result.cases.every(c=>c.modalFrameAndControlGeometryAccepted));
 result.comparedRows=result.cases.reduce((sum,c)=>sum+c.rowMatches.length,0);assert(result.comparedRows===84);
 assert.deepEqual(result.externalRequests,[]);result.complete=true;
}catch(error){result.failure??=stage;result.failureErrorName=errorName(error);result.complete=false;}
finally{
 cleanupStarted=true;
 const cleanupDeadline=Math.min(totalDeadline-1000,now()+8500);
 const cleanupWait=async(op,cap)=>{
  if(!op)return false;
  try{await waitFor(op,cap,cleanupDeadline);return true;}
  catch{result.cleanup.failedOrUnknown=true;return false;}
 };
 // Give original initialization promises a bounded opportunity to return their
 // handles. Late returns are adopted above and immediately closed, never reused.
 const pendingInit=result.operations.some(x=>x.name.startsWith('initialize ')&&x.originalStatus==='pending');
 if(pendingInit)await cleanupWait(begin('pending initialization grace','cleanup',()=>new Promise(r=>setTimeout(r,500))),600);
 if(page)result.cleanup.lastTerminalDisposeReturned=await cleanupWait(begin('last terminal dispose','cleanup',()=>page.evaluate(()=>{window.__terminal?.dispose();window.__terminal=null;return true;})),300);
 result.cleanup.pageCloseReturned=await cleanupWait(closeOnce('page close',page,()=>page.close({runBeforeUnload:false})),300);
 result.cleanup.contextCloseReturned=await cleanupWait(closeOnce('context close',context,()=>context.close()),800);
 result.cleanup.connectionCloseReturned=await cleanupWait(closeOnce('browser connection close',browser,()=>browser.close()),800);
 result.cleanup.serverCloseReturned=await cleanupWait(closeOnce('BrowserServer close',server,()=>server.close()),1200);
 if(server&&!processState.exitObserved){
  result.cleanup.forced=true;
  result.cleanup.serverKillReturned=await cleanupWait(closeOnce('BrowserServer kill',server,()=>server.kill(),'kill'),800);
 }
 if(originalProcess&&!processState.exitObserved){
  signalOriginal('SIGTERM');await cleanupWait(begin('original exit after TERM','cleanup',()=>exitPromise),500);
 }
 if(originalProcess&&!processState.exitObserved){
  signalOriginal('SIGKILL');await cleanupWait(begin('original exit after KILL','cleanup',()=>exitPromise),500);
 }
 // Inspect the original promises, including any close triggered by late adoption.
 // An expired bounded wait remains a failure even if that API later returned.
 result.cleanup.pendingInitialization=result.operations.filter(x=>x.name.startsWith('initialize ')&&x.originalStatus==='pending').map(x=>x.name);
 result.cleanup.pendingCleanup=result.operations.filter(x=>x.phase==='cleanup'&&x.originalStatus==='pending').map(x=>x.name);
 const lifecycleFailure=result.operations.some(x=>(x.phase==='cleanup'||x.name.startsWith('initialize '))&&(x.waitTimedOut||x.originalStatus!=='returned'));
 result.cleanup.accepted=Boolean(server&&browser&&context&&page&&processState.exitObserved&&processState.exitCode===0&&processState.signalCode===null&&!result.cleanup.forced&&!result.cleanup.failedOrUnknown&&!lifecycleFailure);
 if(!result.cleanup.accepted){result.complete=false;result.failure??='original cleanup failed or unknown';}
 if(result.failure)result.complete=false;
 // Unknown handles/APIs cannot be allowed to keep this tool alive indefinitely.
 // The exit request is a failure, not an observation that descendants exited.
 result.cleanup.ownExitRequested=!result.cleanup.accepted;
 result.elapsedMS=elapsed();
 if(outputCreated){
  try{
   // Keep a separate 64KiB deadline-receipt reserve; do not overwrite a receipt.
   const budget=begin('final receipt budget','receipt',()=>retainedTotal());
   const used=await waitFor(budget,500,totalDeadline-100);
   let receiptRaw;
   const op=begin('final receipt write','receipt',()=>{
    receiptRaw=JSON.stringify(result,null,2)+'\n';assert(used+Buffer.byteLength(receiptRaw)+65536<=16*1024*1024);
    return writeFile(join(output,'replay-receipt.json'),receiptRaw,{mode:0o600,flag:'wx'});
   });
   await waitFor(op,700,totalDeadline-100);
   // The receipt records its own write as pending; only this following marker
   // reports the observed original write return. A missing marker is unknown.
   await waitFor(begin('receipt write return marker','receipt',()=>writeFile(join(output,'replay-receipt-written.json'),JSON.stringify({originalWriteReturned:true,receiptSHA256:digest(Buffer.from(receiptRaw)),complete:result.complete,elapsedMS:elapsed()})+'\n',{mode:0o600,flag:'wx'})),200,totalDeadline-10);
  }catch{result.complete=false;result.failure??='original receipt failed or unknown';result.cleanup.ownExitRequested=true;}
 }
 clearTimeout(hardStop);
}
if(!result.complete)process.exitCode=1;
console.log(JSON.stringify({complete:result.complete,cases:result.cases.length,failure:result.failure,cleanup:result.cleanup}));
if(result.cleanup.ownExitRequested)process.exit(1);
