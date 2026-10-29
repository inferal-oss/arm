import {it,expect} from 'vitest';
import {readFileSync} from 'node:fs';
import {parseHTML} from 'linkedom';
import {loadStore} from '../third_party/semblance/src/node/load.ts';
import {Runtime,EventBus,MOD} from '../third_party/semblance/src/index.ts';
import {DEFAULT_LAYERS} from '../third_party/semblance/src/ns.ts';
import {DomRenderer} from '../third_party/semblance/src/render/dom.ts';
import {createGraphUI,ARM} from '../../internal/server/web/semblance/graph-ui.mjs';
import {count,ownerDisplay,stringifyJSON} from '../../internal/server/web/core.mjs';
import {namedLabel} from '../../internal/server/web/resource-names.mjs';

const nativeCodeText=n=>(n.querySelector('code')??n).textContent;
const main=readFileSync(new URL('../../internal/server/web/semblance/main.mjs',import.meta.url),'utf8');
const owned=main.slice(main.indexOf('function owned('),main.indexOf('function connectionState('));
function fixture(){
 const store=loadStore('wat',readFileSync(new URL('../../internal/server/web/semblance/app.ttl',import.meta.url),'utf8'));
 const runtime=new Runtime(store),projection=runtime.project({modality:MOD.Screen,layers:DEFAULT_LAYERS,prefer:'sparql',roots:[ARM+'shell']},e=>{throw e});
 const {document}=parseHTML('<!doctype html><html><body><div id="app"></div></body></html>');
 const renderer=new DomRenderer(projection.index,document.getElementById('app'),ARM+'shell');renderer.flush();
 const app={store,runtime,projection,renderer,flush:()=>renderer.flush()},ui=createGraphUI(app,{onError:e=>{throw e}});
 const bus=new EventBus(runtime,projection);bus.attach(renderer,h=>{app.flush();ui.handled(h)});
 return{ui,document,store,root:ui.bind('monitorContent'),node:h=>renderer.nodeOf(projection.index.rootOf(h.iri)),close(){runtime.dispose();store.dispose?.();}};
}
function hostFixture(){
 const x=fixture(),state={status:{host:'c'.repeat(32),owner:'1000',owner_username:'synthetic',display_name:'synthetic-host',cursor:9007199254740993n,capabilities:['durable-events','future-capability'],infrastructure_ports:[{name:'synthetic',network:'tcp4',address:'192.0.2.1',port:42000,mode:'hold',state:'infrastructure'}]}};
 const source=main.slice(main.indexOf('let hostView=null;'),main.indexOf('function updateFreshness('));
 const api=new Function('ui','roots','state','namedLabel','owner','count','stringifyJSON','C','ARM',owned+source+';return {renderHost,get view(){return hostView;}};')(x.ui,{host:x.root},state,namedLabel,l=>ownerDisplay(l,state.status,l?.host),count,stringifyJSON,'https://semblance.dev/ns/component#',ARM);
 api.renderHost();return{...x,state,api};
}
it('Host exposes exact published capability and infrastructure text through native Code values',()=>{
 const x=hostFixture();try{
  expect(x.node(x.api.view.technical).tagName).toBe('PRE');expect(x.node(x.api.view.technical).getAttribute('tabindex')).toBe('0');
  expect(x.api.view.technical.value).toBe('durable-events\nfuture-capability');
  expect(nativeCodeText(x.node(x.api.view.technical))).toBe('durable-events\nfuture-capability');
  expect(nativeCodeText(x.node(x.api.view.infrastructure))).toBe(stringifyJSON(x.state.status.infrastructure_ports));
  expect(x.document.body.textContent).toContain('9,007,199,254,740,993');
 }finally{x.close();}
});
it('Host refresh keeps original disclosure and Code nodes while null/empty/current data updates',()=>{
 const x=hostFixture();try{
  const view=x.api.view,technical=x.node(view.technical),infrastructure=x.node(view.infrastructure),disclosure=x.node(view.technicalDisclosure);view.technicalDisclosure.open=true;
  for(const caps of [null,[],['durable-events','changed-capability']]){
   x.state.status={...x.state.status,draining:true,capabilities:caps,infrastructure_ports:null};x.api.renderHost();
   expect(x.api.view).toBe(view);expect(view.technicalDisclosure.open).toBe(true);
   expect(x.node(view.technical)).toBe(technical);expect(x.node(view.infrastructure)).toBe(infrastructure);expect(x.node(view.technicalDisclosure)).toBe(disclosure);
   expect(nativeCodeText(technical)).toBe((caps??[]).join('\n'));expect(nativeCodeText(infrastructure)).toBe('[]');
  }
 }finally{x.close();}
});
it('Host retains the exact display cap/marker and recreates disposed content without stale values',()=>{
 const x=hostFixture();try{
  const old=x.api.view;x.state.status.infrastructure_ports[0].detail='x'.repeat(256100);x.api.renderHost();
  const raw=stringifyJSON(x.state.status.infrastructure_ports),expected=raw.slice(0,256000)+'\n[display truncated]';
  expect(nativeCodeText(x.node(old.infrastructure))).toBe(expected);expect(expected.length).toBe(256020);
  x.root.replace();expect(old.authority.disposed).toBe(true);expect(old.infrastructure.disposed).toBe(true);
  x.state.status.infrastructure_ports=[];x.api.renderHost();expect(x.api.view).not.toBe(old);expect(nativeCodeText(x.node(x.api.view.infrastructure))).toBe('[]');
 }finally{x.close();}
});

