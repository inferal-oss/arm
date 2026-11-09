import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createMeshConsole,createKubeInventory} from './mesh.mjs';
import {createForwardConsole} from './forward.mjs';
import {exactKubernetesSelection} from './mesh-kubernetes-workflow.mjs';

// An effect-boundary double: this suite checks domain requests and lifecycle
// behavior. The parent integration separately checks real RDF/DOM/event output.
function graphFixture() {
  let active=null;
  class Component {
    constructor(kind,label='',props={}){this.kind=kind;this.label=label;this.children=[];this.events={};this.value='';this.checked=false;this.open=false;this.disabled=false;this.hidden=false;Object.assign(this,props);}
    set(props){Object.assign(this,props);return this;}
    append(...children){assert.ok(['stack','grid','toolbar','card','dialog','disclosure'].includes(this.kind),this.kind+' cannot own components');for(const child of children){child.detach();child.parent=this;this.children.push(child);}return this;}
    replace(...children){for(const child of this.children)child.parent=null;this.children=[];return this.append(...children);}
    on(kind,fn){(this.events[kind]??=[]).push(fn);return this;}
    async fire(kind){for(const fn of this.events[kind]??[])await fn();}
    focus(){active=this;}
    detach(){if(this.parent){this.parent.children=this.parent.children.filter(n=>n!==this);this.parent=null;}}
    dispose(){this.detach();this.disposed=true;}
    showModal(){this.open=true;}
    close(){this.open=false;for(const fn of this.events.close??[])fn();}
    get isConnected(){return this.root===true||Boolean(this.parent?.isConnected);}
  }
  const graph={get activeComponent(){return active;},batch:fn=>fn()};
  for(const kind of ['heading','text','badge','code'])graph[kind]=(label,props={})=>new Component(kind,String(label??''),typeof props==='object'?props:{});
  graph.stack=(children=[],props={})=>new Component('stack','',props).append(...children);
  graph.grid=(children=[],props={})=>new Component('grid','',props).append(...children);
  graph.toolbar=(label,children=[])=>new Component('toolbar',label).append(...children);
  graph.descriptions=pairs=>new Component('descriptions',pairs.map(([label,value])=>`${label}: ${value}`).join(' '),{pairs});
  graph.button=(label,fn,props={})=>new Component('button',label,props).on('activate',fn);
  for(const kind of ['field','area','check'])graph[kind]=(label,props={},fn)=>{const item=new Component(kind,label,props);if(fn)item.on(kind==='check'?'change':'input',fn);return item;};
  graph.select=(label,options=[],props={},fn)=>{const item=new Component('select',label,{options,...props});if(fn)item.on('change',fn);return item;};
  for(const kind of ['card','disclosure'])graph[kind]=(label,children=[],props={})=>new Component(kind,label,props).append(...children,...(props.actions??[]));
  graph.json=(value,label='JSON')=>graph.disclosure(label,[graph.code(JSON.stringify(value,(_,v)=>typeof v==='bigint'?String(v):v))]);
  graph.island=(name,widget,props={})=>new Component('island',props.label||name,props);
  const root=graph.stack();root.root=true;
  graph.dialog=(label,children=[])=>{const d=new Component('dialog',label).append(...children);root.append(d);return d;};
  const all=(node=root)=>[node,...node.children.flatMap(c=>all(c))];
  const find=(label,kind)=>all().find(n=>n.label===label&&(!kind||n.kind===kind));
  return {graph,root,all,find,dialog:()=>all().find(n=>n.kind==='dialog'&&n.open),text:()=>all().map(n=>n.label).join(' ')};
}
const flush=()=>new Promise(resolve=>setImmediate(resolve));

function meshFixture({saved=new Map(),loseDecision=false,losePrepare=false,storageFailure=false,onKubeInventory=()=>{}}={}) {
  const f=graphFixture(),calls=[],now=Date.now(),host='b'.repeat(32);let live=true,known=false,intercept=null;
  const node={host,name:'Build node',local:false,health:'connected',trust:{state:'active'},scope_freshness:{known:true,stale:false},outbound_scope:{target_host:host,grant:{permissions:['observe-host','node-stop']},node_stop:{supported:true,reason:'Usable fixed completion check'}}};
  const plan={schema:'arm.node-stop.plan/v1',key:'stop-ui',target:{host,owner:'501',home:'/home/operator/arm',backend_generation:'c'.repeat(32),certificate_sha256:'d'.repeat(64),spki_sha256:'e'.repeat(64),directory:{owner:501,inode:42,device:7},startup_contract:'arm-desired-state-gated-v1'},actor:{source_host:'a'.repeat(32),pair:'f'.repeat(64),grant_revision:7,certificate_sha256:'1'.repeat(64),spki_sha256:'2'.repeat(64)},desired_state_revision:9,policy:{force:false,wait:true},reason:'Finish maintenance',impact:{complete:true,scope_sha256:'3'.repeat(64),outstanding:1,obligations:[{id:'compile-lease',kind:'lease',state:'active',revision:4}],blockers:[],affected_peers:['a'.repeat(32)]},observed_at:new Date(now).toISOString(),expires_at:new Date(now+120000).toISOString(),supervisor_scope:'The host stays running. External service-manager ownership is unverified.'};
  const op={key:'stop-ui',kind:'stop',targets:['host:'+host],revision:3,phase:'awaiting_review',worker_live:true,approved:false,lookup_only:false,can_prepare_fresh:false,review_revision:2,review_sha256:'a'.repeat(64),review_expires_at:plan.expires_at,review:{operation:'stop-arm-node',key:'stop-ui',review_sha256:'a'.repeat(64),owner_username:'operator',history_used:4,history_capacity:128,completion_check:{available:true,detail:'Checks only the original ARM backend.'},plan}};
  const call=async(name,q)=>{
    calls.push([name,structuredClone(q)]);
    const result=await(async()=>{
      if(intercept){const result=intercept(name,q);if(result!==undefined)return await result;}
      if(name==='node_list')return {nodes:[node]};if(name==='node_inspect')return node;
      if(name==='mesh_operator_list')return known?[op]:[];if(name==='mesh_operator_connectors')return [];
      if(name==='mesh_operator_prepare'){
        known=true;op.key=q.key;op.review.key=q.key;plan.key=q.key;
        if(q.stop){plan.policy=q.stop.policy;plan.reason=q.stop.reason;}
        if(losePrepare)throw Error('prepare reply lost');return op;
      }
      if(name==='mesh_operator_get'||name==='mesh_operator_stop_lookup')return op;
      if(name==='mesh_operator_decide'){if(loseDecision)throw Error('decision reply lost');Object.assign(op,{phase:'rejected',worker_live:false,approved:false,can_prepare_fresh:true});return op;}
      throw Error(name);
    })();return structuredClone(result);
  };
  const console=createMeshConsole({ui:f.graph,root:f.root,call,connected:()=>live,onKubeInventory,storage:{getItem:k=>saved.get(k),setItem:(k,v)=>{if(storageFailure)throw Error('storage unavailable');saved.set(k,v);}}});
  const open=async()=>{await console.refresh();await f.find('View node','button').fire('activate');};
  const prepare=async()=>{
    await open();await f.find('Stop ARM…','button').fire('activate');
    f.find('Why should this ARM backend stop?','area').value='Finish maintenance';
    const mode=f.find('Stop policy','select');mode.value='wait';await mode.fire('change');
    await f.find('Prepare stop review','button').fire('activate');
  };
  return {...f,console,calls,node,op,saved,open,prepare,intercept:fn=>intercept=fn,offline(){live=false;console.updateConnection();},online(){live=true;console.updateConnection();}};
}

