import {it,expect} from 'vitest';
import {readFileSync} from 'node:fs';
import {parseHTML} from 'linkedom';
import {loadStore} from '../third_party/semblance/src/node/load.ts';
import {Runtime,EventBus,Islands,MOD} from '../third_party/semblance/src/index.ts';
import {DEFAULT_LAYERS} from '../third_party/semblance/src/ns.ts';
import {DomRenderer} from '../third_party/semblance/src/render/dom.ts';
import {createGraphUI,ARM} from '../../internal/server/web/semblance/graph-ui.mjs';
import {createScreenViewer} from '../../internal/server/web/semblance/screen.mjs';

function fixture(){
 const store=loadStore('wat',readFileSync(new URL('../../internal/server/web/semblance/app.ttl',import.meta.url),'utf8')),runtime=new Runtime(store),projection=runtime.project({modality:MOD.Screen,layers:DEFAULT_LAYERS,prefer:'sparql',roots:[ARM+'shell']},e=>{throw e});
 const {document,window}=parseHTML('<!doctype html><html><body><main id="app"></main></body></html>'),renderer=new DomRenderer(projection.index,document.getElementById('app'),ARM+'shell');renderer.flush();
 const app={store,runtime,projection,renderer,flush:()=>renderer.flush()},widgets={},ui=createGraphUI(app,{widgets,onError:e=>{throw e}}),bus=new EventBus(runtime,projection);bus.attach(renderer,h=>{app.flush();ui.handled(h)});
 const islands=new Islands(bus,renderer,{widgets,onError:e=>{throw e}});islands.attach();const root=ui.stack().retain();ui.bind('monitorContent').replace(root);
 const clients=[];let submitted=0;
 class RFB{
  constructor(surface){this.surface=surface;this.events={};this._rfbConnectionState='connecting';this._rfbAppleARD=true;clients.push(this);}
  addEventListener(type,fn){this.events[type]=fn;}
  emit(type,detail={}){if(type==='connect')this._rfbConnectionState='connected';this.events[type]({detail});}
  _fail(){} _initMsg(){} focus(){} disconnect(){this.disconnected=true;this._rfbConnectionState='disconnected';}
  sendCredentials(){submitted++;throw Error('Populated authentication is outside this regression');}
 }
 const viewer=createScreenViewer({ui,root,token:'a'.repeat(64),context:{local:false},loadRFB:async()=>RFB,location:{host:'synthetic.example'},navigator:{},window});
 const all=()=>[...ui.handles.values()],find=(label,kind)=>all().find(h=>h.label===label&&(!kind||h.kind===kind)),node=h=>renderer.nodeOf(projection.index.rootOf(h.iri));
 return{store,ui,document,clients,viewer,find,node,all,get submitted(){return submitted;},click(label){const h=find(label,'Button');expect(h).toBeTruthy();node(h).dispatchEvent(new window.Event('click',{bubbles:true}));},full(){return store.match(null,ui.nn('https://semblance.dev/ns/fullscreen#fullViewport')).some(q=>q.object.value==='true');},graph(){return JSON.stringify(store.match().map(q=>[q.subject.value,q.predicate.value,q.object.value,q.graph.value]));},async flush(){for(let i=0;i<5;i++)await new Promise(r=>setImmediate(r));app.flush();await islands.settled();},close(){viewer.dispose();islands.dispose();bus.dispose?.();runtime.dispose();store.dispose?.();}};
}
async function connect(x){x.click('Connect to screen');await x.flush();x.clients.at(-1).emit('connect');await x.flush();expect(x.viewer.connected).toBe(true);return x.clients.at(-1);}
const dialog=x=>x.all().find(h=>h.kind==='Dialog'&&h.open);

