import {it,expect} from 'vitest';
import {parseHTML} from 'linkedom';
import {loadStore} from '../third_party/semblance/src/node/load.ts';
import {Runtime,EventBus,MOD} from '../third_party/semblance/src/index.ts';
import {DEFAULT_LAYERS} from '../third_party/semblance/src/ns.ts';
import {DomRenderer} from '../third_party/semblance/src/render/dom.ts';

const EX='https://example.org/native-selection#',C='https://semblance.dev/ns/component#';
const graph=`@prefix ex: <${EX}> . @prefix c: <${C}> . @prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .
ex:root a c:Stack ; c:children (ex:views ex:choice) .
ex:state ex:view ex:a .
ex:views a c:Segmented ; c:label "Views" ; c:controls ex:state ; c:property ex:view ; c:compactAs c:Picker ; c:options (ex:a ex:b ex:c) .
ex:choice a c:Choice ; c:label "Choose a view" ; c:controls ex:state ; c:property ex:view ; c:options (ex:a ex:b ex:c) .
ex:a rdfs:label "Alpha" . ex:b rdfs:label "Beta" . ex:c rdfs:label "Gamma" .`;

for(const prefer of ['sparql','function'])it(`native choice and compact picker follow graph, sibling events and selected removal (${prefer})`,()=>{
 const store=loadStore('wat',graph),runtime=new Runtime(store);
 const projection=runtime.project({modality:MOD.Screen,layers:DEFAULT_LAYERS,prefer,roots:[EX+'root']},e=>{throw e});
 const {document,window}=parseHTML('<!doctype html><html><body><main></main></body></html>');
 const renderer=new DomRenderer(projection.index,document.querySelector('main'),EX+'root');renderer.flush();
 let handled=0;
 const bus=new EventBus(runtime,projection,{prefer}),detach=bus.attach(renderer,()=>{handled++;renderer.flush();});
 try{
  const picker=document.querySelector('[data-sem-c="segmented"] select'),choice=document.querySelector('[data-sem-c="choice"] select');
  expect(picker).toBeTruthy();expect(choice).toBeTruthy();
  const controls=[picker,choice],originalOptions=controls.map(s=>[...s.options]);
  const assertSelected=value=>{for(const select of controls){expect(select.value).toBe(EX+value);for(const option of select.options){expect(option.selected).toBe(option.value===EX+value);expect(option.hasAttribute('selected')).toBe(option.value===EX+value);}}};
  assertSelected('a');
  // A real browser's dirty option state is covered by the native browser gate;
  // linkedom establishes both graph projection/event paths and stable identities.
  // Select the target once, as native select choice does. linkedom's false
  // setter clears a selected sibling even when called on a different option.
  [...picker.options].find(option=>option.value===EX+'b').selected=true;
  picker.dispatchEvent(new window.Event('change',{bubbles:true}));assertSelected('b');
  const alpha=[...document.querySelectorAll('[data-sem-c="segmented"] button')].find(b=>b.textContent==='Alpha');expect(alpha).toBeTruthy();
  alpha.dispatchEvent(new window.Event('click',{bubbles:true}));assertSelected('a');
  const f=store.factory,subject=f.namedNode(EX+'state'),predicate=f.namedNode(EX+'view');
  const handledBeforeGraphChange=handled;
  runtime.apply({remove:store.match(subject,predicate,null),add:[f.quad(subject,predicate,f.namedNode(EX+'c'))]});renderer.flush();assertSelected('c');expect(handled).toBe(handledBeforeGraphChange);
  for(const [i,select] of controls.entries()){const options=[...select.options];expect(options).toHaveLength(originalOptions[i].length);for(const [n,option]of options.entries())expect(option).toBe(originalOptions[i][n]);}
  runtime.apply({remove:store.match(subject,predicate,null),add:[]});renderer.flush();expect(handled).toBe(handledBeforeGraphChange);
  for(const select of controls){const old=[...select.options].find(o=>o.value===EX+'c');expect(old.hasAttribute('selected')).toBe(false);expect(old.selected).toBe(false);}
  expect(document.querySelector('[data-sem-shape-invalid]')).toBeNull();
 }finally{detach();runtime.dispose();store.dispose?.();}
});
