import {test} from 'node:test';
import assert from 'node:assert/strict';
import {resourcePresentation} from './resources.mjs';

const cpu={known:true,stale:false,observation:{known:true,available_cores:0},capacity_cores:4,available_cores:0,reserved_cores:5,deficit_cores:1};
test('compact CPU keeps live zero, full promises and a visible shortfall',()=>{
 const m=resourcePresentation({id:'cpu',kind:'cpu',unit:'cores',reserved:5},{cpu});
 assert.deepEqual(m.available,['TO LEASE','0 cores']);
 assert.deepEqual(m.commitment,['PROMISED','5 cores']);
 assert.match(m.warnings.join(' '),/Shortfall: 1 cores/);
});
test('stale capacity remains unavailable with existing promises visible',()=>{
 for(const kind of ['cpu','memory','disk']){
  const budget={...cpu,known:true,stale:true,observation:{total_bytes:100,available_bytes:0}};
  const m=resourcePresentation({id:kind,kind,unit:kind==='cpu'?'cores':'bytes',available:0,budget_total:0,reserved:5},{[kind]:budget});
  assert.match(m.available[1],/unavailable/);
  assert.match(m.commitment[1],/^5/);
  assert.ok(m.warnings.length);
 }
});
test('counted partitions preserve exact commitments, held backing and pending return',()=>{
 const m=resourcePresentation({id:'pool',kind:'custom',unit:'permits',custom:{pool:{name:'Build permits',parent:'parent-pool'},partition:{uncarved_available:0n,configured:2n,committed:9223372036854775807n,direct_reserved:1n,child_backing:2n,held_backing:7n,pending_return:5n}},deficit:3n},{});
 assert.deepEqual(m.available,['AVAILABLE','0 permits']);
 assert.deepEqual(m.commitment,['COMMITTED','9,223,372,036,854,775,807 permits']);
 assert.match(m.warnings.join(' '),/Shortfall: 3 permits.*Held in parent: 7 permits.*Pending return: 5 permits/);
});

test('server custom aliases outrank configured pool names while invalid optional labels keep canonical fallbacks',()=>{
 const resource={id:'pool-exact',kind:'custom',unit:'permits',custom:{pool:{name:'Configured permits'},partition:{uncarved_available:0n,configured:2n,committed:3n,held_backing:2n,pending_return:1n}},deficit:1n};
 const named=resourcePresentation({...resource,display_name:'manual-pool-alias'},{});
 assert.equal(named.label,'manual-pool-alias');
 assert.deepEqual(named.commitment,['COMMITTED','3 permits']);
 assert.match(named.warnings.join(' '),/Shortfall: 1 permits.*Pending return: 1 permits/);
 for(const display_name of [undefined,'','  ',42])assert.equal(resourcePresentation({...resource,display_name},{}).label,'Configured permits');
 assert.equal(resourcePresentation({id:'exact-only',unit:'permits'},{}).label,'exact-only');
});

test('storage pools show disk topology and actual performance rather than trusting tiers',()=>{
 const storage={name:'scratch',tier:'build',enabled:true,location:{path:'/scratch',mount:'/scratch',filesystem:'apfs',source:'/dev/disk7s4',backing_pool:'container-b',devices:[{device:'/dev/disk6',model:'External SSD',transport:'USB',solid_state:true,internal:false}]},budget:{known:true,stale:false,age_ns:0,stale_after_ns:3000000000,capacity_bytes:100,available_bytes:90,reserved_bytes:10,deficit_bytes:0,observation:{total_bytes:200,available_bytes:100}},performance:{at:'2026-10-28T00:00:00Z',write_bytes_per_second:100000000,read_bytes_per_second:200000000,random_read_iops:1000,random_read_latency_ns:1000000,method:'F_NOCACHE'},performance_stale:true};
 const m=resourcePresentation({id:'storage/scratch',kind:'storage',unit:'bytes',storage,reserved:10},{});
 assert.equal(m.label,'scratch · Storage');assert.match(m.storageSummary,/Write .*\/s · Read .*\/s/);
 assert.match(m.notes.join(' '),/disk6.*External SSD.*USB/);assert.match(m.notes.join(' '),/Tier: build \(operator label\)/);assert.match(m.notes.join(' '),/Shared backing pool views overlap/);
 assert.match(m.warnings.join(' '),/Performance measurement is stale/);
 assert.equal(resourcePresentation({id:'s',kind:'storage',unit:'bytes',storage:{...storage,performance:null}},{}).storageSummary,'Performance unmeasured');
});

test('held named storage budgets age without using remote wall clocks',()=>{
 const r={id:'storage',kind:'storage',unit:'bytes',reserved:10,storage:{name:'scratch',enabled:true,budget:{known:true,stale:false,age_ns:0,stale_after_ns:3000000000,capacity_bytes:100,available_bytes:90,reserved_bytes:10,deficit_bytes:0}}};
 const p=resourcePresentation(r,{viewOptions:{connected:true,elapsedMS:3001}});
 assert.match(p.available[1],/unavailable/);assert.match(p.commitment[1],/^10/);assert.match(p.warnings.join(' '),/waiting for fresh availability/);
});
