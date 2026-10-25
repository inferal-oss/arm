import {test} from 'node:test';
import assert from 'node:assert/strict';
import {leaseReviewPanel, reviewConfiguration, reviewDuration, reviewDurationInput} from './lease-reviews.mjs';
class Element {
  constructor(tag) { this.tagName=tag;this.children=[];this.events={};this.attributes={};this._text='';this.disabled=false;this.hidden=false; }
  append(...items){for(const item of items){item.parent=this;this.children.push(item);}} replaceChildren(...items){this.children=items;this._text='';}
  remove(){if(this.parent)this.parent.children=this.parent.children.filter(x=>x!==this);}
  set open(value){this._open=value;this.fire('toggle');} get open(){return this._open??false;}
  set textContent(v){this._text=String(v);this.children=[];} get textContent(){return this._text+this.children.map(x=>x.textContent).join('');}
  setAttribute(k,v){this.attributes[k]=v;} addEventListener(k,f){(this.events[k]??=[]).push(f);} async fire(k){for(const f of this.events[k]??[])await f();} focus(){this.focused=true;}
}
const document={createElement:t=>new Element(t)},all=e=>[e,...e.children.flatMap(all)];
const button=(p,label)=>all(p.element).find(x=>x.tagName==='button'&&x.textContent===label);
const lease={id:'lease',revision:9,state:'active',request:{agent:'owner',reason:'test'}};
const policy={lease:'lease',policy_generation:3,enabled:false,interval_ns:600e9,reply_timeout_ns:60e9,contention_interval_ns:0};
const record={id:'review',agent:'<owner>',state:'replied',created_at:'2026-10-24T12:00:00Z',reply_deadline_at:'2026-10-24T12:01:00Z',leases:[{lease:'lease',lease_revision:9,purpose:'<script>literal</script>',remaining_ns:1e9,resources:{cpu:2,memory_bytes:100,disk_bytes:200},usage:{cpu_quality:'measured',memory_quality:'unknown',stale:true,complete:false},queued_demand:2}],reply:{intent:'release',explanation:'<script>not markup</script>'}};
async function setup(t, options={}){let connected=true,calls=[];const panel=leaseReviewPanel({lease,document,supported:true,connected:()=>connected,onRefresh(){},call:async(op,arg)=>{calls.push([op,arg]);return op==='lease_review_policy'?policy:{items:arg.pending?[]:[record],cursor:1,more:false};},...options});t.after(()=>panel.dispose());await panel.ready;return{panel,calls,offline(){connected=false;panel.updateConnection();}};}
test('configuration binds exact revision and generation and validates durations and safe integer domains',()=>{
 assert.deepEqual(reviewConfiguration(lease,policy,true),{lease:'lease',lease_revision:9,policy_generation:3,enabled:true,interval_ns:600e9,reply_timeout_ns:60e9,contention_interval_ns:0});
 assert.equal(reviewConfiguration(lease,policy,false).enabled,false);
 for(const values of [{interval:'14s'},{timeout:'4s'},{timeout:'2h'},{contention:'11m'},{contention:'1s'}])assert.throws(()=>reviewConfiguration(lease,policy,true,values));
 assert.throws(()=>reviewConfiguration(lease,{...policy,policy_generation:2**53},true));assert.throws(()=>reviewConfiguration(lease,{...policy,lease:'other'},false));
});
test('read-only pending and history show literal explanations and quality without agent reply or lease action',async t=>{
 const{panel,calls}=await setup(t);assert.deepEqual(calls.map(x=>x[0]),['lease_review_policy','lease_review_list','lease_review_list']);
 assert.match(panel.element.textContent,/Agent intent: release/);assert.match(panel.element.textContent,/<script>not markup/);assert.match(panel.element.textContent,/stale · incomplete/);assert.match(panel.element.textContent,/silence never renew/);
 assert.equal(all(panel.element).some(x=>x.tagName==='script'),false);assert.equal(calls.some(x=>x[0]==='lease_review_reply'||x[0]==='lease_action'),false);
});
test('configuration review and decline are inert, save sends the exact displayed CAS pair once',async t=>{
 const{panel,calls}=await setup(t);await button(panel,'Configure check-ins').fire('click');assert.equal(calls.length,3);await button(panel,'Keep current settings').fire('click');assert.equal(calls.length,3);
 await button(panel,'Configure check-ins').fire('click');const enabled=all(panel.element).find(x=>x.type==='checkbox');enabled.checked=true;await enabled.fire('input');await button(panel,'Save reviewed settings').fire('click');await button(panel,'Save reviewed settings').fire('click');
 assert.equal(calls.length,4);assert.deepEqual(calls[3],['lease_review_configure',reviewConfiguration(lease,policy,true)]);
});
test('unknown outcome never retries and disables stale settings until lease/policy refresh',async t=>{
 let mutations=0;
 const{panel}=await setup(t,{call:async(op,arg)=>{if(op==='lease_review_configure'){mutations++;throw Error('lost');}return op==='lease_review_policy'?policy:{items:[],cursor:0,more:false};}});
 await button(panel,'Configure check-ins').fire('click');await button(panel,'Save reviewed settings').fire('click');await button(panel,'Save reviewed settings').fire('click');
 assert.equal(mutations,1);assert.match(panel.element.textContent,/Outcome unknown/);assert.equal(button(panel,'Configure check-ins').disabled,true);
});
test('unsupported/remote feature makes zero API calls',async t=>{let count=0;const{panel}=await setup(t,{supported:false,call:()=>{count++;}});assert.equal(count,0);assert.match(panel.element.textContent,/local backend/);assert.equal(button(panel,'Configure check-ins').disabled,true);});
test('disconnect blocks confirmation and closing during load never renders stale results',async t=>{
 const{panel,calls,offline}=await setup(t);await button(panel,'Configure check-ins').fire('click');offline();await button(panel,'Save reviewed settings').fire('click');assert.equal(calls.length,3);
 let resolve;const delayed=leaseReviewPanel({lease,document,supported:true,connected:()=>true,call:()=>new Promise(done=>resolve=done)});delayed.dispose();resolve(policy);await delayed.ready;assert.equal(delayed.element.textContent.includes('Policy generation'),false);
});
test('history cursor must advance and fields retain whole records on the next page',async t=>{
 let page=0;const{panel}=await setup(t,{call:async(op,arg)=>op==='lease_review_policy'?policy:arg.pending?{items:[],cursor:0,more:false}:{items:[record],cursor:1,more:++page===1}});
 assert.equal(button(panel,'Next history page').hidden,false);await button(panel,'Next history page').fire('click');assert.equal(button(panel,'Configure check-ins').disabled,true);assert.match(panel.element.textContent,/Invalid check-in cursor/);
});

