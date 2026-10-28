import test from 'node:test';
import assert from 'node:assert/strict';
import {controlUI} from './control-test-ui.mjs';
import {createRunsConsole,cleanRunText} from './runs.mjs';
const id='a'.repeat(32),other='b'.repeat(32),run={id,name:'readable-run',command:['test','--all'],agent:'agent',reason:'Verification',state:'active',log_bytes:0};
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function fixture(t,handler,fullscreen=false){const ui=controlUI();ui.toolbar=(label,children)=>ui.stack(children,{kind:'Toolbar',label});ui.disclosure=(label,children=[],props={})=>ui.stack(children,{kind:'AccordionItem',label,...props});ui.code=value=>{const code=ui.text('',{kind:'Code',value,scrolls:0});code.scrollEnd=()=>code.scrolls++;return code;};if(fullscreen)installFullscreen(ui);let online=true,source={};const calls=[],panel=createRunsConsole({ui,root:ui.stack(),connected:()=>online,context:()=>source,call:async(op,args)=>{calls.push([op,args]);return handler?handler(op,args):op==='runs'?[run]:{run,offset:args.offset,next_offset:args.offset,data:'',done:false};}});t.after(()=>panel.dispose());return {ui,panel,calls,offline(){online=false;source={};panel.updateConnection();}};}
const encoded=text=>Buffer.from(text).toString('base64');
test('Runs loads only while visible and opens output following by default',async t=>{const f=fixture(t);await f.panel.refresh();assert.equal(f.calls.length,0);f.panel.setActive(true);await tick();assert.equal(f.calls[0][0],'runs');await f.ui.get('Open readable-run').emit('activate');assert.equal(f.calls.at(-1)[0],'run_output');assert.ok(f.ui.get('Pause following'));assert.equal(f.ui.get('Next output page').disabled,true);});
test('terminal output drains pages and byte cursor preserves split UTF-8',async t=>{
 const data=Buffer.from('A🙂B'),f=fixture(t,(op,args)=>op==='runs'?[run]:{run:{...run,state:'terminal',log_bytes:data.length},offset:args.offset,next_offset:Math.min(data.length,args.offset+3),data:data.subarray(args.offset,args.offset+3).toString('base64'),done:true});
 f.panel.setActive(true);await tick();await f.panel.open(run);assert.equal(f.calls.at(-1)[1].offset,0);await f.panel.refresh();assert.equal(f.calls.at(-1)[1].offset,3);assert.equal(f.ui.all.filter(x=>x.kind==='Code').at(-1).value,'A🙂B');assert.equal(f.ui.get('Next output page').disabled,true);
 const count=f.calls.filter(([op])=>op==='run_output').length;await f.panel.refresh();assert.equal(f.calls.filter(([op])=>op==='run_output').length,count);
});
test('pause stops output reads and explicit next page advances; read-from-start resets cursor',async t=>{
 let n=0;const f=fixture(t,(op,args)=>op==='runs'?[run]:{run:{...run,log_bytes:100},offset:args.offset,next_offset:args.offset+1,data:encoded(String(++n)),done:false});f.panel.setActive(true);await tick();await f.panel.open(run);await f.ui.get('Pause following').emit('activate');const count=n;await f.panel.refresh();assert.equal(n,count);await f.ui.get('Next output page').emit('activate');assert.equal(f.calls.at(-1)[1].offset,1);await f.ui.get('Read from start').emit('activate');assert.equal(f.calls.at(-1)[1].offset,0);assert.ok(f.ui.get('Follow output'));assert.equal(f.ui.all.filter(x=>x.kind==='Code'&&!x.disposed).at(-1).scrolls,1);
});
test('late output cannot mix into replacement selection or a disconnected session',async t=>{
 let resolve,pending=false;const f=fixture(t,(op,args)=>op==='runs'?[run]:pending?new Promise(done=>resolve=done):{run:{...run,id:args.id},offset:0,next_offset:1,data:encoded('B'),done:false});f.panel.setActive(true);await tick();pending=true;const first=f.panel.open(run);await tick();pending=false;await f.panel.open({...run,id:other});resolve({run,offset:0,next_offset:6,data:encoded('STALE!'),done:false});await first;assert.equal(f.ui.all.filter(x=>x.kind==='Code'&&!x.disposed).at(-1).value,'B');
 pending=true;const read=f.panel.refresh();await tick();f.offline();resolve({run:{...run,id:other},offset:1,next_offset:7,data:encoded('STALE!'),done:false});await read;assert.equal(f.ui.all.filter(x=>x.kind==='Code'&&!x.disposed).at(-1).value,'B');
});
test('log display is bounded and reporter loss never claims process completion',async t=>{
 const chunk='x'.repeat(65536);const f=fixture(t,(op,args)=>op==='runs'?[run]:{run:{...run,state:'interrupted',outcome:'reporter_lost',log_bytes:1e6,truncated:true},offset:args.offset,next_offset:args.offset+chunk.length,data:encoded(chunk),done:true});f.panel.setActive(true);await tick();await f.panel.open(run);for(let i=0;i<5;i++)await f.panel.refresh();const code=f.ui.all.filter(x=>x.kind==='Code'&&!x.disposed).at(-1);assert.equal(code.value.length,256*1024);assert.match(f.ui.labels(),/display shows latest 256 KiB/);assert.match(f.ui.labels(),/Reporter lost; process completion is unknown/);assert.equal(f.ui.get('Reporter lost; process completion is unknown').hidden,false);
});
test('output control sequences are escaped; html remains literal stock Code data',()=>{assert.equal(cleanRunText('\x1b[2J\n<script>\r\n\u202e'),String.raw`\u001b[2J`+'\n<script>\n'+String.raw`\u202e`);});
test('live refresh keeps focused run and output controls rather than replacing them',async t=>{const f=fixture(t);f.panel.setActive(true);await tick();const opener=f.ui.get('Open readable-run');opener.focus();await f.panel.refresh();assert.equal(f.ui.get('Open readable-run'),opener);assert.equal(f.ui.activeComponent,opener);await opener.emit('activate');const pause=f.ui.get('Pause following');pause.focus();await f.panel.refresh();assert.equal(f.ui.get('Pause following'),pause);assert.equal(f.ui.activeComponent,pause);});
test('read from start leaves output paused while scheduled inventory polling continues',async t=>{
 let state='active';const f=fixture(t,(op,args)=>op==='runs'?[{...run,state}]:{run:{...run,state,log_bytes:20},offset:args.offset,next_offset:args.offset+1,data:encoded('x'),done:false});
 f.panel.setActive(true);await tick();await f.panel.open(run);await f.ui.get('Read from start').emit('activate');
 const inventoryReads=f.calls.filter(([op])=>op==='runs').length,outputReads=f.calls.filter(([op])=>op==='run_output').length;state='terminal';
 await new Promise(resolve=>setTimeout(resolve,1100));
 assert.ok(f.calls.filter(([op])=>op==='runs').length>inventoryReads);assert.equal(f.calls.filter(([op])=>op==='run_output').length,outputReads);assert.equal(f.panel.selected.state,'terminal');assert.ok(f.ui.get('Follow output'));
});
for(const terminal of [false,true])test(`${terminal?'terminal':'active'} saved backlog catches up in bounded scheduled pages`,async t=>{
 const data=Buffer.from('abcdefghijkl');let pages=0;
 const f=fixture(t,(op,args)=>op==='runs'?[run]:{run:{...run,state:terminal?'terminal':'active',log_bytes:data.length},offset:args.offset,next_offset:Math.min(data.length,args.offset+3),data:data.subarray(args.offset,args.offset+3).toString('base64'),done:terminal});
 const original=f.panel;f.panel.setActive(true);await tick();await original.open(run);
 assert.equal(f.calls.filter(([op])=>op==='run_output').length,1,'catch-up must yield after one page');
 for(let i=0;i<50&&f.calls.filter(([op])=>op==='run_output').length<4;i++)await new Promise(resolve=>setTimeout(resolve,5));
 pages=f.calls.filter(([op])=>op==='run_output').length;assert.equal(pages,4);assert.equal(f.calls.filter(([op])=>op==='runs').length,2,'rapid pages must reuse inventory');
 assert.equal(f.ui.all.filter(x=>x.kind==='Code'&&!x.disposed).at(-1).value,'abcdefghijkl');
 await new Promise(resolve=>setTimeout(resolve,20));assert.equal(f.calls.filter(([op])=>op==='run_output').length,4,'caught-up reader must return to ordinary polling');
});
test('pause cancels an already scheduled rapid catch-up page',async t=>{
 const f=fixture(t,(op,args)=>op==='runs'?[run]:{run:{...run,log_bytes:100},offset:args.offset,next_offset:args.offset+1,data:encoded('x'),done:false});
 f.panel.setActive(true);await tick();await f.panel.open(run);await f.ui.get('Pause following').emit('activate');await new Promise(resolve=>setTimeout(resolve,20));assert.equal(f.calls.filter(([op])=>op==='run_output').length,1);
});
test('explicit open brings run details into focus once and close restores the current opener',async t=>{
 const f=fixture(t);f.panel.setActive(true);await tick();const opener=f.ui.get('Open readable-run');opener.focus();
 await opener.emit('activate');const heading=f.ui.activeComponent;assert.equal(heading.kind,'Heading');assert.equal(heading.label,'readable-run');
 await f.panel.refresh();assert.equal(f.ui.activeComponent,heading,'polling must preserve reading focus');
 const pause=f.ui.get('Pause following');pause.focus();await f.panel.refresh();assert.equal(f.ui.activeComponent,pause,'output updates must not steal control focus');
 await f.ui.get('Close output').emit('activate');assert.equal(f.ui.activeComponent,f.ui.get('Open readable-run'));assert.equal(f.ui.activeComponent,opener);
});
test('closing a run whose opener was filtered out focuses the surviving filter',async t=>{
 const f=fixture(t);f.panel.setActive(true);await tick();await f.ui.get('Open readable-run').emit('activate');
 const filter=f.ui.get('Filter runs, commands or agents');filter.value='no matching run';await filter.emit('input');
 await f.ui.get('Close output').emit('activate');assert.equal(f.ui.activeComponent,filter);
});

