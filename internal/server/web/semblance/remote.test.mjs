import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createRemoteNodeDashboard,remoteResourceLabel} from './remote.mjs';
import {poolRequestCopy} from '../pool-model.mjs';

// Host behavior over graph handles only. The composed runtime separately proves
// RDF events, graph state, Semblance projection and actual browser rendering.
class Component {
  constructor(ui,kind,props={}) {this.ui=ui;this.kind=kind;this.children=[];this.callbacks={};Object.assign(this,{label:'',hidden:false,disabled:false,open:false},props);ui.nodes.add(this);}
  set(props){Object.assign(this,props);return this;}
  append(...children){for(const child of children){child.parent=this;this.children.push(child);}return this;}
  replace(...children){for(const child of this.children)child.parent=null;this.children=[];return this.append(...children);}
  on(type,fn){(this.callbacks[type]??=[]).push(fn);return this;}
  async fire(type='click'){if(this.disabled)return;for(const fn of this.callbacks[type]||[])await fn();}
  contains(handle){for(let node=handle;node;node=node.parent)if(node===this)return true;return false;}
  focus(){this.ui.active=this;}
  showModal(){this.open=true;}
  close(){if(!this.open)return;this.open=false;for(const fn of this.callbacks.close||[])fn();}
  dispose(){this.parent=null;this.replace();}
  get isConnected(){return this===this.ui.root||Boolean(this.parent?.isConnected);}
  get visible(){return this.isConnected&&!this.hidden&&(!this.parent||this.parent.visible)&&(!this.parent||this.parent.kind!=='dialog'||this.parent.open);}
}
function graphUI() {
  const ui={nodes:new Set(),batch:fn=>fn(),get activeComponent(){return this.active;}};
  const component=(kind,props={},children=[])=>new Component(ui,kind,props).append(...children);
  ui.root=component('root');ui.stack=(children=[],props={})=>component('stack',props,children);
  ui.heading=(label,level=2)=>component('heading',{label,level});
  for(const kind of ['text','badge'])ui[kind]=(label,props={})=>component(kind,{label,...props});
  ui.button=(label,fn,props={})=>component('button',{label,...props}).on('click',fn);
  ui.card=(label,children=[])=>component('card',{label},children);
  ui.dialog=(label,children=[],actions=[])=>component('dialog',{label},[...children,...actions]);
  ui.disclosure=(label,children=[],props={})=>component('disclosure',{label,...props},children);
  ui.json=(value,label='JSON')=>component('json',{label,value:poolRequestCopy(value)});
  return ui;
}
const flush=()=>new Promise(resolve=>setImmediate(resolve));
const deferred=()=>{let resolve;const promise=new Promise(yes=>{resolve=yes;});return{promise,resolve};};
const context={host:'destination-host',source_host:'source-host',local:false,pair:'pair',local_revision:2,remote_revision:3,certificate_fingerprint:'pin'};
const ownLease={id:'own-lease',state:'active',expires_at:'2026-10-26T23:59:00Z',request:{reason:'Owned build',origin_peer:context.source_host}};
const otherLease={id:'foreign-lease',state:'active',expires_at:'2026-10-26T23:59:00Z',request:{reason:'Other build',origin_peer:'different-source'}};
function snapshot() {return{
  host:context.host,resources:[
    {kind:'cpu',unit:'logical-core-equivalents',available:0,budget_total:2,reserved:2,availability_known:true},
    {kind:'memory',unit:'bytes',available:0,budget_total:1024,reserved:1024},
    {kind:'disk',unit:'bytes',available:0,budget_total:2048,reserved:2048,availability_known:true}],
  cpu:{known:true,stale:false,age_ns:100e6,stale_after_ns:1000e6,observed_available_cores:2,capacity_cores:2,reserved_cores:2,available_cores:0,deficit_cores:0},
  memory:{stale:false,age_ns:100e6,stale_after_ns:1000e6,total_bytes:8192,estimated_available_bytes:1024,capacity_bytes:1024,reserved_bytes:1024,available_bytes:0,deficit_bytes:0},
  disk:{known:true,stale:false,age_ns:100e6,stale_after_ns:1000e6,total_bytes:8192,reported_available_bytes:2048,capacity_bytes:2048,reserved_bytes:2048,available_bytes:0,deficit_bytes:0},
  leases:[ownLease,otherLease]
};}
function fixture(bound=context) {
  const ui=graphUI(),calls=[],errors=[];
  let mono=0,wall=Date.parse('2026-10-26T10:00:00Z'),value=snapshot(),permissions=['observe-host','observe-own'];
  let contextHandler=null,statusHandler=null,metadataHandler=null,inspectHandler=null;
  const report=()=>({context:{...bound},access_scope:{source_host:bound.source_host,target_host:bound.host,pair:bound.pair,revision:bound.remote_revision,grant:{permissions}},read_only:true});
  const dashboard=createRemoteNodeDashboard({ui,root:ui.root,feed:ui.stack(),context:bound,clock:{mono:()=>mono,wall:()=>wall},onError:error=>errors.push(error),
    call:async(operation,params)=>{
      calls.push([operation,params]);
      if(operation==='node_context')return contextHandler?contextHandler():report();
      if(operation==='status')return statusHandler?statusHandler():poolRequestCopy(value);
      if(operation==='node_metadata')return metadataHandler?metadataHandler():{host:bound.host,hostname:'Destination',platform:{os:'linux',process_architecture:'arm64'}};
      if(operation==='inspect')return inspectHandler?inspectHandler(params):poolRequestCopy(value.leases.find(lease=>lease.id===params.id));
      throw Error('Unexpected operation: '+operation);
    }});
  const find=(label,kind)=>[...ui.nodes].find(node=>node.visible&&node.label===label&&(!kind||node.kind===kind));
  return{ui,dashboard,calls,errors,find,report,get value(){return value;},set value(next){value=next;},
    advance(ms){mono+=ms;wall+=ms;},permissions(next){permissions=next;},context(fn){contextHandler=fn;},status(fn){statusHandler=fn;},metadata(fn){metadataHandler=fn;},inspect(fn){inspectHandler=fn;}};
}

