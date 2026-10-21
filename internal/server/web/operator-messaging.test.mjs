import {test} from 'node:test';
import assert from 'node:assert/strict';
import {prepareMessage,sameTargets,createMessageOperations,deliveryDescription,createOperatorMessaging,operatorReplyTarget} from './operator-messaging.mjs';
const a={id:'agent-a',adapter:'codex',session:'dedicated-session',cwd:'/work/<literal>',registration:'rev-a',delivery:'poll'};
const b={...a,id:'agent-b',adapter:'claude',session:'other',registration:'rev-b',idle:true};
const draft={audience:a.id,body:'<script>literal</script>',lease:'lease',reply_to:'inbox-id'};
const request=()=>prepareMessage(draft,[a,b],'exact-key');
function storage(){let value=null;return {getItem:()=>value,setItem:(_,v)=>{value=v;}};}
const result=r=>({id:'message-id',idempotency_key:r.idempotency_key,audience:r.audience,body:r.body,lease:r.lease,reply_to:r.reply_to,deliveries:{}});

test('review freezes full recipient settings and UTF-8 budget without caller mutation',()=>{
 const r=request();assert.deepEqual(r.targets,[a]);assert.equal(r.reply_to,'inbox-id');
 r.targets[0].cwd='changed';assert.equal(a.cwd,'/work/<literal>');
 const broadcast=prepareMessage({audience:'broadcast',body:'Review all'},[b,a],'broadcast-key');assert.deepEqual(broadcast.targets.map(c=>c.id),[a.id,b.id]);
 for(const field of ['adapter','session','cwd','config_dir','remote','idle','delivery','registration'])assert.equal(sameTargets(request(),[{...a,[field]:field==='idle'?true:'changed'},b]),false,field);
 assert.equal(sameTargets(broadcast,[a]),false);assert.equal(sameTargets(request(),[b,a]),true);
 for(const body of ['','  ','😀'.repeat(4097)])assert.throws(()=>prepareMessage({...draft,body},[a],'key'));
 assert.doesNotThrow(()=>prepareMessage({...draft,body:'😀'.repeat(4096)},[a],'key'));
 assert.throws(()=>prepareMessage({...draft,audience:'operator'},[a],'key'));
});

test('review creation and restoration never deliver; exact confirmation submits only once',async()=>{
 const calls=[],s=storage();const ops=createMessageOperations({storage:s,call:async(op,r)=>{calls.push([op,r]);return result(r);}});
 const r=request();assert.equal(calls.length,0);const op=await ops.submit(r);assert.equal(op.state,'recorded');assert.deepEqual(calls,[['message_send',r]]);
 await assert.rejects(ops.submit(r),/already/);await ops.retry(op);assert.equal(calls.length,1);
 const restored=createMessageOperations({storage:s,call:()=>assert.fail('restore invoked native')});assert.equal(restored.operations.length,0);
});

test('timeout and reload preserve exact request/key; lookup is read-only; retry never creates a new key',async()=>{
 const calls=[],s=storage();const ops=createMessageOperations({storage:s,call:async(op,r)=>{calls.push([op,r]);throw Error('Disconnected after send');}});
 const submitted=request(),op=await ops.submit(submitted);assert.equal(op.state,'unknown');submitted.body='caller edited';assert.equal(op.request.body,draft.body);
 const recoveryCalls=[];const restored=createMessageOperations({storage:s,call:async(op,r)=>{recoveryCalls.push([op,r]);if(op==='message_lookup')return {found:false};return result(r);}});
 assert.equal(recoveryCalls.length,0);const recovered=restored.operations[0];await restored.lookup(recovered);assert.deepEqual(recoveryCalls,[['message_lookup',{idempotency_key:'exact-key'}]]);assert.equal(recovered.state,'unknown');assert.match(recovered.error,/may still commit/);
 await restored.retry(recovered);assert.equal(recovered.state,'recorded');assert.deepEqual(recoveryCalls[1],['message_send',request()]);
});

test('only known precreation refusal is rejected; server internal error and conflicts retain key',async()=>{
 for(const code of ['contact_changed','invalid_request','backend_stopping','internal_error','idempotency_conflict']){
  const s=storage(),ops=createMessageOperations({storage:s,call:async()=>{throw Object.assign(Error(code),{code,definitive:true});}});const op=await ops.submit(request());assert.equal(op.state,['internal_error','idempotency_conflict','backend_stopping'].includes(code)?'unknown':'rejected');
  const restored=createMessageOperations({storage:s,call:()=>assert.fail('implicit retry')});assert.equal(restored.operations.length,op.state==='unknown'?1:0);
 }
});

test('lookup and shared refresh resolve a saved operation without redelivery and expose receipts',async()=>{
 const r=request(),calls=[],msg=result(r);msg.deliveries[a.id]={state:'unknown',receipt:{received_at:'now',acknowledged_at:'later'}};
 const ops=createMessageOperations({storage:storage(),call:async(op)=>{calls.push(op);if(op==='message_lookup')return {found:true,message:msg};throw Error('Timeout');}});
 const op=await ops.submit(r);await ops.lookup(op);assert.equal(op.state,'recorded');assert.equal(op.message,msg);assert.deepEqual(calls,['message_send','message_lookup']);assert.match(deliveryDescription(msg.deliveries[a.id]),/unknown.*acknowledged/);
 const second=createMessageOperations({storage:storage(),call:async()=>{throw Error('Timeout');}});const pending=await second.submit(r);second.observe([msg]);assert.equal(pending.state,'recorded');
});

