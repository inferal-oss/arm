import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createNamingConsole} from './resource-names.mjs';
import {controlUI} from './control-test-ui.mjs';
import {loadedNameTargets,nameReference,sameNameBinding,validNameSnapshot} from '../resource-names.mjs';
import {paletteEntries} from './command-palette.mjs';

const host='a'.repeat(32),other='b'.repeat(32),lease='c'.repeat(32),portLease='d'.repeat(32),pool='pool_'+'e'.repeat(32);
const ref={kind:'lease',authority_host:host,id:lease,binding_lease:''};
const doc=(entries=[],revision=0)=>({version:1,owner:'501',host,revision,entries});
const entry=(reference=ref,name='build-cache',active=true)=>({kind:reference.kind,authority_host:reference.authority_host,id:reference.id,binding_lease:reference.binding_lease,name,active});
const memory=()=>{const data=new Map();return{getItem:k=>data.get(k)??null,setItem:(k,v)=>data.set(k,v),removeItem:k=>data.delete(k),data};};
function fixture(options={}) {
 const ui=controlUI();ui.dialog=(label,children,actions)=>{const d=ui.card(label,[...children,...actions]);d.open=false;d.showModal=()=>{d.open=true;};d.close=()=>{d.open=false;};return d;};
 let source=host,local=true,online=true,context={},targets=[{...ref,label:'Readable build'}];const calls=[],storage=memory();
 const console=createNamingConsole({ui,storage,source:()=>source,owner:()=> '501',context:()=>context,local:()=>local,connected:()=>online,targets:()=>targets,call:async(op,args)=>{calls.push([op,args]);return options.reply?options.reply(op,args):doc();},...options});
 return {ui,console,calls,storage,source:v=>{source=v;console.updateConnection();},context:v=>{context=v;console.updateConnection();},local:v=>{local=v;console.updateConnection();},targets:v=>{targets=v;console.updateConnection();},offline:()=>{online=false;console.updateConnection();}};
}

test('loaded name references normalize pools and derive reservation owner without relying on user order',()=>{
 const resources=[{kind:'custom',id:pool,host:'',custom:{pool:{id:pool,authority:host,name:'Slots'}}},{kind:'port',id:host+'/ports/native/tcp4/127.0.0.1:8080',host,users:[{lease,relationship:'activation'},{lease:portLease,relationship:'reservation'}]}];
 const loaded=loadedNameTargets({status:{host,leases:[{id:lease,host,display_name:'Build'}],resources},nodes:[{host:other,name:'Peer'}]});
 assert.ok(loaded.some(r=>r.kind==='pool'&&r.id===pool&&r.authority_host===host));assert.equal(loaded.find(r=>r.kind==='resource').binding_lease,portLease);assert.ok(loaded.some(r=>r.kind==='host'&&r.id===other));
 assert.equal(nameReference('resource',{kind:'port',id:resources[1].id,host,users:[{lease,relationship:'activation'}]},host),null);
 const conflict={...resources[1],users:[{lease,relationship:'reservation'}]};assert.ok(!loadedNameTargets({status:{host,resources:[resources[1],conflict]}}).some(r=>r.kind==='resource'));
 const changed=nameReference('resource',conflict,host),original=nameReference('resource',resources[1],host);assert.equal(changed.id,original.id);assert.equal(sameNameBinding(changed,original),false);
});

test('naming snapshot validation preserves int64 revisions and rejects ambiguous host names and malformed metadata',()=>{
 assert.ok(validNameSnapshot(doc([],9223372036854775807n),host));
 for(const value of [{...doc(),entries:null},{...doc(),host:other},{...doc(),naming_diagnostic:{code:'name_metadata_invalid'}},doc([entry()],0),doc([entry(),entry({...ref,id:portLease},'BUILD-CACHE')],1),doc([entry({kind:'host',authority_host:host,id:host,binding_lease:''}),entry({kind:'host',authority_host:other,id:other,binding_lease:''},'BUILD-CACHE')],1)])assert.equal(validNameSnapshot(value,host),false);
 assert.ok(validNameSnapshot(doc([entry(),entry({...ref,authority_host:other},'BUILD-CACHE')],1),host));
});

