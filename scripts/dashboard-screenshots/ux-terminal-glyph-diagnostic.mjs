// SOURCE PREPARATION ONLY. Separate review/browser allocation required.
// Four provider-only diagnostic cases; original PTY prefixes remain read-only.
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
const inputBudgetRoot=resolve(process.argv[6]||''),acceptedBudgetRoot=resolve(process.argv[8]||''),repo=resolve(here,'../..');
const outsideOf=(parent,path)=>{const rel=relative(parent,path);return rel==='..'||rel.startsWith('..'+sep);};
const normalizeRow=text=>text.replace(/\u0020+$/u,'');
const result={complete:false,failure:null,cases:[],pairs:[],operations:[],cleanup:{},scope:'Provider-only diagnostic; no ARM output/font/pin modification or resolved-font proof.',
 retainedBudget:{limitBytes:16777216,scope:'Entire immutable --retained-input-root plus fresh --output; future wrapper metadata also shares this cap.'},
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
const exactGrid=(cols,rows)=>[[72,28],[100,35],[160,50]].some(([c,r])=>cols===c&&rows===r);
const digest=b=>createHash('sha256').update(b).digest('hex');
async function retained(path){let bytes=0;for(const e of await readdir(path,{withFileTypes:true})){const p=join(path,e.name);if(e.isSymbolicLink())throw Error('Unexpected artifact link');bytes+=e.isDirectory()?await retained(p):(await stat(p)).size;}return bytes;}
// Budget the entire immutable input-attempt container plus this fresh replay.
// Input and output are disjoint; every write below targets output exclusively.
async function retainedTotal(){return await retained(inputBudgetRoot)+await retained(acceptedBudgetRoot)+await retained(output);}
try{
 assert(process.argv.length===9&&process.argv[3]==='--output'&&process.argv[5]==='--retained-input-root'&&process.argv[7]==='--also-retained-input-root');
 assert(process.argv[2]===root&&process.argv[4]===output&&process.argv[6]===inputBudgetRoot&&process.argv[8]===acceptedBudgetRoot);
 assert(outsideOf(repo,root)&&outsideOf(repo,output)&&outsideOf(repo,inputBudgetRoot)&&outsideOf(repo,acceptedBudgetRoot));
 assert(!outsideOf(inputBudgetRoot,root)&&outsideOf(inputBudgetRoot,output)&&outsideOf(output,inputBudgetRoot));
 assert(outsideOf(inputBudgetRoot,acceptedBudgetRoot)&&outsideOf(acceptedBudgetRoot,inputBudgetRoot)&&outsideOf(acceptedBudgetRoot,output)&&outsideOf(output,acceptedBudgetRoot));
 for(const value of [root,inputBudgetRoot,acceptedBudgetRoot]){
  const info=await work('private input root',()=>lstat(value));assert(info.isDirectory()&&!info.isSymbolicLink()&&(info.mode&0o077)===0);
  assert(await work('resolved input root',()=>realpath(value))===value);
 }
 assert(await work('resolved output parent',()=>realpath(dirname(output)))===dirname(output));
 await work('new replay directory',()=>mkdir(output,{mode:0o700}),()=>{outputCreated=true;});
 assert(await work('receipt reserve',()=>retainedTotal())+131072<16*1024*1024);deadlineReceiptAllowed=true;
 stage='capture receipt';
 const captureRaw=await work('capture receipt',()=>readFile(join(root,'capture-receipt.json')));const receipt=JSON.parse(captureRaw);assert(receipt.complete===true);
 const casesRaw=await work('checkpoint receipt',()=>readFile(join(root,'checkpoints.json')));const cases=JSON.parse(casesRaw);assert(cases.length===36&&cases.length<=48);
 const buildRaw=await work('original build receipt',()=>readFile(join(root,'build-receipt.json')));const build=JSON.parse(buildRaw);
 assert(build.sourceCommit==='8d1039a71ffc401bca3405ee7b49228eea9012e9'&&build.testSelector==='^TestUXTerminalLoopFixture$'&&Object.keys(build.sourceSHA256).length===57);
 // Preserve the original compiled/capture cut as provenance. It does not cover
 // this corrected replay; its separately reviewed self digest identifies it.
 result.sourceLineage={originalCompiledCapture:{sourceCommit:build.sourceCommit,sourceEntries:57,binarySHA256:build.binarySHA256,buildReceiptSHA256:digest(buildRaw),captureReceiptSHA256:digest(captureRaw),checkpointsSHA256:digest(casesRaw),originalReplaySourceSHA256:build.sourceSHA256['scripts/dashboard-screenshots/ux-terminal-loop-replay.mjs']},correctedReplay:{sourceFile:relative(repo,ownSource),sourceSHA256:digest(await work('corrected replay source',()=>readFile(ownSource))),role:'Replay-only source; not covered by original compile receipt. Input receipts and captured bytes remain read-only.'}};
 stage='diagnostic source/input binding';
 const providerRaw=await work('derived provider source',()=>readFile(join(here,'ux-terminal-unicode6.js')));
 const licenseRaw=await work('derived provider license',()=>readFile(join(here,'ux-terminal-unicode6.LICENSE')));
 const acceptedRaw=await work('accepted replay receipt',()=>readFile(join(acceptedBudgetRoot,'replay/replay-receipt.json')));
 const accepted=JSON.parse(acceptedRaw);assert(accepted.complete===true&&accepted.cases.length===36&&accepted.cleanup.accepted===true);
 const acceptedMarker=JSON.parse(await work('accepted write marker',()=>readFile(join(acceptedBudgetRoot,'replay/replay-receipt-written.json'))));
 assert(acceptedMarker.complete===true&&acceptedMarker.originalWriteReturned===true&&acceptedMarker.receiptSHA256===digest(acceptedRaw));
 const selected=[[72,28],[160,50]].map(([cols,rows])=>{const matches=cases.filter(c=>c.cols===cols&&c.rows===rows&&c.name==='full-first');assert(matches.length===1);return matches[0];});
 const diagnosticCases=selected.flatMap(c=>['builtin-6','fixture-width2'].map(provider=>({capture:c,provider})));
 assert(diagnosticCases.length===4);
 result.sourceLineage.diagnostic={sourceFile:relative(repo,ownSource),sourceSHA256:result.sourceLineage.correctedReplay.sourceSHA256,providerSHA256:digest(providerRaw),licenseSHA256:digest(licenseRaw),acceptedReplayReceiptSHA256:digest(acceptedRaw)};
 delete result.sourceLineage.correctedReplay;
 result.retainedBudget.scope='Immutable original capture container plus accepted replay container plus fresh diagnostic output; wrapper metadata also shares16MiB.';
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

 for(const [index,diagnostic] of diagnosticCases.entries()){
  const c=diagnostic.capture,variant=diagnostic.provider==='fixture-width2';
  assert(exactGrid(c.cols,c.rows)&&c.name==='full-first'&&c.bytesThrough<=4*1024*1024);
  stage='original stream hash';const full=await work('read original stream',()=>readFile(join(root,c.stream)));const bytes=full.subarray(0,c.bytesThrough);assert.equal(digest(bytes),c.prefixSHA256);assert(!bytes.includes(Buffer.from('\x1b]52;')));
  stage='diagnostic terminal initialize';
  await work('terminal document',()=>page.setContent('<!doctype html><html lang="en"><meta charset="utf-8"><body style="margin:0;background:#101519"><div id="terminal" style="display:inline-block;padding:16px" aria-label="Owned Program PTY provider diagnostic"></div></body></html>'));
  await work('terminal style',()=>page.addStyleTag({path:join(here,'node_modules/@xterm/xterm/css/xterm.css')}));
  await work('terminal script',()=>page.addScriptTag({path:join(here,'node_modules/@xterm/xterm/lib/xterm.js')}));
  await work('derived provider script',()=>page.addScriptTag({content:providerRaw.toString('utf8')}));
  const provider=await work('provider selection',()=>page.evaluate(({cols,rows,variant})=>{
   window.__terminal?.dispose();
   const t=window.__terminal=new Terminal({cols,rows,fontFamily:'"DejaVu Sans Mono", monospace',fontSize:14,scrollback:0,cursorBlink:false,allowTransparency:false,allowProposedApi:true,theme:{background:'#101519',foreground:'#e1e9ed'}});
   const before=t.unicode.activeVersion;
   if(variant){t.unicode.register(new ARMUnicode6Diagnostic.FixtureEmojiWidth2());t.unicode.activeVersion='fixture-only-U1F642-width2';}
   const result={before,active:t.unicode.activeVersion,versions:[...t.unicode.versions]};
   t.open(document.getElementById('terminal'));return result;
  },{cols:c.cols,rows:c.rows,variant}));
  stage='actual PTY byte replay';let offset=0;
  const write=async data=>{if(data.length)await work('terminal byte write',()=>page.evaluate(async encoded=>{const b=Uint8Array.from(atob(encoded),v=>v.charCodeAt(0));await new Promise(resolve=>window.__terminal.write(b,resolve));},data.toString('base64')));};
  for(const event of c.resizes){assert(event.offset>=offset&&event.offset<=bytes.length&&exactGrid(event.cols,event.rows));await write(bytes.subarray(offset,event.offset));await work('terminal resize',()=>page.evaluate(({cols,rows})=>window.__terminal.resize(cols,rows),event));offset=event.offset;}
  await write(bytes.subarray(offset));
  await work('layout wait',()=>page.evaluate(()=>new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('layout deadline')),1000);requestAnimationFrame(()=>requestAnimationFrame(()=>{clearTimeout(timer);resolve();}));})));
  stage='raw diagnostic cells and CSS';
  const actual=await work('diagnostic observations',()=>page.evaluate(()=>{
   const t=window.__terminal,b=t.buffer.active,row=2,start=28,end=39;
   const span=[...(document.querySelector('.xterm-rows')?.children[row]?.querySelectorAll('span')??[])].find(n=>n.textContent?.includes('🙂'));
   const css=span?getComputedStyle(span):null;
   return {cols:t.cols,rows:t.rows,alternate:b.type==='alternate',activeVersion:t.unicode.activeVersion,text:Array.from({length:t.rows},(_,i)=>b.getLine(i)?.translateToString(true)??''),glyphRegion:{row,start,endExclusive:end,cells:Array.from({length:end-start},(_,i)=>{const x=start+i,c=b.getLine(row)?.getCell(x);return{col:x,chars:c?.getChars()??null,width:c?.getWidth()??null};})},requestedFont:{family:t.options.fontFamily,size:t.options.fontSize},computedCSS:css?{family:css.fontFamily,size:css.fontSize,weight:css.fontWeight,style:css.fontStyle,letterSpacing:css.letterSpacing}:null,qualification:'Computed CSS is observation, not resolved glyph-font/fallback proof. No CDP/font scanning or pixel acceptance.',forbidden:window.__loopForbidden??[]};
  }));
  const expectedRaw=c.state.viewContent.replace(/\x1b\[[0-?]*[ -/]*[@-~]/g,'').split('\n');
  const rowMatches=actual.text.map((s,i)=>i<expectedRaw.length&&normalizeRow(s)===normalizeRow(expectedRaw[i]));
  const cell=col=>actual.glyphRegion.cells.find(x=>x.col===col);
  const item={grid:c.grid,name:c.name,provider:diagnostic.provider,providerSelection:provider,sourcePrefixSHA256:c.prefixSHA256,bytes:c.bytesThrough,resizeTimeline:c.resizes,actual,expectedRawRows:expectedRaw,rowMatches,modelRowsMatch:rowMatches.every(Boolean)&&expectedRaw.length===c.rows,logFirstVisible:actual.text.join('\n').includes('LOG FIRST'),paintAccepted:null};
  stage='diagnostic PNG';const png=await work('original diagnostic PNG',()=>page.locator('#terminal').screenshot({animations:'disabled'}));
  const reserve=Buffer.byteLength(JSON.stringify({...result,cases:[...result.cases,item]}))+131072;
  assert(await work('combined retained bytes',()=>retainedTotal())+png.length+reserve<=16*1024*1024);
  item.png=`${index+1}-${c.grid}-${diagnostic.provider}.png`;await work('retain diagnostic PNG',()=>writeFile(join(output,item.png),png,{mode:0o600,flag:'wx'}));result.cases.push(item);
  stage='provider/row/marker/cell assertions';
  assert.equal(provider.before,'6');assert.equal(provider.active,variant?'fixture-only-U1F642-width2':'6');assert.equal(actual.activeVersion,provider.active);
  assert(actual.cols===c.cols&&actual.rows===c.rows&&actual.alternate&&actual.computedCSS);assert.deepEqual(actual.forbidden,[]);assert(item.modelRowsMatch&&item.logFirstVisible);
  assert(cell(30).chars==='界'&&cell(30).width===2&&cell(31).width===0&&cell(33).chars==='🙂'&&cell(33).width===(variant?2:1));
  assert(cell(34).width===(variant?0:1));assert(actual.glyphRegion.cells.every(x=>x.chars!==null&&x.width!==null));
  if(variant){
   const baseline=result.cases.at(-2);assert(baseline.grid===c.grid&&baseline.provider==='builtin-6'&&baseline.sourcePrefixSHA256===item.sourcePrefixSHA256);
   const rawRowMatches=baseline.actual.text.map((text,i)=>text===actual.text[i]);
   const normalizedRowsMatch=baseline.actual.text.map((text,i)=>normalizeRow(text)===normalizeRow(actual.text[i]));
   const cssKeys=['family','size','weight','style'];
   const pair={grid:c.grid,sameInputPrefix:true,rawRowMatches,normalizedRowsMatch,requestedFontSame:JSON.stringify(baseline.actual.requestedFont)===JSON.stringify(actual.requestedFont),computedFontCSSSame:cssKeys.every(k=>baseline.actual.computedCSS[k]===actual.computedCSS[k]),paintAccepted:null,qualification:'Only provider allocation assertions passed; inspect both saved PNGs before paint attribution. CSS does not identify a resolved font.'};
   result.pairs.push(pair);assert(pair.requestedFontSame&&pair.computedFontCSSSame&&normalizedRowsMatch.every(Boolean));
  }
  item.terminalDisposeReturned=await work('terminal dispose',()=>page.evaluate(()=>{window.__terminal.dispose();window.__terminal=null;return true;}));
 }
 assert(result.cases.length===4&&result.pairs.length===2);
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
