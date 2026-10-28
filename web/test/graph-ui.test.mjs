import {it,expect} from 'vitest';
import {readFileSync} from 'node:fs';
import {parseHTML} from 'linkedom';
import {loadStore} from '../third_party/semblance/src/node/load.ts';
import {Runtime,EventBus,Islands,MOD} from '../third_party/semblance/src/index.ts';
import {DEFAULT_LAYERS} from '../third_party/semblance/src/ns.ts';
import {DomRenderer} from '../third_party/semblance/src/render/dom.ts';
import {createGraphUI,ARM} from '../../internal/server/web/semblance/graph-ui.mjs';
import {createResourceOverview} from '../../internal/server/web/semblance/resources.mjs';
import {createPoolConsole} from '../../internal/server/web/semblance/pools.mjs';

function fixture({widgets={}}={}){
 const store=loadStore('wat',readFileSync(new URL('../../internal/server/web/semblance/app.ttl',import.meta.url),'utf8'));
 const runtime=new Runtime(store),projection=runtime.project({modality:MOD.Screen,layers:DEFAULT_LAYERS,prefer:'sparql',roots:[ARM+'shell']},e=>{throw e});
 const {document,window}=parseHTML('<!doctype html><html><body><div id="app"></div></body></html>');
 const renderer=new DomRenderer(projection.index,document.getElementById('app'),ARM+'shell');renderer.flush();
 const app={store,runtime,projection,renderer,flush:()=>renderer.flush()},ui=createGraphUI(app,{widgets,onError:e=>{throw e}});
 const bus=new EventBus(runtime,projection);bus.attach(renderer,h=>{app.flush();ui.handled(h)});
 return{ui,document,window,store,runtime,projection,renderer,bus,close(){runtime.dispose();store.dispose?.();}};
}
it('resource refresh retains disclosure, focused consumer handles and exact visible commitments',()=>{
 const x=fixture();try{
  const root=x.ui.grid(),overview=createResourceOverview({ui:x.ui,root,owner:()=> 'Operator',onLease:()=>{},help:()=>x.ui.button('Explain',()=>{})});
  x.ui.bind('monitorContent').replace(root);
  const r={id:'permits',kind:'custom',unit:'permits',available:0,reserved:9223372036854775807n,budget_total:1,users:[{lease:'lease-a',reason:'Keep building',quantity:2}],custom:{pool:{name:'Build permits'}}};
  overview.update([r],{});
  const disclosure=[...x.ui.handles.values()].find(h=>h.kind==='AccordionItem'&&h.label.startsWith('Accounting & consumers'));
  expect(disclosure.open).toBe(false);disclosure.open=true;
  const consumer=[...x.ui.handles.values()].find(h=>h.kind==='Button'&&h.label==='Keep building');
  overview.update([{...r,available:1}],{});
  expect(disclosure.open).toBe(true);expect(consumer.disposed).not.toBe(true);
  expect(x.document.body.textContent).toContain('RESERVED · 9,223,372,036,854,775,807 permits');
  overview.update([r],{},'missing');expect(disclosure.disposed).not.toBe(true);
  overview.update([r],{});expect(disclosure.open).toBe(true);
  overview.dispose();expect(consumer.disposed).toBe(true);
 }finally{x.close();}
});
it('root pools do not claim parent backing while child promises and material warnings remain visible',()=>{
 const x=fixture();try{
  const root=x.ui.grid(),overview=createResourceOverview({ui:x.ui,root,owner:()=> 'Operator',onLease:()=>{},help:()=>x.ui.button('Explain',()=>{})});
  x.ui.bind('monitorContent').replace(root);
  overview.update([
   {id:'root',kind:'custom',unit:'permits',users:[],custom:{pool:{id:'root',name:'Root permits'},partition:{configured:4,committed:3,direct_reserved:0,child_backing:3,held_backing:4,uncarved_available:1,pending_return:0}}},
   {id:'child',kind:'custom',unit:'permits',deficit:2,users:[{lease:'lease-a',reason:'Keep building',quantity:3,state:'active'}],custom:{pool:{id:'child',parent:'root',name:'Child permits'},partition:{configured:1,committed:3,direct_reserved:3,child_backing:0,held_backing:3,uncarved_available:0,pending_return:2}}}
  ],{});
  const node=label=>{const handle=[...x.ui.handles.values()].find(h=>h.kind==='Card'&&h.label===label);return x.renderer.nodeOf(x.projection.index.rootOf(handle.iri));};
  const rootNode=node('Root permits'),childNode=node('Child permits');
  expect(rootNode.textContent).not.toContain('Held in parent:');
  expect(rootNode.textContent).toContain('COMMITTED · 3 permits');
  expect(rootNode.textContent).toContain('1 permits');
  expect(childNode.textContent).toContain('COMMITTED · 3 permits');
  expect([...x.ui.handles.values()].filter(h=>h.kind==='AccordionItem').every(h=>!h.open)).toBe(true);
  for(const warning of ['Held in parent: 3 permits.','Pending return: 2 permits.','Shortfall: 2 permits. Existing promises remain charged.']){
   const text=[...childNode.querySelectorAll('*')].find(n=>n.textContent===warning);expect(text).toBeTruthy();expect(text.closest('[hidden]')).toBeNull();
  }
  overview.dispose();
 }finally{x.close();}
});
it('a focused consumer keeps its RDF handle and DOM node through renewal and other consumer membership changes',()=>{
 const x=fixture();try{
  const root=x.ui.grid(),overview=createResourceOverview({ui:x.ui,root,owner:()=> 'Operator',onLease:()=>{},help:()=>x.ui.button('Explain',()=>{})});
  x.ui.bind('monitorContent').replace(root);
  const first={lease:'lease-a',relationship:'direct',reason:'Keep building',quantity:1,state:'active',expires_at:'2026-10-27T17:00:00Z'};
  const r={id:'permits',kind:'custom',unit:'permits',available:1,reserved:1,budget_total:2,users:[first]};
  overview.update([r],{});
  const disclosure=[...x.ui.handles.values()].find(h=>h.kind==='AccordionItem'&&h.label.startsWith('Accounting & consumers'));disclosure.open=true;
  const consumer=[...x.ui.handles.values()].find(h=>h.kind==='Button'&&h.label==='Keep building');
  const node=x.renderer.nodeOf(x.projection.index.rootOf(consumer.iri));let focusEvents=0;node.addEventListener('focus',()=>focusEvents++);consumer.focus();
  // Linkedom dispatches focus; native activeElement retention is checked in Chrome.
  expect(focusEvents).toBe(1);
  const renewed={...first,expires_at:'2026-10-27T18:00:00Z'};
  const second={lease:'lease-b',relationship:'direct',reason:'Another build',quantity:1};
  for(const users of [[renewed],[renewed,second],[renewed]]){
   overview.update([{...r,users}],{});
   expect(consumer.disposed).not.toBe(true);expect(node.isConnected).toBe(true);
   expect(x.renderer.nodeOf(x.projection.index.rootOf(consumer.iri))).toBe(node);
   expect(focusEvents).toBe(1);expect(disclosure.open).toBe(true);
  }
  expect([...x.ui.handles.values()].some(h=>h.label==='Another build')).toBe(false);
  overview.dispose();
 }finally{x.close();}
});
it('pool management uses a stock contained dialog; an empty inventory leaves actions behind its entry',async()=>{
 const x=fixture();try{
  const pool=createPoolConsole({ui:x.ui,root:x.ui.bind('monitorContent'),connected:()=>true,call:async()=>[]});
  pool.update({status:{host:'local',cursor:1},route:{view:'resources'}});await new Promise(resolve=>setImmediate(resolve));
  expect(x.document.querySelector('[role="dialog"]')).toBeFalsy();
  const entry=[...x.ui.handles.values()].find(h=>h.label==='Manage pools…');entry.emit('activate');
  expect(x.document.querySelector('[role="dialog"]').textContent).toContain('No custom pools yet.');
  expect(x.document.querySelector('[role="dialog"] input[type="search"]')).toBeTruthy();
  pool.applyRoute({view:'monitor'});expect(x.document.querySelector('[role="dialog"]')).toBeFalsy();
  pool.dispose();
 }finally{x.close();}
});
it('opening an older hosted dialog raises its existing projection above newer management overlays',()=>{
 const x=fixture();try{
  const lease=x.ui.dialog('Lease detail',[x.ui.button('Lease action',()=>{})]);
  const manager=x.ui.dialog('Pool manager',[x.ui.button('Pool action',()=>{})]);
  const editor=x.ui.dialog('Review pool change',[x.ui.button('Cancel change',()=>{})]);
  const node=h=>x.renderer.nodeOf(x.projection.index.rootOf(h.iri));
  manager.showModal();const managerNode=node(manager),managerAction=managerNode.querySelector('button');
  editor.showModal();expect([...x.document.querySelectorAll('[role="dialog"]')].at(-1).textContent).toContain('Review pool change');
  editor.close();lease.showModal();
  expect([...x.document.querySelectorAll('[role="dialog"]')].at(-1).textContent).toContain('Lease detail');
  expect(node(manager)).toBe(managerNode);expect(node(manager).querySelector('button')).toBe(managerAction);
  lease.close();expect(manager.open).toBe(true);manager.showModal();
  expect([...x.ui.desktopHosts].at(-1)).toBe(manager.iri);expect(manager.disposed).not.toBe(true);
 }finally{x.close();}
});
it('renders the stock Semblance default and runs button effects only after a graph event',()=>{
 const x=fixture();try{let clicks=0;const b=x.ui.button('Review',()=>clicks++);x.ui.bind('monitorContent').replace(b);
 expect(x.document.querySelector('[data-sem-theme="semblance"]')).toBeTruthy();expect(clicks).toBe(0);
 x.renderer.nodeOf(x.projection.index.rootOf(b.iri)).dispatchEvent(new x.window.Event('click',{bubbles:true}));expect(clicks).toBe(1);
 expect(x.store.match(null,x.store.factory.namedNode('https://semblance.dev/ns/behaviour#sequence')).length).toBeGreaterThan(0);
 }finally{x.close();}
});
it('field, choice and checkbox changes live in the graph before their host callback',()=>{
 const x=fixture();try{let seen='',chosen='',checked=false;const a=x.ui.field('Reason',{value:'first'},()=>seen=a.value),s=x.ui.select('Scope',[{value:'host',label:'Host'},{value:'global',label:'Global'}],{},()=>chosen=s.value),check=x.ui.check('Pause',{},()=>checked=check.checked);x.ui.bind('monitorContent').replace(a,s,check);
 const input=x.renderer.nodeOf(x.projection.index.rootOf(a.iri)).querySelector('input');input.value='reviewed';input.dispatchEvent(new x.window.Event('input',{bubbles:true}));expect(a.value).toBe('reviewed');expect(seen).toBe('reviewed');
 const select=x.renderer.nodeOf(x.projection.index.rootOf(s.iri)).querySelector('select');for(const option of select.options)option.selected=option.value===s.iri+'/option/global';select.dispatchEvent(new x.window.Event('change',{bubbles:true}));expect(s.value).toBe('global');expect(chosen).toBe('global');
 const inputCheck=x.renderer.nodeOf(x.projection.index.rootOf(check.iri)).querySelector('input');inputCheck.checked=true;inputCheck.dispatchEvent(new x.window.Event('change',{bubbles:true}));expect(checked).toBe(true);
 }finally{x.close();}
});
it('uses stock Dialog graph open/close and notifies the effect on stock close',()=>{
 const x=fixture();try{let closed=0;const dialog=x.ui.dialog('Exact review',[x.ui.text('No automatic apply')]);dialog.on('close',()=>closed++);dialog.showModal();expect(x.document.querySelector('[role="dialog"]')).toBeTruthy();
 const close=x.document.querySelector('[aria-label="Close"]');expect(close).toBeTruthy();close.dispatchEvent(new x.window.Event('click',{bubbles:true}));expect(dialog.open).toBe(false);expect(closed).toBe(1);
 }finally{x.close();}
});
it('preserves exact review strings, BigInt JSON and reused disclosure identity',()=>{
 const x=fixture();try{const d=x.ui.json({capacity:9223372036854775807n},'Exact'),table=x.ui.table(['Effect','After'],[['Capacity','9007199254740993'],['Identifier','000123']]),facts=x.ui.descriptions([['Code','000123'],['Count','9007199254740993']]);d.open=true;x.ui.bind('monitorContent').replace(d,table,facts);expect(x.document.body.textContent).toContain('9223372036854775807');expect(x.document.querySelector('table').textContent).toContain('9007199254740993');expect(x.document.querySelector('table').textContent).toContain('000123');expect(x.document.querySelector('dl').textContent).toContain('000123');expect(x.document.querySelector('dl').textContent).toContain('9007199254740993');facts.setPairs([['Code','000124']]);expect(x.document.querySelector('dl').textContent).toBe('Code000124');expect(x.store.match(x.ui.nn(facts.iri+'/field/1'),null,null,x.store.factory.defaultGraph())).toHaveLength(0);expect(x.store.match(x.ui.nn(facts.iri+'/field/1/value'),null,null,x.store.factory.defaultGraph())).toHaveLength(0);const n=x.renderer.nodeOf(x.projection.index.rootOf(d.iri));x.ui.bind('monitorContent').replace(d,table,facts);expect(x.renderer.nodeOf(x.projection.index.rootOf(d.iri))).toBe(n);x.ui.bind('monitorContent').replace(d,table);expect(facts.disposed).toBe(true);facts.setPairs([['Late value','must not return']]);expect(x.store.match(x.ui.nn(facts.iri+'/data'),null,null,x.store.factory.defaultGraph())).toHaveLength(0);
 }finally{x.close();}
});

