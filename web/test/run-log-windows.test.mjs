import {it,expect} from 'vitest';
import {readFileSync} from 'node:fs';
import {parseHTML} from 'linkedom';
import {loadStore} from '../third_party/semblance/src/node/load.ts';
import {Runtime,EventBus,MOD} from '../third_party/semblance/src/index.ts';
import {DEFAULT_LAYERS} from '../third_party/semblance/src/ns.ts';
import {DomRenderer} from '../third_party/semblance/src/render/dom.ts';
import {createGraphUI,ARM} from '../../internal/server/web/semblance/graph-ui.mjs';
import {createRunsConsole} from '../../internal/server/web/semblance/runs.mjs';
const C='https://semblance.dev/ns/component#';
function fixture(){
 const store=loadStore('wat',readFileSync(new URL('../../internal/server/web/semblance/app.ttl',import.meta.url),'utf8'));
 const runtime=new Runtime(store),projection=runtime.project({modality:MOD.Screen,layers:DEFAULT_LAYERS,prefer:'sparql',roots:[ARM+'shell']},e=>{throw e});
 const {document,window}=parseHTML('<!doctype html><html><body><div id="app"></div></body></html>');
 const renderer=new DomRenderer(projection.index,document.getElementById('app'),ARM+'shell');renderer.flush();
 const app={store,runtime,projection,renderer,flush:()=>renderer.flush()},ui=createGraphUI(app,{onError:e=>{throw e}});
 const bus=new EventBus(runtime,projection);bus.attach(renderer,h=>{app.flush();ui.handled(h)});
 return{ui,document,window,store,runtime,projection,renderer,bus,root:ui.bind('monitorContent'),close(){runtime.dispose();store.dispose?.();}};
}

const tick=()=>new Promise(resolve=>setImmediate(resolve));
const run={id:'a'.repeat(32),reason:'Window verification',name:'readable-run',command:['echo','example'],state:'active',log_bytes:1};
it('stock pop-out, keyboard detach and window close move the exact log node and keep its cursor',async()=>{
 const x=fixture();let bytes=0;const calls=[];
 const panel=createRunsConsole({ui:x.ui,root:x.root,connected:()=>true,call:async(op,args)=>{calls.push([op,args]);return op==='runs'?[{...run,log_bytes:bytes}]:{run:{...run,log_bytes:++bytes},offset:args.offset,next_offset:args.offset+1,data:Buffer.from('x').toString('base64'),done:false};}});
 try{
  panel.setActive(true);await tick();await panel.open(run);
  const handles=()=>[...x.ui.handles.values()],tabs=handles().find(h=>h.kind==='Tabs'),output=handles().find(h=>h.kind==='Code'&&h.value==='x');
  const node=()=>x.renderer.nodeOf(x.projection.index.rootOf(output.iri)),original=node();
  const click=n=>{expect(n).toBeTruthy();n.dispatchEvent(new x.window.Event('click',{bubbles:true}));};
  const n=calls.length;click(x.document.querySelector('.sem-detach'));await tick();
  expect(tabs.detached).toBe(true);expect(calls.length).toBe(n);expect(node()).toBe(original);expect(original.closest('[data-sem-c="window"]')).toBeTruthy();
  x.ui.put(ARM+'shell',ARM+'view',ARM+'resources',true);panel.setActive(false);await panel.refresh();
  expect(node()).toBe(original);expect(original.isConnected).toBe(true);expect(output.value).toBe('xx');expect(calls.at(-1)[1].offset).toBe(1);
  const pause=handles().find(h=>h.kind==='Button'&&h.label==='Pause following');click(x.renderer.nodeOf(x.projection.index.rootOf(pause.iri)));await tick();
  const reads=bytes;await panel.refresh();expect(bytes).toBe(reads);
  // A real pointer tear commits geometry and stacking before native controls run.
  const stateWin=tabs.iri+'/~window-'+encodeURIComponent(tabs.tab.iri);
  x.ui.put(stateWin,C+'x',440);x.ui.put(stateWin,C+'y',220);x.ui.put(x.ui.windowDesktop.iri,C+'topmost',stateWin,true);
  click(x.document.querySelector('.sem-window-control[data-sem-control="maximize"]'));await tick();
  click(x.document.querySelector('.sem-window-control[data-sem-control="restore"]'));await tick();
  click(x.document.querySelector('.sem-window-control[data-sem-control="minimize"]'));await tick();
  click(x.document.querySelector('[data-sem-c="taskbar"] button'));await tick();
  click(x.document.querySelector('.sem-window-control[data-sem-control="close"]'));await tick();expect(tabs.detached).toBe(false);expect(x.document.querySelector('[data-sem-c="window"]:not([hidden])')).toBeNull();
  const stopped=calls.length;await panel.refresh();expect(calls.length).toBe(stopped);
  x.ui.put(ARM+'shell',ARM+'view',ARM+'monitor',true);panel.setActive(true);await tick();expect(node()).toBe(original);expect(output.value).toBe('xx');expect(original.closest('[data-sem-c="window"]')).toBeNull();
  const tab=x.document.querySelector('[role="tab"]');expect(tab).toBeTruthy();tab.dispatchEvent(Object.assign(new x.window.Event('keydown',{bubbles:true}),{key:'Enter',code:'Enter',shiftKey:true}));await tick();expect(tabs.detached).toBe(true);
  expect(node()).toBe(original);expect(handles().find(h=>h.kind==='Button'&&h.label==='Follow output')).toBe(pause);
  const win=tabs.iri+'/~window-'+encodeURIComponent(tabs.tab.iri);x.ui.put(win,C+'x',120);x.ui.put(win,C+'y',90);x.ui.put(x.ui.windowDesktop.iri,C+'topmost',win,true);
  panel.reset();await tick();expect(x.document.querySelector('[data-sem-c="window"]')).toBeNull();expect(output.disposed).toBe(true);
  const f=x.store.factory;expect(x.store.match(f.namedNode(win),null,null,f.defaultGraph())).toHaveLength(0);expect(x.store.match(f.namedNode(x.ui.windowDesktop.iri),f.namedNode(C+'topmost'),null,f.defaultGraph())).toHaveLength(0);
 }finally{panel.dispose();x.close();}
});
