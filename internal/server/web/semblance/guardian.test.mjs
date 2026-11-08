import test from 'node:test';
import assert from 'node:assert/strict';
import {pressureRows,worstRank,guardianView,activeReversible} from './guardian.mjs';

test('pressure rows never present stale or missing levels as normal',()=>{
  const rows=pressureRows({cpu:{level:'critical',reason:'Run queue per CPU (1m load)'},memory:{level:'normal'},io:{}},false);
  assert.deepEqual(rows.map(r=>[r.key,r.value,r.tone]),[['cpu','CRITICAL','danger'],['memory','NORMAL','success'],['io','UNKNOWN','neutral']]);
  assert.equal(rows[0].reason,'Run queue per CPU (1m load)');
  assert.ok(pressureRows({cpu:{level:'high'}},true).every(r=>r.level==='unknown'));
});

test('worst rank ignores unknown resources and keeps gaps',()=>{
  assert.equal(worstRank({cpu_pressure_rank:1,memory_pressure_rank:null,io_pressure_rank:3}),3);
  assert.equal(worstRank({cpu_pressure_rank:null,memory_pressure_rank:null,io_pressure_rank:null}),null);
});

test('guardian view offers undo only for active reversible actions',()=>{
  const base={id:'a',at:'2026-11-08T00:00:00Z',step:'freeze',group_name:'Chrome',reason:'cpu',reversible:true};
  assert.equal(activeReversible({...base,outcome:'applied'}),true);
  assert.equal(activeReversible({...base,outcome:'planned'}),false);
  assert.equal(activeReversible({...base,outcome:'applied',reverted_at:'2026-11-08T00:01:00Z'}),false);
  const v=guardianView({mode:'act',role:'manned',budget:{used:2,limit:12},incidents:[],groups:[{name:'Chrome',cpu_cores:17.3,memory_bytes:2**31,disk_bytes_per_second:0,processes:190,protected:'',state:['deprioritize']}],actions:[{...base,outcome:'applied'}]});
  assert.equal(v.headline,'Host guardian · act mode · manned host');
  assert.equal(v.groups[0][1],'17.3');assert.equal(v.groups[0][5],'deprioritize');
  assert.equal(v.actions[0].undo,true);
  assert.equal(guardianView({mode:'off'}).off,true);
  assert.equal(guardianView(null).available,false);
});