test('roster uses stock cards, a responsive grid, description lists and named action toolbars',async()=>{
  const f=meshFixture();await f.console.refresh();
  const card=f.find('Build node','card');assert.ok(card);assert.equal(card.parent.kind,'grid');
  assert.ok(card.children.some(c=>c.kind==='descriptions'&&c.pairs.some(([label,value])=>label==='Status'&&value==='connected')));
  const identity=f.find('Host identity · '+f.node.host.slice(0,8)+'…','disclosure');assert.ok(identity);assert.equal(identity.open,false);
  assert.deepEqual(identity.children[0].pairs,[["Stable host ID",f.node.host]]);
  const actions=f.find('Actions for Build node','toolbar');assert.ok(actions);assert.ok(actions.children.some(c=>c.label==='View node'));
  assert.ok(f.find('Mesh actions','toolbar'));
  await f.find('View node','button').fire('activate');
  assert.deepEqual(f.calls.find(([name])=>name==='node_inspect')[1],{host:f.node.host});
});

test('graph Stop composition defaults to refusal and retains exact reviewed keys, target and no-choice focus',async()=>{
  const f=meshFixture();await f.open();await f.find('Stop ARM…','button').fire('activate');
  assert.equal(f.find('Stop policy','select').value,'refuse');assert.equal(f.graph.activeComponent.label,'Keep running');
  await f.find('Prepare stop review','button').fire('activate');assert.equal(f.calls.filter(([n])=>n==='mesh_operator_prepare').length,0);
  f.find('Why should this ARM backend stop?','area').value='Finish maintenance';
  const policy=f.find('Stop policy','select');policy.value='wait';await policy.fire('change');
  await f.find('Prepare stop review','button').fire('activate');
  const request=f.calls.find(([n])=>n==='mesh_operator_prepare')[1];
  assert.deepEqual(request.stop,{host:f.node.host,reason:'Finish maintenance',policy:{force:false,wait:true}});
  assert.equal(f.graph.activeComponent.label,'Keep running');assert.ok(f.find('Stop this ARM backend','button'));
  assert.match(f.text(),/operator \(UID 501\)/);assert.match(f.text(),/compile-lease/);
  await f.find('Keep running','button').fire('activate');
  assert.deepEqual(f.calls.filter(([n])=>n==='mesh_operator_decide').map(([,q])=>q.approve),[false]);
});

test('uncertain Stop decision persists and reconnect never replays it',async()=>{
  const f=meshFixture({loseDecision:true});await f.prepare();await f.find('Stop this ARM backend','button').fire('activate');
  assert.deepEqual(JSON.parse(f.saved.get('arm-mesh-stop-decisions')),[f.op.key]);
  assert.equal(f.calls.filter(([n])=>n==='mesh_operator_decide').length,1);
  f.offline();f.online();await f.console.refresh();
  assert.equal(f.find('Stop this ARM backend','button'),undefined);
  assert.match(f.text(),/decision may already be recorded/);
  assert.equal(f.calls.filter(([n])=>n==='mesh_operator_decide').length,1);
  assert.equal(f.calls.filter(([n])=>n==='mesh_operator_stop_lookup').length,0);
});

test('lost preparation retains original key and unavailable storage prevents stop preparation',async()=>{
  const f=meshFixture({losePrepare:true});await f.prepare();
  const request=f.calls.find(([n])=>n==='mesh_operator_prepare')[1];
  assert.deepEqual(JSON.parse(f.saved.get('arm-mesh-pending')),[request.key]);
  assert.ok(f.find('Look up uncertain operation · '+request.key,'button'));
  assert.equal(f.find('Prepare stop review','button').disabled,true);
  f.offline();f.dialog().close();f.online();await f.console.refresh();
  assert.equal(f.calls.filter(([n])=>n==='mesh_operator_prepare').length,1);
  const blocked=meshFixture({storageFailure:true});await blocked.prepare();
  assert.equal(blocked.calls.filter(([n])=>n==='mesh_operator_prepare').length,0);
  assert.match(blocked.text(),/storage unavailable/);
});

test('stale destination stop support disables entry and disconnect guards exact decisions',async()=>{
  const f=meshFixture();f.node.scope_freshness.stale=true;await f.open();
  assert.equal(f.find('Stop ARM…','button').disabled,true);
  await f.find('Stop ARM…','button').fire('activate');assert.equal(f.find('Prepare stop review','button'),undefined);
  const live=meshFixture();await live.prepare();const approve=live.find('Stop this ARM backend','button');
  live.offline();assert.equal(approve.disabled,true);await approve.fire('activate');
  assert.equal(live.calls.filter(([n])=>n==='mesh_operator_decide').length,0);
});

test('stock connect choices preserve directional grants and no automatic mutation on selection',async()=>{
  const f=meshFixture();await f.find('Connect a node…','button').fire('activate');await flush();
  f.find('Verified SSH destination','field').value='private-fixture';f.find('Reason for connecting','area').value='Reviewed connection';
  assert.equal(f.calls.filter(([n])=>n==='mesh_operator_prepare').length,0);
  await f.find('Prepare connection review','button').fire('activate');
  const request=f.calls.find(([n])=>n==='mesh_operator_prepare')[1];
  assert.deepEqual(request.connect.source_permissions_on_target.permissions,['observe-host']);
  assert.deepEqual(request.connect.target_permissions_on_source.permissions,[]);
});

test('read-only Kubernetes selection expires across context edits and disconnect',async()=>{
  const f=graphFixture(),calls=[];let live=true;
  const inventory=createKubeInventory({ui:f.graph,root:f.root,connected:()=>live,call:async(name,q)=>{
    calls.push([name,q]);if(name==='kube_contexts')return[{name:'fixture',cluster_name:'private',namespace:'default',current:true}];
    const data=kubeData(q);data.nodes[0].uid='exact-uid';return data;
  }});
  await f.find('Browse Kubernetes Nodes','button').fire('activate');await f.find('Select this Node','button').fire('activate');
  assert.equal(inventory.getSelection().node.uid,'exact-uid');
  const query=f.find('Find a Node','field');query.value='other';await query.fire('input');assert.equal(inventory.getSelection(),null);
  await inventory.refresh();await f.find('Select this Node','button').fire('activate');live=false;inventory.updateConnection();assert.equal(inventory.getSelection(),null);
  assert.ok(calls.every(([name])=>['kube_contexts','kube_nodes'].includes(name)));inventory.dispose();
});

