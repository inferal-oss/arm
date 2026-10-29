import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import {replaceOwnership,releaseOwnership} from './graph-ownership.mjs';
import {stringifyJSON} from '../core.mjs';
import {createRunsConsole} from './runs.mjs';

// Execute the production GraphUI class and ownership lifecycle. The RDF store
// is a minimal read/write boundary; Component.replace/dispose are the real code.
function graphFixture({viewport,node}={}){
 const term=(value,termType='NamedNode')=>({value,termType,equals(other){return other?.value===value&&other?.termType===termType;}});
 const factory={namedNode:value=>term(value),literal:value=>term(String(value),'Literal'),defaultGraph:()=>term('','DefaultGraph'),quad:(subject,predicate,object,graph)=>({subject,predicate,object,graph})};
 let quads=[];const equal=(a,b)=>['subject','predicate','object','graph'].every(key=>a[key].equals(b[key]));
 const store={factory,match:(s,p,o,g)=>quads.filter(q=>(!s||q.subject.equals(s))&&(!p||q.predicate.equals(p))&&(!o||q.object.equals(o))&&(!g||q.graph.equals(g))),has:q=>quads.some(old=>equal(old,q))};
 const app={store,projection:{index:{rootOf:iri=>iri}},renderer:{nodeOf:()=>node??null},flush(){},runtime:{transaction:fn=>fn(),onInput:()=>()=>{},apply({remove=[],add=[]}){quads=quads.filter(q=>!remove.some(old=>equal(old,q)));for(const q of add)if(!store.has(q))quads.push(q);}}};
 const source=readFileSync(new URL('./graph-ui.mjs',import.meta.url),'utf8').replace(/^import .*\n/gm,'').replace(/export /g,'');
 const create=runInNewContext(source+'\ncreateGraphUI',{stockC:{},stockBEH:{},stringifyJSON,replaceOwnership,releaseOwnership,queueMicrotask,window:viewport});
 const ui=create(app);return {ui,store,root:ui.stack()};
}
const run={id:'a',name:'generated-name',reason:'Native purpose',state:'active',command:['test']},tick=()=>new Promise(resolve=>setImmediate(resolve));
test('stock opener retains the direct Button type and Effect while owned headings survive empty groups',async t=>{
 const {ui,store,root}=graphFixture();let rows=[run];const panel=createRunsConsole({ui,root,connected:()=>true,call:async op=>op==='runs'?rows:null});t.after(()=>{panel.dispose();root.dispose();});panel.setActive(true);await tick();
 const handles=()=>[...ui.handles.values()],current=handles().find(h=>h.kind==='Heading'&&h.label.startsWith('Current')),history=handles().find(h=>h.kind==='Heading'&&h.label.startsWith('History'));
 const opener=handles().find(h=>h.kind==='Button'&&h.label==='Native purpose'),f=store.factory,RDF='http://www.w3.org/1999/02/22-rdf-syntax-ns#type';
 assert.equal(store.match(f.namedNode(opener.iri),f.namedNode(RDF),f.namedNode('https://semblance.dev/ns/component#Button')).length,1);
 assert.equal(store.match(f.namedNode(opener.iri),f.namedNode(RDF),f.namedNode('https://arm.local/ui#Effect')).length,1);
 rows=[];await panel.refresh();assert.equal(current.disposed,undefined);assert.equal(history.disposed,undefined);assert.equal(current.hidden,true);assert.equal(history.hidden,true);
 rows=[run];await panel.refresh();assert.equal(current.hidden,false);const filter=handles().find(h=>h.kind==='TextField');filter.value='no match';filter.emit('input');assert.equal(current.hidden,true);assert.ok(!current.disposed);filter.value='';filter.emit('input');assert.equal(current.hidden,false);
 const toggle=handles().find(h=>h.kind==='Checkbox'&&h.label==='Hide inactive runs');toggle.checked=false;toggle.emit('change');rows=[{...run,state:'terminal',outcome:'completed'}];await panel.refresh();assert.equal(current.hidden,true);assert.equal(history.hidden,false);assert.ok(current.parents.size>0&&history.parents.size>0);
 panel.dispose();root.dispose();assert.equal(current.disposed,true);assert.equal(history.disposed,true);
});

test('detachable wrapper owns its exact content and retires native geometry with its tab',()=>{
 const {ui,store,root}=graphFixture(),content=ui.stack([ui.code('private log')]),window=ui.detachable('Run log',content),f=store.factory,C='https://semblance.dev/ns/component#';
 root.replace(window);assert.equal(content.disposed,undefined);assert.equal(window.detached,false);
 window.popOut();assert.equal(window.detached,true);assert.ok(content.parents.has(window.tab));
 window.dock();assert.equal(window.detached,false);assert.equal(content.disposed,undefined);
 const iri=window.iri+'/~window-'+encodeURIComponent(window.tab.iri);ui.put(iri,C+'x',120);ui.put(iri,C+'y',80);ui.put(ui.windowDesktop.iri,C+'topmost',iri,true);
 root.replace();assert.equal(window.disposed,true);assert.equal(content.disposed,true);assert.equal(window.tab.disposed,true);
 assert.equal(store.match(f.namedNode(iri),null,null,f.defaultGraph()).length,0);assert.equal(store.match(f.namedNode(ui.windowDesktop.iri),f.namedNode(C+'topmost'),null,f.defaultGraph()).length,0);
 ui.windowDesktop.dispose();root.dispose();
});

test('floating log window is kept reachable after resize and retires its viewport listener',()=>{
 const listeners=new Set(),viewport={innerWidth:390,innerHeight:640,addEventListener(name,fn){assert.equal(name,'resize');listeners.add(fn);},removeEventListener(name,fn){assert.equal(name,'resize');listeners.delete(fn);}};
 const box={left:440,top:800,width:358,height:440},node={get offsetWidth(){return box.width;},get offsetHeight(){return box.height;},getBoundingClientRect:()=>({...box,left:box.left+14,width:box.width-28})};viewport.getComputedStyle=()=>({left:box.left+'px',top:box.top+'px'});const {ui,store,root}=graphFixture({viewport,node}),content=ui.code('output'),window=ui.detachable('Log',content),C='https://semblance.dev/ns/component#',f=store.factory;
 root.replace(window);window.popOut();assert.equal(listeners.size,1);for(const fn of listeners)fn();
 const iri=window.iri+'/~window-'+encodeURIComponent(window.tab.iri),value=p=>store.match(f.namedNode(iri),f.namedNode(C+p),null,f.defaultGraph())[0]?.object.value;
 assert.equal(value('x'),'32');assert.equal(value('y'),'200');
 box.width=522;for(const fn of listeners)fn();assert.equal(value('width'),'356','oversized frame must fit the narrowed viewport');box.width=358;for(const fn of listeners)fn();
 ui.put(iri,C+'windowState',C+'Maximized',true);ui.put(iri,C+'x',100);for(const fn of listeners)fn();assert.equal(value('x'),'100','maximizing must preserve floating geometry');
 ui.put(iri,C+'windowState',C+'Normal',true);for(const fn of listeners)fn();assert.equal(value('x'),'32');
 root.dispose();assert.equal(listeners.size,0);ui.windowDesktop.dispose();
});