function installFullscreen(ui){
 const facts=new Map(),listeners=new Set(),FS='https://semblance.dev/ns/fullscreen#fullViewport';
 const term=value=>({value}),triple=(s,p,value)=>({subject:term(s),predicate:term(p),object:term(String(value))});
 ui.put=(subject,predicate,value)=>{const key=subject+'|'+predicate,old=facts.get(key);facts.set(key,value);if(old===value)return;const delta={add:[triple(subject,predicate,value)],remove:old===undefined?[]:[triple(subject,predicate,old)]};for(const fn of listeners)fn(undefined,delta);};
 ui.app={runtime:{onInput(fn){listeners.add(fn);return ()=>listeners.delete(fn);}},store:{factory:{namedNode:term,defaultGraph:()=>({})},match(subject,predicate){const value=facts.get(subject.value+'|'+predicate.value);return value===undefined?[]:[triple(subject.value,predicate.value,value)];}}};
 const stack=ui.stack;ui.stack=(...args)=>{const h=stack(...args);h.iri='urn:run-test:'+h.id;const set=h.set.bind(h);h.set=props=>{set(props);if('fullViewport'in props)ui.put(h.iri,FS,props.fullViewport);return h;};return h;};
 ui.exitFullscreen=()=>{const surface=ui.all.find(x=>x.fullViewport===true&&!x.disposed);assert.ok(surface);ui.put(surface.iri,FS,false);};
 ui.fullscreenListeners=listeners;
}
test('folded output retains capture, cursor and following across updates',async t=>{
 let bytes=0;const f=fixture(t,(op,args)=>op==='runs'?[run]:{run:{...run,log_bytes:++bytes},offset:args.offset,next_offset:args.offset+1,data:encoded('x'),done:false});
 f.panel.setActive(true);await tick();await f.panel.open(run);const fold=f.ui.get('Saved log output');assert.equal(fold.open,true);fold.open=false;const code=f.ui.all.filter(x=>x.kind==='Code'&&!x.disposed).at(-1),scrolls=code.scrolls;
 await f.panel.refresh();assert.equal(fold.open,false);assert.equal(code.value,'xx');assert.equal(code.scrolls,scrolls,'hidden output must not scroll');assert.equal(f.calls.at(-1)[1].offset,1);fold.open=true;await f.panel.refresh();assert.equal(code.value,'xxx');assert.ok(f.ui.get('Pause following'));
});
test('fullscreen shares the transcript and restores focus/folding through button or stock Escape',async t=>{
 const f=fixture(t,undefined,true);f.panel.setActive(true);await tick();await f.panel.open(run);const fold=f.ui.get('Saved log output'),code=f.ui.all.filter(x=>x.kind==='Code'&&!x.disposed).at(-1);fold.open=false;
 const expand=f.ui.get('Fullscreen log'),calls=f.calls.length;await expand.emit('activate');const surface=f.ui.all.find(x=>x.fullViewport===true&&!x.disposed);assert.ok(surface);assert.equal(fold.open,true);assert.equal(f.ui.activeComponent,expand);assert.equal(f.calls.length,calls,'fullscreen must not start another poller');
 await f.panel.refresh();assert.equal(f.ui.all.filter(x=>x.kind==='Code'&&!x.disposed).at(-1),code);assert.equal(f.ui.activeComponent,expand);
 f.ui.exitFullscreen();await tick();assert.equal(surface.fullViewport,false);assert.equal(expand.label,'Fullscreen log');assert.equal(fold.open,false);assert.equal(f.ui.activeComponent,expand);
 await expand.emit('activate');await f.ui.get('Exit fullscreen').emit('activate');assert.equal(surface.fullViewport,false);assert.equal(fold.open,false);assert.equal(f.ui.activeComponent,expand);
});
for(const mode of ['hide','reset','dispose'])test(`fullscreen retracts its graph fact before ${mode}`,async t=>{
 const f=fixture(t,undefined,true);f.panel.setActive(true);await tick();await f.panel.open(run);await f.ui.get('Fullscreen log').emit('activate');const surface=f.ui.all.find(x=>x.fullViewport===true&&!x.disposed);
 if(mode==='hide')f.panel.setActive(false);else f.panel[mode]();assert.equal(surface.fullViewport,false);await tick();assert.ok(!f.ui.all.some(x=>x.fullViewport===true&&!x.disposed));if(mode==='dispose')assert.equal(f.ui.fullscreenListeners.size,0);
});