const kubeData=(request={context:'fixture',namespace:'default'})=>({cluster:{context:request.context,server:'https://cluster.example:6443',ca_sha256:'a'.repeat(64),auth_subject:'fixture-user',auth_groups:['authenticated']},inventory:{total:1,matched:1,truncated:false},namespace:{name:request.namespace,uid:'namespace-uid'},nodes:[{name:'worker',uid:'node-uid',os:'linux',arch:'arm64',ready:true,allocatable_cpu:'180000000000n',allocatable_memory:'535328752Ki',allocatable_ephemeral:'20Gi',capacity:{cpu:'192',memory:'512Gi','ephemeral-storage':'25Gi'},allocatable:Object.fromEntries(Array.from({length:14},(_,i)=>['vendor.example/resource-'+i,String(i)])),conditions:['Ready=True'],zone:'west',instance_type:'large'}]});

for (const [entry, primary] of [['Install ARM on a host…', 'Prepare installation review'], ['Connect a node…', 'Prepare connection review']]) {
 test(`${entry} shares Node discovery and fences unsupported effects`, async () => {
  const f=meshFixture();
  f.intercept((name,q)=>name==='kube_contexts'?[{name:'fixture',namespace:'default',current:true}]:name==='kube_nodes'?kubeData(q):name==='mesh_operator_kubernetes_artifacts'?[]:undefined);
  try {
   assert.equal(f.find('Available Kubernetes Nodes','card'),undefined);
   await f.find(entry,'button').fire('activate');await flush();
   const method=f.find('Connection method','select');method.value='@kubernetes';await method.fire('change');await flush();
   assert.equal(f.find('Verified SSH destination','field').hidden,true);
   await f.find('Select this Node','button').fire('activate');
   assert.match(f.text(),/reviews are unavailable in this build/);
   assert.equal(f.find(primary,'button').disabled,true);
   await f.find(primary,'button').fire('activate');
   assert.equal(f.calls.some(([name])=>['mesh_operator_prepare','mesh_operator_provision_prepare','mesh_operator_kubernetes_prepare','mesh_operator_decide'].includes(name)),false);
   assert.equal(f.saved.has('arm-mesh-kubernetes-pending'),false);
   method.value='';await method.fire('change');
   assert.equal(f.find('Verified SSH destination','field').hidden,false);
   assert.equal(f.find(primary,'button').disabled,false);
   assert.equal(f.calls.filter(([name])=>name==='kube_nodes').length,1);
  } finally { f.console.dispose(); }
 });
}

test('exact Kubernetes selection preserves cluster groups and refuses missing or changed identity',()=>{
 const data=kubeData();Object.assign(data.cluster,{auth_subject:'operator',auth_groups:['authenticated']});
 const selection={context:'fixture',cluster:data.cluster,namespace:data.namespace,node:data.nodes[0]};
 const exact=exactKubernetesSelection(selection);assert.deepEqual(exact.cluster.auth_groups,['authenticated']);exact.cluster.auth_groups.push('changed');assert.deepEqual(selection.cluster.auth_groups,['authenticated']);
 for(const changed of [{cluster:{...data.cluster,context:'other'}},{cluster:{...data.cluster,server:'http://cluster.example'}},{cluster:{...data.cluster,auth_groups:null}},{namespace:{name:'default'}},{node:{...selection.node,uid:''}},{node:{...selection.node,taints:['dedicated=other:NoSchedule']}}])assert.throws(()=>exactKubernetesSelection({...selection,...changed}));
});

test('Kubernetes stock cards summarize quantities, retain all exact details and focus only matching accepted identity',async()=>{
  const f=graphFixture(),published=[],calls=[];
  const inventory=createKubeInventory({ui:f.graph,root:f.root,connected:()=>true,onKubeInventory:value=>published.push(value),call:async(name,q)=>{calls.push([name,q]);if(name==='kube_contexts')return[{name:'fixture',cluster_name:'cluster',namespace:'default',current:true}];const data=kubeData(q);data.nodes.push({...data.nodes[0],name:'tainted-worker',uid:'tainted-uid',taints:['dedicated=other:NoSchedule']});data.inventory.total=data.inventory.matched=2;return data;}});
  assert.equal(await inventory.open(),true);const snapshot=published.at(-1),card=f.find('worker','card');
  assert.equal(card.parent.kind,'grid');assert.equal(card.parent.columns,2);
  const facts=card.children.find(c=>c.kind==='descriptions').pairs;
  assert.deepEqual(facts.slice(0,3),[['CPU allocatable','180 cores'],['RAM allocatable','≈ 510.5 GiB'],['Disk allocatable','20 GiB']]);
  const technical=card.children.find(c=>c.label==='Technical details');assert.equal(technical.open,false);
  assert.ok(technical.children[0].pairs.some(([label,value])=>label==='Exact UID'&&value==='node-uid'));
  assert.ok(technical.children[0].pairs.some(([label,value])=>label==='CPU allocatable (raw)'&&value==='180000000000n'));
  assert.ok(technical.children[0].pairs.some(([label])=>label==='Allocatable · vendor.example/resource-13'));
  const choose=f.find('Select this Node','button');assert.ok(card.actions.includes(choose));
  const unavailable=f.find('tainted-worker','card').actions[0];assert.equal(unavailable.disabled,true);await unavailable.fire('activate');assert.equal(inventory.getSelection(),null);
  const reference={uid:'node-uid',context:'fixture',namespace:snapshot.namespace,server:snapshot.cluster.server,ca_sha256:snapshot.cluster.ca_sha256,node:snapshot.nodes[0]};
  const reads=calls.length;assert.equal(inventory.focusKubernetesNode(reference),true);assert.equal(f.graph.activeComponent,card);assert.equal(inventory.getSelection(),null);assert.equal(calls.length,reads);
  for(const invalid of [{uid:'other'},{context:'other'},{namespace:'other'},{namespace:{name:'default',uid:'replaced'}},{server:'https://replacement.example'},{ca_sha256:'b'.repeat(64)},{node:{uid:'other'}}])assert.equal(inventory.focusKubernetesNode({...reference,...invalid}),false);
  snapshot.nodes[0].name='external mutation';snapshot.namespace.uid='external namespace';
  assert.equal(inventory.focusKubernetesNode({...reference,namespace:{name:'default',uid:'namespace-uid'},node:{uid:'node-uid'}}),true);
  await choose.fire('activate');assert.equal(inventory.getSelection().node.allocatable_memory,'535328752Ki');assert.equal(inventory.getSelection().node.allocatable_cpu,'180000000000n');
  assert.ok(calls.every(([name])=>['kube_contexts','kube_nodes'].includes(name)));inventory.dispose();assert.equal(published.at(-1),null);assert.equal(f.root.children.length,0);
});

