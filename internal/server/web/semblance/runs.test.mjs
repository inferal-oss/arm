import test from 'node:test';
import assert from 'node:assert/strict';
import {controlUI} from './control-test-ui.mjs';
import {createRunsConsole,cleanRunText} from './runs.mjs';
const id='a'.repeat(32),other='b'.repeat(32),run={id,name:'readable-run',command:['test','--all'],agent:'agent',reason:'Verification',state:'active',log_bytes:0};
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function fixture(t,handler,fullscreen=false){const ui=controlUI();const stack=ui.stack;ui.stack=(...args)=>{const h=stack(...args),dispose=h.dispose.bind(h);h.dispose=()=>{if(h.disposed)return;for(const child of h.children)child.dispose();dispose();};return h;};ui.iconButton=(label,icon,fn,props)=>ui.button(label,fn,props);ui.tileWindows=value=>{ui.tiled=value;};ui.component=(kind,props)=>ui.text('',{kind,...props});ui.toolbar=(label,children)=>ui.stack(children,{kind:'Toolbar',label});ui.disclosure=(label,children=[],props={})=>ui.stack(children,{kind:'AccordionItem',label,...props});ui.detachable=(label,content)=>{const h=ui.stack([content],{kind:'Tabs',label,detached:false});h.popOut=async()=>{h.detached=true;await h.emit('detach');};h.dock=async()=>{h.detached=false;await h.emit('detach');};return h;};ui.code=value=>{const code=ui.text('',{kind:'Code',value,scrolls:0});code.scrollEnd=()=>code.scrolls++;return code;};if(fullscreen)installFullscreen(ui);let online=true,source={};const calls=[],panel=createRunsConsole({ui,root:ui.stack(),connected:()=>online,context:()=>source,call:async(op,args)=>{calls.push([op,args]);return handler?handler(op,args):op==='runs'?[run]:{run,offset:args.offset,next_offset:args.offset,data:'',done:false};}});t.after(()=>panel.dispose());return {ui,panel,calls,offline(){online=false;source={};panel.updateConnection();},reconnect(){online=true;source={};panel.updateConnection();}};}
const encoded=text=>Buffer.from(text).toString('base64');
test('Runs loads only while visible and opens output following by default',async t=>{const f=fixture(t);await f.panel.refresh();assert.equal(f.calls.length,0);f.panel.setActive(true);await tick();assert.equal(f.calls[0][0],'runs');await f.ui.get('Verification').emit('activate');assert.equal(f.calls.at(-1)[0],'run_output');assert.ok(f.ui.get('Pause following'));assert.equal(f.ui.get('Next output page').disabled,true);});
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
test('live refresh keeps focused run and output controls rather than replacing them',async t=>{const f=fixture(t);f.panel.setActive(true);await tick();const opener=f.ui.get('Verification');opener.focus();await f.panel.refresh();assert.equal(f.ui.get('Verification'),opener);assert.equal(f.ui.activeComponent,opener);await opener.emit('activate');const pause=f.ui.get('Pause following');pause.focus();await f.panel.refresh();assert.equal(f.ui.get('Pause following'),pause);assert.equal(f.ui.activeComponent,pause);});
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
 const f=fixture(t);f.panel.setActive(true);await tick();const opener=f.ui.get('Verification');opener.focus();
 await opener.emit('activate');const heading=f.ui.activeComponent;assert.equal(heading.kind,'Heading');assert.equal(heading.label,'Verification');
 await f.panel.refresh();assert.equal(f.ui.activeComponent,heading,'polling must preserve reading focus');
 const pause=f.ui.get('Pause following');pause.focus();await f.panel.refresh();assert.equal(f.ui.activeComponent,pause,'output updates must not steal control focus');
 await f.ui.get('Back to runs').emit('activate');assert.equal(f.ui.activeComponent,f.ui.get('Verification'));assert.equal(f.ui.activeComponent,opener);
});
test('closing a run whose opener was filtered out focuses the surviving filter',async t=>{
 const f=fixture(t);f.panel.setActive(true);await tick();await f.ui.get('Verification').emit('activate');
 const filter=f.ui.get('Search runs');filter.value='no matching run';await filter.emit('input');
 await f.ui.get('Back to runs').emit('activate');assert.equal(f.ui.activeComponent,filter);
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
test('compact purpose rows stay stable through active-first reordering and search full identities',async t=>{
 let records=[{...run,state:'terminal',name:'four-word-generated-display-name',created_at:'2026-10-28T12:00:00.123456789Z'},{...run,id:other,reason:'Compile preview',command:['/a/very/long/private/tool','--mode','preview'],agent:'preview-agent',state:'active',created_at:'2026-10-27T12:00:00Z'}];
 const f=fixture(t,op=>op==='runs'?records:null);f.panel.setActive(true);await tick();
 const opener=f.ui.get('Verification');opener.focus();const roots=f.ui.all.filter(x=>x.kind==='Stack'&&x.children.some(child=>child.label==='Current · 1'));assert.ok(roots.length);const list=roots.at(-1);assert.equal(list.children[1].children[0].children[0].label,'Compile preview');
 assert.equal(f.ui.all.filter(x=>x.kind==='Card').length,0);assert.ok(!list.children.some(x=>String(x.label).includes('four-word')));
 records=[{...records[0],state:'active'},{...records[1],state:'terminal'}];await f.panel.refresh();assert.equal(f.ui.get('Verification'),opener);assert.equal(f.ui.activeComponent,opener);assert.equal(list.children[1].children[0].children[0],opener);
 const filter=f.ui.get('Search runs');filter.value=other;await filter.emit('input');assert.ok(f.ui.get('Compile preview'));assert.equal(list.children.filter(x=>!x.hidden).length,2);
});
test('selected run gets its own compact view with full metadata in closed Details',async t=>{
 const f=fixture(t);f.panel.setActive(true);await tick();await f.ui.get('Verification').emit('activate');const technical=f.ui.get('Details');assert.ok(technical);assert.equal(technical.open,false);const heading=f.ui.activeComponent;assert.equal(heading.label,'Verification');
 const history=f.ui.all.find(x=>x.kind==='Stack'&&x.children.includes(f.ui.get('Search runs')));assert.equal(history.hidden,true);
 await f.panel.refresh();assert.equal(technical.open,false);assert.equal(f.ui.activeComponent,heading);
 await f.ui.get('Back to runs').emit('activate');assert.equal(history.hidden,false);assert.equal(f.ui.activeComponent,f.ui.get('Verification'));
});
test('full Details retains raw capture failure state and the child exit code',async t=>{
 const failed={...run,state:'terminal',outcome:'log_failed',exit_code:0};const f=fixture(t,op=>op==='runs'?[failed]:{run:failed,offset:0,next_offset:0,data:'',done:true});f.panel.setActive(true);await tick();await f.panel.open(failed);const details=f.ui.get('Details');assert.equal(details.open,false);assert.ok(details.children.some(x=>String(x.label).includes('Outcome: log_failed')&&String(x.label).includes('Exit code: 0')));assert.ok(f.ui.get('Log failed'));
});
test('search matches the displayed failure status and outcome',async t=>{
 const failed={...run,state:'terminal',outcome:'log_failed',exit_code:0};const f=fixture(t,op=>op==='runs'?[failed]:null);f.panel.setActive(true);await tick();const filter=f.ui.get('Search runs');filter.value='Log failed';await filter.emit('input');assert.ok(f.ui.get('Verification'));filter.value='log_failed';await filter.emit('input');assert.ok(f.ui.get('Verification'));
});
test('row moves restore a blurred opener and leave unrelated focus alone',async t=>{
 let records=[{...run,created_at:'2026-10-28T12:00:00Z'},{...run,id:other,reason:'Other task',state:'terminal',created_at:'2026-10-28T11:00:00Z'}];const f=fixture(t,op=>op==='runs'?records:null);f.panel.setActive(true);await tick();const opener=f.ui.get('Verification'),list=f.ui.all.find(x=>x.role==='list');const replace=list.replace.bind(list);list.replace=(...children)=>{replace(...children);if(f.ui.activeComponent===opener)f.ui.activeComponent=null;return list;};opener.focus();records=records.map(r=>({...r,state:r.id===id?'terminal':'active'}));await f.panel.refresh();assert.equal(f.ui.activeComponent,opener);
 const filter=f.ui.get('Search runs');filter.focus();await f.panel.refresh();assert.equal(f.ui.activeComponent,filter);
});

test('selected and fullscreen log errors remain visible and recover independently of list errors',async t=>{
 let listFailure=false,outputFailure=true;
 const f=fixture(t,(op,args)=>{if(op==='runs'){if(listFailure)throw Error('inventory refused');return [run];}if(outputFailure)throw Error('log refused');return {run,offset:args.offset,next_offset:args.offset,data:'',done:false};},true);
 f.panel.setActive(true);await tick();await f.panel.open(run);let warning=f.ui.all.find(x=>x.role==='status'&&!x.hidden&&String(x.label).includes('Output unavailable: log refused'));assert.ok(warning);
 await f.ui.get('Fullscreen log').emit('activate');const fold=f.ui.get('Saved log output');fold.open=false;const surface=f.ui.all.find(x=>x.fullViewport===true);assert.ok(surface.children.includes(warning),'error belongs outside folded output');
 listFailure=true;await f.panel.refresh();assert.match(warning.label,/inventory refused/);assert.match(warning.label,/log refused/);
 listFailure=false;await f.panel.refresh();assert.doesNotMatch(warning.label,/inventory refused/);assert.match(warning.label,/log refused/);assert.ok(f.ui.all.some(x=>String(x.label).startsWith('Stale output')));
 outputFailure=false;await f.panel.refresh();assert.equal(warning.hidden,true);assert.ok(!f.ui.all.some(x=>!x.hidden&&String(x.label).startsWith('Stale output')));assert.equal(surface.fullViewport,true);
});

test('detached log follows across navigation with the same cursor, controls and reader',async t=>{
 let n=0;const f=fixture(t,(op,args)=>op==='runs'?[run]:{run:{...run,log_bytes:++n},offset:args.offset,next_offset:args.offset+1,data:encoded('x'),done:false});
 f.panel.setActive(true);await tick();await f.panel.open(run);
 const window=f.ui.all.find(x=>x.kind==='Tabs'),code=f.ui.all.filter(x=>x.kind==='Code').at(-1),pause=f.ui.get('Pause following');
 const reads=n;await window.popOut();assert.equal(n,reads,'moving the log must not create a second reader');
 f.panel.setActive(false);await f.panel.refresh();assert.equal(n,reads+1);assert.equal(f.calls.at(-1)[1].offset,1);assert.equal(code.value,'xx');assert.equal(f.ui.get('Pause following'),pause);
 await pause.emit('activate');await f.panel.refresh();assert.equal(n,reads+1);assert.equal(code.value,'xx');
 await window.dock();const count=f.calls.length;await f.panel.refresh();assert.equal(f.calls.length,count,'returned log on hidden page must stop polling');
 f.panel.setActive(true);await tick();assert.equal(f.ui.get('Follow output'),pause);assert.equal(code.value,'xx');
});
for(const mode of ['reset','dispose'])test(`detached log retires and rejects late output on ${mode}`,async t=>{
 let resolve,pending=false;const f=fixture(t,(op,args)=>op==='runs'?[run]:pending?new Promise(done=>resolve=done):{run,offset:args.offset,next_offset:args.offset,data:'',done:false});
 f.panel.setActive(true);await tick();await f.panel.open(run);const window=f.ui.all.find(x=>x.kind==='Tabs');await window.popOut();f.panel.setActive(false);
 pending=true;const read=f.panel.refresh();await tick();f.panel[mode]();assert.equal(window.disposed,true);
 resolve({run:{...run,log_bytes:6},offset:0,next_offset:6,data:encoded('STALE!'),done:false});await read;
 assert.ok(!f.ui.all.some(x=>String(x.value).includes('STALE!')));const count=f.calls.length;await f.panel.refresh();assert.equal(f.calls.length,count);
});

function logUI(f,purpose){
 const window=f.ui.all.find(h=>h.kind==='Tabs'&&!h.disposed&&h.label.includes(purpose));assert.ok(window,purpose);
 const walk=h=>[h,...h.children.flatMap(walk)],nodes=walk(window);
 return {window,output:nodes.find(h=>h.kind==='Code'),button:label=>{const button=nodes.find(h=>h.label===label&&!h.disposed);assert.ok(button,label);return button;}};
}
const multipleRuns=n=>Array.from({length:n},(_,i)=>({...run,id:String(i+1).padStart(32,'0'),reason:'Watch agent '+i,agent:'agent-'+i}));
test('multiple windows keep independent cursors, pause, restart, completion and close state',async t=>{
 const records=multipleRuns(2);let completed=false;
 const f=fixture(t,(op,args)=>op==='runs'?records:{run:{...records.find(r=>r.id===args.id),state:completed&&args.id===records[1].id?'terminal':'active',outcome:completed?'completed':'',log_bytes:args.offset+1},offset:args.offset,next_offset:args.offset+1,data:encoded(args.id===records[0].id?'A':'B'),done:completed&&args.id===records[1].id});
 f.panel.setActive(true);await tick();await f.panel.open(records[0]);const a=logUI(f,records[0].reason);await a.window.popOut();
 const history=f.ui.all.find(h=>h.children.includes(f.ui.get('Search runs')));assert.equal(history.hidden,false,'detaching must expose the roster for opening more');
 await f.panel.open(records[1]);const b=logUI(f,records[1].reason);await b.window.popOut();assert.equal(f.panel.openRuns.length,2);
 await a.button('Pause following').emit('activate');const aText=a.output.value,bText=b.output.value;await f.panel.refresh();assert.equal(a.output.value,aText);assert.equal(b.output.value,bText+'B');
 await a.button('Read from start').emit('activate');assert.equal(f.calls.at(-1)[1].id,records[0].id);assert.equal(f.calls.at(-1)[1].offset,0);assert.equal(a.output.value,'A');
 completed=true;await f.panel.refresh();const bReads=f.calls.filter(([op,args])=>op==='run_output'&&args.id===records[1].id).length;
 await f.panel.refresh();assert.equal(f.calls.filter(([op,args])=>op==='run_output'&&args.id===records[1].id).length,bReads,'completed reader must drain and stop independently');
 f.panel.closeLog(records[0].id);assert.equal(a.output.disposed,true);assert.equal(b.output.disposed,undefined);assert.equal(logUI(f,records[1].reason).output,b.output);assert.deepEqual(f.panel.openRuns.map(r=>r.id),[records[1].id]);
 assert.ok(f.calls.every(([op])=>['runs','run_output'].includes(op)),'closing a log must never stop its execution');
});
test('reopening a watched run deduplicates its reader and the open-log limit never evicts peers',async t=>{
 const records=multipleRuns(9),f=fixture(t,(op,args)=>op==='runs'?records:{run:records.find(r=>r.id===args.id),offset:args.offset,next_offset:args.offset,data:'',done:false});
 f.panel.setActive(true);await tick();for(const run of records.slice(0,8)){await f.panel.open(run);await logUI(f,run.reason).window.popOut();}
 const first=logUI(f,records[0].reason),calls=f.calls.length;await f.panel.open(records[0]);assert.equal(f.calls.length,calls);assert.equal(logUI(f,records[0].reason).output,first.output);assert.equal(f.panel.openRuns.length,8);
 await f.panel.open(records[8]);assert.equal(f.panel.openRuns.length,8);assert.equal(first.output.disposed,undefined);assert.ok(f.ui.all.some(h=>!h.hidden&&String(h.label).startsWith('Eight logs are open')));
 f.panel.closeLog(records[7].id);await f.panel.open(records[8]);assert.equal(f.panel.openRuns.length,8);assert.equal(first.output.disposed,undefined);
});
test('shared inventory and log requests keep two real in-flight slots and cancel queued readers',async t=>{
 const records=multipleRuns(5);let hold=false,live=0,peak=0;const pending=[];
 const handler=async(op,args)=>{live++;peak=Math.max(peak,live);try{
  if(op==='runs')return records;
  if(hold)await new Promise(resolve=>pending.push({id:args.id,resolve}));
  return {run:records.find(r=>r.id===args.id),offset:args.offset,next_offset:args.offset+1,data:encoded('x'),done:false};
 }finally{live--;}};
 const f=fixture(t,handler);f.panel.setActive(true);await tick();for(const run of records){await f.panel.open(run);await logUI(f,run.reason).window.popOut();}
 hold=true;const before=f.calls.length,refresh=f.panel.refresh();await tick();assert.equal(pending.length,2);assert.ok(live<=2);
 await logUI(f,records[2].reason).button('Pause following').emit('activate');f.panel.closeLog(records[3].id);
 for(let i=0;i<10;i++){const batch=pending.splice(0);for(const item of batch)item.resolve();await tick();if(!live&&!pending.length)break;}
 await refresh;hold=false;assert.ok(peak<=2);const calls=f.calls.slice(before);assert.equal(calls.filter(([op])=>op==='runs').length,1,'inventory is shared across windows');
 assert.deepEqual(calls.filter(([op])=>op==='run_output').map(([,args])=>args.id).sort(),[records[0].id,records[1].id,records[4].id].sort());
});
for(const mode of ['reset','dispose'])test(`all windows and queued reads retire on ${mode} without accepting stale output`,async t=>{
 const records=multipleRuns(3);let hold=false;const pending=[];
 const f=fixture(t,async(op,args)=>{if(op==='runs')return records;if(hold)await new Promise(resolve=>pending.push(resolve));return {run:records.find(r=>r.id===args.id),offset:args.offset,next_offset:args.offset+6,data:encoded(hold?'STALE!':'fresh!'),done:false};});
 f.panel.setActive(true);await tick();for(const run of records){await f.panel.open(run);await logUI(f,run.reason).window.popOut();}
 const outputs=records.map(r=>logUI(f,r.reason).output);hold=true;const refresh=f.panel.refresh();await tick();assert.equal(pending.length,2);const count=f.calls.length;
 f.panel[mode]();f.panel.setActive(false);for(const resolve of pending)resolve();await refresh;await tick();assert.equal(f.calls.length,count);assert.equal(f.panel.openRuns.length,0);assert.ok(outputs.every(h=>h.disposed));assert.ok(!f.ui.all.some(h=>String(h.value).includes('STALE!')));
});
test('catch-up pages yield fairly and decode split UTF-8 independently for each run',async t=>{
 const records=multipleRuns(2),buffers=[Buffer.from('A🙂BC🙂D'),Buffer.from('B🙂CD🙂E')];let ready=false;
 const f=fixture(t,(op,args)=>{if(op==='runs')return records;const i=records.findIndex(r=>r.id===args.id),data=ready?buffers[i]:Buffer.alloc(0);return {run:{...records[i],state:'terminal',log_bytes:data.length},offset:args.offset,next_offset:Math.min(data.length,args.offset+3),data:data.subarray(args.offset,args.offset+3).toString('base64'),done:ready};});
 f.panel.setActive(true);await tick();for(const run of records){await f.panel.open(run);await logUI(f,run.reason).window.popOut();}
 ready=true;const start=f.calls.length;await f.panel.refresh();for(let i=0;i<50&&records.some((r,j)=>logUI(f,r.reason).output.value!==buffers[j].toString());i++)await new Promise(r=>setTimeout(r,5));
 for(const [i,run]of records.entries()){assert.equal(logUI(f,run.reason).output.value,buffers[i].toString());assert.deepEqual(f.calls.slice(start).filter(([op,args])=>op==='run_output'&&args.id===run.id).map(([,args])=>args.offset),[0,3,6,9]);}
});
test('fullscreen and output failures in one window do not change another window',async t=>{
 const records=multipleRuns(2);let failure=false;
 const f=fixture(t,(op,args)=>{if(op==='runs')return records;if(failure&&args.id===records[0].id)throw Error('first log refused');return {run:records.find(r=>r.id===args.id),offset:args.offset,next_offset:args.offset+1,data:encoded('x'),done:false};},true);
 f.panel.setActive(true);await tick();for(const run of records){await f.panel.open(run);await logUI(f,run.reason).window.popOut();}
 const a=logUI(f,records[0].reason),b=logUI(f,records[1].reason);a.button('Saved log output').open=false;
 await a.button('Fullscreen log').emit('activate');const aSurface=a.window.children[0];assert.equal(aSurface.fullViewport,true);await b.button('Fullscreen log').emit('activate');const bSurface=b.window.children[0];assert.equal(aSurface.fullViewport,false);assert.equal(a.button('Saved log output').open,false);assert.equal(bSurface.fullViewport,true);
 failure=true;const before=b.output.value;await f.panel.refresh();assert.equal(b.output.value,before+'x');assert.ok(a.window.children[0].children.some(h=>String(h.label).includes('first log refused')));assert.ok(!b.window.children[0].children.some(h=>String(h.label).includes('first log refused')));
 f.panel.closeLog(records[0].id);assert.equal(bSurface.fullViewport,true);assert.equal(b.output.disposed,undefined);
 await b.button('Tile log windows').emit('activate');assert.equal(bSurface.fullViewport,false);assert.equal(f.ui.tiled,true);await b.window.dock();assert.equal(f.ui.tiled,false);assert.equal(f.panel.selected.id,records[1].id);
});

test('a slow output reply does not freeze the shared polling clock for another log',async t=>{
 const records=multipleRuns(2);let hold=false,release,started;const began=new Promise(resolve=>started=resolve);let peerReads=0;
 const f=fixture(t,async(op,args)=>{if(op==='runs')return records;if(hold&&args.id===records[0].id)await new Promise(resolve=>{release=resolve;started();});if(args.id===records[1].id)peerReads++;return {run:records.find(r=>r.id===args.id),offset:args.offset,next_offset:args.offset+1,data:encoded('x'),done:false};});
 t.after(()=>release?.());f.panel.setActive(true);await tick();for(const run of records){await f.panel.open(run);await logUI(f,run.reason).window.popOut();}
 hold=true;await began;await tick();const before=peerReads;await new Promise(resolve=>setTimeout(resolve,1150));assert.ok(peerReads>before,'healthy peer must continue polling while the first reply remains outstanding');
});


test('eight retained transcripts keep the aggregate display bound',async t=>{
 const records=multipleRuns(8),size=320*1024,page=64*1024;
 const f=fixture(t,(op,args)=>{if(op==='runs')return records;const i=records.findIndex(r=>r.id===args.id),next=Math.min(size,args.offset+page);return {run:{...records[i],state:'terminal',log_bytes:size},offset:args.offset,next_offset:next,data:encoded(String(i).repeat(next-args.offset)),done:true};});
 f.panel.setActive(true);await tick();for(const record of records){await f.panel.open(record);await logUI(f,record.reason).window.popOut();}
 for(let i=0;i<100&&records.some(r=>logUI(f,r.reason).output.value.length<256*1024);i++)await new Promise(resolve=>setTimeout(resolve,5));
 const outputs=records.map(r=>logUI(f,r.reason).output.value);assert.equal(outputs.reduce((n,text)=>n+text.length,0),8*256*1024);
 for(const [i,text]of outputs.entries())assert.equal(text,String(i).repeat(256*1024));
});
test('inventory removal, filtering and reconnect retain run identity and independent pause',async t=>{
 const records=multipleRuns(2);let inventory=records;
 const f=fixture(t,(op,args)=>op==='runs'?inventory:{run:records.find(r=>r.id===args.id),offset:args.offset,next_offset:args.offset+1,data:encoded(args.id===records[0].id?'A':'B'),done:false});
 f.panel.setActive(true);await tick();for(const record of records){await f.panel.open(record);await logUI(f,record.reason).window.popOut();}
 const a=logUI(f,records[0].reason),b=logUI(f,records[1].reason);await a.button('Pause following').emit('activate');const paused=a.output.value;
 inventory=[records[1]];const filter=f.ui.get('Search runs');filter.value='nothing matches';await filter.emit('input');await f.panel.refresh();assert.equal(a.output.value,paused);assert.ok(/^B+$/.test(b.output.value));assert.deepEqual(f.panel.openRuns.map(r=>r.id),records.map(r=>r.id));
 f.offline();const before=f.calls.length,peer=b.output.value;await f.panel.refresh();assert.equal(f.calls.length,before);assert.match(f.ui.labels(),/Disconnected/);
 f.reconnect();await tick();await f.panel.refresh();assert.equal(a.output.value,paused);assert.ok(b.output.value.length>peer.length);assert.ok(/^B+$/.test(b.output.value));assert.equal(logUI(f,records[0].reason).output,a.output);assert.equal(logUI(f,records[1].reason).output,b.output);
});
