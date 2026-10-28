import {test} from 'node:test';
import assert from 'node:assert/strict';
import {availabilityFacts,mountNodeAvailability} from './node-availability.mjs';

function fixture(){
  const all=[];
  class C {
    constructor(label='',props={}){this.label=label;this.children=[];this.events={};this.disabled=false;Object.assign(this,props);all.push(this);}
    append(...children){this.children.push(...children);return this;}
    replace(...children){this.children=children;return this;}
    set(props){Object.assign(this,props);return this;}
    dispose(){this.disposed=true;}
    async fire(){return this.action?.();}
  }
  const ui={stack:(children=[])=>new C().append(...children),card:(label,children)=>new C(label).append(...children),text:(label,props)=>new C(label,props),button:(label,action)=>new C(label,{action}),select:(label,options,props)=>new C(label,props),area:(label,props)=>new C(label,props),descriptions:rows=>new C(JSON.stringify(rows))};
  return {ui,root:ui.stack(),find:label=>all.find(c=>c.label===label),all};
}
const host='a'.repeat(32);
const report={schema:1,host,owner_declared:{profile:'intermittent',revision:1,updated_at:'2026-10-27T00:00:00Z'},observed_at:'2026-10-27T00:00:00Z'};
const flush=()=>new Promise(resolve=>setImmediate(resolve));

test('declared mobility, offline connection and stale resources remain separate',()=>{
  const facts=availabilityFacts({host,report,profileState:'reported',connection:'offline',resourceState:'stale'});
  assert.match(facts[0][1],/Mobile \/ intermittent.*owner declared/);
  assert.equal(facts[1][1],'Offline');assert.match(facts[2][1],/Stale/);
  assert.match(availabilityFacts({host:'b'.repeat(32),report,profileState:'reported'})[0][1],/Unknown/);
  assert.equal(availabilityFacts({host,connection:'__proto__'})[1][1],'Unknown');
});

test('remote panel reads only exact selected report and never offers self mutation',async()=>{
  const f=fixture();let reads=0,sets=0;
  const panel=mountNodeAvailability({...f,host,sourceHost:'b'.repeat(32),local:false,read:async()=>{reads++;return report;},setSelf:async()=>{sets++;}});
  await flush();assert.equal(reads,1);assert.equal(sets,0);assert.equal(f.find('Save owner availability'),undefined);panel.dispose();
});

test('uncertain self update retains original key and needs explicit read before another change',async()=>{
  const f=fixture(),requests=[];let keys=0;
  const panel=mountNodeAvailability({...f,host,sourceHost:host,local:true,read:async()=>report,setSelf:async q=>{requests.push(q);throw Error('lost reply');},newKey:()=>`original-${++keys}`});
  await flush();f.find('Private reason for changing availability').value='owner declaration';
  await f.find('Save owner availability').fire();await f.find('Save owner availability').fire();
  assert.equal(requests.length,1);assert.equal(requests[0].key,'original-1');assert.equal(requests[0].expected_revision,1);
  assert.ok(f.all.some(c=>String(c.label).includes('Original key: original-1')));
  await panel.refresh();await f.find('Save owner availability').fire();assert.equal(requests.length,2);assert.equal(requests[1].key,'original-2');panel.dispose();
});

test('acknowledged self update refreshes visible declaration without replaying mutation',async()=>{
  const f=fixture();let current=structuredClone(report),reads=0,sets=0;
  const panel=mountNodeAvailability({...f,host,sourceHost:host,local:true,read:async()=>{reads++;return current;},setSelf:async q=>{sets++;current={...report,owner_declared:{profile:q.profile,revision:q.expected_revision+1,updated_at:'2026-10-27T00:01:00Z'}};},newKey:()=> 'acknowledged-update'});
  await flush();f.find('Availability profile').value='stable';f.find('Private reason for changing availability').value='owner declaration';
  await f.find('Save owner availability').fire();
  assert.equal(sets,1);assert.equal(reads,2);assert.ok(f.all.some(c=>String(c.label).includes('Stable (owner declared)')));assert.equal(f.find('Save owner availability').disabled,false);panel.dispose();
});