test('Kubernetes technical rows stay bounded while full records and published data retain late extended fields',async()=>{
  const f=graphFixture(),published=[],data=kubeData();
  data.nodes[0].capacity=Object.fromEntries(Array.from({length:35},(_,i)=>['capacity-'+i,String(i)]));
  data.nodes[0].allocatable=Object.fromEntries(Array.from({length:300},(_,i)=>['vendor.example/resource-'+i,String(i)]));
  data.nodes[0].taints=['dedicated=other:NoSchedule'];
  const inventory=createKubeInventory({ui:f.graph,root:f.root,connected:()=>true,onKubeInventory:value=>published.push(value),call:async name=>name==='kube_contexts'?[{name:'fixture',namespace:'default',current:true}]:data});
  await inventory.open();const card=f.find('worker','card'),technical=card.children.find(c=>c.label==='Technical details');
  const pairs=technical.children[0].pairs;
  assert.equal(pairs.filter(([label])=>label.startsWith('Capacity · ')||label.startsWith('Allocatable · ')).length,64);
  assert.equal(pairs.length,74);assert.ok(pairs.some(([label,value])=>label==='Exact UID'&&value==='node-uid'));assert.ok(pairs.some(([label,value])=>label==='Taints'&&value==='dedicated=other:NoSchedule'));
  assert.match(technical.children.find(c=>c.kind==='text').label,/271 additional capacity or allocatable fields omitted/);
  const full=technical.children.find(c=>c.label==='Exact Kubernetes Node record');assert.match(full.children[0].label,/"vendor.example\/resource-299":"299"/);
  assert.equal(Object.keys(published.at(-1).nodes[0].allocatable).length,300);assert.equal(published.at(-1).nodes[0].allocatable['vendor.example/resource-299'],'299');
  inventory.dispose();
});

test('Kubernetes snapshots clear synchronously and reject delayed responses across edit, close and disconnect boundaries',async()=>{
  const f=graphFixture(),published=[],calls=[];let live=true,next=null;
  const inventory=createKubeInventory({ui:f.graph,root:f.root,connected:()=>live,onKubeInventory:value=>published.push(value),call:async(name,q)=>{calls.push([name,q]);if(name==='kube_contexts')return[{name:'fixture',namespace:'default',current:true},{name:'other',namespace:'work'}];if(next){const pending=next;next=null;return pending;}return kubeData(q);}});
  await inventory.open();const oldChoose=f.find('Select this Node','button');await oldChoose.fire('activate');
  const query=f.find('Find a Node','field');query.value='new';await query.fire('input');assert.equal(published.at(-1),null);assert.equal(inventory.getSelection(),null);
  let release;next=new Promise(resolve=>release=resolve);const pending=inventory.refresh();assert.equal(published.at(-1),null);
  await f.find('Hide Kubernetes Nodes','button').fire('activate');const count=published.length;
  release(kubeData({context:'fixture',namespace:'default'}));assert.equal(await pending,false);assert.equal(published.length,count);await oldChoose.fire('activate');assert.equal(inventory.getSelection(),null);
  assert.equal(await inventory.open(),true);
  const namespace=f.find('Namespace','field');namespace.value='work';await namespace.fire('input');assert.equal(published.at(-1),null);
  await inventory.refresh();assert.equal(published.at(-1).namespace.name,'work');
  const context=f.find('Context','select');context.value='other';await context.fire('change');assert.equal(published.at(-1).context,'other');assert.equal(published.at(-1).namespace.name,'work');
  live=false;inventory.updateConnection();assert.equal(published.at(-1),null);assert.equal(inventory.focusKubernetesNode({uid:'node-uid',context:'other',namespace:'work'}),false);
  const before=calls.length;inventory.dispose();await inventory.refresh();await inventory.open();assert.equal(calls.length,before);assert.equal(published.at(-1),null);
});

test('Kubernetes publication checks current field values as well as request versions',async()=>{
  const f=graphFixture(),published=[];let release,next=null;
  const inventory=createKubeInventory({ui:f.graph,root:f.root,connected:()=>true,onKubeInventory:value=>published.push(value),call:async(name,q)=>{if(name==='kube_contexts')return[{name:'fixture',namespace:'default',current:true}];if(next){const result=next;next=null;return result;}return kubeData(q);}});
  await inventory.open();next=new Promise(resolve=>release=resolve);const pending=inventory.refresh();
  f.find('Namespace','field').value='replacement';release(kubeData());assert.equal(await pending,false);assert.equal(published.at(-1),null);
  inventory.dispose();
});

test('Kubernetes explicit open is idempotent while reading contexts and the mesh API forwards accepted snapshots',async()=>{
  const f=graphFixture(),calls=[];let release;const contexts=new Promise(resolve=>release=resolve);
  const inventory=createKubeInventory({ui:f.graph,root:f.root,connected:()=>true,call:async(name,q)=>{calls.push(name);return name==='kube_contexts'?contexts:kubeData(q);}});
  const first=inventory.open(),second=inventory.open();assert.deepEqual(calls,['kube_contexts']);release([{name:'fixture',namespace:'default',current:true}]);assert.equal(await first,true);assert.equal(await second,true);await inventory.open();assert.deepEqual(calls,['kube_contexts','kube_nodes']);assert.equal(inventory.getSelection(),null);inventory.dispose();
  const published=[],mesh=meshFixture({onKubeInventory:value=>published.push(value)});
  mesh.intercept((name,q)=>name==='kube_contexts'?[{name:'fixture',namespace:'default',current:true}]:name==='kube_nodes'?kubeData(q):undefined);
  assert.equal(await mesh.console.openKubernetesInventory(),true);assert.equal(published.at(-1).context,'fixture');
  assert.equal(mesh.console.focusKubernetesNode({uid:'node-uid',context:'fixture',namespace:'default'}),true);
  assert.equal(mesh.calls.filter(([name])=>name==='mesh_operator_prepare').length,0);mesh.console.dispose();assert.equal(published.at(-1),null);
});

test('graph forwarding retains private token on uncertain start, requires acknowledgment, and sends only token digest',async()=>{
  const f=graphFixture(),calls=[];const forward=createForwardConsole({ui:f.graph,root:f.root,connected:()=>true,call:async(name,q)=>{
    calls.push([name,q]);if(name==='mesh_forward_start')throw Error('reply lost');if(name==='mesh_forward_list')return[];throw Error(name);
  }});
  for(const [label,value]of [['Workspace ID','a'.repeat(32)],['Destination host ID','b'.repeat(32)],['Destination service name','http'],['Resource reservation reason','Private fixture']])f.find(label,'field').value=value;
  await f.find('Prepare private token','button').fire('activate');await f.find('Start exact listener','button').fire('activate');
  assert.equal(calls.length,0);f.find('I saved the key and token privately','check').checked=true;
  await f.find('Start exact listener','button').fire('activate');
  const request=calls.find(([name])=>name==='mesh_forward_start')[1];assert.match(request.token_sha256,/^[a-f0-9]{64}$/);assert.equal(request.token,undefined);
  assert.match(f.text(),/Keep this token/);assert.equal(f.find('Prepare private token','button').disabled,true);forward.updateConnection();
  await f.find('Prepare private token','button').fire('activate');assert.equal(calls.filter(([name])=>name==='mesh_forward_start').length,1);
  assert.match(f.text(),/Inspect listener/);forward.dispose();assert.equal(f.root.children.length,0);
});