test('name form opens read-only and submits exact ID and CAS only on explicit Set',async()=>{
 const f=fixture({reply:(op,args)=>op==='name_list'?doc([],9007199254740993n):doc([entry(ref,args.name)],9007199254740994n)});
 await f.console.open();assert.deepEqual(f.calls,[['name_list',{all:true}]]);f.ui.get('Custom name').value='Build.Output';await f.ui.get('Set custom name').emit('activate');
 assert.deepEqual(f.calls[1],['name_mutate',{operation:'set',...ref,name:'Build.Output',expected_revision:9007199254740993n}]);assert.equal(f.console.pending,null);
});

test('clear releases the selected exact retained binding without needing a visible resource',async()=>{
 const old=entry(ref,'Old.Cache',false),f=fixture({reply:op=>op==='name_list'?doc([old],7):doc([],8)});f.targets([]);await f.console.open();assert.equal(f.ui.get('Set custom name').disabled,true);
 await f.ui.get('Clear exact reservation').emit('activate');assert.deepEqual(f.calls[1],['name_mutate',{operation:'clear',...ref,name:'Old.Cache',expected_revision:7}]);
});

test('local source labels for an already loaded peer use only the local mutation API',async()=>{
 const peer={kind:'host',authority_host:other,id:other,binding_lease:'',label:'Peer'},f=fixture({reply:(op,args)=>op==='name_list'?doc():doc([entry(peer,args.name)],1)});f.targets([peer]);await f.console.open();f.ui.get('Custom name').value='worker-two';await f.ui.get('Set custom name').emit('activate');
 assert.equal(f.calls[1][0],'name_mutate');assert.equal(f.calls[1][1].authority_host,other);assert.equal(f.calls[1][1].id,other);assert.ok(!f.calls.some(([op])=>op==='mesh_dispatch'));
});

test('remote context, source replacement and changed port incarnation prevent stale form submission',async()=>{
 const remote=fixture();remote.local(false);assert.equal(await remote.console.open(),false);assert.equal(remote.calls.length,0);
 const f=fixture();await f.console.open();f.source(other);f.ui.get('Custom name').value='stale-name';await f.ui.get('Set custom name').emit('activate');assert.equal(f.calls.length,1);
 const target={kind:'resource',authority_host:host,id:host+'/ports/native/tcp4/127.0.0.1:8080',binding_lease:portLease,label:'HTTP'},p=fixture();p.targets([target]);await p.console.open();p.targets([{...target,binding_lease:lease}]);assert.equal(p.ui.get('Set custom name').disabled,true);await p.ui.get('Set custom name').emit('activate');assert.equal(p.calls.length,1);
});

test('a lost naming response retains exact request and never retries after reread or reopen',async()=>{
 const f=fixture({reply:op=>{if(op==='name_list')return doc();throw Error('Connection lost');}});await f.console.open();f.ui.get('Custom name').value='unknown-cache';await f.ui.get('Set custom name').emit('activate');
 assert.equal(f.calls.filter(([op])=>op==='name_mutate').length,1);assert.equal(f.console.pending.id,lease);assert.equal(f.storage.data.size,1);assert.match(f.ui.get('Resource names').children[0].children.map(x=>x.label).join(' '),/responses may be unknown/);
 await f.ui.get('Read current names').emit('activate');await f.console.open();assert.equal(f.calls.filter(([op])=>op==='name_mutate').length,1);assert.equal(f.console.pending.name,'unknown-cache');
});

test('revision refusal requires explicit fresh read before another submission',async()=>{
 const f=fixture({reply:op=>{if(op==='name_list')return doc([],7);throw Object.assign(Error('name_revision_conflict'),{definitive:true});}});await f.console.open();f.ui.get('Custom name').value='new-cache';await f.ui.get('Set custom name').emit('activate');assert.equal(f.calls.length,2);assert.equal(f.ui.get('Set custom name').disabled,true);await f.ui.get('Set custom name').emit('activate');assert.equal(f.calls.length,2);await f.ui.get('Read current names').emit('activate');assert.equal(f.calls.length,3);
});

