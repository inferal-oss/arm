import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import {bytes,numeric,percent} from '../core.mjs';
import {monitorStale,remainingValue} from '../monitor-capacity.mjs';
import {allDevices,selectedDeviceIDs,aggregateIO,aggregateIOWindow} from '../io-devices.mjs';

// Run the actual production summary path. UI handles and chart projection are
// doubled; freshness, reported measurements and their formatting are real.
// This is not RDF/DOM or native-browser evidence.
const source=readFileSync(new URL('./main.mjs',import.meta.url),'utf8');
const start=source.indexOf('function updateDescriptions('),end=source.indexOf('\nfunction renderResources()',start);
assert.ok(start>=0&&end>start);
const declarations=source.slice(start,end);
const field=()=>({set(value){Object.assign(this,value);},setPairs(rows){this.rows=rows;},setSeries(){},replace(){}});

function fixture({used=12,total=100,available=7,filesystemStale=false,pageStale=false,connected=true,elapsed=0,diskQuality,networkQuality}={}){
  const measurement=value=>({value});
  const sample={at:'2026-10-27T00:00:00Z',cpu_busy_percent:measurement(0),cores:[],
    memory:{available:measurement(0),total:measurement(0),used:measurement(0)},
    swap:{available:measurement(0),total:measurement(0),used:measurement(0)},
    filesystem:{available:measurement(available),total:measurement(total),used:measurement(used)},
    filesystem_stale:filesystemStale,disk_io:[],network:[],...(diskQuality?{disk_quality:diskQuality}:{}),...(networkQuality?{network_quality:networkQuality}:{})};
  const page={latest:sample,age_ns:0,stale_after_ns:1e9,stale:pageStale};
  const model=Object.fromEntries(['cpuValue','cpuNote','cpuChart','cores','ramValue','ramChart','ramFacts','ramNote','diskValue','diskChart','diskFacts','diskNote'].map(name=>[name,field()]));
  model.coreMap=new Map();
  model.io=Object.fromEntries(['disk_io','network'].map(name=>[name,{...Object.fromEntries(['quality','summary','rates','readChart','writeChart','selectors'].map(key=>[key,field()])),deviceMap:new Map()}]));
  const state={monitor:page,connected,received:0,history:[]};
  const scope={paused:false,lastMonitorStale:null,state,monitorModel:model,monitorFresh:field(),
    performance:{now:()=>elapsed},ui:{batch:fn=>fn()},monitorStale,remainingValue,bytes,numeric,percent,
    when:value=>value,GiB:1024**3,series:()=>[],rate:value=>value==null?'unavailable':bytes(value)+'/s',
    ioSelections:{disk_io:allDevices(),network:allDevices()},selectedDeviceIDs,aggregateIO,aggregateIOWindow,
    initMonitor(){throw Error('The fixture must retain its existing handles.');}};
  const render=runInNewContext(declarations+'\nrenderMonitor',scope);
  const before=JSON.stringify(state);
  render();
  assert.equal(JSON.stringify(state),before,'rendering must not rewrite reported observations/history');
  return {model,scope,render};
}

test('filesystem usage shows reported used/total, without deriving it from availability',()=>{
  const {model}=fixture();
  assert.equal(model.diskNote.label,'12 B of 100 B used');
  assert.equal(model.diskValue.value,'7 B');
});

test('a live host with a stale filesystem refuses a current usage summary',()=>{
  const {model,scope}=fixture({filesystemStale:true});
  assert.match(scope.monitorFresh.label,/^Live host observations/);
  assert.equal(model.diskNote.label,'Space usage unavailable (stale observation)');
  assert.equal(model.diskValue.value,'unavailable');
});

test('page staleness, elapsed expiry and disconnect qualify the usage summary',()=>{
  for(const options of [{pageStale:true},{elapsed:1001},{connected:false}]){
    const {model}=fixture(options);
    assert.equal(model.diskNote.label,'Space usage unavailable (stale observation)');
    assert.equal(model.diskValue.value,'unavailable');
  }
});

test('unknown used or total remains unavailable without hiding independent known availability',()=>{
  for(const options of [{used:null},{total:null},{used:null,total:null}]){
    const {model}=fixture(options);
    assert.equal(model.diskNote.label,'Space usage unavailable');
    assert.equal(model.diskValue.value,'7 B');
  }
});

test('reported zero usage, total and availability remain valid zeroes',()=>{
  const {model}=fixture({used:0,total:0,available:0});
  assert.equal(model.diskNote.label,'0 B of 0 B used');
  assert.equal(model.diskValue.value,'0 B');
});

test('stale and recovered summaries update the existing handles without changing measurements',()=>{
  const {model,scope,render}=fixture({used:0,total:100,available:0});
  const handle=model.diskNote;
  assert.equal(handle.label,'0 B of 100 B used');
  scope.state.monitor.latest.filesystem_stale=true;
  render();
  assert.equal(handle.label,'Space usage unavailable (stale observation)');
  scope.state.monitor.latest.filesystem_stale=false;
  render();
  assert.equal(model.diskNote,handle);
  assert.equal(handle.label,'0 B of 100 B used');
  assert.equal(scope.state.monitor.latest.filesystem.used.value,0);
});


test('routine reported I/O badges hide, while partial/unavailable status remains visible and recovers',()=>{
 const {model,scope,render}=fixture({diskQuality:'reported',networkQuality:'partial'});
 assert.equal(model.io.disk_io.quality.hidden,true);
 assert.equal(model.io.network.quality.hidden,false);
 assert.equal(model.io.network.quality.label,'partial');
 scope.state.monitor.latest.disk_quality='unavailable';
 scope.state.monitor.latest.network_quality='reported';render();
 assert.equal(model.io.disk_io.quality.hidden,false);
 assert.equal(model.io.disk_io.quality.label,'unavailable');
 assert.equal(model.io.network.quality.hidden,true);
});