function activityFixture(){
 const x=fixture(),base=9007199254740993n,events=Array.from({length:100},(_,i)=>({sequence:base+BigInt(i),type:'lease.released',entity:'d'.repeat(32),at:'2026-10-29T12:00:00Z',data:{reason:'Synthetic reason',exact:9223372036854775807n}}));
 const state={status:{host:'c'.repeat(32)},events},activityDetails=x.ui.stack(),activityTimeline=x.ui.timeline('Events',50);
 let api;const activityPage=x.ui.select('History',[{value:'0',label:'Latest'}],{},()=>api.renderActivity()),eventChoice=x.ui.select('Event',[],{},()=>api.renderEventDetail()),inspected=[];
 x.root.replace(activityPage,activityTimeline,eventChoice,activityDetails);
 const source=main.slice(main.indexOf('let activityRows='),main.indexOf('function updateActivityFeed('));
 api=new Function('ui','state','connection','session','activityDetails','activityTimeline','activityPage','eventChoice','when','showLease','stringifyJSON',owned+source+`;return{renderActivity,renderEventDetail,get detail(){return activityDetail;},context(next){if('connection'in next)connection=next.connection;if('session'in next)session=next.session;if('host'in next)state.status.host=next.host;}};`)(x.ui,state,{},{},activityDetails,activityTimeline,activityPage,eventChoice,v=>new Date(v).toISOString(),id=>inspected.push(id),stringifyJSON);
 api.renderActivity();eventChoice.value=String(base+80n);api.renderEventDetail();return{...x,base,state,api,activityDetails,activityPage,eventChoice,inspected};
}
it('Activity keeps exact sequence/native reader handles and open state across replay, arrivals and row movement',()=>{
 const x=activityFixture();try{
  const model=x.api.detail,recordNode=x.node(model.record),codeNode=x.node(model.code);model.record.open=true;
  x.state.events=x.state.events.map(e=>({...e}));x.api.renderActivity();
  expect(x.api.detail).toBe(model);expect(x.node(model.record)).toBe(recordNode);expect(x.node(model.code)).toBe(codeNode);expect(model.record.open).toBe(true);
  x.state.events.push({...x.state.events.at(-1),sequence:x.base+100n});x.api.renderActivity();
  expect(x.api.detail).toBe(model);expect(model.sequence).toBe(String(x.base+80n));expect(model.record.open).toBe(true);
  expect(nativeCodeText(codeNode)).toContain('9223372036854775807');expect(nativeCodeText(codeNode)).toContain(String(x.base+80n));
 }finally{x.close();}
});
it('Activity updates current data and the inspect callback without replacing the valid selected subtree',()=>{
 const x=activityFixture();try{
  const model=x.api.detail,record=x.node(model.record);model.record.open=true;const id='e'.repeat(32);
  x.state.events=x.state.events.map(e=>String(e.sequence)===model.sequence?{...e,entity:id,data:{reason:'Changed current data'}}:e);x.api.renderActivity();
  expect(x.api.detail).toBe(model);expect(x.node(model.record)).toBe(record);expect(model.record.open).toBe(true);
  model.inspect.emit('activate');expect(x.inspected).toEqual([id]);expect(nativeCodeText(x.node(model.code))).toContain('Changed current data');
  x.state.events=x.state.events.map(e=>String(e.sequence)===model.sequence?{...e,type:'host.metadata.changed'}:e);const inspect=model.inspect;x.api.renderActivity();expect(inspect.disposed).toBe(true);expect(model.inspect).toBeNull();expect(model.record.open).toBe(true);
 }finally{x.close();}
});
it('Activity retires genuine selection/page changes and preserves the exact display cap',()=>{
 const x=activityFixture();try{
  const old=x.api.detail;x.eventChoice.value=String(x.base+79n);x.api.renderEventDetail();expect(old.record.disposed).toBe(true);expect(x.api.detail).not.toBe(old);
  const current=x.api.detail;x.state.events=x.state.events.map(e=>String(e.sequence)===current.sequence?{...e,data:{detail:'x'.repeat(256100)}}:e);x.api.renderActivity();
  const raw=stringifyJSON(x.state.events.find(e=>String(e.sequence)===current.sequence));expect(nativeCodeText(x.node(current.code))).toBe(raw.slice(0,256000)+'\n[display truncated]');
  x.activityPage.value='1';x.api.renderActivity();expect(x.eventChoice.value).toBe('');expect(current.record.disposed).toBe(true);expect(x.api.detail).toBeNull();
 }finally{x.close();}
});
it('Activity separates identical sequence IDs across authority, connection and session contexts',()=>{
 const x=activityFixture();try{
  for(const change of [{host:'f'.repeat(32)},{connection:{}},{session:{}}]){
   const old=x.api.detail,sequence=old.sequence;x.api.context(change);x.api.renderActivity();
   expect(old.record.disposed).toBe(true);expect(x.api.detail).toBeNull();expect(x.eventChoice.value).toBe('');
   x.eventChoice.value=sequence;x.api.renderEventDetail();expect(x.api.detail).not.toBe(old);
  }
 }finally{x.close();}
});
it('Activity recreates disposed detail children, releases stale graph handles and does not recreate a disposed root',()=>{
 const x=activityFixture();try{
  const old=x.api.detail;x.activityDetails.replace();expect(old.record.disposed).toBe(true);x.api.renderEventDetail();expect(x.api.detail).not.toBe(old);
  const baseline=x.ui.handles.size;
  for(let i=0;i<12;i++){x.eventChoice.value=String(x.base+BigInt(80-i%2));x.api.renderEventDetail();}
  expect(x.ui.handles.size).toBe(baseline);const model=x.api.detail;x.activityDetails.dispose();const remaining=x.ui.handles.size;x.api.renderEventDetail();
  expect(model.record.disposed).toBe(true);expect(x.api.detail).toBeNull();expect(x.ui.handles.size).toBe(remaining);
 }finally{x.close();}
});