test('checkpoint and custom claim records are lazy structured trees and byte quantities are human readable',async t=>{
 const sample=structuredClone(record);sample.leases[0].resources.memory_bytes=2147483648;sample.leases[0].resources.disk_bytes=1073741824;sample.leases[0].usage.memory_bytes=536870912;
 sample.leases[0].pools=[{pool:'tokens',quantity:9223372036854775807n}];sample.leases[0].ports=[{name:'listener',port:8080}];
 const{panel}=await setup(t,{call:async(op,arg)=>op==='lease_review_policy'?policy:{items:arg.pending?[]:[sample],cursor:1,more:false}});
 assert.match(panel.element.textContent,/2 GiB memory · 1 GiB disk/);assert.match(panel.element.textContent,/512 MiB/);assert.equal(all(panel.element).some(x=>x.tagName==='pre'),false);
 const trees=all(panel.element).filter(x=>x.className==='json-view');assert.equal(trees.length,3);assert.ok(trees.every(x=>all(x).find(e=>e.tagName==='details').open===false));
 const custom=trees.find(x=>x.attributes['aria-label']==='Custom claims');await all(custom).find(x=>x.textContent==='Expand root'&&x.tagName==='button').fire('click');const branch=all(custom).filter(x=>x.tagName==='details')[1];branch.open=true;
 assert.match(custom.textContent,/9223372036854775807/);await all(custom).find(x=>x.textContent==='Collapse all'&&x.tagName==='button').fire('click');assert.ok(all(custom).filter(x=>x.tagName==='details').every(x=>!x.open));
});

test('human review durations use concise units and bounded useful precision without changing exact form values',()=>{
 const examples=[[0,'0s'],[1,'1ns'],[999,'999ns'],[1000,'1µs'],[1234,'1.234µs'],[999999,'999.999µs'],[1000000,'1ms'],[1234567,'1.235ms'],[999999999,'1s'],[1000000000,'1s'],[1234567890,'1.235s'],[59999999999,'1m'],[60000000000,'1m'],[600000000000,'10m'],[120000000000,'2m'],[1799133950000,'29m59s'],[1799600000000,'30m'],[3661900000000,'1h1m2s'],[604800000000000,'7d'],[9223372036854775807n,'106751d23h47m17s']];
 for(const[ns,label]of examples)assert.equal(reviewDuration(ns),label,String(ns));
 for(const invalid of [-1,NaN,Infinity,-Infinity,1.5,true,'600000000000',Number.MAX_SAFE_INTEGER+1,9223372036854775808n])assert.equal(reviewDuration(invalid),'Invalid duration');
 assert.equal(reviewDuration(null),'Unavailable');assert.equal(reviewDuration(undefined),'Unavailable');
 assert.equal(reviewDurationInput(0),'0s');assert.equal(reviewDurationInput(600000000000),'10m');assert.equal(reviewDurationInput(15123456789),'15.123456789s');assert.equal(reviewDurationInput(600000000001),'10m0.000000001s');
 const params=reviewConfiguration(lease,policy,true,{interval:reviewDurationInput(15123456789),contention:'0s',timeout:reviewDurationInput(5000000123)});
 assert.equal(params.interval_ns,15123456789);assert.equal(params.reply_timeout_ns,5000000123);assert.equal(reviewDuration(params.interval_ns),'15.123s');
});
test('rendered policy and captured remaining time are human readable while reviewed form sends exact durations',async t=>{
 const enabled={...policy,enabled:true,interval_ns:600000000000,contention_interval_ns:120000000000,reply_timeout_ns:600000000000};const sample=structuredClone(record);sample.leases[0].remaining_ns=1799133950000;
 let mutation;
 const{panel}=await setup(t,{call:async(op,arg)=>{if(op==='lease_review_configure'){mutation=arg;return enabled;}return op==='lease_review_policy'?enabled:{items:arg.pending?[]:[sample],cursor:1,more:false};}});
 assert.match(panel.element.textContent,/Every 10m · reply window 10m · 2m while work is waiting/);assert.match(panel.element.textContent,/29m59s/);assert.doesNotMatch(panel.element.textContent,/1799\.13395s|Every 600s/);
 await button(panel,'Configure check-ins').fire('click');const inputs=all(panel.element).filter(x=>x.tagName==='input'&&x.type==='text');assert.deepEqual(inputs.map(x=>x.value),['10m','2m','10m']);
 await button(panel,'Save reviewed settings').fire('click');assert.equal(mutation.interval_ns,600000000000);assert.equal(mutation.contention_interval_ns,120000000000);assert.equal(mutation.reply_timeout_ns,600000000000);
});
