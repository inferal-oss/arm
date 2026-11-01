import test from 'node:test';
import assert from 'node:assert/strict';
import {createAgentsConsole} from './agents.mjs';
import {readDashboardRoute,dashboardRouteURL} from '../route.mjs';

function fixture(){
 let serial=0;const handles=[];
 class Handle{
  constructor(kind,props={}){this.iri='urn:fixture:'+ ++serial;this.kind=kind;this.children=[];this.parents=new Set();this.events=new Map();this.retainCount=0;this.disposed=false;Object.assign(this,props);handles.push(this);}
  set(props){assert.equal(this.disposed,false,'late update reached a disposed handle');Object.assign(this,props);return this;}
  replace(...children){const next=children.flat();for(const c of next)c.parents.add(this);for(const c of this.children)if(!next.includes(c)){c.parents.delete(this);if(!c.parents.size&&!c.retainCount)c.dispose();}this.children=next;return this;}
  append(...children){return this.replace(...this.children,...children);}
  dispose(){if(this.disposed)return;this.disposed=true;for(const c of this.children){c.parents.delete(this);if(!c.parents.size&&!c.retainCount)c.dispose();}this.children=[];}
  retain(){this.retainCount++;return this;}focus(){}setPairs(v){this.pairs=v;}showModal(){this.open=true;}dock(){this.detached=false;this.emit('detach');}popOut(){this.detached=true;this.emit('detach');}reveal(){}on(name,fn){if(!this.events.has(name))this.events.set(name,new Set());this.events.get(name).add(fn);return()=>this.events.get(name).delete(fn);}emit(name){for(const fn of this.events.get(name)??[])fn();}
 }
 const make=(kind,label,props={})=>new Handle(kind,{label,...props}),stack=children=>new Handle('Stack').replace(...(children??[]));
 const ui={batch:fn=>fn(),put(){},component:(kind,props)=>new Handle(kind,props),stack,heading:(s)=>make('Heading',s),text:(s,p)=>make('Text',s,p),code:s=>make('Code','',{value:s}),json:(s,label)=>make('Code',label,{value:JSON.stringify(s)}),descriptions:p=>make('Descriptions','',{pairs:p}),toolbar:(label,c)=>make('Toolbar',label).replace(...c),field:(label,p,fn)=>make('Field',label,{...p,activate:fn}),select:(label,o,p,fn)=>make('Choice',label,{value:o[0].value,...p,activate:fn}),check:(label,p,fn)=>make('Checkbox',label,{...p,activate:fn}),button:(label,fn)=>make('Button',label,{activate:fn}),island:(name,widget)=>make('Island',name,{widget}),disclosure:(label,c)=>make('Disclosure',label).replace(...c),dialog:(label,c,actions)=>make('Dialog',label).replace(...c,...actions),detachable:(label,content)=>{const h=make('Tabs',label).replace(content);h.tab=make('Tab',label);return h;}};
 return {ui,handles,root:stack()};
}
test('standalone route retains only a public identity key',()=>{
 const id='1'.repeat(32),url=dashboardRouteURL({view:'agents',agent:id},'http://localhost/?secret=value#connect=private');
 assert.equal(url,'/?view=agents&agent='+id);assert.equal(readDashboardRoute('http://localhost'+url).agent,id);
 assert.equal(readDashboardRoute('http://localhost/?view=agents&agent='+id+'&agent='+id).agent,undefined);
});
test('auth reset invalidates pending reads and disposes owned report windows',async()=>{
 const {ui,handles,root}=fixture(),id='1'.repeat(32);let resolveRead;const source={};
 const console=createAgentsConsole({ui,root,connected:()=>true,context:()=>source,call:op=>op==='agent_status_read'?new Promise(resolve=>resolveRead=resolve):Promise.resolve({}),onNavigate(){},onLease(){},onRun(){},location:{href:'http://localhost/'}});
 const opening=console.applyRoute({agent:id});assert.equal(handles.filter(h=>h.kind==='Tabs'&&!h.disposed).length,1);
 console.reset();resolveRead({entry:{identity:{host:'local',contact:{id:'fixture',adapter:'generic'}}}});await opening;
 assert.equal(handles.filter(h=>h.kind==='Tabs'&&!h.disposed).length,0);console.dispose();
});
test('setup banner is collapsed presentation provenance and resets with auth',async()=>{
 const {ui,handles,root}=fixture();let resolveList;const source={};
 const console=createAgentsConsole({ui,root,connected:()=>true,context:()=>source,call:op=>op==='agent_status_list'?new Promise(resolve=>resolveList=resolve):Promise.resolve([]),onNavigate(){},onLease(){},onRun(){},location:{href:'http://localhost/'}});
 console.setActive(true);
 resolveList({rows:[],total:0,project_setup:{projects:[{project:'example',setup:true,policy_revision:3,provenance:'stale'}]}});
 await Promise.resolve();await Promise.resolve();
 const disclosure=handles.find(h=>h.label==='Project status setup preferences');
 assert.equal(disclosure.hidden,false);
 assert.match(disclosure.children[0].label,/example: setup preference on · saved revision 3 · stale/);
 assert.match(disclosure.children[0].label,/do not enable providers or enroll agents/);
 console.reset();assert.equal(disclosure.hidden,true);assert.equal(disclosure.children[0].label,'');console.dispose();
});
test('hidden docked reports stop polling while floating peers retain independent following and close controls',async()=>{
 const {ui,handles,root}=fixture(),source={},calls=[],pendingTimers=new Map();let sequence=0,holdNextList=false,releaseList;
 const timers={setTimeout(fn){const id=++sequence;pendingTimers.set(id,fn);return id;},clearTimeout(id){pendingTimers.delete(id);}};
 const turn=()=>new Promise(resolve=>setImmediate(resolve));
 const advance=async()=>{const tasks=[...pendingTimers.values()];pendingTimers.clear();for(const fn of tasks)fn();await turn();};
 const ids=['1'.repeat(32),'2'.repeat(32)],entry=id=>({id,identity:{host:'local',contact:{id,adapter:'generic'}}});
 const console=createAgentsConsole({ui,root,timers,connected:()=>true,context:()=>source,call:async(op,q)=>{calls.push({op,id:q?.id});if(op==='agent_status_list'&&holdNextList){holdNextList=false;return new Promise(resolve=>releaseList=resolve);}return op==='agent_status_list'?{rows:ids.map(entry),total:2}:op==='agent_status_read'?{entry:entry(q.id)}:[];},onNavigate(){},onLease(){},onRun(){},location:{href:'http://localhost/'}});
 console.setActive(true);await turn();
 await console.applyRoute({agent:ids[0]});await console.applyRoute({agent:ids[1]});
 const windows=handles.filter(h=>h.kind==='Tabs');assert.equal(windows.length,2);
 windows[0].popOut();console.setActive(false);calls.length=0;await advance();
 assert.ok(calls.some(q=>q.op==='agent_status_list'));assert.deepEqual(calls.filter(q=>q.op==='agent_status_read').map(q=>q.id),[ids[0]],'hidden docked peer must not follow a floating peer');
 windows[0].dock();assert.equal(pendingTimers.size,0,'native close/dock of last floating view must stop the timer');
 calls.length=0;await advance();assert.deepEqual(calls,[]);assert.equal(windows[0].disposed,false,'docking retains cached owned content');
 windows[1].popOut();await advance();assert.deepEqual(calls.filter(q=>q.op==='agent_status_read').map(q=>q.id),[ids[1]]);
 calls.length=0;holdNextList=true;await advance();assert.equal(typeof releaseList,'function');windows[1].dock();releaseList({rows:ids.map(entry),total:2});await turn();
 assert.deepEqual(calls.map(q=>q.op),['agent_status_list'],'docking during an accepted inventory read must not start runs or report reads');assert.equal(pendingTimers.size,0);
 windows[1].popOut();
 const descendants=h=>[h,...h.children.flatMap(descendants)];
 const close=descendants(windows[1]).find(h=>h.label==='Close status');assert.ok(close,'floating body must contain its own close action');close.activate();
 assert.equal(windows[1].disposed,true);assert.equal(windows[0].disposed,false);assert.equal(pendingTimers.size,0);
 calls.length=0;console.setActive(true);await turn();assert.deepEqual(calls.filter(q=>q.op==='agent_status_read').map(q=>q.id),[ids[0]],'returning to page resumes the retained cached view only');
 console.dispose();assert.equal(pendingTimers.size,0);
});

