import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import {namedLabel} from '../resource-names.mjs';
import {createMeshConsole} from './mesh.mjs';
import {controlUI} from './control-test-ui.mjs';

// Execute the production refresh function with transport and component handles
// doubled, following main-lifecycle.test.mjs. This is not RDF/DOM evidence.
const source=readFileSync(new URL('./main.mjs',import.meta.url),'utf8');
const start=source.indexOf('async function refresh(){'),end=source.indexOf('\nasync function connect(){',start);
assert.ok(start>=0&&end>start,'the actual local refresh function must be selected');
const refreshSource=source.slice(start,end);
const host='a'.repeat(32),leaseID='b'.repeat(32),revision=9007199254740993n,generatedName='acorn-blue-cider-dove';

test('local status refresh applies host name set change and clear without reconnecting or changing canonical identities',async()=>{
 const lease=Object.freeze({id:leaseID,host,revision}),calls=[],errors=[],labels=[];
 let next,held=null;
 const connection={async call(operation){calls.push(operation);if(operation==='status')return held??next;if(['messages','agents'].includes(operation))return [];throw Error('Unexpected operation '+operation);}};
 const state={connected:true,status:null},hostLabel={label:generatedName,set(value){Object.assign(this,value);labels.push(this.label);}};
 const scope={state,connection,hostLabel,namedLabel,refreshBusy:false,refreshAgain:false,remoteDashboard:null,view:'host',
  performance:{now:()=>17},namingConsole:{updateConnection(){}},updateMessages(){},updateActivityFeed(){},renderCurrent(){},
  scheduleRefresh(){throw Error('No successor refresh was requested.');},notice:message=>errors.push(message)};
 const refresh=runInNewContext(refreshSource+'\nrefresh',scope);
 for(const label of [generatedName,'build-host','build-cache-host',generatedName,undefined]){
  next=Object.freeze({host,owner:'501',cursor:revision,leases:Object.freeze([lease]),...(label?{display_name:label}:{})});
  await refresh();
  assert.equal(hostLabel.label,label??'Host '+host.slice(0,8));
  assert.equal(state.status,next);assert.equal(state.status.host,host);assert.equal(state.status.cursor,revision);
  assert.equal(state.status.leases[0],lease);assert.equal(lease.id,leaseID);assert.equal(lease.revision,revision);
  assert.equal(scope.connection,connection);assert.equal(state.connected,true);
 }
 assert.deepEqual(labels,[generatedName,'build-host','build-cache-host',generatedName,'Host '+host.slice(0,8)]);
 // The new header assignment must remain after the existing connection fence.
 let release;held=new Promise(resolve=>{release=resolve;});const pending=refresh();scope.connection={};
 release({...next,display_name:'Retired connection label'});await pending;
 assert.equal(hostLabel.label,'Host '+host.slice(0,8));assert.equal(state.status,next);assert.equal(labels.length,5);
 assert.deepEqual(errors,[]);assert.ok(calls.every(operation=>['status','messages','agents'].includes(operation)));
 assert.equal(calls.filter(operation=>operation==='status').length,6);
});

function rosterFixture(){
 const ui=controlUI();
 ui.grid=(children=[],props={})=>ui.stack(children,props);
 ui.toolbar=(label,children=[])=>ui.card(label,children);
 ui.disclosure=(label,children=[],props={})=>ui.card(label,children,props);
 ui.descriptions=pairs=>Object.assign(ui.text(pairs.map(([label,value])=>`${label}: ${value}`).join(' ')),{pairs});
 const card=ui.card;ui.card=(label,children=[],props={})=>card(label,[...children,...(props.actions??[])],props);
 const root=ui.stack(),calls=[],opened=[];
 let node;
 const console=createMeshConsole({ui,root,connected:()=>true,openNode:entry=>opened.push(entry),
  call:async(operation,args)=>{calls.push([operation,args]);if(operation==='node_list')return Object.freeze({revision,nodes:Object.freeze([node])});if(operation==='mesh_operator_list')return [];throw Error('Unexpected operation '+operation);},
  storage:{getItem:()=>null,setItem(){throw Error('Refresh must not write recovery state.');}}});
 const visible=()=>{const walk=handle=>[handle,...handle.children.flatMap(walk)];return walk(root);};
 return{ui,console,calls,opened,visible,node:value=>{node=value;}};
}

test('mesh roster cache refresh applies host name set change and clear while node callbacks retain exact IDs and revisions',async()=>{
 const f=rosterFixture(),base={host,name:'Signed node name',local:false,health:'connected',trust:Object.freeze({state:'active',revision}),outbound_scope:Object.freeze({revision:47n})};
 let previous=null;
 for(const label of [generatedName,'build-peer','build-cache-peer',generatedName,undefined]){
  const node=Object.freeze({...base,...(label?{display_name:label}:{})});f.node(node);await f.console.refresh();
  const title=label??base.name,card=f.visible().find(handle=>handle.kind==='Card'&&handle.label===title);
  assert.ok(card,'the current roster must display '+title);assert.notEqual(card,previous);
  const identity=f.visible().find(handle=>handle.pairs?.some(([key])=>key==='Stable host ID'));
  assert.deepEqual(identity.pairs,[['Stable host ID',host]]);
  const open=f.visible().find(handle=>handle.label==='Open dashboard ↗');assert.ok(open);await open.emit('activate');
  assert.equal(f.opened.at(-1),node);assert.equal(f.opened.at(-1).host,host);assert.equal(f.opened.at(-1).trust.revision,revision);
  assert.equal(f.opened.at(-1).outbound_scope.revision,47n);assert.equal(node.name,base.name);
  // Identical snapshots still retain cached handles. Only a changed label invalidates them.
  await f.console.refresh();assert.equal(f.visible().find(handle=>handle.kind==='Card'&&handle.label===title),card);
  previous=card;
 }
 assert.equal(f.opened.length,5);assert.deepEqual(f.opened.map(node=>node.display_name),[generatedName,'build-peer','build-cache-peer',generatedName,undefined]);
 assert.ok(f.calls.every(([operation])=>['node_list','mesh_operator_list'].includes(operation)));
 assert.equal(f.calls.filter(([operation])=>operation==='node_list').length,10);
 f.console.dispose();
});
