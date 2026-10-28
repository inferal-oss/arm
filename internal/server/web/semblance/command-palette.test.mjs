import test from 'node:test';
import assert from 'node:assert/strict';
import {createCommandPalette,paletteEntries} from './command-palette.mjs';
const C='https://semblance.dev/ns/component#',KEY='https://semblance.dev/ns/keyboard#',ARM='https://arm.local/ui#';

// A host-effect fixture only. Stock graph filtering, ARIA, keyboard and nested
// reaction delivery require the composed RDF/browser integration gate.
function fixture(){let next=0;const props=new Map(),lists=new Map(),handles=new Map();
  class Handle{constructor(iri,kind,values={}){this.iri=iri;this.kind=kind;this.props={};this.callbacks={};this.set(values);handles.set(iri,this);}set(v){Object.assign(this.props,v);return this;}on(e,fn){this.callbacks[e]=fn;return this;}showModal(){this.props.open=true;}close(){this.props.open=false;}set value(v){this.props.value=v;}dispose(){this.disposed=true;handles.delete(this.iri);ui.desktopHosts.delete(this.iri);for(const key of props.keys())if(key.startsWith(this.iri+'|')||key.startsWith(this.iri+'/'))props.delete(key);}}
  const ui={batch:fn=>fn(),component:(kind,p)=>new Handle(ARM+'c'+(++next),kind,p),bind:(name,kind)=>handles.get(ARM+name)||new Handle(ARM+name,kind),put:(s,p,v)=>{const key=s+'|'+p;if(v===null)props.delete(key);else props.set(key,v);},list:(s,p,v)=>lists.set(s+'|'+p,v),desktopHosts:new Set(),syncDesktop(){}};
  return {ui,props,handles,entries:palette=>(lists.get(palette.element.iri+'|'+C+'entries')||[]).map(id=>handles.get(id))};
}

test('loaded inventory includes every category with concise titles and identity metadata',()=>{
 const data={status:{host:'host-a',resources:[{id:'r1',kind:'cpu'},{id:'r2',kind:'extension-kind'}],leases:[{id:'l1',owner:'501',owner_username:'Y',request:{reason:'Compile parser'}}]},nodes:[{host:'node-a',name:'Studio'}],pools:[{pool:{id:'p1',name:'GPU',description:'Shared accelerators'}}],messages:[{id:'m1',title:'Review',sender:'agent-a',body:'Parser complete'}]};
 const {entries}=paletteEntries(data);for(const category of ['Commands','Resources','Leases','Nodes','Pools','Messages'])assert.ok(entries.some(e=>e.section===category),category);
 assert.match(entries.find(e=>e.action==='lease').label,/Compile parser.*l1 @ host-a/);
 assert.match(entries.find(e=>e.action==='lease').help,/Owner Y/);assert.match(entries.find(e=>e.action==='lease').keywords,/l1\nhost-a[\s\S]*Y/);
 assert.match(entries.find(e=>e.action==='message').label,/agent-a.*Review.*m1 @ host-a/);
 assert.match(entries.find(e=>e.action==='message').keywords,/Parser complete/);assert.ok(!entries.find(e=>e.action==='message').help.includes('Parser complete'));
 assert.ok(entries.some(e=>e.label.includes('extension-kind')));
 assert.equal(new Set(entries.map(e=>e.key)).size,entries.length);
 assert.equal(paletteEntries({leases:[{id:'same',host:'a'},{id:'same',host:'b'}]}).entries.filter(e=>e.action==='lease').length,2);
});

test('records and label fields are bounded without dumping arbitrary secret metadata',()=>{
 const leases=Array.from({length:300},(_,i)=>({id:'l'+i,request:{reason:'x'.repeat(10000)},token:'DO-NOT-INDEX',credentials:{password:'HIDDEN'}}));
 const result=paletteEntries({leases});assert.equal(result.entries.filter(e=>e.action==='lease').length,256);assert.equal(result.omitted.Leases,44);
 assert.ok(result.entries.every(e=>e.label.length<=200&&e.help.length<=512));assert.ok(result.entries.every(e=>![e.label,e.help].join(' ').includes('DO-NOT-INDEX')&&![e.label,e.help].join(' ').includes('HIDDEN')));
 assert.equal(paletteEntries({messages:[{id:'x'.repeat(1025)}]}).entries.filter(e=>e.action==='message').length,0);
});