test('search indexes display names while retaining canonical callback identities',()=>{
 const values=paletteEntries({status:{host,leases:[{id:lease,host,display_name:'Build outputs',request:{reason:'Compile'}}],resources:[{id:host+'/cpu',host,kind:'cpu',display_name:'Compute farm'}]},nodes:[{host:other,display_name:'Worker two',name:'Original signed name'}],pools:[{pool:{id:pool,authority:host,name:'Original pool'},display_name:'Custom slots'}]});
 for(const [action,name,id]of [['lease','Build outputs',lease],['resource','Compute farm',host+'/cpu'],['node','Worker two',other],['pool','Custom slots',pool]]){const e=values.entries.find(e=>e.action===action);assert.ok(e.label.includes(name));assert.ok(e.keywords.includes(name));assert.equal(e.id,id);}
});

test('manual reference uses registered exact host, original CAS and only an explicit local set',async()=>{
 const peer={kind:'host',authority_host:other,id:other,binding_lease:'',label:'Visible peer'},f=fixture({reply:(op,args)=>op==='name_list'?doc([],7):doc([entry(args,args.name)],8)});f.targets([{...ref,label:'Build'},peer]);await f.console.open();
 const manual=f.ui.get('Use an exact reference from a registered node');manual.checked=true;await manual.emit('change');f.ui.get('Registered target host').value=other;f.ui.get('Reference kind').value='lease';f.ui.get('Exact resource ID').value=portLease;f.ui.get('Custom name').value='remote-build';assert.equal(f.calls.length,1);await f.ui.get('Exact resource ID').emit('input');await f.ui.get('Set custom name').emit('activate');
 assert.deepEqual(f.calls[1],['name_mutate',{operation:'set',kind:'lease',authority_host:other,id:portLease,binding_lease:'',name:'remote-build',expected_revision:7}]);assert.ok(!f.calls.some(([op])=>op==='mesh_dispatch'||op==='node_inspect'));
});

test('manual malformed or unregistered reference cannot submit and revoked scope remains a backend refusal',async()=>{
 const peer={kind:'host',authority_host:other,id:other,binding_lease:'',label:'Peer'},f=fixture({reply:op=>{if(op==='name_list')return doc([],3);throw Object.assign(Error('forbidden: observation was revoked'),{definitive:true});}});f.targets([peer]);await f.console.open();const manual=f.ui.get('Use an exact reference from a registered node');manual.checked=true;await manual.emit('change');f.ui.get('Registered target host').value='f'.repeat(32);f.ui.get('Exact resource ID').value=lease;f.ui.get('Custom name').value='peer-work';await f.ui.get('Exact resource ID').emit('input');await f.ui.get('Set custom name').emit('activate');assert.equal(f.calls.length,1);
 f.ui.get('Registered target host').value=other;f.ui.get('Exact resource ID').value='not an exact ID';await f.ui.get('Exact resource ID').emit('input');await f.ui.get('Set custom name').emit('activate');assert.equal(f.calls.length,1);
 f.ui.get('Exact resource ID').value=lease;await f.ui.get('Exact resource ID').emit('input');await f.ui.get('Set custom name').emit('activate');assert.equal(f.calls.length,2);assert.equal(f.ui.get('Set custom name').disabled,true);assert.equal(f.calls[1][0],'name_mutate');
});

test('manual port binding field is an optional assertion and never a claimed observation',async()=>{
 const f=fixture({reply:(op,args)=>op==='name_list'?doc([],5):doc([entry({...args,binding_lease:portLease},args.name)],6)});await f.console.open();const manual=f.ui.get('Use an exact reference from a registered node');manual.checked=true;await manual.emit('change');f.ui.get('Registered target host').value=host;f.ui.get('Reference kind').value='resource';f.ui.get('Exact resource ID').value=host+'/ports/native/tcp4/127.0.0.1:8080';f.ui.get('Custom name').value='preview-http';await f.ui.get('Exact resource ID').emit('input');await f.ui.get('Set custom name').emit('activate');assert.equal(f.calls[1][1].binding_lease,'');assert.deepEqual(Object.keys(f.calls[1][1]).sort(),['authority_host','binding_lease','expected_revision','id','kind','name','operation']);
});

