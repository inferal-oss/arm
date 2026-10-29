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