function provisionFixture({losePrepare=false,loseDecision=false}={}) {
  const f=meshFixture();let op=null;
  f.intercept((name,q)=>{
    if(name==='node_list')return{nodes:[]};if(name==='mesh_operator_list')return op?[op]:[];
    if(name==='mesh_operator_connectors')return[{id:'native-profile',ssh_destination:'build-host'}];
    if(name==='mesh_operator_provision_prepare'){
      const image='b'.repeat(64),transfer={install_root:'/home/operator/.local/share/arm/provisioned',key:q.key,sha256:image,size_bytes:4096,expected_uid:'1000',os:'linux',arch:'arm64',expected_arm_version:'0.1.0-dev',expected_lifecycle_protocol:1,reason:q.provision.reason};
      op={key:q.key,kind:'provision',targets:['ssh:build-host'],revision:3,phase:'awaiting_review',worker_live:true,approved:false,lookup_only:false,review_revision:2,review_sha256:'d'.repeat(64),review_expires_at:new Date(Date.now()+120000).toISOString(),review:{operation:'install-and-start-arm-node',key:q.key,request:{connection_key:q.provision.connection_key,ssh_destination:'build-host',transfer,ssh_identity:{account:'operator',host_key:'ssh-ed25519 SHA256:fixture'},remote_home:'/home/operator/.local/share/arm',startup:{schema:1,resources:{cpu:2,memory_bytes:0,disk_bytes:0},listen:'127.0.0.1:0'}},artifact_source:'verified local catalog',final_binary:transfer.install_root+'/artifact-'+image+'/arm',effect:'Install these exact user-level bytes. Review mesh trust separately.'}};
      if(losePrepare)throw Error('preparation reply lost');return op;
    }
    if(name==='mesh_operator_get'||name==='mesh_operator_provision_lookup')return op;
    if(name==='mesh_operator_decide'){
      if(loseDecision)throw Error('decision reply lost');
      Object.assign(op,{phase:q.approve?'applying':'rejected',approved:q.approve,revision:op.revision+1,worker_live:q.approve,lookup_only:q.approve,...(!q.approve?{error:{code:'confirmation_declined',message:'Kept unchanged'}}:{})});return op;
    }
  });
  const prepare=async()=>{
    await f.find('Install ARM on a host…','button').fire('activate');await flush();
    f.find('Verified SSH destination','field').value='build-host';f.find('Reason for installing ARM','area').value='Build on spare capacity';
    await f.find('Prepare installation review','button').fire('activate');
  };
  return {...f,prepare,get install(){return op;}};
}

test('installation preserves two distinct decisions, exact software review and decline focus',async()=>{
  const f=provisionFixture();await f.prepare();
  assert.equal(f.graph.activeComponent.label,'Keep host unchanged');assert.ok(f.find('Install reviewed ARM node','button'));
  assert.match(f.text(),/operator \(UID 1000\)/);assert.match(f.text(),/Two separate decisions/);
  assert.ok(f.find('SSH pin, image digest and startup settings','disclosure'));
  assert.equal(f.find('Trust and connect','button'),undefined);
  await f.find('Keep host unchanged','button').fire('activate');
  const decision=f.calls.find(([n])=>n==='mesh_operator_decide')[1];
  assert.equal(decision.key,f.install.key);assert.equal(decision.review_sha256,f.install.review_sha256);assert.equal(decision.approve,false);
  assert.equal(f.find('Review mesh connection…','button'),undefined);
});

test('uncertain installation preparation or decision retains original exact-key data without resend',async()=>{
  const lost=provisionFixture({losePrepare:true});await lost.prepare();
  const q=lost.calls.find(([n])=>n==='mesh_operator_provision_prepare')[1];
  assert.deepEqual(JSON.parse(lost.saved.get('arm-mesh-provision-pending')).prepares,[q]);
  lost.offline();lost.online();await lost.console.refresh();
  assert.equal(lost.calls.filter(([n])=>n==='mesh_operator_provision_prepare').length,1);
  const f=provisionFixture({loseDecision:true});await f.prepare();await f.find('Install reviewed ARM node','button').fire('activate');
  assert.deepEqual(JSON.parse(f.saved.get('arm-mesh-provision-pending')).decisions,[f.install.key]);
  f.offline();f.online();await f.console.refresh();
  assert.equal(f.find('Install reviewed ARM node','button'),undefined);
  assert.equal(f.calls.filter(([n])=>n==='mesh_operator_decide').length,1);
  assert.equal(f.calls.filter(([n])=>n==='mesh_operator_provision_lookup').length,0);
});

function uninstallFixture({loseDecision=false,loseFinish=false}={}) {
  const f=meshFixture(),op=f.op,stop=structuredClone(op.review.plan),id={device:7,inode:50,owner:501,born_seconds:1700000000,born_nanoseconds:1,birth_known:true};
  stop.key='uninstall-'+'1'.repeat(64);stop.impact.obligations=[];stop.impact.outstanding=0;
  const installation={install_root:'/private/install',key:'2'.repeat(64),sha256:'3'.repeat(64),size_bytes:4096,expected_uid:'501',os:'darwin',arch:'arm64',expected_arm_version:'dev',expected_lifecycle_protocol:1,reason:'managed publication'};
  const review={schema:'arm.node-uninstall/v1',key:op.key,reason:stop.reason,installation,ownership:{schema:1,transfer:structuredClone(installation),root_identity:id,artifact_identity:{...id,inode:51},binary_identity:{...id,inode:52},referenced_homes:[stop.target.home]},stop:{plan:stop,review_sha256:'4'.repeat(64),owner_username:'operator'},retained_recovery_helper:'/private/install/recovery-'+'5'.repeat(64)+'/arm',preserved:['database','node identity and grants','configuration and startup policy','lease and operation history','backups and retained artifacts','companion catalog configuration','recovery helper and uninstall receipt']};
  Object.assign(op,{kind:'uninstall',review});f.node.outbound_scope.grant.permissions.push('node-uninstall');
  f.intercept((name,q)=>{
    if(name==='mesh_operator_uninstall_context')return{host:f.node.host,candidates:[{provision_key:'publication-original',home:stop.target.home,binary:'/private/install/artifact-'+installation.sha256+'/arm',sha256:installation.sha256,size_bytes:4096,os:'darwin',arch:'arm64'}]};
    if(name==='mesh_operator_prepare'){op.key=q.key;review.key=q.key;review.reason=stop.reason=q.uninstall.reason;stop.policy=q.uninstall.policy;return op;}
    if(name==='mesh_operator_decide'){
      if(loseDecision)throw Error('decision reply lost');
      Object.assign(op,{phase:q.approve?'incomplete':'rejected',approved:q.approve,lookup_only:q.approve,worker_live:false,revision:op.revision+1,error:q.approve?undefined:{code:'confirmation_declined',message:'Kept installed'}});
      if(q.approve)op.result={receipt:{schema:'arm.node-uninstall/v1',plan:structuredClone(review),review_sha256:op.review_sha256,phase:'stop-unknown',recovery_identity:{...id,inode:53}},original_stop_observed:false,artifact_absent:false};return op;
    }
    if(name==='mesh_operator_uninstall_lookup'){if(op.result)op.result.original_stop_observed=true;op.revision++;return op;}
    if(name==='mesh_operator_uninstall_finish'){if(loseFinish)throw Error('finish reply lost');op.result.receipt.phase='removed';op.result.original_stop_observed=op.result.artifact_absent=true;op.phase='completed';op.revision++;return op;}
  });
  const prepare=async()=>{
    await f.open();await f.find('Uninstall managed ARM…','button').fire('activate');
    const choice=f.find('Original installation','select');choice.value='publication-original';await choice.fire('change');
    f.find('Why remove this managed installation?','area').value='Finish maintenance';
    await f.find('Prepare uninstall review','button').fire('activate');
  };
  return {...f,prepare};
}