it('MessageList renders rich component bodies with stock conversation semantics',()=>{
 const x=fixture();try{let replies=0;const body=x.ui.stack([x.ui.text('Build complete'),x.ui.badge('Acknowledged'),x.ui.button('Reply',()=>replies++)]),list=x.ui.messageList('Operations');x.ui.bind('monitorContent').replace(list);list.setMessages([{id:'m1',author:'build-agent',at:'2026-10-26T20:00:00Z',summary:'Build complete',body}]);
 expect(x.document.querySelector('[data-sem-c="message-list"]').getAttribute('role')).toBe('log');expect(x.document.querySelector('[data-sem-c="message"]')).toBeTruthy();expect(x.document.body.textContent).toContain('build-agent');expect(x.document.body.textContent).toContain('Acknowledged');expect(replies).toBe(0);
 x.document.querySelector('[data-sem-c="button"]').dispatchEvent(new x.window.Event('click',{bubbles:true}));expect(replies).toBe(1);
 }finally{x.close();}
});
// Add to graph-ui.test.mjs (uses its existing real WAT/SPARQL fixture).
import {createCommandPalette} from '../../internal/server/web/semblance/command-palette.mjs';
it('stock palette filters graph options and delivers a raised Button effect exactly once',()=>{
 const x=fixture();let runs=0;const palette=createCommandPalette({ui:x.ui,onLease:id=>{expect(id).toBe('lease-one');runs++;}});
 try{
  palette.update({status:{host:'local-host',leases:[{id:'lease-one',request:{reason:'Unique compiler task'}},{id:'lease-two',request:{reason:'Different task'}}]}});
  palette.open();const input=x.document.querySelector('[data-sem-c="command-palette"] input[role="combobox"]');expect(input).toBeTruthy();
  input.value='Unique compiler';input.dispatchEvent(new x.window.Event('input',{bubbles:true}));
  expect(palette.element.value).toBe('Unique compiler');
  const options=[...x.document.querySelectorAll('[role="option"]')].filter(el=>!el.hidden&&!el.closest('[hidden]'));
  expect(options).toHaveLength(1);expect(options[0].textContent).toContain('Unique compiler task');
  options[0].dispatchEvent(new x.window.Event('click',{bubbles:true}));
  expect(runs).toBe(1);expect(palette.element.open).toBe(false);
  expect(x.store.match(null,x.store.factory.namedNode(ARM+'requested'))).toHaveLength(0);
  // An unrelated event must never replay the nested selection marker.
  const b=x.ui.button('Unrelated',()=>{});x.ui.bind('monitorContent').replace(b);
  x.document.querySelector('[data-sem-c="button"]').dispatchEvent(new x.window.Event('click',{bubbles:true}));expect(runs).toBe(1);
  palette.update({context:'remote',status:{leases:[{id:'lease-one'}]}});expect(palette.open()).toBe(false);
  expect(x.store.match(x.store.factory.namedNode(palette.element.iri+'/keyboard'),x.store.factory.namedNode('https://semblance.dev/ns/keyboard#shortcut'))).toHaveLength(0);
 }finally{palette.dispose();x.close();}
});
it('optional variants are absent and stock grid parameters remain valid graph data',()=>{
 const x=fixture();try{const b=x.ui.button('Inspect',()=>{},{tone:undefined,emphasis:undefined}),grid=x.ui.grid([b]);x.ui.bind('monitorContent').replace(grid);
 expect(x.document.querySelector('[data-sem-shape-invalid]')).toBeNull();expect(x.store.match(x.ui.nn(b.iri),x.ui.nn('https://semblance.dev/ns/style#tone'))).toHaveLength(0);expect(x.document.querySelector('[data-sem-c="grid"]').getAttribute('data-columns')).toBe('1');
 }finally{x.close();}
});
it('effect markers are consumed before native focus reentry and close is delivered once',()=>{
 const x=fixture();try{let runs=0,closes=0;const dialog=x.ui.dialog('Inspect exact resource',[x.ui.text('Read only')]);dialog.on('close',()=>{closes++;x.ui.handled({target:x.ui.nn(button.iri),type:'https://semblance.dev/ns/behaviour#Focus'});});
 const button=x.ui.button('Open inspector',()=>{runs++;expect(x.store.match(null,x.ui.nn(ARM+'requested'))).toHaveLength(0);x.ui.handled({target:x.ui.nn(button.iri),type:'https://semblance.dev/ns/behaviour#Focus'});dialog.showModal();});x.ui.bind('monitorContent').replace(button);
 x.document.querySelector('[data-sem-c="button"]').dispatchEvent(new x.window.Event('click',{bubbles:true}));expect(runs).toBe(1);
 x.document.querySelector('[aria-label="Close"]').dispatchEvent(new x.window.Event('click',{bubbles:true}));expect(closes).toBe(1);
 }finally{x.close();}
});
it('messages preserve exact instants in the viewer clock and evict retired author data',()=>{
 const x=fixture();try{const list=x.ui.messageList(),at='2026-10-26T20:00:00.123Z';x.ui.bind('monitorContent').replace(list);list.setMessages([{id:'old',author:'retired',at,body:x.ui.text('old')}]);
 const published=x.store.match(null,x.ui.nn('https://www.w3.org/ns/activitystreams#published'))[0].object.value;expect(Date.parse(published)).toBe(Date.parse(at));expect(published.slice(11,13)).toBe(String(new Date(at).getHours()).padStart(2,'0'));
 list.setMessages([{id:'new',author:'current',at,body:x.ui.text('new')}]);expect(x.store.match(x.ui.nn(list.iri+'/author/retired'))).toHaveLength(0);expect(x.document.body.textContent).not.toContain('old');
 }finally{x.close();}
});
it('palette keywords find full identifiers while primary labels stay concise and secondary help preserves exact IDs',()=>{
 const x=fixture(),id='0123456789abcdef0123456789abcdef';let selected='';const palette=createCommandPalette({ui:x.ui,onLease:value=>selected=value});try{
 palette.update({status:{host:'host',leases:[{id,request:{reason:'Short title'}}]}});palette.open();const input=x.document.querySelector('[role="combobox"]');input.value=id;input.dispatchEvent(new x.window.Event('input',{bubbles:true}));
 const options=[...x.document.querySelectorAll('[role="option"]')].filter(e=>!e.closest('[hidden]'));expect(options).toHaveLength(1);
 const primary=options[0].querySelector('.sem-ta-label'),secondary=options[0].querySelector('.sem-ta-help');
 expect(primary?.textContent).toContain('Short title');expect(primary?.textContent).not.toContain(id);expect(secondary?.textContent).toContain('ID '+id);
 options[0].dispatchEvent(new x.window.Event('click',{bubbles:true}));expect(selected).toBe(id);
 }finally{palette.dispose();x.close();}
});

