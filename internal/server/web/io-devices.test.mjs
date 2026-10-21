import test from 'node:test';
import assert from 'node:assert/strict';
import {allDevices,noDevices,selectedDeviceIDs,toggleDevice,aggregateIO,aggregateIOWindow} from './io-devices.mjs';

const device=(id,read,write)=>({id,name:id,read_bytes_per_second:{value:read,quality:read==null?'unavailable':'reported'},write_bytes_per_second:{value:write,quality:write==null?'unavailable':'reported'}});

test('All follows observed identities; explicit custom selection never silently broadens',()=>{
 const current=[device('b',1,2),device('a',3,4)],all=allDevices();
 assert.deepEqual(selectedDeviceIDs(all,current),['a','b']);
 assert.deepEqual(selectedDeviceIDs(all,[...current,device('c',5,6)]),['a','b','c']);
 const custom=toggleDevice(all,current,'a',false);
 assert.deepEqual(selectedDeviceIDs(custom,[...current,device('c',5,6)]),['b']);
 const every=toggleDevice(custom,current,'a',true);
 assert.equal(every.mode,'custom');
 assert.deepEqual(selectedDeviceIDs(every,[...current,device('c',5,6)]),['a','b']);
 assert.deepEqual(selectedDeviceIDs(custom,[]),['b'],'missing selected identity remains selected and unavailable');
 assert.deepEqual(selectedDeviceIDs(noDevices(),current),[]);
 assert.deepEqual(selectedDeviceIDs(all,current),['a','b'],'selection operations do not mutate their input');
});

test('combined rates preserve real zero and independently propagate unknown directions',()=>{
 assert.deepEqual(aggregateIO([device('a',2,5),device('b',3,7)],['a','b']),{read:5,write:12,selected:2,present:2,membershipMatches:true});
 assert.equal(aggregateIO([device('a',0,0)],['a']).read,0);
 assert.equal(aggregateIO([device('a',0,0)],['a']).write,0);
 const partial=aggregateIO([device('a',2,5),device('b',null,7)],['a','b']);
 assert.equal(partial.read,null);assert.equal(partial.write,12);
 for (const value of [null,undefined,NaN,Infinity,-1,'4']) assert.equal(aggregateIO([device('a',value,2)],['a']).read,null);
 const stale=device('a',3,4);stale.read_bytes_per_second.quality='unavailable';
 assert.equal(aggregateIO([stale],['a']).read,null);
 assert.equal(aggregateIO([device('a',Number.MAX_VALUE,1),device('b',Number.MAX_VALUE,1)],['a','b']).read,null,'overflow cannot fabricate a finite total');
});

test('missing, duplicate, empty and changed identity sets cannot fabricate zero or doubled totals',()=>{
 for (const selected of [[],['absent'],['a','absent']]) {
  const totals=aggregateIO([device('a',3,4)],selected);
  assert.equal(totals.read,null);assert.equal(totals.write,null);
 }
 assert.equal(aggregateIO([device('a',3,4),device('a',9,10)],['a']).read,null);
 assert.equal(aggregateIO([device('a',3,4),device('b',9,10)],['a'],{strictMembership:true}).read,null);
 assert.equal(aggregateIO([device('a',3,4),device('b',9,10)],['a']).read,3,'unselected traffic is excluded from a custom total');
});

test('window aggregation fixes membership and carries counter reset/unavailable gaps',()=>{
 const history=[
  {at:'2026-01-01T00:00:00Z',sequence:1,disk:[{id:'a',read:1,write:2}]},
  {at:'2026-01-01T00:00:02Z',sequence:2,disk:[{id:'a',read:2,write:3},{id:'b',read:4,write:5}]},
  {at:'2026-01-01T00:00:04Z',sequence:3,disk:[{id:'a',read:null,write:null},{id:'b',read:4,write:5}]},
  {at:'2026-01-01T00:00:06Z',sequence:4,disk:[{id:'a',read:3,write:4},{id:'b',read:5,write:6}]},
 ];
 const current=[device('a',3,4),device('b',5,6)],all=aggregateIOWindow(history,'disk',allDevices(),current);
 assert.deepEqual(all.map(point=>point.read),[null,6,null,8]);
 assert.deepEqual(all.map(point=>point.write),[null,8,null,10]);
 assert.deepEqual(all.map(point=>point.at),history.map(point=>point.at));
 const custom=aggregateIOWindow(history,'disk',{mode:'custom',ids:['a']},current);
 assert.deepEqual(custom.map(point=>point.read),[1,2,null,3]);
 const missing=aggregateIOWindow(history,'disk',{mode:'custom',ids:['gone']},current);
 assert.ok(missing.every(point=>point.read===null&&point.write===null));
 const none=aggregateIOWindow(history,'network',noDevices(),[]);
 assert.ok(none.every(point=>point.read===null&&point.write===null));
});
