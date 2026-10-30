import {it,expect} from 'vitest';
import {readFileSync} from 'node:fs';
import {parseHTML} from 'linkedom';
import {loadStore} from '../third_party/semblance/src/node/load.ts';
import {Runtime,EventBus,MOD} from '../third_party/semblance/src/index.ts';
import {DEFAULT_LAYERS} from '../third_party/semblance/src/ns.ts';
import {DomRenderer} from '../third_party/semblance/src/render/dom.ts';
import {createGraphUI,ARM} from '../../internal/server/web/semblance/graph-ui.mjs';
import {createMeshConsole,createKubeInventory} from '../../internal/server/web/semblance/mesh.mjs';
const C='https://semblance.dev/ns/component#',RDF='http://www.w3.org/1999/02/22-rdf-syntax-ns#type';
const local='a'.repeat(32),host='b'.repeat(32),now='2026-10-29T12:00:00Z';
function fixture(){
 const store=loadStore('wat',readFileSync(new URL('../../internal/server/web/semblance/app.ttl',import.meta.url),'utf8'));
 const runtime=new Runtime(store),projection=runtime.project({modality:MOD.Screen,layers:DEFAULT_LAYERS,prefer:'sparql',roots:[ARM+'shell']},e=>{throw e});
 const {document,window}=parseHTML('<!doctype html><html><body><div id="app"></div></body></html>');
 const renderer=new DomRenderer(projection.index,document.getElementById('app'),ARM+'shell');renderer.flush();
 const app={store,runtime,projection,renderer,flush:()=>renderer.flush()},ui=createGraphUI(app,{onError:e=>{throw e}}),bus=new EventBus(runtime,projection);bus.attach(renderer,h=>{app.flush();ui.handled(h)});
 const root=ui.stack();ui.bind('monitorContent').replace(root);
 const all=()=>[...ui.handles.values()],find=(label,kind)=>all().find(h=>h.label===label&&(!kind||h.kind===kind)),node=h=>renderer.nodeOf(projection.index.rootOf(h.iri));
 return {ui,root,document,window,store,all,find,node,click(h){(h.kind==='AccordionItem'?node(h).querySelector('button'):node(h)).dispatchEvent(new window.Event('click',{bubbles:true}));},types:h=>store.match(ui.nn(h.iri),ui.nn(RDF)).map(q=>q.object.value),async flush(){for(let i=0;i<4;i++)await new Promise(r=>setImmediate(r));app.flush();},close(){bus.dispose?.();runtime.dispose();store.dispose?.();}};
}
const peer={host,name:'worker.example',display_name:'Synthetic-'+ 'complete-label'.repeat(8),local:false,health:'reachable',trust:{state:'active',address:'endpoint.example:42224',pair:'e'.repeat(64),certificate_fingerprint:'f'.repeat(64),spki_sha256:'1'.repeat(64),execution_account:{uid:'1000',username:'synthetic'},inbound_grant:{permissions:['observe-host']}},outbound_scope:{source_host:local,target_host:host,grant:{permissions:['observe-host']}},scope_freshness:{known:true,stale:false},metadata:{schema_version:1,host,platform:{os:'linux',process_architecture:'arm64'},build:{arm_version:'synthetic'},inventory:{logical_cpus:8,host_memory:{total_bytes:9007199254740993n}},capabilities:[],reported_at:now}};
const report={schema:1,host,owner_declared:{profile:'stable',revision:9007199254740991,updated_at:now},observed_at:now};
it('native Node dialog keeps exact read bindings, native control types and open reader identity through passive refresh',async()=>{
 const x=fixture(),calls=[];let console;
 try{
  console=createMeshConsole({ui:x.ui,root:x.root,connected:()=>true,call:async(op,args)=>{calls.push([op,args]);if(op==='node_list')return{source_host:local,nodes:[peer]};if(op==='mesh_operator_list')return[];expect(args).toEqual({host});if(op==='node_inspect')return peer;if(op==='node_availability_read_v1')return report;if(op==='node_availability_inspect_v1')return{schema:1,host,connection_state:'reachable',declaration_state:'reported',resource_freshness:{cpu:'unknown',memory:'unknown',disk:'unknown'}};throw Error(op);}});
  await console.refresh();x.click(x.find('View node','Button'));await x.flush();const d=x.all().find(h=>h.kind==='Dialog');expect(x.types(d)).toContain(C+'Dialog');expect(d.body).toBeTruthy();expect(x.types(d.body)).toContain(ARM+'NodeDetailBody');
  const disclosure=x.find('Reported metadata','AccordionItem');x.click(disclosure);await x.flush();expect(disclosure.open).toBe(true);const reader=x.node(disclosure),view=x.find('View node','Button'),viewNode=x.node(view);let focus=0;reader.querySelector('button').addEventListener('focus',()=>focus++);disclosure.focus();const beforeFocus=focus;
  await console.refresh();await x.flush();expect(x.node(disclosure)).toBe(reader);expect(x.node(view)).toBe(viewNode);expect(disclosure.open).toBe(true);expect(focus).toBe(beforeFocus);expect(x.document.body.textContent).toContain('9007199254740993');expect(x.types(view)).toContain(C+'Button');expect(calls.filter(([op])=>op==='node_inspect')).toHaveLength(1);
  const close=x.node(d).querySelector('.sem-close');close.dispatchEvent(Object.assign(new x.window.Event('keydown',{bubbles:true,cancelable:true}),{key:'Escape',code:'Escape'}));await x.flush();expect(d.disposed).toBe(true);expect(disclosure.disposed).toBe(true);
  expect(calls.every(([op])=>['node_list','mesh_operator_list','node_inspect','node_availability_read_v1','node_availability_inspect_v1'].includes(op))).toBe(true);
 }finally{console?.dispose();x.close();}
});
it('closing the native pending Node dialog discards its exact late reply without resurrecting readers',async()=>{
 const x=fixture();let resolve,console;const calls=[];
 try{
  console=createMeshConsole({ui:x.ui,root:x.root,connected:()=>true,call:async(op,args)=>{calls.push([op,args]);if(op==='node_list')return{source_host:local,nodes:[peer]};if(op==='mesh_operator_list')return[];if(op==='node_inspect'){expect(args).toEqual({host});return await new Promise(r=>resolve=r);}throw Error(op);}});await console.refresh();x.click(x.find('View node','Button'));await x.flush();const d=x.all().find(h=>h.kind==='Dialog');expect(x.types(d)).toContain(C+'Dialog');x.node(d).querySelector('.sem-close').dispatchEvent(new x.window.Event('click',{bubbles:true}));await x.flush();expect(d.disposed).toBe(true);resolve(peer);await x.flush();expect(x.all().some(h=>h.kind==='Dialog')).toBe(false);expect(x.find('Reported metadata','AccordionItem')).toBeUndefined();expect(calls.map(([op])=>op)).toEqual(['node_list','mesh_operator_list','node_inspect']);
 }finally{resolve?.(peer);console?.dispose();x.close();}
});
it('native Kubernetes selection remains local, retains raw units and rejects stale context or namespace UID bindings',async()=>{
 const x=fixture(),calls=[],pending=[],snapshots=[];let inventory,hold=false;
 const contexts=[{name:'synthetic-a',namespace:'inventory-a',current:true},{name:'synthetic-b',namespace:'inventory-b'}];
 const data=q=>({cluster:{context:q.context,server:'https://control.example:6443',ca_sha256:'d'.repeat(64)},namespace:{name:q.namespace,uid:q.context+'-namespace-uid'},nodes:[{name:'worker.example',uid:q.context+'-node-uid',os:'linux',arch:'arm64',ready:true,unschedulable:false,taints:[],allocatable_cpu:'180000000000n',allocatable_memory:'535328752Ki',allocatable_ephemeral:'20Gi',capacity:{cpu:'192'},allocatable:Object.fromEntries(Array.from({length:70},(_,i)=>['vendor.example/resource-'+i,i===0?'9007199254740993':String(i+1)])),conditions:['Ready=True']}]});
 try{
  inventory=createKubeInventory({ui:x.ui,root:x.root,connected:()=>true,onKubeInventory:s=>snapshots.push(s),call:async(op,q)=>{calls.push([op,q]);if(op==='kube_contexts')return contexts;if(op==='kube_nodes')return hold?await new Promise(r=>pending.push({q,resolve:r})):data(q);throw Error(op);}});await inventory.open();await x.flush();const card=x.all().find(h=>h.kind==='Card'&&h.label==='worker.example');expect(x.types(card)).toContain(ARM+'KubeNodeCard');const choose=x.find('Select this Node','Button');expect(x.types(choose)).toContain(C+'Button');const before=calls.length;x.click(choose);await x.flush();expect(calls).toHaveLength(before);expect(x.document.body.textContent).toContain('Selected Node');
  const exact=x.find('Exact Kubernetes Node record','AccordionItem');x.click(exact);await x.flush();expect(x.document.body.textContent).toContain('9007199254740993');expect(x.document.body.textContent).toContain('7 additional capacity or allocatable fields omitted');expect(x.document.body.textContent).toContain('180 cores');
  const q=calls.find(([op])=>op==='kube_nodes')[1],current=data(q),reference={uid:current.nodes[0].uid,context:q.context,namespace:current.namespace,server:current.cluster.server,ca_sha256:current.cluster.ca_sha256,node:current.nodes[0]};
  expect(inventory.focusKubernetesNode({...reference,namespace:{...current.namespace,uid:'other-namespace'}})).toBe(false);expect(inventory.focusKubernetesNode({...reference,server:'https://other.example'})).toBe(false);expect(inventory.focusKubernetesNode(reference)).toBe(true);expect(calls).toHaveLength(before);
  hold=true;const choice=x.find('Context','Choice');const change=index=>{const select=x.node(choice).querySelector('select');for(const option of select.options)option.selected=false;select.options[index].selected=true;select.dispatchEvent(new x.window.Event('change',{bubbles:true}));};
  change(1);await x.flush();expect(choice.value).toBe(contexts[1].name);change(0);await x.flush();expect(pending).toHaveLength(2);pending[1].resolve(data(pending[1].q));await x.flush();pending[0].resolve(data(pending[0].q));await x.flush();expect(snapshots.at(-1).cluster.context).toBe('synthetic-a');expect(x.document.body.textContent).not.toContain('Selected Node');expect(calls.every(([op])=>['kube_contexts','kube_nodes'].includes(op))).toBe(true);
 }finally{for(const p of pending)p.resolve(data(p.q));inventory?.dispose();x.close();}
});
