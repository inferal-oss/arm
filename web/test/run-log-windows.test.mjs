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

it('multiple native windows retain independent nodes, tile, reveal existing logs and close independently',async()=>{
 const x=fixture(),records=[{...run,id:'1'.repeat(32),reason:'Agent one work',agent:'agent-one'},{...run,id:'2'.repeat(32),reason:'Agent two work',agent:'agent-two'}];
 const calls=[];const panel=createRunsConsole({ui:x.ui,root:x.root,connected:()=>true,call:async(op,args)=>{calls.push([op,args]);if(op==='runs')return records;const record=records.find(r=>r.id===args.id),data=Buffer.from(record.agent+'\n');return {run:{...record,log_bytes:args.offset+data.length},offset:args.offset,next_offset:args.offset+data.length,data:data.toString('base64'),done:false};}});
 try{
  const click=node=>{expect(node).toBeTruthy();node.dispatchEvent(new x.window.Event('click',{bubbles:true}));},nodeOf=h=>x.renderer.nodeOf(x.projection.index.rootOf(h.iri));
  panel.setActive(true);await tick();const outputs=[];
  for(const record of records){await panel.open(record);const output=[...x.ui.handles.values()].find(h=>h.kind==='Code'&&h.value.includes(record.agent));outputs.push({handle:output,node:nodeOf(output)});click(x.document.querySelector('.sem-detach'));await tick();}
  expect(x.document.querySelectorAll('[data-sem-c="window"]:not([hidden])')).toHaveLength(2);expect(x.ui.windowFrames.size).toBe(2);const f=x.store.factory;expect(x.store.match(f.namedNode(x.ui.windowDesktop.iri),f.namedNode(C+'topmost'),null,f.defaultGraph())[0].object.value).toBe([...x.ui.windowFrames][1].frameIRI);
  const windows=()=>outputs.map(o=>o.node.closest('[data-sem-c="window"]'));
  const control=(frame,label)=>[...frame.querySelectorAll('button')].find(n=>n.getAttribute('aria-label')===label||n.textContent.trim()===label);
  click(control(windows()[0],'Tile log windows'));await tick();expect(x.ui.windowsTiled).toBe(true);expect(x.document.querySelectorAll('[data-sem-tiled]')).toHaveLength(2);expect(x.document.querySelector('[data-sem-c="taskbar"][data-sem-edge="bottom"]')).toBeTruthy();
  for(const output of outputs)expect(nodeOf(output.handle)).toBe(output.node);
  click(control(windows()[0],'Pause following'));await tick();const paused=outputs[0].handle.value,before=outputs[1].handle.value;await panel.refresh();expect(outputs[0].handle.value).toBe(paused);expect(outputs[1].handle.value.length).toBeGreaterThan(before.length);
  click(control(windows()[1],'Float log windows'));await tick();expect(x.ui.windowsTiled).toBe(false);expect(x.document.querySelectorAll('[data-sem-tiled]')).toHaveLength(0);expect(x.document.querySelector('[data-sem-c="taskbar"][data-sem-edge]')).toBeNull();
  click(windows()[0].querySelector('[data-sem-control="minimize"]'));await tick();const count=calls.length;await panel.open(records[0]);await tick();expect(calls.length).toBe(count);expect(x.document.querySelectorAll('[data-sem-c="window"]:not([hidden])')).toHaveLength(2);expect(nodeOf(outputs[0].handle)).toBe(outputs[0].node);expect(control(windows()[0],'Follow output')).toBeTruthy();
  click(control(windows()[0],'Close log'));await tick();expect(x.ui.windowFrames.size).toBe(1);expect(outputs[0].handle.disposed).toBe(true);expect(nodeOf(outputs[1].handle)).toBe(outputs[1].node);expect(outputs[1].node.isConnected).toBe(true);
  const tail=outputs[1].handle.value;await panel.refresh();expect(outputs[1].handle.value.length).toBeGreaterThan(tail.length);
  panel.reset();await tick();expect(x.ui.windowFrames.size).toBe(0);expect(x.document.querySelector('[data-sem-c="window"]')).toBeNull();
 }finally{panel.dispose();x.close();}
});
