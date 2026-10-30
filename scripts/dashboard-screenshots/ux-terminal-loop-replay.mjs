// SOURCE PREPARATION ONLY. Separate review/browser allocation required.
// Replays actual owned Program PTY bytes, never direct model text as screen data.
import assert from 'node:assert/strict';
import {readFile,writeFile,mkdir,readdir,stat,lstat} from 'node:fs/promises';
import {resolve,dirname,join,relative,sep} from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
import {chromium} from 'playwright';
const here=dirname(fileURLToPath(import.meta.url));
const root=resolve(process.argv[2]||'');
const repo=resolve(here,'../..');const rel=relative(repo,root);assert(process.argv[2]&&resolve(process.argv[2])===process.argv[2]&&(rel==='..'||rel.startsWith('..'+sep)));
const rootInfo=await lstat(root);assert(rootInfo.isDirectory()&&!rootInfo.isSymbolicLink()&&(rootInfo.mode&0o077)===0);
const exactGrid=(cols,rows)=>[[72,28],[100,35],[160,50]].some(([c,r])=>cols===c&&rows===r);
const digest=b=>createHash('sha256').update(b).digest('hex');
const json=async path=>JSON.parse(await readFile(path,'utf8'));
async function retained(path){let bytes=0;for(const e of await readdir(path,{withFileTypes:true})){const p=join(path,e.name);if(e.isSymbolicLink())throw Error('Unexpected artifact link');bytes+=e.isDirectory()?await retained(p):(await stat(p)).size;}return bytes;}
const output=join(root,'replay');
await mkdir(output,{mode:0o700}); // Refuse a prior/replacement render directory.
let browser,context,page;
let stage='capture receipt';
const result={complete:false,failure:null,cases:[],cleanup:{},limits:['Pinned xterm emulator of actual owned PTY bytes; not every physical terminal/font/platform.','Original model row/text comparator is separate from actual emulator cells. No OS descendant-reap or production Run startup/auth claim.']};
const started=Date.now();let timeout;
try{
 const receipt=await json(join(root,'capture-receipt.json'));assert(receipt.complete===true);
 const cases=await json(join(root,'checkpoints.json'));assert(cases.length===36&&cases.length<=48);
 stage='browser launch';browser=await chromium.launch({headless:true});
 context=await browser.newContext({serviceWorkers:'block',locale:'en-US',viewport:{width:1952,height:1032}});
 result.externalRequests=[];await context.route('**/*',route=>{result.externalRequests.push('refused');return route.abort();});
 await context.addInitScript(()=>{window.__loopForbidden=[];window.WebSocket=class{constructor(){window.__loopForbidden.push('WebSocket');throw Error('Native socket refused');}};Object.defineProperty(navigator,'clipboard',{value:{readText(){window.__loopForbidden.push('clipboard-read');throw Error('Native clipboard refused');},writeText(){window.__loopForbidden.push('clipboard-write');throw Error('Native clipboard refused');}}});});
 page=await context.newPage();page.setDefaultTimeout(4000);
 timeout=setTimeout(()=>{result.failure='original replay deadline';context?.close().catch(()=>{});},30000);
 for(const [index,c] of cases.entries()){
  assert(exactGrid(c.cols,c.rows));assert(c.bytesThrough<=4*1024*1024);
  stage='original stream hash';const full=await readFile(join(root,c.stream));const bytes=full.subarray(0,c.bytesThrough);assert.equal(digest(bytes),c.prefixSHA256);assert(!bytes.includes(Buffer.from('\x1b]52;')));
  stage='terminal initialize';
  await page.setContent('<!doctype html><html lang="en"><meta charset="utf-8"><body style="margin:0;background:#101519"><div id="terminal" style="display:inline-block;padding:16px" aria-label="Owned Program PTY replay"></div></body></html>');
  await page.addStyleTag({path:join(here,'node_modules/@xterm/xterm/css/xterm.css')});
  await page.addScriptTag({path:join(here,'node_modules/@xterm/xterm/lib/xterm.js')});
  await page.evaluate(({cols,rows})=>{window.__terminal?.dispose();window.__terminal=new Terminal({cols,rows,fontFamily:'"DejaVu Sans Mono", monospace',fontSize:14,scrollback:0,cursorBlink:false,allowTransparency:false,theme:{background:'#101519',foreground:'#e1e9ed'}});window.__terminal.open(document.getElementById('terminal'));},{cols:c.cols,rows:c.rows});
  stage='actual PTY byte replay';let offset=0;
  const write=async data=>{if(data.length)await page.evaluate(async encoded=>{const b=Uint8Array.from(atob(encoded),v=>v.charCodeAt(0));await new Promise(resolve=>window.__terminal.write(b,resolve));},data.toString('base64'));};
  for(const event of c.resizes){assert(event.offset>=offset&&event.offset<=bytes.length);assert(exactGrid(event.cols,event.rows));await write(bytes.subarray(offset,event.offset));await page.evaluate(({cols,rows})=>window.__terminal.resize(cols,rows),event);offset=event.offset;}
  await write(bytes.subarray(offset));
  await page.evaluate(()=>new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('layout deadline')),1000);requestAnimationFrame(()=>requestAnimationFrame(()=>{clearTimeout(timer);resolve();}));}));
  stage='actual cells and model comparator';
  const actual=await page.evaluate(()=>{const t=window.__terminal,b=t.buffer.active;return{cols:t.cols,rows:t.rows,alternate:b.type==='alternate',text:Array.from({length:t.rows},(_,i)=>b.getLine(i)?.translateToString(true)??''),wideSamples:(()=>{const a=[];for(let y=0;y<t.rows;y++)for(let x=0;x<t.cols;x++){const c=b.getLine(y)?.getCell(x);if(c&&(c.getWidth()!==1||c.getChars().length>1)&&a.length<96)a.push({row:y,col:x,chars:c.getChars(),width:c.getWidth()});}return a;})(),rightEdgeSamples:Array.from({length:Math.min(32,t.rows)},(_,y)=>{const c=b.getLine(y)?.getCell(t.cols-1);return{row:y,col:t.cols-1,chars:c?.getChars()??'',width:c?.getWidth()??null};}),cellSampleQualification:'At most96 wide/multi-character cells and32 right-edge cells; no exhaustive glyph-paint proof',forbidden:window.__loopForbidden??[]};});
  const expected=c.state.viewContent.replace(/\x1b\[[0-?]*[ -/]*[@-~]/g,'').split('\n').map(x=>x.trimEnd());
  const rowMatches=actual.text.map((s,i)=>s===expected[i]);
  const visible=actual.text.join('\n');
  const tail=['inline-follow-tail','inline-paused-tail','inline-end-tail','full-end-tail'].includes(c.name);
  const first=c.name==='full-first',middle=c.name==='full-middle-resize';
  const item={grid:c.grid,name:c.name,sourcePrefixSHA256:c.prefixSHA256,bytes:c.bytesThrough,actual,modelRowsMatch:rowMatches.every(Boolean)&&expected.length===c.rows,rowMatches,tailRequired:tail,logFinalVisible:visible.includes('LOG FINAL'),logFirstVisible:visible.includes('LOG FIRST'),logMiddleVisible:visible.includes('LOG MIDDLE'),model:c.state.reader,resizeTimeline:c.resizes};
  stage='original replay PNG';const png=await page.locator('#terminal').screenshot({animations:'disabled'});const receiptReserve=Buffer.byteLength(JSON.stringify({...result,cases:[...result.cases,item]}))+65536;if(await retained(root)+png.length+receiptReserve>16*1024*1024)throw Error('retained artifact budget');
  item.png=`${String(index+1).padStart(2,'0')}-${c.grid}-${c.name}.png`;await writeFile(join(output,item.png),png,{mode:0o600});result.cases.push(item);
  assert(actual.cols===c.cols&&actual.rows===c.rows&&actual.alternate);assert.deepEqual(actual.forbidden,[]);assert(item.modelRowsMatch);if(tail)assert(item.logFinalVisible);if(first)assert(item.logFirstVisible);if(middle)assert(item.logMiddleVisible);item.terminalDisposeReturned=await page.evaluate(()=>{window.__terminal.dispose();window.__terminal=null;return true;});
 }
 assert.deepEqual(result.externalRequests,[]);result.complete=result.failure===null;if(!result.complete)process.exitCode=1;
}catch(error){result.failure??=stage;process.exitCode=1;}
finally{
 clearTimeout(timeout);if(page){result.cleanup.lastTerminalDisposeReturned=await page.evaluate(()=>{window.__terminal?.dispose();window.__terminal=null;return true;}).catch(()=>false);}
 for(const [name,value] of [['contextClosed',context],['browserClosed',browser]]){try{if(value){await value.close();result.cleanup[name]=true;}else result.cleanup[name]=false;}catch{result.cleanup[name]=false;result.complete=false;process.exitCode=1;}}
 if(result.failure){result.complete=false;process.exitCode=1;}result.elapsedMS=Date.now()-started;
 await writeFile(join(output,'replay-receipt.json'),JSON.stringify(result,null,2)+'\n',{mode:0o600});
}
console.log(JSON.stringify({complete:result.complete,cases:result.cases.length,failure:result.failure,cleanup:result.cleanup}));
