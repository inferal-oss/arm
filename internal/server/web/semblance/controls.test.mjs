import test from 'node:test';
import assert from 'node:assert/strict';
import {controlUI} from './control-test-ui.mjs';
import {createLeaseActions,createLeaseReviews} from './lease-controls.mjs';
import {createOperatorMessaging} from './messages.mjs';
const now=()=>Date.parse('2026-10-26T12:00:00Z');
const lease={id:'a'.repeat(32),revision:7,state:'active',expires_at:'2026-10-26T12:05:00Z',request:{reason:'Test compile',agent:'agent-one'}};
const contact={id:'agent-one',adapter:'poll',session:'s1',registration:'r1',delivery:'poll',cwd:'/fixture'};
const flush=()=>new Promise(resolve=>setImmediate(resolve));
const memory=()=>{let raw;return {getItem:()=>raw??null,setItem:(_,value)=>{raw=value;}};};

function actions(t,options={}){const ui=controlUI(),calls=[],pending=[];let online=true;const panel=createLeaseActions({ui,root:ui.stack(),lease,leases:[],now,connected:()=>online,call:async(op,args)=>{calls.push([op,args]);return {...lease,state:'terminal',outcome:'released'};},onPending:value=>pending.push(value),...options});t.after(()=>panel.dispose());return{ui,panel,calls,pending,offline(){online=false;panel.updateConnection();}};}

test('lease review has no effect until exact confirmation and declining is inert',async t=>{
  const f=actions(t);await f.ui.get('Release').emit('activate');assert.equal(f.calls.length,0);await f.ui.get('Keep lease').emit('activate');assert.equal(f.calls.length,0);await f.ui.get('Release').emit('activate');await f.ui.get('Confirm release').emit('activate');
  assert.deepEqual(f.calls,[['lease_action',{id:lease.id,revision:7,action:'release',consumers:[]}]]);assert.deepEqual(f.pending,[true,false]);
});
test('lease disconnect blocks an already reviewed confirmation',async t=>{const f=actions(t);await f.ui.get('Release').emit('activate');f.offline();assert.equal(f.ui.get('Confirm release').disabled,true);await f.ui.get('Confirm release').emit('activate');assert.equal(f.calls.length,0);});
test('lease unknown outcome is never replayed and controls stay finished',async t=>{let calls=0;const f=actions(t,{call:async()=>{calls++;throw Error('Lost reply');}});await f.ui.get('Cancel').emit('activate');await f.ui.get('Confirm cancellation').emit('activate');f.panel.updateConnection();await f.ui.get('Cancel').emit('activate');assert.equal(calls,1);assert.match(f.ui.labels(),/outcome is unknown/);assert.equal(f.ui.get('Cancel').disabled,true);});
test('lease extension revalidates exact duration at submission',async t=>{const f=actions(t);await f.ui.get('Extend').emit('activate');f.ui.get('New duration from now').value='1s';await f.ui.get('New duration from now').emit('input');assert.equal(f.ui.get('Confirm extension').disabled,true);assert.equal(f.calls.length,0);f.ui.get('New duration from now').value='10m';await f.ui.get('New duration from now').emit('input');await f.ui.get('Confirm extension').emit('activate');assert.equal(f.calls[0][1].ttl_ns,600e9);assert.equal(f.calls[0][1].revision,7);});

test('check-in settings retain lease revision and policy generation, with explicit save',async()=>{
  const ui=controlUI(),calls=[],policy={lease:lease.id,policy_generation:4,enabled:false,interval_ns:600e9,contention_interval_ns:0,reply_timeout_ns:60e9};
  const panel=createLeaseReviews({ui,root:ui.stack(),lease,supported:true,connected:()=>true,call:async(op,args)=>{calls.push([op,args]);return op==='lease_review_policy'?policy:op==='lease_review_list'?{cursor:0,items:[],more:false}:{};}});
  await panel.ready;await ui.get('Configure check-ins').emit('activate');assert.equal(calls.filter(([op])=>op==='lease_review_configure').length,0);ui.get('Enable periodic check-ins').checked=true;await ui.get('Enable periodic check-ins').emit('change');await ui.get('Save reviewed settings').emit('activate');
  assert.deepEqual(calls.at(-1),['lease_review_configure',{lease:lease.id,lease_revision:7,policy_generation:4,enabled:true,interval_ns:600e9,reply_timeout_ns:60e9,contention_interval_ns:0}]);panel.dispose();
});
test('unsupported check-ins never read or mutate backend',async()=>{const ui=controlUI();let calls=0;const p=createLeaseReviews({ui,root:ui.stack(),lease,supported:false,connected:()=>true,call:()=>{calls++;}});await p.ready;assert.equal(calls,0);assert.equal(ui.get('Configure check-ins').disabled,true);p.dispose();});
test('disposed check-in load cannot enable a policy or configuration',async()=>{const ui=controlUI();let resolve;const p=createLeaseReviews({ui,root:ui.stack(),lease,supported:true,connected:()=>true,call:()=>new Promise(done=>{resolve=done;})});p.dispose();resolve({lease:lease.id,enabled:true,policy_generation:1});await p.ready;assert.equal(ui.get('Configure check-ins').disabled,true);});