it('closing nested detail stays inside the highest surviving hosted modal after its opener is replaced',()=>{
 for(const kind of ['dialog','drawer'])for(const close of ['button','escape']){
  const x=fixture();try{
   const page=x.ui.button('Page navigation',()=>lower.showModal(page));x.ui.bind('monitorContent').replace(page);
   const openManager=x.ui.button('Open pool management',()=>manager.showModal(openManager)),lower=x.ui.dialog('Older overlay',[openManager]);
   const opener=x.ui.button('Original consumer',()=>detail.showModal(opener)),manager=x.ui[kind]('Pool management',[opener]);
   const detail=x.ui.dialog('Lease detail',[x.ui.text('Original exact lease')]);
   const node=h=>x.renderer.nodeOf(x.projection.index.rootOf(h.iri)),click=h=>node(h).dispatchEvent(new x.window.Event('click',{bubbles:true}));
   click(page);expect(lower.open).toBe(true);click(openManager);expect(manager.open).toBe(true);click(opener);expect(detail.open).toBe(true);detail.returnTo=page;
   const managerNode=node(manager),panel=managerNode.querySelector('[role="dialog"]'),lowerPanel=node(lower).querySelector('[role="dialog"]');
   const replacement=x.ui.button('Current consumer',()=>{});manager.replace(replacement);
   expect(opener.disposed).toBe(true);expect(x.ui.handles.has(opener.iri)).toBe(false);
   const focus=[];panel.addEventListener('focus',()=>focus.push('manager'));lowerPanel.addEventListener('focus',()=>focus.push('older'));node(page).addEventListener('focus',()=>focus.push('page'));
   let closed=0;detail.on('close',()=>closed++);
   const closeButton=node(detail).querySelector('[aria-label="Close"]');
   if(close==='button')closeButton.dispatchEvent(new x.window.Event('click',{bubbles:true}));
   else closeButton.dispatchEvent(Object.assign(new x.window.Event('keydown',{bubbles:true,cancelable:true}),{key:'Escape',code:'Escape'}));
   expect(detail.open).toBe(false);expect(closed).toBe(1);expect(manager.open).toBe(true);expect(lower.open).toBe(true);
   expect(node(manager)).toBe(managerNode);expect(focus).toEqual(['manager']);expect(replacement.disposed).not.toBe(true);expect(opener.disposed).toBe(true);
  }finally{x.close();}
 }
});
it('closing the last hosted modal restores the live opener or the page reading fallback',()=>{
 for(const removed of [false,true]){
  const x=fixture();try{
   const opener=x.ui.button('Open detail',()=>{}),heading=x.ui.bind('heading');x.ui.bind('monitorContent').replace(opener);
   const detail=x.ui.dialog('Final detail',[x.ui.text('Read only')]),node=h=>x.renderer.nodeOf(x.projection.index.rootOf(h.iri));
   detail.showModal(opener);detail.returnTo=null;
   if(removed)x.ui.bind('monitorContent').replace(x.ui.text('Consumer removed'));
   const focus=[];node(heading).addEventListener('focus',()=>focus.push('heading'));if(!removed)node(opener).addEventListener('focus',()=>focus.push('opener'));
   node(detail).querySelector('[aria-label="Close"]').dispatchEvent(new x.window.Event('click',{bubbles:true}));
   expect(detail.open).toBe(false);expect(focus).toEqual([removed?'heading':'opener']);
  }finally{x.close();}
 }
});
it('an explicit pending close callback keeps its requested focus inside the surviving modal',()=>{
 const x=fixture();try{
  const opener=x.ui.button('Original action',()=>{}),chosen=x.ui.button('Explicit next action',()=>{}),manager=x.ui.dialog('Manager',[opener,chosen]),detail=x.ui.dialog('Detail',[x.ui.text('Read only')]);
  const node=h=>x.renderer.nodeOf(x.projection.index.rootOf(h.iri));manager.showModal();detail.showModal(opener);
  const focus=[];node(opener).addEventListener('focus',()=>focus.push('opener'));node(chosen).addEventListener('focus',()=>focus.push('chosen'));
  detail.on('close',()=>chosen.focus());x.ui.batch(()=>detail.close());
  expect(focus).toEqual(['chosen']);expect(detail.open).toBe(false);expect(manager.open).toBe(true);
 }finally{x.close();}
});
it('programmatic reading focus does not remove native controls from the Tab order',()=>{
 const x=fixture();try{const button=x.ui.button('Keep running',()=>{}),text=x.ui.stack([x.ui.text('Selected message')]);x.ui.bind('monitorContent').replace(button,text);button.focus();expect(x.store.match(x.ui.nn(button.iri),x.ui.nn(ARM+'focusable'))).toHaveLength(0);expect(x.document.querySelector('[data-sem-c="button"]').getAttribute('tabindex')).not.toBe('-1');button.hidden=true;button.focus();expect(x.store.match(x.ui.nn(button.iri),x.ui.nn(ARM+'focusable'))).toHaveLength(0);button.hidden=false;const blocks=[];x.renderer.nodeOf(x.projection.index.rootOf(text.iri)).scrollIntoView=options=>blocks.push(options.block);text.focus();expect(x.document.querySelector('[data-sem-c="stack"][tabindex="-1"]')).toBeTruthy();text.focus();expect(blocks).toEqual(['center','center']);
 }finally{x.close();}
});
it('a pointer-opened overlay remembers its invoking control even when the browser did not focus it',()=>{
 const x=fixture();try{const dialog=x.ui.drawer('Live activity',[x.ui.text('Latest events')]),button=x.ui.button('Live activity',()=>dialog.showModal());x.ui.bind('monitorContent').replace(button);
 x.renderer.nodeOf(x.projection.index.rootOf(button.iri)).dispatchEvent(new x.window.Event('click',{bubbles:true}));expect(dialog.open).toBe(true);expect(dialog.opener).toBe(button);
 let restored=0;button.focus=()=>restored++;x.document.querySelector('[aria-label="Close"]').dispatchEvent(new x.window.Event('click',{bubbles:true}));expect(dialog.open).toBe(false);expect(restored).toBe(1);
 const fallback=x.ui.button('Page navigation',()=>{});x.ui.bind('monitorContent').append(fallback);let fallbackFocus=0;fallback.focus=()=>fallbackFocus++;dialog.showModal(button);dialog.returnTo=fallback;button.hidden=true;x.document.querySelector('[aria-label="Close"]').dispatchEvent(new x.window.Event('click',{bubbles:true}));expect(restored).toBe(1);expect(fallbackFocus).toBe(1);
 }finally{x.close();}
});


