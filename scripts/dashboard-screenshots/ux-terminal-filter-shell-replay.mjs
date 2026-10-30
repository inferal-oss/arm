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
const retainedRoots=[6,8,10,12,14,16,18,20].map(i=>resolve(process.argv[i]||'')),repo=resolve(here,'../..');
const budgetRoot=resolve(process.argv[22]||'');
const outsideOf=(parent,path)=>{const rel=relative(parent,path);return rel==='..'||rel.startsWith('..'+sep);};
const normalizeRow=text=>text.replace(/\u0020+$/u,'');
const result={complete:false,failure:null,cases:[],operations:[],cleanup:{},
 retainedBudget:{limitBytes:16777216,scope:'Whole fresh runtime directory plus all eight immutable retained runtime roots.'},
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
const exactGrid=(cols,rows)=>[[45,14]].some(([c,r])=>cols===c&&rows===r);
const digest=b=>createHash('sha256').update(b).digest('hex');
async function retained(path){let bytes=0;for(const e of await readdir(path,{withFileTypes:true})){const p=join(path,e.name);if(e.isSymbolicLink())throw Error('Unexpected artifact link');bytes+=e.isDirectory()?await retained(p):(await stat(p)).size;}return bytes;}
// Budget the native capture, all eight immutable prior runtime containers and this fresh replay.
// Input and output are disjoint; every write below targets output exclusively.
async function retainedTotal(){return await retained(budgetRoot)+(await Promise.all(retainedRoots.map(retained))).reduce((a,b)=>a+b,0);}
try{
 assert(process.argv.length===23&&process.argv[3]==='--output'&&process.argv[21]==='--budget-root');
 assert([5,7,9,11,13,15,17,19].every(i=>process.argv[i]==='--retained-input-root'));
 assert(process.argv[2]===root&&process.argv[4]===output&&process.argv[22]===budgetRoot&&[6,8,10,12,14,16,18,20].every((i,j)=>process.argv[i]===retainedRoots[j]));
 assert(new Set([root,...retainedRoots]).size===9&&new Set(retainedRoots).size===8);
 assert(root!==budgetRoot&&output!==budgetRoot&&!outsideOf(budgetRoot,root)&&!outsideOf(budgetRoot,output));
 assert(outsideOf(repo,budgetRoot)&&outsideOf(root,output)&&outsideOf(output,root));
 for(const p of [budgetRoot,root,...retainedRoots]){
  const info=await work('private canonical input',()=>lstat(p));assert(info.isDirectory()&&!info.isSymbolicLink()&&(info.mode&0o077)===0);
  assert(await work('canonical input',()=>realpath(p))===p);
 }
 assert(retainedRoots.every(a=>outsideOf(a,budgetRoot)&&outsideOf(budgetRoot,a)));
 assert(retainedRoots.every((a,i)=>retainedRoots.every((b,j)=>i===j||(outsideOf(a,b)&&outsideOf(b,a)))));
 assert(await work('canonical output parent',()=>realpath(dirname(output)))===dirname(output));
 await work('new replay directory',()=>mkdir(output,{mode:0o700}),()=>{outputCreated=true;});
 assert(await work('whole root receipt reserve',()=>retainedTotal())+131072<16777216);deadlineReceiptAllowed=true;
 const bindingRaw=await work('native45 binding',()=>readFile(join(repo,'docs/validation/ux-terminal-filter-shell-proposal-2026-10-30.json')));
 assert(digest(bindingRaw)==='4e6301237a2893f4a6dfc06d8c66f50041ab6dd34e6d948598bc1f5c635d9420');const binding=JSON.parse(bindingRaw);
 const captureRaw=await work('original capture receipt',()=>readFile(join(root,'capture-receipt.json'))),receipt=JSON.parse(captureRaw);
 const casesRaw=await work('original checkpoints',()=>readFile(join(root,'checkpoints.json'))),cases=JSON.parse(casesRaw);
 const buildRaw=await work('original build receipt',()=>readFile(join(root,'build-receipt.json'))),build=JSON.parse(buildRaw);
 const names=['leases-scrolled','filter-visible','filter-help','filter-edited','leases-applied','filter-discard-draft','leases-cancelled','filter-quit-ready'];
 assert(receipt.complete===true&&receipt.receipts.length===1&&receipt.checkpoints===8&&!(receipt.postcheckFailures??[]).length);
 assert.deepEqual(cases.map(c=>c.name),names);assert(cases.length===8&&cases.every(c=>c.grid==='45x14'&&c.cols===45&&c.rows===14&&c.stream==='45x14.pty'&&c.resizes.length===0));
 assert(build.complete===true&&build.originalCompileExitCode===0&&build.testSelector==='^TestUXTerminalFilterShellFixture$'&&typeof build.sourceCommit==='string'&&build.sourceCommit.length===40);
 assert(['originalCompileHandleJoined','sourceUnchanged','preservedUnchanged','TMPAndGOTMPEmpty'].every(k=>build[k]===true));
 assert(binding.sourceBinding.requiredEntries===69&&binding.sourceBinding.testSelector===build.testSelector);
 assert.deepEqual(Object.keys(build.sourceSHA256).sort(),[...binding.sourceBinding.members].sort());assert(Object.keys(build.sourceSHA256).length===69);
 for(const [file,h] of Object.entries(build.sourceSHA256))assert(digest(await work('actual69 source',()=>readFile(join(repo,file))))===h);
 for(const [file,h] of Object.entries(binding.currentSource.existingMemberSHA256))assert(build.sourceSHA256[file]===h);
 assert(build.sourceSHA256[relative(repo,ownSource)]===digest(await work('own replay source',()=>readFile(ownSource))));
 const manifests=new Map(binding.retention.historicalRuntimeContainers.map(x=>[x.manifestSHA256,x.entries]));assert(manifests.size===8);
 async function verifyRetained(){
  const observed=new Set();
  for(const p of retainedRoots){
   const raw=await work('old manifest',()=>readFile(join(p,'artifact-manifest.json'))),h=digest(raw);assert(manifests.has(h)&&!observed.has(h));observed.add(h);
   const entries=JSON.parse(raw);assert(Object.keys(entries).length===manifests.get(h));
   async function roster(dir){const found=[];for(const e of await work('old roster',()=>readdir(dir,{withFileTypes:true}))){assert(!e.isSymbolicLink());const path=join(dir,e.name);if(e.isDirectory())found.push(...await roster(path));else{assert(e.isFile());found.push(relative(p,path));}}return found;}
   assert.deepEqual((await roster(p)).sort(),[...Object.keys(entries),'artifact-manifest.json'].sort());
   for(const [file,want] of Object.entries(entries)){const path=resolve(p,file);assert(path!==p&&!outsideOf(p,path));assert(digest(await work('old bytes',()=>readFile(path)))===want);}
  }
  assert(observed.size===8);return observed;
 }
 await verifyRetained();
 const entry=receipt.receipts[0],child=entry.originalChild,c=entry.cleanup;
 const positive=['programStarted','programReturned','sameWrapperReturned','sameModel','quitRequested','quitMsgObserved','initSuppressed','fakeClosed','contextCanceled','inputFocused'];
 assert(child&&!child.testFailed&&positive.every(k=>child[k]===true));
 assert(child.nativeSizeMessages>=1&&child.pastes===2&&child.wheelInputs===2&&child.footerInputs===1&&child.mouseProduced===3&&child.mouseDelivered===3&&child.mouseActive===0&&child.reads===0&&child.activeFakeCalls===0&&!(child.calls??[]).length&&!(child.forbidden??[]).length&&child.fakeCloses===1);
 assert(child.inputMode==='filter'&&child.inputValue==='probe-draftq'&&child.filter==='probe-draftq');
 assert(c.childExitCode===0&&['childWaited','finalDrainAfterChildJoin','termiosRestored','slaveClosed','masterClosed'].every(k=>c[k]===true));
 assert((c.finalMasterEOFObserved||c.finalMasterEIOObserved)&&!c.finalDrainTimedOut&&!c.ownedChildTerminationRequested&&!c.cleanupOutputTruncated&&!c.cleanupErrors.length);
 const diagnosticRaw=await work('original readiness',()=>readFile(join(root,'45x14','readiness.json'))),diagnostic=JSON.parse(diagnosticRaw);
 const actionsRaw=await work('original input actions',()=>readFile(join(root,'input-actions.json'))),actions=JSON.parse(actionsRaw);
 assert(digest(diagnosticRaw)===entry.diagnosticSHA256&&digest(actionsRaw)===entry.actionsSHA256);
 assert(diagnosticRaw.length<=16384&&diagnostic.records.length<=48&&actionsRaw.length<=8192&&actions.length<=24);
 assert.deepEqual(Object.keys(diagnostic.descriptor).sort(),['inputUnwrapped','inputTerminal','outputTerminal','sameOutputFD','sameOutputName','termFilePreserved','sizeMatches'].sort());
 assert(!diagnostic.unknown&&diagnostic.startup.valid&&diagnostic.startup.sealed&&!diagnostic.startup.drift&&diagnostic.programResult==='nil'&&Object.values(diagnostic.descriptor).every(value=>value===true));
 for(const key of ['callback-entry','cmd-created','cmd-start','cmd-produced','typed-delivery','typed-frame'])assert(diagnostic.counters[key]===3);
 assert(diagnostic.counters['parsed-wheel']===2&&diagnostic.counters['parsed-click']===1&&!diagnostic.counters['cmd-nil']);
 const stream=await work('actual full45 stream',()=>readFile(join(root,'45x14.pty')));assert(stream.length<=4194304&&!stream.includes(Buffer.from('\x1b]52;')));
 const frame=entry.initialBarrier.frame;assert.deepEqual(diagnostic.frame,frame);assert(frame.full&&frame.requested===frame.returned&&Number.isInteger(frame.start)&&Number.isInteger(frame.end)&&frame.start>=0&&frame.end>frame.start&&frame.end<=stream.length&&frame.end-frame.start===frame.returned);
 const initial=stream.subarray(frame.start,frame.end);assert(digest(initial)===frame.sha256&&frame.fingerprint===diagnostic.startup.fingerprint);
 assert(['\x1b[?1049h','\x1b[?1002h','\x1b[?1006h','[q] Exit','Inspect'].every(s=>initial.includes(Buffer.from(s))));
 const inputCodes={down:'\x1b[B','/':'/',q:'q',enter:'\r',esc:'\x1b', 'wheel-down':'\x1b[<65;11;7M'};
 let pastes=0,previousActionPrefix=0;
 for(const a of actions){
  assert(Number.isInteger(a.requested)&&a.returned===a.requested&&a.requested>0&&typeof a.hex==='string');
  const data=Buffer.from(a.hex,'hex');assert(data.length===a.requested&&data.toString('hex')===a.hex);
  assert(Number.isInteger(a.bytesThrough)&&a.bytesThrough>=previousActionPrefix&&a.bytesThrough<=stream.length&&digest(stream.subarray(0,a.bytesThrough))===a.prefixSHA256);previousActionPrefix=a.bytesThrough;
  if(a.kind==='paste'){const text=['probe-draft','-discard'][pastes++];assert(text&&data.equals(Buffer.from('\x1b[200~'+text+'\x1b[201~')));}
  else if(a.kind==='footer-quit'){assert(a===actions.at(-1));const r=entry.footerPress.region;assert(data.equals(Buffer.from(`\x1b[<0;${r.x+Math.floor(r.width/2)+1};14M`)));}
  else assert(Object.hasOwn(inputCodes,a.kind)&&data.equals(Buffer.from(inputCodes[a.kind])));
 }
 const count=k=>actions.filter(a=>a.kind===k).length;
 assert(pastes===2&&count('down')>=1&&count('down')<=12&&count('/')===3&&count('q')===1&&count('enter')===1&&count('esc')===1&&count('wheel-down')===2&&count('footer-quit')===1);
 assert.deepEqual(actions.map(a=>a.kind),[...Array(count('down')).fill('down'),'/','paste','wheel-down','wheel-down','q','enter','/','paste','esc','/','footer-quit']);
 assert.deepEqual(entry.footerPress.region,cases.at(-1).state.regions.find(r=>r.key==='ctrl+c'&&r.y===13));
 assert.deepEqual(entry.footerPress.write,actions.at(-1));assert(entry.footerPress.revision===cases.at(-1).state.revision);
 assert(actions.at(-1).bytesThrough>=cases.at(-1).bytesThrough);
 let previous=0;
 for(const item of cases){
  assert(Number.isInteger(item.bytesThrough)&&item.bytesThrough>=previous&&item.bytesThrough<=stream.length);previous=item.bytesThrough;
  assert(digest(stream.subarray(0,item.bytesThrough))===item.prefixSHA256);
  const s=item.state;assert(s.sameModel&&s.view===2&&s.initSuppressed&&s.nativeSizeMessages>=1&&s.defaultLeases&&s.defaultRuns&&!s.pending&&s.unknownEmpty&&s.readClosures===0&&!(s.calls??[]).length&&s.mouseActive===0&&s.mouseProduced===s.mouseDelivered);
 }
 const covered=new Set(),total=cases[1].state.viewport.total;
 for(const i of [1,2]){const v=cases[i].state.viewport;assert(v.total===total);for(let row=v.offset;row<Math.min(v.total,v.offset+v.height);row++)covered.add(row);}
 assert(covered.size===total&&[...covered].every(n=>n>=0&&n<total));
 result.sourceLineage={sourceCommit:build.sourceCommit,sourceMembers:69,binarySHA256:build.binarySHA256,buildReceiptSHA256:digest(buildRaw),captureReceiptSHA256:digest(captureRaw),checkpointSHA256:digest(casesRaw)};
 result.formRowCoverage={total,covered:covered.size};result.externalRequests=[];
 stage='browser initialization';
 await work('initialize BrowserServer',()=>chromium.launchServer({headless:true,host:'127.0.0.1',port:0,timeout:5000}),retainServer);
 await work('initialize browser connection',()=>chromium.connect(server.wsEndpoint(),{timeout:3000}),value=>{browser=value;lateClose('late browser connection close',value,()=>value.close());});
 await work('initialize context',()=>browser.newContext({serviceWorkers:'block',locale:'en-US',viewport:{width:1024,height:768}}),value=>{context=value;lateClose('late context close',value,()=>value.close());});
 await work('network refusal',()=>context.route('**/*',route=>{result.externalRequests.push('refused');return route.abort();}));
 await work('native path refusal',()=>context.addInitScript(()=>{window.__loopForbidden=[];window.WebSocket=class{constructor(){window.__loopForbidden.push('WebSocket');throw Error('Native socket refused');}};Object.defineProperty(navigator,'clipboard',{value:{readText(){window.__loopForbidden.push('clipboard-read');throw Error('Clipboard refused');},writeText(){window.__loopForbidden.push('clipboard-write');throw Error('Clipboard refused');}}});}));
 await work('initialize page',()=>context.newPage(),value=>{page=value;lateClose('late page close',value,()=>value.close({runBeforeUnload:false}));});page.setDefaultTimeout(4000);
 for(const [index,capture] of cases.entries()){
  stage='actual45 prefix replay';const bytes=stream.subarray(0,capture.bytesThrough);
  await work('terminal document',()=>page.setContent('<!doctype html><html lang="en"><meta charset="utf-8"><body style="margin:0;background:#101519"><div id="terminal" style="display:inline-block;padding:16px" aria-label="Original native45 PTY replay"></div></body></html>'));
  await work('terminal style',()=>page.addStyleTag({path:join(here,'node_modules/@xterm/xterm/css/xterm.css')}));
  await work('terminal script',()=>page.addScriptTag({path:join(here,'node_modules/@xterm/xterm/lib/xterm.js')}));
  await work('terminal initialize',()=>page.evaluate(()=>{window.__terminal?.dispose();window.__terminal=new Terminal({cols:45,rows:14,fontFamily:'"DejaVu Sans Mono", monospace',fontSize:14,scrollback:0,cursorBlink:false,allowTransparency:false,theme:{background:'#101519',foreground:'#e1e9ed'}});window.__terminal.open(document.getElementById('terminal'));}));
  await work('actual prefix write',()=>page.evaluate(async encoded=>{await new Promise(resolve=>window.__terminal.write(Uint8Array.from(atob(encoded),v=>v.charCodeAt(0)),resolve));},bytes.toString('base64')));
  await work('original layout settle',()=>page.evaluate(()=>new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('layout deadline')),1000);requestAnimationFrame(()=>requestAnimationFrame(()=>{clearTimeout(timer);resolve();}));})));
  const actual=await work('actual cells',()=>page.evaluate(({regions,modal})=>{
   const t=window.__terminal,b=t.buffer.active,cell=(x,y)=>{const c=b.getLine(y)?.getCell(x);return{chars:c?.getChars()??'',width:c?.getWidth()??null};};
   return{cols:t.cols,rows:t.rows,alternate:b.type==='alternate',text:Array.from({length:t.rows},(_,i)=>b.getLine(i)?.translateToString(false)??''),cursor:{x:b.cursorX,y:b.cursorY},
    regionRows:regions.filter(r=>['ctrl+c','q','enter','esc'].includes(r.key)).map(r=>({...r,visible:r.x>=0&&r.y>=0&&r.width>0&&r.height>0&&r.x+r.width<=t.cols&&r.y+r.height<=t.rows,text:b.getLine(r.y)?.translateToString(false,r.x,r.x+r.width)??''})),
    viewportRows:modal?Array.from({length:modal.height-2},(_,i)=>b.getLine(modal.y+1+i)?.translateToString(false,modal.x+2,modal.x+modal.width-2)??''):[],
    borderRows:modal?Array.from({length:modal.height},(_,i)=>({row:i,left:cell(modal.x,modal.y+i),right:cell(modal.x+modal.width-1,modal.y+i)})):[],forbidden:window.__loopForbidden??[]};
  },{regions:capture.state.regions,modal:capture.state.modal??null}));
  const expectedRawRows=capture.state.viewContent.replace(/\x1b\[[0-?]*[ -/]*[@-~]/g,'').split('\n');
  const matches=actual.text.map((line,i)=>i<expectedRawRows.length&&normalizeRow(line)===normalizeRow(expectedRawRows[i]));
  const item={grid:'45x14',name:capture.name,bytes:capture.bytesThrough,prefixSHA256:capture.prefixSHA256,actual,expectedRawRows,rowMatches:matches,modelRowsMatch:expectedRawRows.length===14&&matches.every(Boolean),inputMode:capture.state.inputMode,inputValue:capture.state.inputValue,inputFocused:capture.state.inputFocused,filter:capture.state.filter,viewport:capture.state.viewport};
  // Preserve the original image/cells before any paint/semantic assertion.
  stage='retain original45 PNG';const png=await work('native45 PNG',()=>page.locator('#terminal').screenshot({animations:'disabled'}));
  assert(await work('whole native budget',()=>retainedTotal())+png.length+Buffer.byteLength(JSON.stringify({...result,cases:[...result.cases,item]}))+131072<=16777216);
  item.png=`${index+1}-45x14-${capture.name}.png`;await work('PNG write',()=>writeFile(join(output,item.png),png,{mode:0o600,flag:'wx'}));result.cases.push(item);
  stage='actual row/field/footer assertions';assert(actual.cols===45&&actual.rows===14&&actual.alternate&&item.modelRowsMatch);assert.deepEqual(actual.forbidden,[]);
  const s=capture.state,visible=actual.viewportRows.join('\n');
  if(s.inputMode==='filter'){
   assert(s.inputFocused&&s.modal.width===45&&s.modal.x===0&&s.modal.y>=4&&s.modal.y+s.modal.height<=10);
   for(const edge of actual.borderRows){assert(edge.left.chars===(edge.row===0?'╔':edge.row===s.modal.height-1?'╚':'║')&&edge.right.chars===(edge.row===0?'╗':edge.row===s.modal.height-1?'╝':'║')&&edge.left.width===1&&edge.right.width===1);}
   assert(actual.regionRows.some(r=>r.key==='ctrl+c'&&r.visible&&r.y===13&&r.text.includes('[ctrl+c] Exit')));
   if(capture.name==='filter-help')assert(s.viewport.offset>0&&visible.includes('[Enter] Apply filter')&&visible.includes('[Esc] Back'));
   else assert(s.draftVisibleInViewport&&visible.includes(s.inputValue)&&s.viewport.offset===0);
  }
  if(capture.name==='leases-scrolled')assert(s.inputMode===''&&s.filter===''&&s.viewport.offset>=3);
  if(capture.name==='filter-visible')assert(s.inputValue==='probe-draft'&&s.filter==='');
  if(capture.name==='filter-help')assert(s.inputValue==='probe-draft'&&s.filter==='');
  if(capture.name==='filter-edited')assert(s.inputValue==='probe-draftq'&&s.filter==='');
  if(['leases-applied','leases-cancelled'].includes(capture.name))assert(s.filter==='probe-draftq'&&s.inputMode===''&&!s.inputFocused&&s.visibleLeaseIDs.length===0&&s.selected===0);
  if(capture.name==='filter-discard-draft')assert(s.inputValue==='probe-draftq-discard'&&s.filter==='probe-draftq');
  if(capture.name==='filter-quit-ready')assert(s.inputValue==='probe-draftq'&&s.filter==='probe-draftq');
  item.terminalDisposeReturned=await work('terminal dispose',()=>page.evaluate(()=>{window.__terminal.dispose();window.__terminal=null;return true;}));
 }
 assert(result.cases.length===8&&result.cases.every(c=>c.modelRowsMatch));result.comparedRows=result.cases.reduce((n,c)=>n+c.rowMatches.length,0);assert(result.comparedRows===112);assert.deepEqual(result.externalRequests,[]);
 await verifyRetained();for(const [file,h] of Object.entries(build.sourceSHA256))assert(digest(await work('source after replay',()=>readFile(join(repo,file))))===h);
 result.complete=true;
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