test('stock palette owns explicit entries and shortcut, never auto discovers application effects',()=>{
 const f=fixture(),palette=createCommandPalette({ui:f.ui});const iri=palette.element.iri;
 assert.equal(palette.element.kind,'CommandPalette');assert.ok(f.ui.desktopHosts.has(iri));
 assert.equal(f.props.get(iri+'|'+C+'lists'),iri+'/explicit');assert.equal(f.props.get(iri+'|'+C+'recentLimit'),0);
 assert.equal(f.props.get(iri+'/keyboard|'+KEY+'shortcut'),'mod+k');assert.equal(f.props.get(iri+'/keyboard|'+C+'opens'),iri);
 assert.ok(palette.open());assert.equal(palette.element.props.open,true);
 assert.ok(f.entries(palette).every(e=>e.kind==='Button'&&e.callbacks.activate));
});

test('selections dispatch only navigation callbacks using the latest matching identity',()=>{
 const f=fixture(),calls=[],palette=createCommandPalette({ui:f.ui,navigate:r=>calls.push(['route',r]),onLease:(id,l)=>calls.push(['lease',id,l.state]),onNode:n=>calls.push(['node',n.host]),onMessage:id=>calls.push(['message',id])});
 const data={status:{host:'home',resources:[{id:'cpu',kind:'cpu'}],leases:[{id:'lease',state:'waiting'}]},nodes:[{host:'peer'}],pools:[{pool:{id:'pool',name:'Tokens'}}],messages:[{id:'message',body:'Ready'}]};palette.update(data);
 const lease=f.entries(palette).find(e=>e.props.label.startsWith('Lease'));const iri=lease.iri;
 palette.update({...data,status:{...data.status,leases:[{id:'lease',state:'running'}]}});
 assert.equal(f.entries(palette).find(e=>e.props.label.startsWith('Lease')).iri,iri);lease.callbacks.activate();
 for(const prefix of ['Node','Message','Pool','Resource'])f.entries(palette).find(e=>e.props.label.startsWith(prefix)).callbacks.activate();
 assert.deepEqual(calls,[['lease','lease','running'],['node','peer'],['route',{view:'messages',lease:null}],['message','message'],['route',{view:'resources',pool:'pool',lease:null}],['route',{view:'resources',pool:null,lease:null}]]);
});

test('removed entries and retained callbacks cannot select stale or remote resources',()=>{
 const f=fixture();let selected=0;const palette=createCommandPalette({ui:f.ui,onLease:()=>selected++});palette.update({leases:[{id:'a'}]});
 const old=f.entries(palette).find(e=>e.props.label.startsWith('Lease'));palette.update({leases:[]});old.callbacks.activate();assert.equal(selected,0);assert.equal(old.disposed,true);
 palette.update({leases:[{id:'a'}]});const current=f.entries(palette).find(e=>e.props.label.startsWith('Lease'));palette.open();palette.update({context:'remote',leases:[{id:'a'}]});current.callbacks.activate();
 assert.equal(selected,0);assert.equal(palette.open(),false);assert.equal(palette.element.props.open,false);assert.equal(f.entries(palette).length,0);assert.equal(f.props.has(palette.element.iri+'/keyboard|'+KEY+'shortcut'),false);
 palette.update({leases:[{id:'a'}]});assert.equal(palette.open(),false);palette.update({context:'local',leases:[{id:'a'}]});assert.equal(palette.open(),true);
});

test('dispose releases overlays, metadata and entry handles and makes late callbacks inert',()=>{
 const f=fixture();let selected=0;const palette=createCommandPalette({ui:f.ui,onLease:()=>selected++});palette.update({leases:[{id:'a'}]});const old=f.entries(palette).find(e=>e.props.label.startsWith('Lease'));
 palette.dispose();palette.dispose();old.callbacks.activate();palette.update({leases:[{id:'b'}]});assert.equal(selected,0);assert.equal(f.handles.size,0);assert.equal(f.props.size,0);assert.equal(f.ui.desktopHosts.size,0);assert.equal(palette.open(),false);
});

