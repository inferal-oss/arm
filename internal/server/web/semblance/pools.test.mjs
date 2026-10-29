import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createPoolConsole} from './pools.mjs';
import {poolRequestCopy} from '../pool-model.mjs';

// This fixture models the graph-handle contract only. Real Semblance graph/DOM
// projection and stock Semblance rendering belong to the composed UI checks.
class Component {
  constructor(ui,kind,props={}) {this.ui=ui;this.kind=kind;this.children=[];this.callbacks={};Object.assign(this,{label:'',value:'',hidden:false,disabled:false,open:false},props);ui.nodes.add(this);}
  set(props){Object.assign(this,props);return this;}
  append(...children){for(const child of children){child.parent=this;this.children.push(child);}return this;}
  replace(...children){for(const child of this.children)child.parent=null;this.children=[];return this.append(...children);}
  on(type,fn){(this.callbacks[type]??=[]).push(fn);return this;}
  async fire(type='click'){if(this.disabled)return;for(const fn of this.callbacks[type]||[])await fn({target:this});}
  focus(){this.ui.active=this;}
  showModal(){this.open=true;}
  close(){if(!this.open)return;this.open=false;for(const fn of this.callbacks.close||[])fn();}
  dispose(){this.parent=null;this.replace();}
  get isConnected(){return this===this.ui.root||this===this.ui.desktop||Boolean(this.parent?.isConnected);}
  get visible(){return this.isConnected&&!this.hidden&&(!this.parent||this.parent.visible)&&(!this.parent||this.parent.kind!=='dialog'||this.parent.open);}
}
function graphUI() {
  const ui={nodes:new Set(),batch:fn=>fn(),get activeComponent(){return this.active;}};
  const component=(kind,props={},children=[])=>new Component(ui,kind,props).append(...children);
  ui.root=component('root');ui.desktop=component('desktop');ui.stack=(children=[],props={})=>component('stack',props,children);
  ui.heading=(label,level=2)=>component('heading',{label,level});
  for(const kind of ['text','badge','code'])ui[kind]=(label,props={})=>component(kind,{label,...props});
  ui.button=(label,fn,props={})=>component('button',{label,...props}).on('click',fn);
  for(const kind of ['field','area'])ui[kind]=(label,props={},fn)=>{const c=component(kind,{label,...props});if(fn)c.on('input',fn);return c;};
  ui.select=(label,options,props={},fn)=>{const c=component('select',{label,options,...props});if(fn)c.on('change',fn);return c;};
  ui.card=(label,children=[],props={})=>component('card',{label,...props},children);
  ui.dialog=(label,children=[],actions=[])=>{const dialog=component('dialog',{label});dialog.body=component('stack',{},children);dialog.append(dialog.body,...actions);dialog.replace=(...content)=>{dialog.body.replace(...content);return dialog;};ui.desktop.append(dialog);return dialog;};
  ui.disclosure=(label,children=[],props={})=>component('disclosure',{label,...props},children);
  ui.json=(value,label='JSON')=>component('disclosure',{label,value:poolRequestCopy(value)});
  ui.table=(headers,rows)=>component('table',{headers,rows});
  ui.descriptions=pairs=>component('descriptions',{pairs});
  ui.grid=(children=[],props={})=>component('grid',props,children);
  return ui;
}
const flush=()=>new Promise(resolve=>setImmediate(resolve));
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no;});return{promise,resolve,reject};};
const snapshot=()=>({
  pool:{id:'permits',name:'Review permits',scope:'global',authority:'local-host',revision:7,capacity:9223372036854775807n,state:'active',owner:'501',owner_username:'alice',unit:'permits'},
  reserved:9223372036854775807n,available:0,deficit:0,
  users:[{lease:'lease-a',quantity:9223372036854775807n,reason:'Retain build',owner:'501',state:'active'}],queued:[],
  review:{authority:'local-host',consumers:[{id:'lease-a',revision:2}]}
});
function fixture(options={}) {
  const ui=graphUI(),calls=[],storageValues=new Map();
  let live=true,view=snapshot(),cursor=0,route={view:'resources',pool:'permits',poolAfter:'0',poolBefore:null};
  let listHandler=null,inspectHandler=null,historyHandler=null,consumersHandler=null,mutateHandler=null,lookupHandler=null;
  const storage={getItem:key=>storageValues.get(key)??null,setItem:(key,value)=>storageValues.set(key,value)};
  const console=createPoolConsole({ui,root:ui.root,storage,key:()=> 'review-key',connected:()=>live,
    onNavigate:next=>{route={view:'resources',...next};update();},
    call:async(operation,params)=>{
      calls.push([operation,poolRequestCopy(params)]);
      if(operation==='pool_list')return listHandler?listHandler(params):[poolRequestCopy(view)];
      if(operation==='pool_inspect')return inspectHandler?inspectHandler(params):poolRequestCopy(view);
      if(operation==='pool_history_page')return historyHandler?historyHandler(params):{pool:params.id,entries:[],previous_before:null,next_after:null};
      if(operation==='pool_consumers')return consumersHandler?consumersHandler(params):{pool:params.id,descendants:true,entries:[],cursor:'',more:false};
      if(operation==='pool_mutate') {
        if(mutateHandler)return mutateHandler(params);
        return{sequence:1,idempotency_key:params.idempotency_key,operation:params.operation,reason:params.reason,before:poolRequestCopy(view.pool),after:{...view.pool,revision:8,capacity:params.capacity??view.pool.capacity},actor:'501'};
      }
      if(operation==='pool_lookup')return lookupHandler?lookupHandler(params):{found:false};
      throw Error(operation);
    },...options});
  const update=()=>console.update({status:{host:'local-host',owner:'501',owner_username:'alice',cursor:++cursor},route});
  const find=(label,kind)=>[...ui.nodes].find(node=>node.visible&&node.label===label&&(!kind||node.kind===kind));
  return{ui,console,calls,storageValues,update,find,
    get view(){return view;},set view(next){view=next;},
    route(next){route=next;update();},
    live(value){live=value;console.updateConnection();},
    list(fn){listHandler=fn;},inspect(fn){inspectHandler=fn;},history(fn){historyHandler=fn;},consumers(fn){consumersHandler=fn;},mutate(fn){mutateHandler=fn;},lookup(fn){lookupHandler=fn;}};
}
async function edit(f,operation='Change total capacity…') {
  await f.find(operation,'button').fire();
  f.find('Why are you making this change?','area').value='Release spare build permits';
  return f.find('New total capacity','field');
}