test('same-host successor connection requires a new form snapshot',async()=>{
 const f=fixture();await f.console.open();f.context({replacement:true});f.ui.get('Custom name').value='stale-context';await f.ui.get('Set custom name').emit('activate');assert.equal(f.calls.length,1);await f.console.open();assert.equal(f.calls.length,2);assert.equal(f.calls[1][0],'name_list');
});

test('unknown history preserves multiple still-applicable source-owner intents until a newer CAS is observed',async()=>{
 let revision=0;const f=fixture({reply:op=>{if(op==='name_list')return doc([],revision);throw Error('Lost response');}});await f.console.open();f.ui.get('Custom name').value='first-cache';await f.ui.get('Set custom name').emit('activate');await f.ui.get('Read current names').emit('activate');f.ui.get('Custom name').value='second-cache';await f.ui.get('Set custom name').emit('activate');
 assert.equal(f.console.pendingRecords.length,2);assert.deepEqual(f.console.pendingRecords.map(record=>[record.source,record.owner,record.request.name]),[[host,'501','first-cache'],[host,'501','second-cache']]);
 await f.console.open();assert.equal(f.console.pendingRecords.length,2);revision=1;await f.ui.get('Read current names').emit('activate');assert.equal(f.console.pendingRecords.length,0);assert.equal(f.calls.filter(([op])=>op==='name_mutate').length,2);
});

test('wrong-owner snapshots and malformed successful mutation bodies cannot become known outcomes',async()=>{
 const wrong=fixture({reply:()=>({...doc(),owner:'502'})});await wrong.console.open();assert.equal(wrong.ui.get('Set custom name').disabled,true);assert.equal(wrong.calls.length,1);
 for(const result of [{...doc([entry()],1),owner:'502'}, {...doc([entry()],1),host:other},doc([],1),null]){
  const f=fixture({reply:op=>op==='name_list'?doc():result});await f.console.open();f.ui.get('Custom name').value='build-cache';await f.ui.get('Set custom name').emit('activate');assert.equal(f.console.pendingRecords.length,1);assert.equal(f.console.pendingRecords[0].source,host);assert.equal(f.console.pendingRecords[0].owner,'501');assert.equal(f.ui.get('Set custom name').disabled,true);
 }
});


test('unchanged exact naming read preserves dirty value and original CAS/write arguments',async()=>{
 const revision=9223372036854775806n,saved=[entry(ref,'saved-name'),entry(ref,'retained-name',false)];let reads=0;
 const f=fixture({reply:(op,args)=>op==='name_list'?doc(++reads===1?saved:[...saved].reverse(),revision):doc([entry(ref,args.name)],revision+1n)});
 await f.console.open();f.ui.get('Custom name').value='dirty-name';await f.ui.get('Read current names').emit('activate');assert.equal(f.ui.get('Custom name').value,'dirty-name');
 await f.ui.get('Set custom name').emit('activate');assert.deepEqual(f.calls.at(-1),['name_mutate',{operation:'set',...ref,name:'dirty-name',expected_revision:revision}]);assert.equal(f.console.pending,null);
});

test('pending read disables edits and applies the eligible current value rather than a captured draft',async()=>{
 for(const [before,atReply,expected] of [['saved-name','later-draft','later-draft'],['early-draft','later-draft','later-draft'],['early-draft','saved-name','saved-name']]){
  let resolve,reads=0;const saved=doc([entry(ref,'saved-name')],9007199254740993n),f=fixture({reply:()=>++reads===1?saved:new Promise(r=>resolve=r)});
  await f.console.open();const name=f.ui.get('Custom name');name.value=before;const pending=f.ui.get('Read current names').emit('activate');
  assert.equal(name.disabled,true);assert.equal(f.ui.get('Resource to name').disabled,true);assert.equal(f.ui.get('Close').disabled,true);
  // Native edits cannot occur while disabled; deliberately simulate an external
  // interleaving to prove the result does not restore a stale captured string.
  name.value=atReply;resolve(doc([entry(ref,'saved-name')],9007199254740993n));await pending;assert.equal(name.value,expected);assert.equal(name.disabled,false);assert.equal(f.calls.filter(([op])=>op==='name_mutate').length,0);
 }
});