test('mismatched stored result cannot silently replace an uncertain operation; storage failure is visible',async()=>{
 const s={getItem:()=>null,setItem:()=>{throw Error('Quota');}};const ops=createMessageOperations({storage:s,call:async(op)=>op==='message_lookup'?{found:true,message:{...result(request()),body:'different'}}:Promise.reject(Error('timeout'))});const op=await ops.submit(request());await ops.lookup(op);assert.equal(op.state,'unknown');assert.match(op.error,/does not match/);assert.match(ops.warning,/cannot save/);
});

test('eight unresolved sends are retained; a ninth cannot discard older uncertain requests',async()=>{
 const s=storage(),ops=createMessageOperations({storage:s,call:async()=>{throw Error('Timeout');}});
 for(let i=0;i<8;i++)await ops.submit({...request(),idempotency_key:'key-'+i});
 await assert.rejects(ops.submit({...request(),idempotency_key:'ninth'}),/eight/);
 const restored=createMessageOperations({storage:s,call:()=>assert.fail('implicit send')});assert.equal(restored.operations.length,8);assert.equal(restored.operations[0].request.idempotency_key,'key-0');
});

class Element {
 constructor(tag,doc){this.tagName=tag;this.ownerDocument=doc;this.children=[];this.parentNode=null;this.listeners={};this.attributes={};this._text='';this.value='';this.hidden=false;this.disabled=false;}
 append(...nodes){for(const node of nodes){node.remove();node.parentNode=this;this.children.push(node);}}
 remove(){if(this.parentNode){this.parentNode.children=this.parentNode.children.filter(n=>n!==this);this.parentNode=null;}}
 replaceChildren(...nodes){for(const old of this.children)old.parentNode=null;this.children=[];this._text='';this.append(...nodes);}
 set textContent(text){this.replaceChildren();this._text=String(text);}
 get textContent(){return this._text+this.children.map(n=>n.textContent).join('');}
 setAttribute(k,v){this.attributes[k]=String(v);}
 addEventListener(k,f){(this.listeners[k]||=[]).push(f);}
 async fire(k,event={preventDefault(){}}){for(const f of this.listeners[k]||[])await f(event);}
 all(){return this.children.flatMap(n=>[n,...n.all()]);}
 contains(n){return this===n||this.all().includes(n);}
 querySelectorAll(selector){return this.all().filter(n=>selector.startsWith('.')?n.className?.split(' ').includes(selector.slice(1)):n.tagName===selector);}
 querySelector(selector){return this.querySelectorAll(selector)[0]||null;}
 focus(){this.ownerDocument.activeElement=this;}
 scrollIntoView(){}
}
function uiFixture(){
 const doc={activeElement:null,createElement:tag=>new Element(tag,doc)},root=doc.createElement('section'),calls=[];let live=true;
 const ui=createOperatorMessaging({root,document:doc,storage:storage(),key:()=> 'ui-key',connected:()=>live,onLease(){},call:async(op,r)=>{calls.push([op,r]);return result(r);}});
 const update=(contacts=[a,b],messages=[])=>ui.update({contacts,messages,leases:[]});update();
 const find=label=>root.all().find(n=>n.tagName==='button'&&n.textContent===label);
 return {ui,root,doc,calls,update,find,setLive(value){live=value;update();},recipient:root.all().find(n=>n.id==='message-recipient'),body:root.all().find(n=>n.id==='message-body'),form:root.querySelector('form')};
}

test('rendered review decline, stale membership and disconnected confirmation never submit',async()=>{
 const f=uiFixture();f.recipient.value=a.id;f.body.value=draft.body;await f.form.fire('submit');assert.equal(f.calls.length,0);
 await f.find('Back to editing').fire('click');assert.equal(f.body.value,draft.body);assert.equal(f.calls.length,0);
 await f.form.fire('submit');const back=f.find('Back to editing'),confirm=f.find('Confirm send');back.focus();
 f.update([{...a,session:'changed'},b]);assert.equal(f.find('Confirm send'),confirm);assert.equal(f.doc.activeElement,back);assert.equal(confirm.disabled,true);assert.match(f.root.textContent,/settings or broadcast membership changed/);
 await confirm.fire('click');assert.equal(f.calls.length,0);
 await back.fire('click');await f.form.fire('submit');f.setLive(false);assert.equal(f.find('Confirm send').disabled,true);await f.find('Confirm send').fire('click');assert.equal(f.calls.length,0);
 f.setLive(true);assert.equal(f.calls.length,0);
});