test('cancelling a create review preserves empty inventory as row content',async()=>{
  const f=fixture(),label='No custom pools yet. Create one to coordinate counted permits.';f.list(()=>[]);f.route({view:'resources',pool:null});await flush();await f.find('Manage pools…','button').fire();assert.ok(f.find(label,'text'));
  await f.find('Create pool…','button').fire();f.find('Pool name','field').value='Review only';f.find('Initial capacity','field').value='3';f.find('Why are you making this change?','area').value='Inspect the review';await f.find('Review change','button').fire();
  const empty=f.find(label,'text');assert.ok(empty);await f.find('Cancel change','button').fire();assert.equal(f.find(label,'text'),empty);assert.equal(f.calls.filter(([op])=>op==='pool_mutate').length,0);
});

test('pool graph review preserves exact int64 counts and default-cancel focus; only confirmation dispatches',async()=>{
  const f=fixture();f.update();await flush();
  const capacity=await edit(f);capacity.value='9223372036854775806';
  await f.find('Review change','button').fire();
  assert.equal(f.calls.filter(([op])=>op==='pool_mutate').length,0);
  assert.equal(f.ui.active.label,'Cancel change');
  const request=f.find('Exact request and complete consumer revisions').value;
  assert.equal(request.capacity,9223372036854775806n);
  assert.equal(String(request.revision),'7');assert.deepEqual(request.reviewed.consumers,poolRequestCopy([{id:'lease-a',revision:2}]));
  await f.find('Set total capacity to 9,223,372,036,854,775,806','button').fire();await flush();
  const sent=f.calls.filter(([op])=>op==='pool_mutate');assert.equal(sent.length,1);assert.deepEqual(sent[0][1],request);
});