test('uninstall renders exact original installation and retained data; finish requires fresh native lookup and separate decision',async()=>{
  const f=uninstallFixture();await f.prepare();
  assert.equal(f.graph.activeComponent.label,'Keep installed');assert.ok(f.find('Approve managed uninstall','button'));
  assert.match(f.text(),/Original transfer/);assert.match(f.text(),/database/);
  await f.find('Approve managed uninstall','button').fire('activate');
  assert.equal(f.find('Review finishing removal…','button'),undefined);
  await f.find('Inspect original uninstall','button').fire('activate');
  await f.find('Review finishing removal…','button').fire('activate');
  assert.equal(f.calls.filter(([n])=>n==='mesh_operator_uninstall_finish').length,0);
  await f.find('Finish exact removal','button').fire('activate');
  const finish=f.calls.find(([n])=>n==='mesh_operator_uninstall_finish')[1];
  assert.equal(finish.key,f.op.key);assert.equal(finish.review_sha256,f.op.review_sha256);
  assert.match(f.text(),/Managed executable removal verified/);
});

test('uncertain uninstall decision cannot be resent or implicitly finished after reconnect',async()=>{
  const f=uninstallFixture({loseDecision:true});await f.prepare();await f.find('Approve managed uninstall','button').fire('activate');
  assert.deepEqual(JSON.parse(f.saved.get('arm-mesh-uninstall-pending')).decisions,[f.op.key]);
  f.offline();f.online();await f.console.refresh();
  assert.equal(f.find('Approve managed uninstall','button'),undefined);
  assert.equal(f.find('Review finishing removal…','button'),undefined);
  assert.equal(f.calls.filter(([n])=>n==='mesh_operator_decide').length,1);
  assert.equal(f.calls.filter(([n])=>n==='mesh_operator_uninstall_finish').length,0);
});


test('chosen node mounts advisory availability and closes its exact panel owner',async()=>{
 const f=meshFixture();f.node.display_name='CustomPeer';
 f.intercept((name,q)=>{
  if(name==='node_availability_read_v1')return {schema:1,host:q.host,owner_declared:{profile:'intermittent',revision:1,updated_at:'2026-10-27T00:00:00Z'},observed_at:'2026-10-27T00:00:00Z'};
  if(name==='node_availability_inspect_v1')return {schema:1,host:q.host,connection_state:'offline',declaration_state:'stale',resource_freshness:{cpu:'stale',memory:'stale',disk:'stale'}};
 });
 await f.open();await flush();
 assert.ok(f.find('Availability','card'));assert.ok(f.text().includes('CustomPeer'));assert.match(f.text(),/Mobile \/ intermittent.*owner declared/);assert.match(f.text(),/Offline/);assert.match(f.text(),/Stale; unavailable for placement/);
 assert.equal(f.find('Save owner availability','button'),undefined);
 const calls=f.calls.filter(([n])=>n==='node_availability_read_v1'||n==='node_availability_inspect_v1');assert.ok(calls.length>=2);for(const [,q]of calls)assert.deepEqual(q,{host:f.node.host});
 const box=f.find('Availability','card');await f.find('Close','button').fire('activate');assert.equal(box.disposed,true);f.console.dispose();
});

test('late declaration cannot repopulate a disposed chosen node panel',async()=>{
 const f=meshFixture();let release;
 const held=new Promise(resolve=>{release=resolve;});
 f.intercept(name=>name==='node_availability_read_v1'?held:undefined);
 await f.open();const box=f.find('Availability','card');assert.ok(box);
 await f.find('Close','button').fire('activate');
 release({schema:1,host:f.node.host,owner_declared:{profile:'stable',revision:1,updated_at:'2026-10-27T00:00:00Z'},observed_at:'2026-10-27T00:00:00Z'});await flush();
 assert.equal(box.disposed,true);assert.equal(f.find('Availability','card'),undefined);assert.doesNotMatch(f.text(),/Stable \(owner declared\)/);f.console.dispose();
});


test('stock node refresh retains a pending owner update across its panel replacement',async()=>{
 const f=meshFixture();f.node.local=true;let reject;
 const pending=new Promise((resolve,rejectPromise)=>{reject=rejectPromise;});
 const report={schema:1,host:f.node.host,owner_declared:{profile:'intermittent',revision:1,updated_at:'2026-10-27T00:00:00Z'},observed_at:'2026-10-27T00:00:00Z'};
 f.intercept((name,q)=>{
  if(name==='node_list')return {source_host:f.node.host,nodes:[f.node]};
  if(name==='node_availability_read_v1')return report;
  if(name==='node_availability_inspect_v1')return {schema:1,host:q.host,connection_state:'reachable',resource_freshness:{cpu:'unknown',memory:'unknown',disk:'unknown'}};
  if(name==='node_availability_set_v1')return pending;
 });
 await f.open();await flush();f.find('Private reason for changing availability','area').value='owner declaration';
 const write=f.find('Save owner availability','button').fire('activate');await flush();
 const original=f.calls.find(([name])=>name==='node_availability_set_v1')[1];
 await f.console.refresh(true);await flush();
 assert.match(f.text(),new RegExp(original.key));
 f.find('Private reason for changing availability','area').value='owner declaration';await f.find('Save owner availability','button').fire('activate');assert.equal(f.calls.filter(([n])=>n==='node_availability_set_v1').length,1);
 reject(Error('original reply lost'));await write;
 await f.find('Save owner availability','button').fire('activate');assert.equal(f.calls.filter(([n])=>n==='node_availability_set_v1').length,1);
 await f.find('Read current declaration','button').fire('activate');assert.equal(f.find('Save owner availability','button').disabled,false);f.console.dispose();
});


test('mesh optional display names update without changing the exact selected host or supplied name',async()=>{
 const f=meshFixture();f.node.name='';f.node.display_name='readable-peer';await f.console.refresh();assert.ok(f.text().includes('readable-peer'));
 f.node.display_name='updated-peer';await f.console.refresh();assert.ok(f.text().includes('updated-peer'));
 f.node.display_name='';f.node.name='Chosen node';await f.console.refresh();assert.ok(f.text().includes('Chosen node'));
 await f.open();assert.ok(f.calls.some(([name,p])=>name==='node_inspect'&&p.host===f.node.host));
 assert.equal(f.calls.some(([,p])=>p?.host==='readable-peer'||p?.host==='updated-peer'),false);
});