function messaging(options={}){const ui=controlUI(),calls=[];let online=true;const panel=createOperatorMessaging({ui,root:ui.stack(),storage:memory(),key:()=> 'message-key',connected:()=>online,call:async(op,args)=>{calls.push([op,args]);return {id:'m1',...args};},...options});panel.update({contacts:[contact],messages:[],leases:[lease]});return {ui,calls,panel,offline(){online=false;panel.updateConnection();}};}
async function reviewMessage(f,audience='agent-one'){f.ui.get('Recipient').value=audience;f.ui.get('Message').value='Please report progress';await f.ui.get('Review message').emit('activate');}
test('message send requires exact recipient review and sends the reviewed key',async()=>{const f=messaging();await reviewMessage(f);assert.equal(f.calls.length,0);await f.ui.get('Confirm send').emit('activate');assert.equal(f.calls.length,1);assert.equal(f.calls[0][0],'message_send');assert.equal(f.calls[0][1].idempotency_key,'message-key');assert.deepEqual(f.calls[0][1].targets,[contact]);f.panel.dispose();});
test('changing exact registration invalidates message confirmation',async()=>{const f=messaging();await reviewMessage(f);f.panel.update({contacts:[{...contact,registration:'replacement'}],messages:[],leases:[lease]});assert.equal(f.ui.get('Confirm send').disabled,true);await f.ui.get('Confirm send').emit('activate');assert.equal(f.calls.length,0);f.panel.dispose();});
test('disconnect blocks a reviewed message without dropping draft',async()=>{const f=messaging();await reviewMessage(f);f.offline();assert.equal(f.ui.get('Confirm send').disabled,true);await f.ui.get('Confirm send').emit('activate');assert.equal(f.calls.length,0);f.panel.dispose();});
test('unknown message only retries exact saved request on explicit action',async()=>{let sends=0;const seen=[];const f=messaging({call:async(op,args)=>{seen.push([op,args]);if(op==='message_send'){sends++;throw Error('Transport lost');}return {found:false};}});await reviewMessage(f);await f.ui.get('Confirm send').emit('activate');assert.equal(sends,1);f.panel.update({contacts:[contact],messages:[],leases:[lease]});assert.equal(sends,1);await f.ui.get('Check stored result').emit('activate');assert.equal(sends,1);await f.ui.get('Retry exact request').emit('activate');assert.equal(sends,2);assert.deepEqual(seen[0][1],seen.at(-1)[1]);f.panel.dispose();});
test('restored message request is lookup-only until explicit retry',async()=>{const storage=memory();const f=messaging({storage,call:async()=>{throw Error('Lost');}});await reviewMessage(f);await f.ui.get('Confirm send').emit('activate');f.panel.dispose();const restored=messaging({storage});await flush();assert.equal(restored.calls.length,0);assert.match(restored.ui.labels(),/Restored after reload/);restored.panel.dispose();});
test('stale sender registration cannot create a reply to replacement',async()=>{const f=messaging();f.panel.update({contacts:[{...contact,registration:'new'}],messages:[{id:'incoming',sender:contact.id,sender_contact:contact,audience:'operator',body:'hello',created_at:'2026-10-26T12:00:00Z'}],leases:[]});assert.equal(f.ui.get('Reply'),undefined);assert.match(f.ui.labels(),/original sender is no longer registered/);f.panel.dispose();});

test('lease destructive confirmation carries the exact attached-consumer revisions',async t=>{
  const consumer={id:'consumer',revision:9,state:'active',request:{storage_lease:lease.id}},f=actions(t,{lease:{...lease,request:{kind:'storage',reason:'Test storage'},artifact:{path:'/fixture/owned'}},leases:[consumer]});
  await f.ui.get('Release').emit('activate');assert.match(f.ui.labels(),/\/fixture\/owned/);await f.ui.get('Confirm release').emit('activate');assert.deepEqual(f.calls[0][1].consumers,[{id:'consumer',revision:9}]);
});
test('an action settling after detail disposal clears pending and still reports its result',async t=>{
  let done,result;const f=actions(t,{call:()=>new Promise(resolve=>{done=resolve;}),onResult:value=>{result=value;}});
  await f.ui.get('Release').emit('activate');const sending=f.ui.get('Confirm release').emit('activate');f.panel.dispose();done({...lease,state:'ending'});await sending;assert.equal(result.state,'ending');assert.deepEqual(f.pending,[true,false]);
});
test('check-in unknown save outcome disables stale settings and never resends',async()=>{
  const ui=controlUI(),policy={lease:lease.id,policy_generation:3,enabled:false,contention_interval_ns:0};let saves=0;
  const p=createLeaseReviews({ui,root:ui.stack(),lease,supported:true,connected:()=>true,call:async(op)=>{if(op==='lease_review_policy')return policy;if(op==='lease_review_list')return {cursor:0,items:[],more:false};saves++;throw Error('Lost save reply');}});
  await p.ready;await ui.get('Configure check-ins').emit('activate');await ui.get('Save reviewed settings').emit('activate');assert.equal(saves,1);assert.equal(ui.get('Configure check-ins').disabled,true);assert.match(ui.labels(),/Outcome unknown/);p.updateConnection();assert.equal(saves,1);p.dispose();
});
test('broadcast membership change blocks exact confirmation even when original agents remain',async()=>{
  const f=messaging();await reviewMessage(f,'broadcast');f.panel.update({contacts:[contact,{...contact,id:'agent-two',registration:'r2'}],messages:[],leases:[]});assert.equal(f.ui.get('Confirm broadcast').disabled,true);await f.ui.get('Confirm broadcast').emit('activate');assert.equal(f.calls.length,0);f.panel.dispose();
});

