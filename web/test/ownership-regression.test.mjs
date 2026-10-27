import {it,expect} from 'vitest';
import {readFileSync} from 'node:fs';
import {parseHTML} from 'linkedom';
import {loadStore} from '../third_party/semblance/src/node/load.ts';
import {Runtime,EventBus,MOD} from '../third_party/semblance/src/index.ts';
import {DEFAULT_LAYERS} from '../third_party/semblance/src/ns.ts';
import {DomRenderer} from '../third_party/semblance/src/render/dom.ts';
import {createGraphUI,ARM} from '../../internal/server/web/semblance/graph-ui.mjs';
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
it('replaces a visible graph collection repeatedly without disappearing or retaining old cards',()=>{
 const x=fixture();try{
  for(let i=0;i<8;i++)x.ui.batch(()=>x.root.replace(x.ui.card('Sample '+i,[x.ui.text('CPU '+i)])));
  expect(x.document.body.textContent).toContain('Sample 7');expect(x.document.body.textContent).toContain('CPU 7');
  expect(x.document.body.textContent).not.toContain('CPU 6');expect(x.ui.handles.size).toBeLessThan(12);
  expect(x.store.match(null,x.store.factory.namedNode(C+'label')).filter(q=>q.object.value.startsWith('Sample '))).toHaveLength(1);
 }finally{x.close();}
});
it('reparented persistent fields preserve graph state and their exact rendered node',()=>{
 const x=fixture();try{const field=x.ui.field('Reason',{value:'kept'}),first=x.ui.card('Before',[field]);x.root.replace(first);
  const original=x.renderer.nodeOf(x.projection.index.rootOf(field.iri));
  const after=x.ui.card('After',[field]);x.root.replace(after);
  expect(first.disposed).toBe(true);expect(field.disposed).not.toBe(true);expect(field.value).toBe('kept');expect(field.parents.size).toBe(1);
  expect(x.renderer.nodeOf(x.projection.index.rootOf(field.iri))).toBe(original);
 }finally{x.close();}
});
it('a dialog append preserves initial body contents and disposes both content and actions',()=>{
 const x=fixture();try{const content=x.ui.text('Exact initial plan'),action=x.ui.button('Keep unchanged',()=>{}),dialog=x.ui.dialog('Review',[content],[action]);
  dialog.append(x.ui.text('Additional instruction'));dialog.showModal();
  expect(x.document.body.textContent).toContain('Exact initial plan');expect(x.document.body.textContent).toContain('Additional instruction');
  dialog.dispose();expect(x.ui.handles.has(content.iri)).toBe(false);expect(x.ui.handles.has(action.iri)).toBe(false);
 }finally{x.close();}
});
it('replacing a static Turtle list removes blank-node cells and uses a new head per update',()=>{
 const x=fixture();try{const header=x.ui.bind('statusline'),n=x.ui.text('fresh');header.append(n);const f=x.store.factory;
  const head=()=>x.store.match(f.namedNode(header.iri),f.namedNode(C+'children'),null,f.defaultGraph())[0].object.value;
  const first=head();header.replace(...header.children);expect(head()).not.toBe(first);expect(x.document.body.textContent).toContain('fresh');
  expect(header.subjects.size).toBeLessThan(15);
 }finally{x.close();}
});
it('Choice changes retire removed option data rather than retaining stale registrations',()=>{
 const x=fixture();try{const choice=x.ui.select('Recipient',[{value:'old',label:'Old registration'}]);x.root.replace(choice);
  choice.set({options:[{value:'new',label:'Current registration'}],value:'new'});
  expect(choice.value).toBe('new');expect(x.store.match(x.store.factory.namedNode(choice.iri+'/option/old'),null,null,x.store.factory.defaultGraph())).toHaveLength(0);
 }finally{x.close();}
});