for(const mode of ['Install ARM on a host…','Connect a node…']){
  test(`${mode} keeps an unavailable cluster explicit inside the selected transport`,async()=>{
    const f=meshFixture(),reads=[];
    f.intercept((name,q)=>{
      if(name==='kube_contexts'){reads.push(name);return[{name:'fixture',cluster_name:'synthetic',namespace:'default',current:true}];}
      if(name==='kube_nodes'){reads.push(name);throw Object.assign(Error('private API diagnostic'),{code:'kubernetes_unavailable'});}
      if(name==='mesh_operator_kubernetes_artifacts')return[];
    });
    await f.find(mode,'button').fire('activate');await flush();
    const method=f.find('Connection method','select');
    assert.ok(method.options.some(o=>o.label==='SSH destination'));
    assert.ok(method.options.some(o=>o.label==='Kubernetes Pod'));
    method.value='@kubernetes';await method.fire('change');await flush();
    assert.deepEqual(reads,['kube_contexts','kube_nodes'],'the actual inventory request must complete before checking its unavailable state');
    assert.match(f.text(),/Cluster unavailable\. Could not load Nodes/);
    assert.doesNotMatch(f.text(),/The cluster reports no Nodes|No Nodes match this filter/);
    assert.equal(f.find('Select this Node','button'),undefined);
    assert.equal(f.find('Available Kubernetes Nodes','card'),undefined);
    assert.deepEqual(reads,['kube_contexts','kube_nodes']);
    assert.equal(f.calls.some(([name])=>['mesh_operator_prepare','mesh_operator_kubernetes_prepare','mesh_operator_decide'].includes(name)),false);
    method.value='';await method.fire('change');
    assert.equal(f.find('Verified SSH destination','field').hidden,false);
    f.console.dispose();
  });
}

for(const [code,notice] of [['kubernetes_forbidden','Access denied.'],['kubernetes_unauthorized','Credentials were not accepted.'],['kubernetes_not_found','no longer exists.'],['kubernetes_timeout','took too long'],['kubernetes_cancelled','read was cancelled.']]){
 test(`Kubernetes inventory keeps ${code} separate from empty Nodes`,async()=>{
  const f=graphFixture();
  const inventory=createKubeInventory({ui:f.graph,root:f.root,connected:()=>true,call:async name=>{
    if(name==='kube_contexts')return[{name:'fixture',namespace:'default',current:true}];
    throw Object.assign(Error('private API endpoint credential diagnostic'),{code});
  }});
  try{assert.equal(await inventory.open(),false);assert.ok(f.text().includes(notice));assert.doesNotMatch(f.text(),/private API|The cluster reports no Nodes|No Nodes match/);assert.equal(inventory.getSelection(),null);}
  finally{inventory.dispose();}
 });
}

for(const [name,data,query,expected]of[
 ['true empty',{nodes:[],inventory:{total:0,matched:0,truncated:false}},'','The cluster reports no Nodes.'],
 ['filtered empty',{nodes:[],inventory:{total:3,matched:0,truncated:false}},'missing','No Nodes match this filter.'],
 ['malformed nodes',{nodes:null,inventory:{total:0,matched:0,truncated:false}},'','Could not use the Node inventory returned by Kubernetes.'],
 ['missing empty counts',{nodes:[]},'','Could not use the Node inventory returned by Kubernetes.'],
 ['inconsistent counts',{nodes:[],inventory:{total:3,matched:0,truncated:false}},'','Could not use the Node inventory returned by Kubernetes.']
]){
 test(`Kubernetes inventory distinguishes ${name}`,async()=>{
  const f=graphFixture();let response=kubeData();
  const inventory=createKubeInventory({ui:f.graph,root:f.root,connected:()=>true,call:async name=>name==='kube_contexts'?[{name:'fixture',namespace:'default',current:true}]:response});
  try{
   assert.equal(await inventory.open(),true);await f.find('Select this Node','button').fire('activate');assert.ok(inventory.getSelection());
   response={...kubeData(),...data};f.find('Find a Node','field').value=query;
   await inventory.refresh();assert.ok(f.text().includes(expected),f.text());assert.equal(inventory.getSelection(),null);assert.equal(f.find('Select this Node','button'),undefined);
   if(name.includes('malformed')||name.includes('counts'))assert.doesNotMatch(f.text(),/The cluster reports no Nodes|No Nodes match this filter/);
  }finally{inventory.dispose();}
 });
}

for (const [name, mutate] of [
 ['truthy readiness', d=>d.nodes[0].ready='true'],
 ['truthy cordon', d=>d.nodes[0].unschedulable='false'],
 ['malformed taints', d=>d.nodes[0].taints='none'],
 ['duplicate UID', d=>{d.nodes.push({...d.nodes[0]});d.inventory.total=d.inventory.matched=2;}],
 ['missing namespace UID', d=>delete d.namespace.uid],
 ['changed cluster', d=>d.cluster.context='replacement'],
 ['missing positive counts', d=>delete d.inventory],
 ['contradictory truncation', d=>d.inventory.truncated=true]
]) {
 test(`Kubernetes refuses ${name} without retaining selection`, async()=>{
  const f=graphFixture();let data=kubeData();
  const inventory=createKubeInventory({ui:f.graph,root:f.root,connected:()=>true,call:async name=>name==='kube_contexts'?[{name:'fixture',namespace:'default',current:true}]:data});
  try {
   assert.equal(await inventory.open(),true);await f.find('Select this Node','button').fire('activate');
   data=structuredClone(data);mutate(data);assert.equal(await inventory.refresh(),false);
   assert.equal(inventory.getSelection(),null);assert.equal(f.find('Select this Node','button'),undefined);
   assert.doesNotMatch(f.text(),/The cluster reports no Nodes|No Nodes match this filter/);
  } finally {inventory.dispose();}
 });
}

test('embedded refresh recovers context discovery and never converts malformed contexts into empty inventory',async()=>{
 const f=graphFixture(),calls=[];let fail=true;
 const inventory=createKubeInventory({ui:f.graph,root:f.root,embedded:true,connected:()=>true,call:async(name,q)=>{
  calls.push(name);
  if(name==='kube_contexts'){if(fail)return {private:'credential helper output'};return[{name:'fixture',namespace:'default',current:true}];}
  return kubeData(q);
 }});
 try{
  assert.equal(await inventory.open(),false);assert.match(f.text(),/Could not use the Kubernetes contexts/);
  assert.doesNotMatch(f.text(),/credential helper|No Kubernetes contexts are available/);
  fail=false;await f.find('Refresh Kubernetes inventory','button').fire('activate');
  assert.deepEqual(calls,['kube_contexts','kube_contexts','kube_nodes']);assert.ok(f.find('worker','card'));
 }finally{inventory.dispose();}
});

