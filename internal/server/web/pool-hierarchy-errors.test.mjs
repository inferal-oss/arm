import {test} from 'node:test';
import assert from 'node:assert/strict';
import {samePoolRequest} from './pool-model.mjs';
import {createPoolOperations} from './pool-operations.mjs';
const request=()=>({operation:'set',id:'p',revision:1,capacity:1,reason:'Exact reviewed change',idempotency_key:'key',reviewed:{authority:'local',consumers:[]}});
for(const code of ['hierarchy_review_too_large','hierarchy_review_required'])test(`${code} is a definite first refusal but cannot settle an earlier lost reply`,async()=>{
 let calls=0,lost=false;
 const ops=createPoolOperations({authority:()=> 'local',storage:{getItem:()=>null,setItem(){}},call:async(op,q)=>{calls++;assert.equal(op,'pool_mutate');assert.equal(samePoolRequest(q,request()),true);throw Object.assign(Error('Complete hierarchy review refused'),lost?{}:{code});}});
 const first=await ops.submit(request());assert.equal(first.state,'rejected');assert.equal(calls,1);
 const other=createPoolOperations({authority:()=> 'local',storage:{getItem:()=>null,setItem(){}},call:async()=>{throw lost?Error('reply lost'):Object.assign(Error('Complete hierarchy review refused'),{code});}});
 lost=true;const unknown=await other.submit(request());assert.equal(unknown.state,'unknown');lost=false;await other.retry(unknown);assert.equal(unknown.state,'unknown');assert.match(unknown.error,/earlier outcome remains uncertain/);
});