test('read begun before an uncertain write cannot clear the write fence',async()=>{
  const f=fixture(),requests=[];let reads=0,release;
  const held=new Promise(resolve=>{release=resolve;});
  const panel=mountNodeAvailability({...f,host,sourceHost:host,local:true,read:async()=>++reads===2?held:report,setSelf:async q=>{requests.push(q);throw Error('lost reply');},newKey:()=>`held-write-${requests.length+1}`});
  await flush();const oldRead=panel.refresh();await flush();
  f.find('Private reason for changing availability').value='owner declaration';
  await f.find('Save owner availability').fire();assert.equal(requests.length,1);
  release(report);await oldRead;await f.find('Save owner availability').fire();
  assert.equal(requests.length,1,'an old read cleared the uncertain write fence');
  await panel.refresh();await f.find('Save owner availability').fire();assert.equal(requests.length,2);panel.dispose();
});


test('read begun during an in-flight uncertain write cannot clear the write fence',async()=>{
  const f=fixture(),requests=[];let reads=0,releaseRead,rejectWrite;
  const heldRead=new Promise(resolve=>{releaseRead=resolve;});
  const heldWrite=new Promise((resolve,reject)=>{rejectWrite=reject;});
  const panel=mountNodeAvailability({...f,host,sourceHost:host,local:true,read:async()=>++reads===2?heldRead:report,setSelf:async q=>{requests.push(q);return heldWrite;},newKey:()=>`during-write-${requests.length+1}`});
  await flush();f.find('Private reason for changing availability').value='owner declaration';
  const write=f.find('Save owner availability').fire();await flush();
  const duringRead=panel.refresh();await flush();rejectWrite(Error('lost reply'));await write;
  releaseRead(report);await duringRead;await f.find('Save owner availability').fire();
  assert.equal(requests.length,1,'a during-write read cleared the uncertain write fence');
  assert.equal(f.find('Save owner availability').disabled,true);
  await panel.refresh();await f.find('Save owner availability').fire();assert.equal(requests.length,2);panel.dispose();
});


test('remounted owner panel preserves its original pending and unknown mutation boundary',async()=>{
 const state={epoch:0,pending:false,uncertain:false},requests=[];let reject,reads=0;
 const held=new Promise((resolve,rejectPromise)=>{reject=rejectPromise;});
 const options={host,sourceHost:host,local:true,mutationState:state,read:async()=>{reads++;return report;},setSelf:async q=>{requests.push(q);if(requests.length===1)return held;throw Error('lost second reply');},newKey:()=>`remount-${requests.length+1}`};
 const first=fixture(),panel=mountNodeAvailability({...first,...options});await flush();
 first.find('Private reason for changing availability').value='owner declaration';const write=first.find('Save owner availability').fire();await flush();panel.dispose();
 const next=fixture(),remounted=mountNodeAvailability({...next,...options});await flush();
 assert.equal(reads,1,'remount silently read through a pending write');assert.ok(next.all.some(c=>String(c.label).includes('Original key: remount-1')));
 next.find('Private reason for changing availability').value='owner declaration';await next.find('Save owner availability').fire();assert.equal(requests.length,1);
 await remounted.refresh();await next.find('Save owner availability').fire();assert.equal(requests.length,1);
 reject(Error('lost original reply'));await write;assert.equal(state.uncertain,true);
 await next.find('Save owner availability').fire();assert.equal(requests.length,1);
 await remounted.refresh();await next.find('Save owner availability').fire();assert.equal(requests.length,2);remounted.dispose();
});


test('disconnection or failed read cannot re-enable a stale local owner editor',async()=>{
 const f=fixture();let connected=true,fail=false,sets=0;
 const panel=mountNodeAvailability({...f,host,sourceHost:host,local:true,connected:()=>connected,read:async()=>{if(fail)throw Error('unavailable');return report;},setSelf:async()=>{sets++;},newKey:()=> 'stale-editor'});
 await flush();f.find('Private reason for changing availability').value='owner declaration';
 fail=true;await panel.refresh();panel.updateConnection();await f.find('Save owner availability').fire();assert.equal(sets,0);assert.equal(f.find('Save owner availability').disabled,true);
 fail=false;await panel.refresh();connected=false;panel.updateConnection();connected=true;panel.updateConnection();await f.find('Save owner availability').fire();assert.equal(sets,0);
 await panel.refresh();await f.find('Save owner availability').fire();assert.equal(sets,1);panel.dispose();
});
