import {test} from 'node:test';
import assert from 'node:assert/strict';
import {Backend} from './backend.mjs';

class Socket extends EventTarget {
 static OPEN=1;
 constructor(url,protocols){super();this.url=url;this.protocols=protocols;this.readyState=1;this.sent=[];queueMicrotask(()=>this.dispatchEvent(new Event('open')));}
 send(value){this.sent.push(value);}
 close(){this.readyState=3;this.dispatchEvent(new Event('close'));}
 receive(value){this.dispatchEvent(Object.assign(new Event('message'),{data:value}));}
}
const fixture=(options={})=>new Backend('a'.repeat(64),{Socket,location:{host:'127.0.0.1:1234'},...options});

test('transport keeps bearer out of URL/payload and preserves int64 protocol quantities',async()=>{
 const b=fixture();await b.ready;
 try{
  assert.equal(b.socket.url,'ws://127.0.0.1:1234/v1/ws');assert.deepEqual(b.socket.protocols,['arm.v1','arm.auth.'+'a'.repeat(64)]);
  const promise=b.call('pool_capacity_set',{capacity:9007199254740993n,reason:'explicit review'});
  assert.match(b.socket.sent[0],/"capacity":9007199254740993/);assert.ok(!b.socket.sent[0].includes('a'.repeat(64)));
  b.socket.receive('{"version":1,"id":"1","result":{"capacity":9007199254740993}}');assert.equal((await promise).capacity,9007199254740993n);
 }finally{b.disconnect();}
});

test('bounded pending requests reject on disconnect and never replay on a successor connection',async()=>{
 const b=fixture();await b.ready;const requests=Array.from({length:4},()=>b.call('inspect',{id:'exact'}));const outcomes=Promise.allSettled(requests);
 await assert.rejects(b.call('release',{id:'exact',revision:1}),/request limit/);assert.equal(b.socket.sent.length,4);
 b.disconnect();assert.ok((await outcomes).every(r=>r.status==='rejected'&&/outcome may be unknown/.test(r.reason.message)));assert.equal(b.pending.size,0);
 const next=fixture();await next.ready;assert.equal(next.socket.sent.length,0);next.disconnect();
});

test('retired sockets cannot deliver late telemetry/events/watch/results to current UI',async()=>{
 let current=true,events=0,monitors=0,watches=0,closed=0;
 const b=fixture({isCurrent:()=>current,onEvent:()=>events++,onMonitor:()=>monitors++,onWatch:()=>watches++,onClose:()=>closed++});await b.ready;
 const request=b.call('monitor_subscribe');b.socket.receive('{"version":1,"id":"1","result":{"subscription":"1"}}');await request;
 b.meshWatchID='watch';current=false;
 for(const packet of [{event:{sequence:1}},{telemetry:{sequence:1},subscription:'1'},{subscription:'watch'}])b.socket.receive(JSON.stringify({version:1,...packet}));
 b.disconnect();assert.deepEqual([events,monitors,watches,closed],[0,0,0,0]);
});

test('definitive backend refusal remains final and malformed protocol closes without replay',async()=>{
 let errors=0;const b=fixture({onError:()=>errors++});await b.ready;
 const p=b.call('renew',{id:'exact',revision:3});b.socket.receive('{"version":1,"id":"1","error":{"code":"revision_conflict","message":"Changed"}}');
 await assert.rejects(p,e=>e.code==='revision_conflict'&&e.definitive===true);assert.equal(b.socket.sent.length,1);
 const next=b.call('status');const rejected=assert.rejects(next,/disconnected/);b.socket.receive('{"version":2,"id":"2","result":{}}');await rejected;assert.equal(errors,1);assert.equal(b.socket.readyState,3);assert.equal(b.socket.sent.length,2);
});

test('naming diagnostics require a current matched local response and never alter its action result',async()=>{
 const warnings=[];let local=true,current=true;const b=fixture({localNaming:()=>local,isCurrent:()=>current,onNamingDiagnostic:value=>warnings.push(value)});await b.ready;
 try{
  for(const packet of [{id:'foreign',result:[]},{event:{sequence:1}},{subscription:'other',telemetry:{sequence:2}}])b.socket.receive(JSON.stringify({version:1,...packet,naming_diagnostic:{code:'name_metadata_invalid',message:'private raw bytes'}}));assert.equal(warnings.length,0);
  const cleanup=b.call('release',{id:'exact-id'});b.socket.receive('{"version":1,"id":"1","result":{"id":"exact-id","state":"terminal"},"naming_diagnostic":{"code":"name_metadata_invalid","message":"private raw bytes"}}');assert.equal((await cleanup).state,'terminal');assert.equal(warnings.length,1);assert.ok(!warnings[0].includes('private raw bytes'));
  const inspect=b.call('inspect',{id:'exact-id'});b.socket.receive('{"version":1,"id":"2","result":{"id":"exact-id"}}');await inspect;assert.equal(warnings.length,1);
  const snapshot=b.call('status');b.socket.receive('{"version":1,"id":"3","result":{"host":"source"}}');await snapshot;assert.deepEqual(warnings.at(-1),null);
  local=false;const remote=b.call('status');b.socket.receive('{"version":1,"id":"4","result":{},"naming_diagnostic":{"code":"name_metadata_invalid"}}');await remote;assert.equal(warnings.length,2);
  current=false;b.socket.receive('{"version":1,"id":"3","result":{},"naming_diagnostic":{"code":"name_metadata_invalid"}}');assert.equal(warnings.length,2);
 }finally{b.disconnect();}
});

test('older healthy naming replies cannot clear a newer accepted diagnostic',async()=>{
 const warnings=[],b=fixture({localNaming:()=>true,onNamingDiagnostic:value=>warnings.push(value)});await b.ready;
 try{const older=b.call('status'),newer=b.call('name_list');b.socket.receive('{"version":1,"id":"2","result":{},"naming_diagnostic":{"code":"name_metadata_invalid"}}');await newer;assert.equal(warnings.length,1);
  b.socket.receive('{"version":1,"id":"1","result":{"host":"source"}}');await older;assert.equal(warnings.length,1);assert.notEqual(warnings[0],null);
  const healthy=b.call('status');b.socket.receive('{"version":1,"id":"3","result":{"host":"source"}}');await healthy;assert.equal(warnings.at(-1),null);
 }finally{b.disconnect();}
});

test('contradictory unhealthy name mutation replies remain unknown without changing lifecycle results',async()=>{
 const b=fixture({localNaming:()=>true});await b.ready;
 try{const mutation=b.call('name_mutate',{id:'exact'});b.socket.receive('{"version":1,"id":"1","result":{"revision":1},"naming_diagnostic":{"code":"name_metadata_invalid"}}');await assert.rejects(mutation,/outcome may be unknown/);
  const release=b.call('release',{id:'exact'});b.socket.receive('{"version":1,"id":"2","result":{"id":"exact","state":"terminal"},"naming_diagnostic":{"code":"name_metadata_invalid"}}');assert.equal((await release).state,'terminal');
 }finally{b.disconnect();}
});