test('changed revisions and disconnect disable review; close, reconnect and cancellation never dispatch',async()=>{
  const f=fixture();f.update();await flush();await edit(f);await f.find('Review change','button').fire();
  const confirm=f.find('Set total capacity to 9,223,372,036,854,775,807','button');
  f.view.pool.revision=8;f.update();await flush();assert.equal(confirm.disabled,true);
  f.live(false);assert.equal(confirm.disabled,true);
  await f.find('Cancel change','button').fire();f.live(true);await flush();
  assert.equal(f.calls.filter(([op])=>op==='pool_mutate').length,0);
  await edit(f);f.find('Change total capacity','dialog').close();
  assert.equal(f.find('Review change','button'),undefined);
  assert.equal(f.calls.filter(([op])=>op==='pool_mutate').length,0);
});

test('unknown dispatch retains exact request/key; lookup and same-key retry require explicit actions',async()=>{
  const f=fixture();f.update();await flush();
  f.mutate(()=>{throw Error('Backend disconnected');});
  const capacity=await edit(f);capacity.value='9223372036854775806';await f.find('Review change','button').fire();
  const exact=f.find('Exact request and complete consumer revisions').value;
  await f.find('Set total capacity to 9,223,372,036,854,775,806','button').fire();await flush();
  assert.equal(f.calls.filter(([op])=>op==='pool_mutate').length,1);
  assert.equal(f.calls.filter(([op])=>op==='pool_lookup').length,0);
  const saved=JSON.parse(f.storageValues.get('arm-pool-operations-v1'));assert.equal(saved[0].request.idempotency_key,'review-key');
  f.live(false);f.live(true);await flush();assert.equal(f.calls.filter(([op])=>op==='pool_mutate').length,1);
  await f.find('Check stored result','button').fire();assert.equal(f.calls.filter(([op])=>op==='pool_lookup').length,1);
  assert.ok(f.find('No stored result yet. The original request may still commit; only an explicit retry of this same request/key is safe.'));
  await f.find('Retry exact request','button').fire();assert.equal(f.calls.filter(([op])=>op==='pool_mutate').length,2);
  assert.deepEqual(f.calls.filter(([op])=>op==='pool_mutate')[1][1],exact);
});

test('cancelled/closed editor ignores late inspection and preserves original pool route',async()=>{
  const f=fixture();f.update();await flush();
  const held=deferred();f.inspect(()=>held.promise);await edit(f);
  const pending=f.find('Review change','button').fire();
  await f.find('Cancel','button').fire();held.resolve(snapshot());await pending;
  assert.equal(f.find('Proposed change'),undefined);assert.equal(f.calls.filter(([op])=>op==='pool_mutate').length,0);
  assert.ok(f.find('Review permits','heading'));
});

test('subtree pages bind exact selected pool; obsolete response after disconnect cannot refresh data',async()=>{
  const f=fixture();f.update();await flush();
  const held=deferred();f.consumers(()=>held.promise);f.update();await flush();
  f.live(false);held.resolve({pool:'permits',descendants:true,entries:[{use:{lease:'stale-lease',reason:'Old observation',owner:'501',state:'active'},direct_claims:[]}],cursor:'',more:false});await flush();
  assert.equal(f.find('stale-lease','button'),undefined);
  f.consumers(()=>({pool:'foreign-pool',descendants:true,entries:[],cursor:'',more:false}));f.live(true);await flush();
  assert.ok(f.find('Subtree claim page differs from the selected pool.'));
});