test('leaving Kubernetes discards late carrier metadata and late Nodes while preserving SSH editor state',async()=>{
 const f=meshFixture();let release;
 const late=new Promise(resolve=>release=resolve);
 f.intercept((name,q)=>name==='kube_contexts'?[{name:'fixture',namespace:'default',current:true}]:name==='kube_nodes'?kubeData(q):name==='mesh_operator_kubernetes_artifacts'?late:undefined);
 try{
  await f.find('Install ARM on a host…','button').fire('activate');await flush();
  f.find('Verified SSH destination','field').value='fixture-host';
  f.find('Reason for installing ARM','area').value='Reviewed fixture';
  const method=f.find('Connection method','select');method.value='@kubernetes';
  const opening=method.fire('change');await flush();method.value='';await method.fire('change');
  release([{sha256:'f'.repeat(64),size_bytes:1024,os:'linux',arch:'arm64',expected_arm_version:'fixture',expected_lifecycle_protocol:1}]);await opening;await flush();
  assert.equal(f.find('Verified SSH destination','field').value,'fixture-host');
  assert.equal(f.find('Reason for installing ARM','area').value,'Reviewed fixture');
  assert.equal(f.find('Trusted Linux carriers','heading'),undefined);
  assert.equal(f.find('Prepare installation review','button').disabled,false);
  assert.equal(f.calls.some(([name])=>name==='mesh_operator_provision_prepare'),false);
 }finally{f.console.dispose();}
});

for (const [entry, label, reason, operation] of [
 ['Install ARM on a host…','Prepare installation review','Reason for installing ARM','mesh_operator_provision_prepare'],
 ['Connect a node…','Prepare connection review','Reason for connecting','mesh_operator_prepare']
]) {
 test(`${entry} keeps the SSH preparation locked across method changes`, async () => {
  const f = meshFixture();
  const started = Promise.withResolvers(), reply = Promise.withResolvers();
  f.intercept((name, q) => {
   if (name === operation) {
    started.resolve(q);
    return reply.promise;
   }
   if (name === 'kube_contexts') return [{name:'fixture',namespace:'default',current:true}];
   if (name === 'kube_nodes') return kubeData(q);
   if (name === 'mesh_operator_kubernetes_artifacts') return [];
  });
  try {
   await f.find(entry, 'button').fire('activate');
   f.find('Verified SSH destination', 'field').value = 'fixture-host';
   f.find(reason, 'area').value = 'Inspect the fixture host';
   const button = f.find(label, 'button'), method = f.find('Connection method', 'select');
   const roundTrip = async () => {
    method.value = '@kubernetes';
    await method.fire('change');
    method.value = '';
    await method.fire('change');
    assert.equal(button.disabled, true);
    await button.fire('activate');
   };
   const preparing = button.fire('activate');
   const original = await started.promise;
   await roundTrip();
   reply.reject(Error('Preparation reply lost'));
   await preparing;
   await roundTrip();
   assert.deepEqual(f.calls.filter(([name]) => name === operation).map(([, q]) => q.key), [original.key]);
   assert.match(f.text(), new RegExp(original.key));
  } finally {
   reply.resolve(null);
   f.console.dispose();
  }
 });
}

test('refresh after reconnect reloads Nodes and trusted carriers and clears the disconnected notice', async () => {
 const f = meshFixture();
 f.intercept((name, q) => {
  if (name === 'kube_contexts') return [{name:'fixture',namespace:'default',current:true}];
  if (name === 'kube_nodes') return kubeData(q);
  if (name === 'mesh_operator_kubernetes_artifacts') return [];
 });
 try {
  await f.find('Install ARM on a host…', 'button').fire('activate');
  const method = f.find('Connection method', 'select');
  method.value = '@kubernetes';
  await method.fire('change');
  await flush();
  await f.find('Select this Node', 'button').fire('activate');
  f.offline();
  assert.match(f.text(), /Disconnected\. Refresh the exact Kubernetes selection/);
  assert.equal(f.find('Selected Node', 'heading'), undefined);
  assert.equal(f.find('Trusted Linux carriers', 'heading'), undefined);
  f.online();
  await f.find('Refresh Kubernetes inventory', 'button').fire('activate');
  assert.ok(f.find('worker', 'card'));
  assert.ok(f.find('Trusted Linux carriers', 'heading'));
  assert.doesNotMatch(f.text(), /Disconnected\. Refresh the exact Kubernetes selection/);
  assert.equal(f.find('Prepare installation review', 'button').disabled, true);
  assert.equal(f.calls.filter(([name]) => name === 'mesh_operator_kubernetes_artifacts').length, 2);
 } finally {
  f.console.dispose();
 }
});

const cardinalityNodes = count => Array.from({length:count},(_,index)=>({...kubeData().nodes[0],name:`worker-${index}`,uid:`node-uid-${index}`}));
for (const [name, returned, total, matched, query] of [
 ['empty positive unfiltered',0,3,3,''],
 ['empty positive filtered',0,5,3,'worker'],
 ['short below limit',1,3,3,''],
 ['short truncated at limit',49,60,60,'']
]) {
 test(`Kubernetes refuses ${name} cardinality without publishing false empty inventory`,async()=>{
  const f=graphFixture(),published=[];let response=kubeData();
  const inventory=createKubeInventory({ui:f.graph,root:f.root,connected:()=>true,onKubeInventory:value=>published.push(value),call:async name=>name==='kube_contexts'?[{name:'fixture',namespace:'default',current:true}]:response});
  try{
   assert.equal(await inventory.open(),true);await f.find('Select this Node','button').fire('activate');assert.ok(inventory.getSelection());
   response={...kubeData(),nodes:cardinalityNodes(returned),inventory:{total,matched,truncated:true}};
   f.find('Find a Node','field').value=query;
   assert.equal(await inventory.refresh(),false);assert.equal(published.at(-1),null);assert.equal(inventory.getSelection(),null);
   assert.equal(f.find('Select this Node','button'),undefined);
   assert.match(f.text(),/Could not use the Node inventory returned by Kubernetes/);
   assert.doesNotMatch(f.text(),/The cluster reports no Nodes|No Nodes match this filter|No Nodes were returned/);
  }finally{inventory.dispose();}
 });
}

test('Kubernetes accepts exact limit-truncated cardinality as positive inventory',async()=>{
 const f=graphFixture(),published=[];
 const response={...kubeData(),nodes:cardinalityNodes(50),inventory:{total:60,matched:60,truncated:true}};
 const inventory=createKubeInventory({ui:f.graph,root:f.root,connected:()=>true,onKubeInventory:value=>published.push(value),call:async(name,request)=>{
  if(name==='kube_contexts')return[{name:'fixture',namespace:'default',current:true}];
  assert.equal(request.limit,50);return response;
 }});
 try{
  assert.equal(await inventory.open(),true);assert.equal(published.at(-1).nodes.length,50);
  assert.match(f.text(),/50 matching Nodes shown of 60/);
  assert.doesNotMatch(f.text(),/The cluster reports no Nodes|No Nodes match this filter|Could not use the Node inventory/);
  await f.find('Select this Node','button').fire('activate');assert.equal(inventory.getSelection().node.uid,'node-uid-0');
 }finally{inventory.dispose();}
});
