import test from 'node:test';
import assert from 'node:assert/strict';
import {hostPresentation,detailTime,displayCommand,exactRunRecord} from './detail-presentation.mjs';
const local='a'.repeat(32),remote='b'.repeat(32);
test('host name and Local marker require exact identity, never a selected node or hostname',()=>{
 const hosts=[{host:local,display_name:'Build station'},{host:remote,name:'Remote station',local:true}];
 assert.equal(hostPresentation(local,{localHost:local,hosts}).label,'Build station · Local');
 assert.equal(hostPresentation(remote,{localHost:local,hosts}).label,'Remote station');
 assert.equal(hostPresentation(remote,{selectedHost:remote,hosts}).local,false);
 assert.equal(hostPresentation('localhost',{localHost:'localhost',hosts:[{host:'localhost',display_name:'Same name'}]}).local,false);
 assert.equal(hostPresentation(remote,{localHost:local,hosts:[{host:local,display_name:'Wrong host'}]}).name,'Unknown host');
 assert.equal(hostPresentation('',{localHost:local,hosts}).local,false);
 assert.equal(hostPresentation(remote,{hosts:[{host:remote}]}).name,'Unnamed host');
});
test('human timing excludes absent, invalid and year-one values without altering exact record',()=>{
 for(const at of [undefined,null,'','broken','0001-01-01T00:00:00Z'])assert.equal(detailTime(at),'Not recorded');
 const raw='2026-10-29T12:34:56.123456789Z';assert.match(detailTime(raw,'Not recorded','en-US'),/2026/);assert.ok(!detailTime(raw).includes('T12:'));
 const record={created_at:raw,last_reported_at:'0001-01-01T00:00:00Z',log_bytes:9007199254740993n};assert.match(exactRunRecord(record),/9007199254740993/);assert.match(exactRunRecord(record),/123456789Z/);assert.match(exactRunRecord(record),/0001-01-01/);
 assert.equal(displayCommand(['/tools/go','test','a b']),'/tools/go test "a b"');
});