test('pool route retains exact sparse int64 journal boundaries, including empty backward page',async()=>{
  const f=fixture();f.route({view:'resources',pool:'permits',poolAfter:null,poolBefore:'9223372036854775807'});await flush();
  assert.equal(f.calls.find(([op])=>op==='pool_history_page')[1].before,9223372036854775807n);
  assert.ok(f.find('No entries at this journal boundary.'));
  assert.equal(f.calls.filter(([op])=>op==='pool_mutate').length,0);
});

test('read-only oversized hierarchy refuses edits and disposal ignores all pending responses',async()=>{
  const f=fixture();f.view.review.unavailable=true;f.update();await flush();
  assert.equal(f.find('Change total capacity…','button').disabled,true);
  const held=deferred();f.list(()=>held.promise);f.update();await flush();f.console.dispose();held.resolve([snapshot()]);await flush();
  assert.equal(f.ui.root.children.length,0);assert.equal(f.calls.filter(([op])=>op==='pool_mutate').length,0);
});


function hierarchySnapshot() {
  const view=snapshot();view.pool.capacity=12;view.reserved=1;view.available=4;view.children=[];
  view.partition={configured:12,direct_reserved:1,child_backing:7,committed:8,held_backing:12,uncarved_available:4,pending_return:0,deficit:0};
  view.review.hierarchy={schema_version:1,pool:view.pool.id,root:view.pool.id,pool_count:2,consumer_count:1,snapshot_sha256:'a'.repeat(64)};return view;
}
test('child review inherits units and scope, binds complete hierarchy and stales on sibling-only change',async()=>{
  const f=fixture();f.view=hierarchySnapshot();f.update();await flush();
  await f.find('Create subpool…','button').fire();
  assert.equal(f.find('Scope','select'),undefined);assert.equal(f.find('Count unit','field'),undefined);
  f.find('Subpool name','field').value='Compiler permits';f.find('Initial capacity','field').value='2';f.find('Why are you making this change?','area').value='Carve existing capacity';
  await f.find('Review change','button').fire();const confirm=f.find('Create subpool','button');assert.ok(confirm);
  assert.equal(f.ui.active.label,'Cancel change');
  const request=f.find('Exact request and complete consumer revisions').value;
  assert.equal(request.parent,'permits');assert.equal(request.scope,'global');assert.equal(request.unit,'permits');assert.equal(String(request.capacity),'2');
  f.view.review.hierarchy.snapshot_sha256='b'.repeat(64);f.update();await flush();
  assert.equal(confirm.disabled,true);await confirm.fire();assert.equal(f.calls.filter(([op])=>op==='pool_mutate').length,0);
});

test('subtree cursor history commits after successful current read and keeps direct child claims visible',async()=>{
  const f=fixture();let failure=false;
  f.consumers(params=>{if(params.after&&failure)throw Error('Read disconnected');return{pool:params.id,descendants:true,entries:[{use:{lease:params.after?'lease-b':'lease-a',owner:'501',reason:'Exact child work',state:'active'},direct_claims:[{pool:'permits',quantity:1}]}],cursor:params.after?'':'next',more:!params.after};});
  f.update();await flush();failure=true;await f.find('Next leases','button').fire();
  assert.equal(f.find('Previous leases','button').disabled,true);assert.ok(f.find('lease-a','button'));
  failure=false;await f.find('Next leases','button').fire();assert.equal(f.find('Previous leases','button').disabled,false);assert.ok(f.find('lease-b','button'));
  assert.ok(f.find('Review permits · 1 permits'));
  await f.find('Previous leases','button').fire();assert.equal(f.find('Previous leases','button').disabled,true);assert.ok(f.find('lease-a','button'));
});

test('closing after live refresh restores equivalent action or unavailable title',async()=>{
  const f=fixture();f.update();await flush();const before=f.find('Change total capacity…','button');await edit(f);
  f.view.pool.revision=8;f.update();await flush();assert.equal(before.isConnected,false);
  f.find('Change total capacity','dialog').close();assert.equal(f.ui.active,f.find('Change total capacity…','button'));
  await edit(f);f.list(()=>[]);f.update();await flush();f.find('Change total capacity','dialog').close();
  assert.equal(f.ui.active,f.find('Pool unavailable','heading'));
});