test('full whitelisted metadata enters stock keywords without lengthening presentation and follows tail-only changes',()=>{
 const f=fixture(),palette=createCommandPalette({ui:f.ui});const body='a'.repeat(16370)+'unique-tail';
 const message={id:'complete-message-id',host:'full-host-identity',sender:'agent',body};palette.update({messages:[message]});
 const entry=f.entries(palette).find(e=>e.props.label.startsWith('Message'));const displayed={...entry.props};
 assert.ok(entry.props.label.length<200);assert.ok(entry.props.help.length<=512);
 const property=entry.iri+'|'+C+'keywords';assert.ok(f.props.get(property).includes(body));assert.ok(f.props.get(property).includes('complete-message-id'));
 palette.update({messages:[{...message,body:body.replace('unique-tail','changed-tail')}]});
 assert.deepEqual(entry.props,displayed);assert.ok(f.props.get(property).includes('changed-tail'));assert.ok(!f.props.get(property).includes('unique-tail'));
 const descriptors=paletteEntries({leases:[{id:'l',owner:'complete-owner',token:'PRIVATE',request:{reason:'Compile'}}]}).entries;
 assert.match(descriptors.find(e=>e.action==='lease').keywords,/complete-owner/);assert.ok(!JSON.stringify(descriptors.map(e=>e.keywords)).includes('PRIVATE'));
});

test('Kubernetes results retain exact context, CA, namespace and Node UID without selecting or mutating',()=>{
 const f=fixture();let browses=0,selected=null;const p=createCommandPalette({ui:f.ui,onBrowseKubernetes:()=>browses++,onKubernetesNode:value=>selected=value});
 const snapshot={context:'inferal',cluster:{server:'https://cluster.invalid',ca_sha256:'a'.repeat(64)},namespace:{name:'default',uid:'namespace-a'},nodes:[{uid:'node-uid',name:'worker',os:'linux',arch:'arm64',ready:true}]};
 p.update({kubernetes:snapshot});let entries=f.entries(p);const browse=entries.find(h=>h.props.label==='Browse Kubernetes nodes');browse.callbacks.activate();assert.equal(browses,1);
 const entry=entries.find(h=>h.props.label.startsWith('Kubernetes'));entry.callbacks.activate();assert.equal(selected.uid,'node-uid');assert.equal(selected.namespace.uid,'namespace-a');assert.equal(selected.ca_sha256,'a'.repeat(64));
 const old=entry.callbacks.activate,firstKey=entry.iri;selected=null;p.update({kubernetes:{...snapshot,namespace:{name:'default',uid:'namespace-b'}}});assert.notEqual(f.entries(p).find(h=>h.props.label.startsWith('Kubernetes')).iri,firstKey);old();assert.equal(selected,null);
 p.update({kubernetes:null});assert.ok(!f.entries(p).some(h=>h.props.label.startsWith('Kubernetes')));p.dispose();
});


test('server names are primary, missing optional names use supplied fallbacks and exact identities remain searchable/actionable',()=>{
 const data={status:{host:'local-id',display_name:'local-words',resources:[{id:'resource-id',host:'local-id',display_name:'resource-words',host_display_name:'local-words',kind:'cpu'}],leases:[{id:'lease-id',host:'remote-id',host_display_name:'remote-words',display_name:'lease-words',request:{reason:'Build'}}]},nodes:[{host:'peer-id',display_name:'peer-words'},{host:'supplied-id',name:'Chosen node'}],pools:[{display_name:'manual-pool',pool:{id:'pool-id',name:'Chosen permits'}}]};
 const entries=paletteEntries(data).entries;
 for(const [id,words] of [['resource-id','resource-words'],['lease-id','lease-words'],['peer-id','peer-words'],['supplied-id','Chosen node'],['pool-id','manual-pool']]){
  const entry=entries.find(e=>e.id===id);assert.ok(entry.label.includes(words));assert.ok(entry.keywords.includes(id));assert.ok(entry.help.includes(id));assert.equal(entry.id,id);
 }
 assert.ok(entries.find(e=>e.id==='resource-id').label.includes('local-words'));
 const leaseEntry=entries.find(e=>e.id==='lease-id');assert.ok(leaseEntry.label.includes('remote-words'));assert.ok(leaseEntry.keywords.includes('remote-words'));assert.ok(leaseEntry.help.includes('remote-id'));assert.equal(leaseEntry.host,'remote-id');
 const f=fixture(),selected=[];const palette=createCommandPalette({ui:f.ui,onLease:id=>selected.push(id)});palette.update(data);
 f.entries(palette).find(e=>e.props.label.includes('lease-words')).callbacks.activate();assert.deepEqual(selected,['lease-id']);palette.dispose();
});