import {createEphemeralInputs} from '../../internal/server/web/semblance/ephemeral-inputs.mjs';
it('real RDF/event bus excludes private native values from every graph and LDES through submit and destroy',async()=>{
 const widgets={},x=fixture({widgets}),islands=new Islands(x.bus,x.renderer,{widgets,onError:e=>{throw e}});islands.attach();
 const secrets=['review-password-<unique>','review-clipboard-<unique>','http://127.0.0.1/#connect='+'d'.repeat(64)];
 const privateFields=createEphemeralInputs([{name:'password',label:'Password',type:'password'},{name:'clipboard',label:'Clipboard',multiline:true},{name:'link',label:'One-use link',readOnly:true,initial:secrets[2]}]);
 const graphText=()=>JSON.stringify(x.store.match().map(q=>[q.subject.value,q.predicate.value,q.object.value,q.graph.value]));
 const assertPrivate=()=>{for(const value of secrets)expect(graphText()).not.toContain(value);};
 try{
  const island=x.ui.island('privacy-review',privateFields.widget);let submitted;
  const submit=x.ui.button('Submit private inputs',()=>{submitted=privateFields.take();});x.ui.bind('monitorContent').replace(island,submit);await islands.settled();expect(privateFields.mounted).toBe(true);assertPrivate();
  const before=graphText();
  for(const [i,name]of ['password','clipboard','link'].entries()){
   const input=x.document.querySelector(`[aria-label="${['Password','Clipboard','One-use link'][i]}"]`);expect(input).toBeTruthy();
   if(name!=='link')input.value=secrets[i];
   for(const type of ['input','change','focus','keydown','keyup'])input.dispatchEvent(Object.assign(new x.window.Event(type,{bubbles:true}),type.startsWith('key')?{key:'a',code:'KeyA'}:{}));
  }
  expect(graphText()).toBe(before);assertPrivate();
  x.renderer.nodeOf(x.projection.index.rootOf(submit.iri)).dispatchEvent(new x.window.Event('click',{bubbles:true}));
  expect(submitted).toEqual({password:secrets[0],clipboard:secrets[1],link:secrets[2]});expect(graphText()).not.toBe(before);assertPrivate();
  for(const input of x.document.querySelectorAll('.sem-island-widget input,.sem-island-widget textarea'))expect(input.value).toBe('');
  privateFields.write('password',secrets[0]);x.ui.bind('monitorContent').replace(x.ui.text('Closed'));expect(privateFields.mounted).toBe(false);assertPrivate();
 }finally{islands.dispose();x.close();}
});

it('missing private island ontology refuses clearly instead of inserting an unrenderable late class',()=>{
 const x=fixture({widgets:{}});try{
  const kind=x.ui.nn(ARM+'Island'),subclass=x.ui.nn('http://www.w3.org/2000/01/rdf-schema#subClassOf');
  x.runtime.apply({remove:x.store.match(kind,subclass),add:[]});
  expect(()=>x.ui.island('must-not-mount',{mount(){throw Error('Must not reach widget');}})).toThrow(/before constructing the runtime/);
 }finally{x.close();}
});