test('conversation and composer use the stock centered ReadingMeasure contract',()=>{
  const f=messaging();assert.equal(f.panel.element.measure,'ReadingMeasure');assert.equal(f.ui.get('Operator conversation').kind,'MessageList');f.panel.dispose();
});
test('focus-held conversation updates drain on focusout without needing a different snapshot',async()=>{
  const f=messaging(),first={id:'m1',sender:contact.id,sender_contact:contact,audience:'operator',body:'First body',created_at:'2026-10-26T12:00:00Z'};
  f.panel.update({contacts:[contact],messages:[first],leases:[]});const log=f.ui.get('Operator conversation'),reply=f.ui.get('Reply');reply.focus();
  const next={...first,body:'Updated body'},second={...first,id:'m2',body:'Second body',created_at:'2026-10-26T12:01:00Z'};
  f.panel.update({contacts:[contact],messages:[next,second],leases:[]});assert.equal(log.messages.length,1);assert.equal(log.messages[0].body.children[0].label,'First body');
  // Movement inside the conversation keeps the held update parked.
  await log.emit('focusout');assert.equal(log.messages.length,1);
  f.ui.get('Message').focus();await log.emit('focusout');assert.equal(log.messages.length,2);assert.equal(log.messages[0].body.children[0].label,'Updated body');assert.equal(log.messages[1].body.children[0].label,'Second body');assert.equal(f.calls.length,0);
  f.panel.dispose();
});
test('returning to conversation drains held updates even without a focusout notification',()=>{
  const f=messaging(),first={id:'m1',sender:contact.id,sender_contact:contact,audience:'operator',body:'Original',created_at:'2026-10-26T12:00:00Z'};
  f.panel.update({contacts:[contact],messages:[first],leases:[]});const log=f.ui.get('Operator conversation');f.ui.get('Reply').focus();
  const snapshot={contacts:[contact],messages:[{...first,body:'Latest'}],leases:[]};f.panel.update(snapshot);assert.equal(log.messages[0].body.children[0].label,'Original');
  // A removed or hidden view need not deliver a native focusout to the log.
  f.ui.get('Message').focus();f.panel.update(snapshot);assert.equal(log.messages[0].body.children[0].label,'Latest');assert.equal(f.calls.length,0);f.panel.dispose();
});

for(const enabled of [false,true])test(`check-in presentation with policy ${enabled?'on':'off'} never claims an absent pending request`,async()=>{
 const ui=controlUI(),calls=[],policy={lease:lease.id,policy_generation:1,enabled,interval_ns:600e9,reply_timeout_ns:60e9,contention_interval_ns:0};
 const panel=createLeaseReviews({ui,root:ui.stack(),lease,supported:true,connected:()=>true,call:async(op,args)=>{calls.push([op,args]);return op==='lease_review_policy'?policy:{cursor:0,items:[],more:false};}});await panel.ready;
 assert.equal(ui.get('Awaiting the owning agent'),undefined);assert.ok(ui.get('No reply is currently requested.'));assert.equal(ui.get('Check-in policy & safety').open,false);assert.equal(ui.get('Conversation history').open,false);assert.equal(ui.get('Configure check-ins').disabled,false);assert.ok(calls.every(([op])=>op==='lease_review_policy'||op==='lease_review_list'));panel.dispose();
});
test('check-in presentation reserves awaiting heading for pending requests and shows read failure honestly',async()=>{
 const ui=controlUI(),policy={lease:lease.id,policy_generation:1,enabled:false};const panel=createLeaseReviews({ui,root:ui.stack(),lease,supported:true,connected:()=>true,call:async(op,args)=>op==='lease_review_policy'?policy:{cursor:0,items:args.pending?[{state:'pending',agent:'worker',leases:[]}]:[],more:false}});await panel.ready;assert.ok(ui.get('Awaiting the owning agent'));assert.equal(ui.get('No reply is currently requested.'),undefined);assert.ok(ui.get('Off'));panel.dispose();
 const errorUI=controlUI(),failed=createLeaseReviews({ui:errorUI,root:errorUI.stack(),lease,supported:true,connected:()=>true,call:async()=>{throw Error('Synthetic read unavailable')}});await failed.ready;assert.ok(errorUI.get('Unavailable'));assert.equal(errorUI.get('Configure check-ins').disabled,true);assert.match(errorUI.labels(),/Synthetic read unavailable/);failed.dispose();
});
