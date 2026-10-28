import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import {replaceOwnership,releaseOwnership} from './graph-ownership.mjs';
import {stringifyJSON} from '../core.mjs';
import {createRunsConsole} from './runs.mjs';

// Execute the production GraphUI class and ownership lifecycle. The RDF store
// is a minimal read/write boundary; Component.replace/dispose are the real code.
function graphFixture(){
 const term=(value,termType='NamedNode')=>({value,termType,equals(other){return other?.value===value&&other?.termType===termType;}});
 const factory={namedNode:value=>term(value),literal:value=>term(String(value),'Literal'),defaultGraph:()=>term('','DefaultGraph'),quad:(subject,predicate,object,graph)=>({subject,predicate,object,graph})};
 let quads=[];const equal=(a,b)=>['subject','predicate','object','graph'].every(key=>a[key].equals(b[key]));
 const store={factory,match:(s,p,o,g)=>quads.filter(q=>(!s||q.subject.equals(s))&&(!p||q.predicate.equals(p))&&(!o||q.object.equals(o))&&(!g||q.graph.equals(g))),has:q=>quads.some(old=>equal(old,q))};
 const app={store,projection:{index:{rootOf:iri=>iri}},renderer:{nodeOf:()=>null},flush(){},runtime:{transaction:fn=>fn(),onInput:()=>()=>{},apply({remove=[],add=[]}){quads=quads.filter(q=>!remove.some(old=>equal(old,q)));for(const q of add)if(!store.has(q))quads.push(q);}}};
 const source=readFileSync(new URL('./graph-ui.mjs',import.meta.url),'utf8').replace(/^import .*\n/gm,'').replace(/export /g,'');
 const create=runInNewContext(source+'\ncreateGraphUI',{stockC:{},stockBEH:{},stringifyJSON,replaceOwnership,releaseOwnership});
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
 rows=[{...run,state:'terminal',outcome:'completed'}];await panel.refresh();assert.equal(current.hidden,true);assert.equal(history.hidden,false);assert.ok(current.parents.size>0&&history.parents.size>0);
 panel.dispose();root.dispose();assert.equal(current.disposed,true);assert.equal(history.disposed,true);
});
