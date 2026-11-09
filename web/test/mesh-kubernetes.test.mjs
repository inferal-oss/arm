import {it,expect} from 'vitest';
import {readFileSync} from 'node:fs';
import {parseHTML} from 'linkedom';
import {loadStore} from '../third_party/semblance/src/node/load.ts';
import {Runtime,EventBus,MOD} from '../third_party/semblance/src/index.ts';
import {DEFAULT_LAYERS} from '../third_party/semblance/src/ns.ts';
import {DomRenderer} from '../third_party/semblance/src/render/dom.ts';
import {createGraphUI,ARM} from '../../internal/server/web/semblance/graph-ui.mjs';
import {createKubeInventory,createMeshConsole} from '../../internal/server/web/semblance/mesh.mjs';
import {parseJSON} from '../../internal/server/web/core.mjs';
const fixtureWire=parseJSON(readFileSync(new URL('./fixtures/kubernetes-browser-node.json',import.meta.url),'utf8'));

function fixture(){
 const store=loadStore('wat',readFileSync(new URL('../../internal/server/web/semblance/app.ttl',import.meta.url),'utf8'));
 const runtime=new Runtime(store),projection=runtime.project({modality:MOD.Screen,layers:DEFAULT_LAYERS,prefer:'sparql',roots:[ARM+'shell']},e=>{throw e});
 const {document,window}=parseHTML('<!doctype html><html><body><main></main></body></html>');
 const renderer=new DomRenderer(projection.index,document.querySelector('main'),ARM+'shell');renderer.flush();
 const app={store,runtime,projection,renderer,flush:()=>renderer.flush()},ui=createGraphUI(app,{onError:e=>{throw e}});
 const bus=new EventBus(runtime,projection);bus.attach(renderer,h=>{app.flush();ui.handled(h)});
 ui.put(ARM+'shell',ARM+'view',ARM+'mesh',true);
 return{ui,document,window,store,runtime,projection,renderer,root:ui.bind('meshContent'),close(){runtime.dispose();store.dispose?.();}};
}
const cluster={context:'fixture',server:'https://cluster.example:6443',ca_sha256:'a'.repeat(64),auth_subject:'fixture-user',auth_groups:['fixture-group']};
const namespace={name:'default',uid:'namespace-uid'};
const node={name:'fixture-worker',uid:'node-uid',os:'linux',arch:'arm64',ready:true,allocatable_cpu:'4',allocatable_memory:'8Gi',allocatable_ephemeral:'20Gi'};
it('renders discovered contexts and actual stock Node cards through WAT projection',async()=>{
 const x=fixture(),calls=[];let inventory;
 try{
  inventory=createKubeInventory({ui:x.ui,root:x.root,connected:()=>true,call:async(name,q)=>{calls.push([name,q]);return name==='kube_contexts'?[{name:'fixture',cluster_name:'fixture-cluster',namespace:'default',current:true}]:{cluster,namespace,nodes:[node],inventory:{total:1,matched:1,truncated:false}};}});
  expect(await inventory.open()).toBe(true);
  expect(calls.map(([name])=>name)).toEqual(['kube_contexts','kube_nodes']);
  expect(x.document.body.textContent).toContain('fixture-worker');
  expect(x.document.querySelector('[data-sem-shape-invalid]')).toBeNull();
  const choose=[...x.document.querySelectorAll('button')].find(e=>e.textContent==='Select this Node');
  expect(choose).toBeTruthy();
  expect(choose.closest('[hidden]')).toBeNull();
  choose.dispatchEvent(new x.window.Event('click',{bubbles:true}));
  expect(inventory.getSelection()?.node.uid).toBe('node-uid');
 }finally{inventory?.dispose();x.close();}
});
for (const [entry, primary, mode] of [['Install ARM on a host…','Prepare installation review','installation'],['Connect a node…','Prepare connection review','connection']]) {
it(`uses synthetic inventory inside ${mode} with no standalone Kubernetes area`,async()=>{
 const x=fixture(),calls=[],saved=new Map();let mesh;
 const tick=()=>new Promise(resolve=>setImmediate(resolve));
 try{
  mesh=createMeshConsole({ui:x.ui,root:x.root,connected:()=>true,storage:{getItem:k=>saved.get(k)??null,setItem:(k,v)=>saved.set(k,v)},call:async(name,q)=>{
    calls.push([name,q]);
    if(name==='mesh_operator_connectors'||name==='mesh_operator_kubernetes_artifacts')return [];
    if(name==='kube_contexts')return[{name:'fixture',cluster_name:'fixture-cluster',namespace:'default',current:true}];
    if(name==='kube_nodes')return structuredClone(fixtureWire.result);
    throw Error('Unexpected effect '+name);
  }});
  expect(x.document.body.textContent).not.toContain('Available Kubernetes Nodes');
  const install=[...x.document.querySelectorAll('button')].find(e=>e.textContent===entry);
  install.dispatchEvent(new x.window.Event('click',{bubbles:true}));await tick();
  const source=[...x.ui.handles.values()].find(h=>h.label==='Connection method');
  const select=x.renderer.nodeOf(x.projection.index.rootOf(source.iri)).querySelector('select');
  [...select.options].find(o=>o.value===source.iri+'/option/%40kubernetes').selected=true;
  select.dispatchEvent(new x.window.Event('change',{bubbles:true}));await tick();await tick();
  const dialog=x.document.querySelector('[role="dialog"]');
  expect(dialog.textContent).toContain('fixture-worker');
  expect(dialog.textContent).toContain('4 cores');
  if(mode==='installation')expect(dialog.textContent).toContain('No trusted Linux carrier is imported');
  const choose=[...dialog.querySelectorAll('button')].find(e=>e.textContent==='Select this Node');
  expect(choose.closest('[hidden]')).toBeNull();
  choose.dispatchEvent(new x.window.Event('click',{bubbles:true}));
  const prepare=[...dialog.querySelectorAll('button')].find(e=>e.textContent===primary);
  prepare.dispatchEvent(new x.window.Event('click',{bubbles:true}));await tick();
  expect(prepare.disabled).toBe(true);
  expect(dialog.textContent).toContain(`Kubernetes ${mode} reviews are unavailable in this build`);
  expect(calls.every(([name])=>['mesh_operator_connectors','mesh_operator_kubernetes_artifacts','kube_contexts','kube_nodes'].includes(name))).toBe(true);
  expect(x.document.querySelector('[data-sem-shape-invalid]')).toBeNull();
 }finally{mesh?.dispose();x.close();}
});
}
