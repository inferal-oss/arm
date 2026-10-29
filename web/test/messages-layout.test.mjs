import {it,expect} from 'vitest';
import {readFileSync} from 'node:fs';
import {parseHTML} from 'linkedom';
import {loadStore} from '../third_party/semblance/src/node/load.ts';
import {Runtime,EventBus,MOD} from '../third_party/semblance/src/index.ts';
import {DEFAULT_LAYERS} from '../third_party/semblance/src/ns.ts';
import {DomRenderer} from '../third_party/semblance/src/render/dom.ts';
import {compileStyles} from '../third_party/semblance/src/style/compile.ts';
import {createGraphUI,ARM} from '../../internal/server/web/semblance/graph-ui.mjs';
import {createOperatorMessaging} from '../../internal/server/web/semblance/messages.mjs';

function fixture({storage}={}){
 const store=loadStore('wat',readFileSync(new URL('../../internal/server/web/semblance/app.ttl',import.meta.url),'utf8'));
 const runtime=new Runtime(store),projection=runtime.project({modality:MOD.Screen,layers:DEFAULT_LAYERS,prefer:'sparql',roots:[ARM+'shell']},e=>{throw e});
 const {document,window}=parseHTML('<!doctype html><html><body><div id="app"></div></body></html>');
 const renderer=new DomRenderer(projection.index,document.getElementById('app'),ARM+'shell');renderer.flush();
 const app={store,runtime,projection,renderer,flush:()=>renderer.flush()},ui=createGraphUI(app,{onError:e=>{throw e}});
 const bus=new EventBus(runtime,projection);bus.attach(renderer,h=>{app.flush();ui.handled(h)});
 const calls=[],root=ui.bind('messagesContent');ui.put(ARM+'shell',ARM+'view',ARM+'messages',true);
 const panel=createOperatorMessaging({ui,root,connected:()=>false,storage,call:async(...args)=>{calls.push(args);throw Error('Must not call transport');}});
 return {ui,panel,calls,document,store,root,close(){panel.dispose();runtime.dispose();store.dispose?.();}};
}
const body='First paragraph.\n\nSecond paragraph.\n'+ 'unbroken'.repeat(80);
it('component message bodies retain paragraphs and wrap without hiding exact authority or receipts',()=>{
 const x=fixture();try{
 const recipient='synthetic-recipient-'+ 'a'.repeat(80),receipt='First receipt line.\nSecond receipt line.';
 x.panel.update({contacts:[],messages:[{id:'synthetic-message',sender:'operator',audience:recipient,body,created_at:'2026-10-01T12:00:00Z',deliveries:{[recipient]:{state:'queued',receipt:{text:receipt}}}}],leases:[]});
 const bodies=[...x.document.querySelectorAll('[data-sem-c="arm-message-body"]')];
 expect(bodies.map(n=>n.textContent)).toContain(body);expect(bodies.map(n=>n.textContent)).toContain(receipt);
 const pairs=[...x.document.querySelectorAll('[data-sem-c="arm-message-facts"] .sem-pair')].map(n=>[n.querySelector('dt')?.textContent,n.querySelector('dd')?.textContent]);
 expect(pairs).toContainEqual(['Recipient',recipient]);expect(pairs.some(([label])=>label==='Delivery')).toBe(true);
 const css=compileStyles(x.store);expect(css).toMatch(/arm-message-body[^}]*white-space: pre-wrap/s);expect(css).toMatch(/arm-message-body[^}]*overflow-wrap: anywhere/s);
 expect(x.calls).toEqual([]);
 }finally{x.close();}
});
it('restored unknown requests keep exact key, paragraphs, warning and explicit recovery actions while offline',()=>{
 const request={idempotency_key:'synthetic-key-'+ 'b'.repeat(90),audience:'synthetic-worker',targets:[],body};
 const x=fixture({storage:{getItem:()=>JSON.stringify([{request}])}});try{
 const facts=x.document.querySelector('[data-sem-c="arm-message-facts"]');expect(facts.textContent).toContain(request.idempotency_key);
 expect(x.document.querySelector('[data-sem-c="arm-message-body"]').textContent).toBe(body);
 const buttons=[...x.document.querySelectorAll('button')];
 for(const label of ['Check stored result','Retry exact request'])expect(buttons.find(n=>n.textContent===label)?.hasAttribute('disabled')).toBe(true);
 expect(x.document.body.textContent).toContain('Outcome uncertain');expect(x.document.body.textContent).toContain('Check the stored result before retrying.');expect(x.calls).toEqual([]);
 }finally{x.close();}
});