test('composer confirmation pins displayed targets and refresh preserves draft, focus, reply links and literal text',async()=>{
 const f=uiFixture();f.recipient.value='broadcast';f.body.value=draft.body;await f.form.fire('submit');assert.match(f.root.textContent,/Review broadcast · 2 recipients/);
 await f.find('Confirm broadcast').fire('click');assert.equal(f.calls.length,1);assert.deepEqual(f.calls[0][1].targets,[a,b]);assert.equal(f.root.all().some(n=>n.tagName==='script'),false);
 const msg={id:'incoming',sender:a.id,audience:'operator',body:'Agent asks a question',created_at:'2026-10-21T10:00:00Z',deliveries:{}};
 f.update([a,b],[msg]);await f.find('Reply').fire('click');assert.equal(f.recipient.value,a.id);assert.equal(f.doc.activeElement,f.body);f.body.value='Draft reply';
 f.update([a,b],[{...msg,body:'Updated incoming record'}]);assert.equal(f.body.value,'Draft reply');assert.equal(f.doc.activeElement,f.body);assert.match(f.root.textContent,/Reply to agent-a · incoming/);
});

test('restored uncertain operations are visible before connection or first backend snapshot',async()=>{
 const saved=storage(),pending=createMessageOperations({storage:saved,call:async()=>{throw Error('Disconnected');}});await pending.submit(request());
 const doc={activeElement:null,createElement:tag=>new Element(tag,doc)},root=doc.createElement('section');
 createOperatorMessaging({root,document:doc,storage:saved,connected:()=>false,onLease(){},call:()=>assert.fail('restoration retried')});
 assert.match(root.textContent,/Outcome uncertain/);assert.match(root.textContent,/exact-key/);assert.equal(root.all().find(n=>n.tagName==='button'&&n.textContent==='Check stored result').disabled,true);
});

test('all eight maximum-size uncertain request envelopes survive reload including wrapper overhead',async()=>{
 const s=storage(),ops=createMessageOperations({storage:s,call:async()=>{throw Error('Timeout');}}),expected=[];
 for(let i=0;i<8;i++){const r={...request(),idempotency_key:'max-key-'+i};r.targets[0].cwd='x'.repeat(262144-JSON.stringify(r).length+r.targets[0].cwd.length);assert.equal(JSON.stringify(r).length,262144);expected.push(r);await ops.submit(r);}
 assert.ok(s.getItem().length>2097152,'regression must cross old raw read boundary');
 const restored=createMessageOperations({storage:s,call:()=>assert.fail('restore sent')});assert.deepEqual(restored.operations.map(op=>op.request),expected);assert.equal(restored.warning,'');
});

test('oversized saved data warns explicitly and oversized new requests cannot silently replace retained work',async()=>{
 const oversized={getItem:()=> 'x'.repeat(8*(262144+32)+3),setItem(){}};
 const restored=createMessageOperations({storage:oversized,call:()=>assert.fail('oversized restore sent')});assert.match(restored.warning,/could not be read/);
 const s=storage(),ops=createMessageOperations({storage:s,call:async()=>{throw Error('Timeout');}});await ops.submit(request());const before=s.getItem();
 await assert.rejects(ops.submit({...request(),idempotency_key:'too-large',targets:[{...a,cwd:'x'.repeat(262145)}]}),/too large/);assert.equal(s.getItem(),before);assert.equal(ops.operations.length,1);
});

test('a rejected exact retry cannot erase an earlier uncertain request that might still complete',async()=>{
 const s=storage();let attempts=0;
 const ops=createMessageOperations({storage:s,call:async()=>{if(!attempts++)throw Error('Original send disconnected');throw Object.assign(Error('Recipients changed'),{code:'contact_changed'});}});
 const op=await ops.submit(request());await ops.retry(op);assert.equal(op.state,'unknown');assert.match(op.error,/prior outcome remains uncertain/);
 const restored=createMessageOperations({storage:s,call:()=>assert.fail('implicit retry')});assert.deepEqual(restored.operations[0].request,request());
});


test('operator reply uses the agent sender snapshot and refuses a replacement before review',async()=>{
 const source={...a},msg={id:'agent-question',sender:'agent:'+a.id,sender_contact:source,audience:'operator',body:'Please clarify',deliveries:{}};
 assert.equal(operatorReplyTarget(msg,[a,b]),a);
 assert.equal(operatorReplyTarget(msg,[{...a,registration:'replacement'},b]),null);
 assert.equal(operatorReplyTarget({...msg,audience:b.id},[a,b]),null);
 const f=uiFixture();f.update([a,b],[msg]);await f.find('Reply').fire('click');assert.equal(f.recipient.value,a.id);f.body.value='Operator answer';
 f.update([{...a,registration:'replacement'},b],[msg]);await f.form.fire('submit');assert.equal(f.calls.length,0);assert.match(f.root.textContent,/original sender changed/);assert.equal(f.find('Confirm send'),undefined);
 f.update([a,b],[msg]);await f.form.fire('submit');await f.find('Confirm send').fire('click');assert.equal(f.calls.length,1);assert.deepEqual(f.calls[0][1].targets,[a]);assert.equal(f.calls[0][1].reply_to,msg.id);
});