it('Clipboard leaves full view without replacing the client/frame or moving private inputs into the viewport',async()=>{
 const x=fixture();try{
  const client=await connect(x),frame=client.surface,sentinel='Synthetic bounded clipboard\nFinal line';client.emit('clipboard',{text:sentinel});x.click('Fill window');await x.flush();expect(x.full()).toBe(true);
  x.click('Clipboard');await x.flush();const d=dialog(x),field=x.document.querySelector('textarea');expect(x.full()).toBe(false);expect(x.viewer.connected).toBe(true);expect(x.clients).toHaveLength(1);expect(client.surface).toBe(frame);expect(frame.isConnected).toBe(true);
  expect(x.ui.desktopHosts.has(d.iri)).toBe(true);expect(d.parents.has(x.ui.bind('desktop','Stack'))).toBe(true);expect(x.node(d).contains(field)).toBe(true);expect(x.node(d).contains(frame)).toBe(false);expect(field.value).toBe(sentinel);expect(x.graph()).not.toContain(sentinel);
  x.click('Close clipboard');await x.flush();expect(field.value).toBe('');expect(field.isConnected).toBe(false);expect(d.disposed).toBe(true);expect(x.full()).toBe(false);expect(x.viewer.connected).toBe(true);
  x.click('Clipboard');await x.flush();expect(x.document.querySelector('textarea').value).toBe(sentinel);x.click('Close clipboard');await x.flush();x.click('Fill window');await x.flush();expect(x.full()).toBe(true);expect(x.clients).toHaveLength(1);expect(x.submitted).toBe(0);expect(x.graph()).not.toContain(sentinel);
 }finally{x.close();}
});
it('empty native authentication remains inline after close, validates privately and preserves existing Cancel disconnect',async()=>{
 const x=fixture();try{
  const client=await connect(x);for(const types of [['password'],['username','password'],['username','password','target']]){
   x.click('Fill window');await x.flush();expect(x.full()).toBe(true);client.emit('credentialsrequired',{types});await x.flush();const d=dialog(x),fields=[...x.node(d).querySelectorAll('input')];expect(x.full()).toBe(false);expect(fields).toHaveLength(types.length);expect(fields.every(n=>n.value==='')).toBe(true);expect(x.ui.desktopHosts.has(d.iri)).toBe(true);
   x.click('Sign in');await x.flush();expect(x.submitted).toBe(0);expect(x.node(d).textContent).toContain('Enter mac '+(types.includes('username')?'username':'password')+'.');expect(fields.every(n=>n.value==='')).toBe(true);
   x.node(d).querySelector('.sem-close').dispatchEvent(new x.document.defaultView.Event('click',{bubbles:true}));await x.flush();expect(dialog(x)).toBeUndefined();expect(fields.every(n=>!n.isConnected&&n.value==='')).toBe(true);expect(x.full()).toBe(false);expect(x.viewer.connected).toBe(true);expect(x.clients).toHaveLength(1);
  }
  x.click('Fill window');await x.flush();client.emit('credentialsrequired',{types:['password']});await x.flush();x.click('Cancel');await x.flush();expect(x.viewer.connected).toBe(false);expect(client.disconnected).toBe(true);expect(x.full()).toBe(false);expect(dialog(x)).toBeUndefined();expect(x.submitted).toBe(0);
 }finally{x.close();}
});
it('stale, duplicate and disposed modal requests do not change a healthy presentation or replay private state',async()=>{
 const x=fixture();try{
  const old=await connect(x);x.viewer.disconnect();await x.flush();const current=await connect(x);x.click('Fill window');await x.flush();old.emit('credentialsrequired',{types:['password']});old.emit('clipboard',{text:'Synthetic stale cache'});await x.flush();expect(x.full()).toBe(true);expect(dialog(x)).toBeUndefined();expect(x.viewer.connected).toBe(true);
  x.click('Clipboard');await x.flush();const d=dialog(x);expect(x.document.querySelector('textarea').value).toBe('');x.click('Fill window');await x.flush();expect(x.full()).toBe(true);x.click('Clipboard');await x.flush();expect(dialog(x)).toBe(d);expect(x.full()).toBe(true);expect(x.clients).toHaveLength(2);
  x.viewer.disconnect();await x.flush();expect(x.full()).toBe(false);expect(dialog(x)).toBeUndefined();current.emit('credentialsrequired',{types:['password']});await x.flush();expect(x.full()).toBe(false);expect(dialog(x)).toBeUndefined();x.viewer.dispose();current.emit('credentialsrequired',{types:['password']});current.emit('clipboard',{text:'Synthetic disposed cache'});await x.flush();expect(x.full()).toBe(false);expect(dialog(x)).toBeUndefined();expect(x.submitted).toBe(0);
 }finally{x.close();}
});