test('resource pages bound rendered rows, retain every association and clamp independently',async()=>{
 const {ui,handles,root}=fixture(),source={},ids=['1'.repeat(32),'2'.repeat(32)],opened=[];
 const entry=id=>({id,identity:{host:'local',contact:{id,adapter:'generic'}}});
 const records=(kind,count)=>ids.flatMap(agent=>Array.from({length:count},(_,i)=>kind==='leases'?{id:`${agent}-lease-${i}`,host:'local',state:'active',request:{agent,reason:`Lease ${i}`}}:{id:`${agent}-run-${i}`,host:'local',agent,state:'complete',reason:`Run ${i}`}));
 let leases=records('leases',27),runs=records('runs',29);
 const console=createAgentsConsole({ui,root,timers:{setTimeout(){return 1;},clearTimeout(){}},connected:()=>true,context:()=>source,call:async(op,q)=>op==='agent_status_list'?{rows:ids.map(entry),total:2}:op==='agent_status_read'?{entry:entry(q.id)}:runs,onNavigate(){},onLease:id=>opened.push(id),onRun:r=>opened.push(r.id),location:{href:'http://localhost/'}});
 const turn=()=>new Promise(resolve=>setImmediate(resolve));
 console.update({host:'local',leases});console.setActive(true);await turn();
 await console.applyRoute({agent:ids[0]});await console.applyRoute({agent:ids[1]});
 const windows=handles.filter(h=>h.kind==='Tabs'),descendants=h=>[h,...h.children.flatMap(descendants)],live=i=>descendants(windows[i]),button=(i,label)=>live(i).find(h=>h.kind==='Button'&&h.label===label),labels=i=>live(i).filter(h=>h.kind==='Text').map(h=>h.label).join('\n');
 assert.equal(live(0).filter(h=>h.kind==='Button'&&/^(Lease|Run) \d+ ·/.test(h.label)).length,24);
 assert.match(labels(0),/1–12 of 27 label-associated leases/);assert.match(labels(0),/1–12 of 29 label-associated runs/);
 button(0,'Next leases').activate();button(0,'Next leases').activate();button(0,'Next runs').activate();button(0,'Next runs').activate();
 assert.match(labels(0),/25–27 of 27/);assert.match(labels(0),/25–29 of 29/);assert.equal(button(0,'Next leases').disabled,true);
 assert.match(labels(1),/1–12 of 27/);assert.equal(button(1,'Previous leases').disabled,true);
 button(0,'Lease 26 · active').activate();button(0,'Run 28 · complete').activate();assert.deepEqual(opened,[`${ids[0]}-lease-26`,`${ids[0]}-run-28`]);
 leases=records('leases',2);runs=records('runs',0);console.update({host:'local',leases});console.setActive(true);await turn();
 assert.match(labels(0),/1–2 of 2/);assert.match(labels(0),/0–0 of 0/);assert.equal(button(0,'Previous leases').disabled,true);assert.equal(button(0,'Next runs').disabled,true);
 console.dispose();
});
