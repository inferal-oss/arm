import {test} from 'node:test';
import assert from 'node:assert/strict';
import {metricHelp} from './metric-help.mjs';

test('filesystem explanation separates human policy from collector paths and notes', () => {
  const info = metricHelp('filesystem', {
    measurements: [['Available space', {scope: 'nonprivileged-filesystem-available:/private/arm', method: 'statfs', quality: 'reported', detail: '<script>literal data</script>'}]],
    filesystem: {type: 'apfs', shared: true, detail: 'never sum volume totals as independent capacity'},
  });
  assert.match(info.paragraphs.join(' '), /APFS volumes can share/);
  assert.match(info.paragraphs.join(' '), /snapshots, clones/);
  assert.doesNotMatch(info.paragraphs.join(' '), /statfs|nonprivileged|\/private\/arm|<script>|never sum/);
  assert.ok(info.technical.some(([, value]) => value === 'nonprivileged-filesystem-available:/private/arm'));
  assert.ok(info.technical.some(([, value]) => value === '<script>literal data</script>'));
});

test('I/O, unavailable GPU and reservations have plain independent explanations', () => {
  assert.match(metricHelp('disk_io').paragraphs.join(' '), /count it twice/);
  assert.match(metricHelp('network').paragraphs.join(' '), /VPN/);
  for (const kind of ['disk_io','network']) {
    const copy=metricHelp(kind).paragraphs.join(' ');
    assert.match(copy,/sum of reported counters, not exclusive physical throughput/);
    assert.match(copy,/custom selection keeps exact device identities/);
    assert.match(copy,/gaps rather than adding zero/);
  }
  assert.match(metricHelp('gpu').paragraphs.join(' '), /does not mean the GPU is idle/);
  assert.doesNotMatch(metricHelp('resource-memory').paragraphs.join(' '), /pool follows/);
  const live = metricHelp('resource-memory', {memory: {}});
  assert.match(live.paragraphs.join(' '), /pool follows/);
  assert.match(live.paragraphs.join(' '), /more headroom than necessary/);
  for (const kind of ['cpu', 'memory', 'filesystem', 'disk_io', 'network', 'gpu', 'resource-cpu', 'resource-memory', 'resource-disk', 'resource-port', 'resource-custom']) {
    const info = metricHelp(kind);
    assert.ok(info.title && info.paragraphs.length);
    assert.deepEqual(info.technical, []);
  }
});

test('live disk help explains shared conservative headroom without collector paths', async () => {
  const {diskBudgetFields} = await import('./metric-help.mjs');
  const resource = {budget_total: 100, reserved: 80, available: 20};
  assert.deepEqual(diskBudgetFields(resource, {known: true}), [['LIVE POOL',100],['PROMISED',80],['TO LEASE',20]]);
  assert.deepEqual(diskBudgetFields(resource, {known: false}), [['LIVE POOL',null],['PROMISED',80],['TO LEASE',null]]);
  const info = metricHelp('resource-disk', {disk:{known:false}});
  assert.match(info.paragraphs.join(' '), /state filesystem/);
  assert.match(info.paragraphs.join(' '), /full outstanding disk promises/);
  assert.match(info.paragraphs.join(' '), /Snapshots and clones/i);
  assert.match(info.paragraphs.join(' '), /missing or stale/);
  assert.doesNotMatch(info.paragraphs.join(' '), /f_bavail|statfs|\/Users\//);
});

test('CPU live headroom is separate from promises, physical core count and unknown',async()=>{
 const {cpuBudgetFields}=await import('./metric-help.mjs');
 const resource={budget_total:64,reserved:1.25,available:62.75};
 const cpu={known:true,stale:false,capacity_cores:2.5,reserved_cores:1.25,available_cores:1.25,deficit_cores:0,observation:{known:true,available_cores:2.5}};
 assert.deepEqual(cpuBudgetFields(resource,cpu),[['LIVE HEADROOM',2.5],['PROMISED',1.25],['TO LEASE',1.25],['SHORTFALL',0]]);
 assert.deepEqual(cpuBudgetFields(resource,{...cpu,capacity_cores:0,available_cores:0,deficit_cores:1.25,observation:{known:true,available_cores:0}}),[['LIVE HEADROOM',0],['PROMISED',1.25],['TO LEASE',0],['SHORTFALL',1.25]]);
 for(const unavailable of [null,{...cpu,known:false},{...cpu,stale:true},{...cpu,observation:{known:false,available_cores:null}}])
  assert.deepEqual(cpuBudgetFields(resource,unavailable),[['LIVE HEADROOM',null],['PROMISED',1.25],['TO LEASE',null],['SHORTFALL',null]]);
 const info=metricHelp('resource-cpu',{cpu:{...cpu,stale:true,observation:{scope:'/private/collector-scope',method:'native-counter-source',quality:'reported',detail:'literal <script> data'}}});
 assert.match(info.paragraphs.join(' '),/Other work on the host can shrink/);
 assert.match(info.paragraphs.join(' '),/unsupported accounting scopes/);
 assert.match(info.paragraphs.join(' '),/Unknown does not mean the host is idle/);
 assert.doesNotMatch(info.paragraphs.join(' '),/logical-core budget|native-counter-source|private\/collector|<script>/);
 assert.ok(info.technical.some(([,v])=>v==='native-counter-source'));
 assert.ok(info.technical.some(([,v])=>v==='literal <script> data'));
});

test('CPU freshness ages held snapshots and remote summaries never use local inventory',async()=>{
 const {cpuBudgetKnown,cpuBudgetForView,remoteCPUBudget,cpuCores}=await import('./metric-help.mjs');
 const remote={known:true,stale:false,capacity_cores:2.5,reserved_cores:1.25,available_cores:1.25,deficit_cores:0,observed_available_cores:2.5,eligible_cpus:16,quality:'reported',age_ns:1e9,stale_after_ns:4e9};
 const b=remoteCPUBudget(remote);
 assert.equal(cpuBudgetKnown(cpuBudgetForView(b,{elapsedMS:2000})),true);
 assert.equal(cpuBudgetKnown(cpuBudgetForView(b,{elapsedMS:3000})),false);
 assert.equal(cpuBudgetKnown(cpuBudgetForView(b,{connected:false})),false);
 assert.equal(cpuBudgetKnown(remoteCPUBudget(null)),false);
 assert.equal(remote.stale,false);
 assert.equal(cpuCores(.0004),'<0.01 cores');assert.equal(cpuCores(7.03584),'7.04 cores');assert.equal(cpuCores(18),'18 cores');assert.equal(cpuCores(.25),'0.25 cores');
 assert.equal(cpuCores(null),'unavailable');
 assert.equal(cpuBudgetKnown({...b,capacity_cores:false}),false);
});