test('changed revision or saved reservation resets selection without rebasing a dirty name',async()=>{
 for(const next of [doc([entry(ref,'saved-name')],9007199254740994n),doc([entry(ref,'changed-name')],9007199254740993n),doc([entry(ref,'saved-name'),entry(ref,'retained-name',false)],9007199254740993n),doc([],9007199254740993n)]){
  let reads=0;const f=fixture({reply:()=>++reads===1?doc([entry(ref,'saved-name')],9007199254740993n):next});await f.console.open();f.ui.get('Custom name').value='dirty-name';await f.ui.get('Read current names').emit('activate');
  assert.equal(f.ui.get('Custom name').value,next.entries.find(e=>e.active)?.name??'');assert.equal(f.calls.filter(([op])=>op==='name_mutate').length,0);
 }
});

test('changed selected canonical binding and removed loaded target cannot inherit a dirty name',async()=>{
 const second={...ref,id:portLease},saved=doc([entry(ref,'first-name'),entry(second,'second-name')],7),f=fixture({reply:()=>saved});f.targets([{...ref,label:'First'},{...second,label:'Second'}]);await f.console.open();f.ui.get('Custom name').value='first-draft';
 const selection=f.ui.get('Resource to name');selection.value=JSON.stringify([second.kind,second.authority_host,second.id,second.binding_lease]);await selection.emit('change');assert.equal(f.ui.get('Custom name').value,'second-name');
 f.ui.get('Custom name').value='second-draft';f.targets([{...ref,label:'First'}]);await f.ui.get('Read current names').emit('activate');assert.equal(f.ui.get('Custom name').value,'second-name');assert.equal(f.ui.get('Set custom name').disabled,true);
});

test('late naming read cannot transfer its draft across source owner context closure or disposal',async()=>{
 for(const change of ['source','owner','context','remote','closed','dispose']){
  let reads=0,resolve,currentOwner='501';const f=fixture({owner:()=>currentOwner,reply:()=>++reads===1?doc([entry(ref,'saved-name')],7):new Promise(r=>resolve=r)});await f.console.open();const name=f.ui.get('Custom name');name.value='early-draft';const pending=f.ui.get('Read current names').emit('activate');
  if(change==='source')f.source(other);else if(change==='owner')currentOwner='502';else if(change==='context')f.context({});else if(change==='remote')f.local(false);else if(change==='closed')f.console.dialog.close();else f.console.dispose();
  name.value='later-draft';resolve(doc([entry(ref,'saved-name')],7));await pending;
  assert.equal(name.value,change==='closed'?'saved-name':'later-draft');assert.equal(f.calls.filter(([op])=>op==='name_mutate').length,0);
 }
});

test('unknown naming intents and failed reads retain original protocol without preserving a draft over a fresh selection',async()=>{
 let fail=false;const f=fixture({reply:op=>{if(op==='name_mutate')throw Error('lost');if(fail)throw Error('read failed');return doc([entry(ref,'saved-name')],7);}});await f.console.open();f.ui.get('Custom name').value='unknown-name';await f.ui.get('Set custom name').emit('activate');const pending=f.console.pendingRecords;
 f.ui.get('Custom name').value='unrelated-draft';await f.ui.get('Read current names').emit('activate');assert.equal(f.ui.get('Custom name').value,'saved-name');assert.deepEqual(f.console.pendingRecords,pending);assert.equal(f.calls.filter(([op])=>op==='name_mutate').length,1);
 f.ui.get('Custom name').value='read-error-draft';fail=true;await f.ui.get('Read current names').emit('activate');assert.equal(f.ui.get('Custom name').value,'read-error-draft');assert.equal(f.ui.get('Set custom name').disabled,true);assert.deepEqual(f.console.pendingRecords,pending);
});