test('remote headings keep private collector identifiers in optional graph details',()=>{
  assert.equal(remoteResourceLabel({kind:'cpu',id:'private/cpu'}),'Live CPU headroom');
  assert.equal(remoteResourceLabel({kind:'memory'}),'Memory budget');assert.equal(remoteResourceLabel({kind:'disk'}),'Disk budget');
  assert.equal(remoteResourceLabel({kind:'port',id:'private/ports/boot-private/tcp4/127.0.0.1:1234'}),'TCP4 port · 127.0.0.1:1234');
});
test('live zero stays distinct from missing/stale native budgets and full promises remain visible',async()=>{
  const f=fixture();await f.dashboard.refresh();
  assert.ok(f.find('0 cores','heading'));assert.ok(f.find('0 B','heading'));assert.equal(f.dashboard.stale,false);
  const callCount=f.calls.length;f.advance(1200);f.dashboard.updateFreshness();
  assert.equal(f.find('0 cores','heading'),undefined);assert.equal(f.find('0 B','heading'),undefined);
  assert.ok(f.find('unavailable','heading'));assert.ok(f.find('Live headroom unavailable · promised 2 cores · shortfall unavailable'));
  assert.ok(f.find('Live pool unavailable · promised 1 KiB · shortfall unavailable'));
  assert.ok(f.find('Live pool unavailable · promised 2 KiB · shortfall unavailable'));assert.equal(f.calls.length,callCount);
});
test('missing native budgets never fabricate zero from resource summaries',async()=>{
  const f=fixture();f.value.cpu=null;f.value.memory=null;f.value.disk=null;await f.dashboard.refresh();
  assert.equal(f.find('0 cores','heading'),undefined);assert.equal(f.find('0 B','heading'),undefined);
  assert.ok(f.find('CPU headroom is stale or unavailable. New CPU grants wait; promises stay charged.'));
  assert.ok(f.find('Memory availability is stale or unknown. New grants wait; promises stay charged.'));
  assert.ok(f.find('Disk availability is stale or unknown. New grants wait; promises stay charged.'));
});
test('context, status and metadata are bound to exact destination; no failure falls back locally',async()=>{
  for(const mismatch of ['context','status','metadata']) {
    const f=fixture();
    if(mismatch==='context')f.context(()=>({...f.report(),context:{...context,host:'localhost'}}));
    if(mismatch==='status')f.status(()=>({...snapshot(),host:'localhost'}));
    if(mismatch==='metadata')f.metadata(()=>({host:'localhost'}));
    await f.dashboard.refresh();assert.equal(f.dashboard.stale,true);assert.equal(f.errors.length,1);
    assert.ok([...f.ui.nodes].some(node=>node.visible&&node.label.includes('never falls back to localhost')));
    assert.equal(f.calls.some(([operation])=>!['node_context','status','node_metadata'].includes(operation)),false);
  }
});
test('disconnect invalidates a pending refresh and reconnect owns a separate pending slot',async()=>{
  const f=fixture(),held=deferred();f.status(()=>held.promise);
  const earlier=f.dashboard.refresh();await flush();await f.dashboard.refresh();
  assert.equal(f.calls.filter(([operation])=>operation==='status').length,1);
  f.dashboard.disconnect();f.status(()=>snapshot());await f.dashboard.refresh();
  assert.equal(f.dashboard.stale,false);
  held.resolve({...snapshot(),host:'localhost'});await earlier;assert.equal(f.dashboard.stale,false);assert.equal(f.errors.length,0);
});
test('metadata requests are throttled to one per 30 seconds even after a refused read',async()=>{
  const f=fixture();f.metadata(()=>{throw Error('Metadata unavailable');});
  await f.dashboard.refresh();await f.dashboard.refresh();assert.equal(f.calls.filter(([operation])=>operation==='node_metadata').length,1);
  f.advance(29999);await f.dashboard.refresh();assert.equal(f.calls.filter(([operation])=>operation==='node_metadata').length,1);
  f.advance(1);await f.dashboard.refresh();assert.equal(f.calls.filter(([operation])=>operation==='node_metadata').length,2);
});
test('own-grant details enforce source attribution; full observation grant keeps authorized inspect parity',async()=>{
  const f=fixture();await f.dashboard.refresh();f.dashboard.show('leases');
  assert.equal(f.find('Other build','button').disabled,true);assert.equal(f.find('Owned build','button').disabled,false);
  await f.find('Owned build','button').fire();assert.deepEqual(f.calls.at(-1),['inspect',{id:'own-lease'}]);
  assert.ok(f.find('Destination lease','json'));await f.find('Close','button').fire();
  f.permissions(['observe-host','observe-all']);await f.dashboard.refresh();f.dashboard.show('leases');
  assert.equal(f.find('Other build','button').disabled,false);await f.find('Other build','button').fire();
  assert.deepEqual(f.calls.at(-1),['inspect',{id:'foreign-lease'}]);assert.ok(f.find('Destination lease','json'));
  assert.equal(f.calls.some(([operation])=>!['node_context','status','node_metadata','inspect'].includes(operation)),false);
});
test('wrong own-scope lease result cannot become a displayed exact destination detail',async()=>{
  const f=fixture();await f.dashboard.refresh();f.dashboard.show('leases');f.inspect(()=>({...ownLease,request:{...ownLease.request,origin_peer:'different-source'}}));
  await f.find('Owned build','button').fire();assert.equal(f.find('Destination lease','json'),undefined);
  assert.ok(f.find('Destination lease differs from this source’s own-lease scope.'));
});
test('focused content is held while snapshots advance and releases after focus leaves',async()=>{
  const f=fixture();await f.dashboard.refresh();const focused=f.find('Scope and accounting details','disclosure');focused.focus();
  f.value.resources[1].available=512;await f.dashboard.refresh();
  assert.ok(f.find('View held while reading a control. Newer destination observations are available after focus leaves this panel.'));
  assert.equal(f.find('512 B','heading'),undefined);
  f.ui.active=null;await f.ui.root.children[2].fire('focusout');await flush();
  assert.ok(f.find('512 B','heading'));assert.equal(f.find('View held while reading a control. Newer destination observations are available after focus leaves this panel.'),undefined);
});
test('disconnect leaves opened detail read-only and ignores its late result',async()=>{
  const f=fixture();await f.dashboard.refresh();f.dashboard.show('leases');const held=deferred();f.inspect(()=>held.promise);
  const pending=f.find('Owned build','button').fire();f.dashboard.disconnect();held.resolve(ownLease);await pending;
  assert.ok(f.find('Disconnected · this exact destination lease cannot be refreshed. No operations will be replayed.'));
  assert.equal(f.find('Destination lease','json'),undefined);assert.equal(f.find('Owned build','button').disabled,true);
});
test('dispose invalidates pending results and detaches only this graph root',async()=>{
  const f=fixture(),held=deferred();f.status(()=>held.promise);const pending=f.dashboard.refresh();await flush();f.dashboard.dispose();held.resolve(snapshot());await pending;
  assert.equal(f.ui.root.children.length,0);assert.equal(f.errors.length,0);
});

test('exact wide bootstrap revisions match the destination report and changed evidence remains refused',async()=>{
 const bound={...context,local_revision:9007199254740993n,remote_revision:9223372036854775807n},f=fixture(bound);
 try{await f.dashboard.refresh();assert.equal(f.dashboard.stale,false);assert.equal(f.errors.length,0);
  const before=f.calls.filter(([op])=>op==='status').length;
  f.context(()=>{const report=f.report();report.context.local_revision+=1n;return report;});
  await f.dashboard.refresh();assert.equal(f.dashboard.stale,true);assert.equal(f.errors.length,1);assert.match(f.errors[0].message,/exact selected node/);assert.equal(f.calls.filter(([op])=>op==='status').length,before);
  assert.ok(f.calls.every(([op])=>['node_context','status','node_metadata'].includes(op)));
 }finally{f.dashboard.dispose();}
});