test('journal refresh reuses expanded entry and preserves semantic page-control focus',async()=>{
  const f=fixture();const change={sequence:1,operation:'set',at:'2026-10-26T10:00:00Z',actor:'501',reason:'Exact journal evidence',after:{...snapshot().pool,capacity:4},before:{capacity:3,revision:6},delta:1};
  f.history(params=>({pool:params.id,entries:[change],previous_before:null,next_after:5}));f.update();await flush();
  const technical=f.find('Journal entry 1');technical.open=true;f.find('Next page','button').focus();
  f.view.pool.revision=8;f.update();await flush();
  assert.equal(f.find('Journal entry 1'),technical);assert.equal(technical.open,true);assert.equal(f.ui.active,f.find('Next page','button'));
});


test('overview retains material pool warnings while management is closed; route opens the selected pool',async()=>{
  const f=fixture();f.view={...f.view,deficit:2,partition:{committed:3,direct_reserved:1,child_backing:2,held_backing:7,pending_return:5},review:{unavailable:true}};
  f.route({view:'resources',pool:null});await flush();
  assert.equal(f.find('Create pool…','button'),undefined);
  assert.ok(f.find('Manage pools…','button'));
  assert.ok([...f.ui.nodes].some(n=>n.visible&&n.label.includes('Deficit 2 permits')));
  assert.ok([...f.ui.nodes].some(n=>n.visible&&n.label.includes('Pending return 5 permits, held in parent 7 permits')));
  assert.ok([...f.ui.nodes].some(n=>n.visible&&n.label.includes('Read only: complete tree exceeds')));
  f.route({view:'resources',pool:'permits'});await flush();
  assert.ok(f.find('Review permits','heading'));
  assert.equal(f.find('Find custom pools','field'),undefined);
  assert.equal(f.find('Change total capacity…','button').disabled,true);
  const manager=[...f.ui.nodes].find(n=>n.kind==='dialog'&&n.label==='Custom resource pools');manager.close();
  assert.equal(f.find('Find custom pools','field'),undefined);
  assert.ok(f.find('Manage pools…','button'));
});


test('pool direct and subtree consumer names keep exact lease IDs as the action and secondary identity',async()=>{
 const opened=[],f=fixture({onLease:id=>opened.push(id)});f.view={...f.view,users:f.view.users.map(u=>({...u,lease_display_name:'Readable direct lease'}))};
 f.consumers(()=>({pool:'permits',descendants:true,entries:[{use:{lease:'subtree-lease',lease_display_name:'Readable subtree lease',quantity:1,state:'active',reason:'Subtree work',owner:'501'},direct_claims:[]}],cursor:'',more:false}));
 f.update();await flush();f.console.focusLease('lease-a');assert.ok(f.find('Lease ID lease-a','text'));await f.find('Readable direct lease','button').fire();
 assert.ok(f.find('Lease ID subtree-lease','text'));await f.find('Readable subtree lease','button').fire();
 assert.deepEqual(opened,['lease-a','subtree-lease']);
});


test('explicit review errors receive reading focus without background refresh stealing a draft field',async()=>{
  const f=fixture();f.list(()=>[]);f.route({view:'resources',pool:null});await flush();
  await f.find('Manage pools…','button').fire();await f.find('Create pool…','button').fire();
  const reason=f.find('Why are you making this change?','area');reason.value='Synthetic review from the last field';reason.focus();await f.find('Review change','button').fire();
  assert.match(f.ui.active.label,/Name must/);assert.equal(f.ui.active.kind,'text');
  const name=f.find('Pool name','field');name.focus();f.list(()=>{throw Error('Synthetic background refresh failure');});f.update();await flush();assert.equal(f.ui.active,name);
  assert.equal(f.calls.filter(([op])=>op==='pool_mutate').length,0);f.console.dispose();
});
