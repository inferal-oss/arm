import test from 'node:test';
import assert from 'node:assert/strict';
import {activeRun,runStatus,compactRunText,runCommand,runPurpose,orderRuns,runWhen} from './run-list.mjs';
const now=Date.parse('2026-10-28T12:00:00Z'),run={id:'r',state:'active',command:['/opt/tools/bin/go','test','./...'],reason:'Run unit tests',created_at:'2026-10-28T11:58:00Z'};
test('status preserves unfinished cleanup and reporter-loss uncertainty',()=>{
 for(const state of ['queued','provisioning','ending','reclaiming','needs_attention']){const r={...run,state,outcome:'expired'};assert.equal(activeRun(r),true);assert.notEqual(runStatus(r).label,'Expired');}
 assert.deepEqual(runStatus({...run,state:'ending',outcome:'cancelled'}),{label:'Stopping',tone:'warning'});
 assert.deepEqual(runStatus({...run,state:'interrupted',outcome:'reporter_lost'}),{label:'Reporter lost',tone:'warning'});
 assert.equal(runStatus({...run,state:'terminal',exit_code:2}).label,'Failed');assert.equal(runStatus({...run,state:'terminal',outcome:'completed',exit_code:0}).label,'Completed');
});
test('compact commands and purposes omit executable paths and bound unbroken text safely',()=>{
 assert.equal(runCommand(run),'go test ./...');assert.equal(runPurpose(run),'Run unit tests');
 const command=runCommand({...run,command:['/opt/bin/tool','/private/'+ 'long/'.repeat(30)+'source.file','x'.repeat(400)]});assert.ok(command.startsWith('tool …/source.file'));assert.ok(Array.from(command).length<=100);
 assert.equal(compactRunText('bad\x1b\ntext'),'bad text');assert.equal(compactRunText('🙂'.repeat(8),5),'🙂🙂🙂🙂…');assert.equal(runPurpose({...run,reason:''}),'go test ./...');
});
test('active runs precede recent history with stable identities and creation ordering',()=>{
 const rows=[{...run,id:'past',state:'terminal',created_at:'2026-10-28T11:59:00Z'},{...run,id:'old-active',created_at:'2026-10-27T10:00:00Z'},{...run,id:'new-active'}];assert.deepEqual(orderRuns(rows).map(x=>x.id),['new-active','old-active','past']);assert.equal(rows[0].id,'past');
});
test('friendly elapsed and age labels keep raw timestamps out of the scan list',()=>{
 assert.equal(runWhen(run,now),'2m elapsed');assert.equal(runWhen({...run,state:'terminal',ended_at:'2026-10-28T11:59:00Z'},now),'1m ago');assert.equal(runWhen({...run,state:'terminal',ended_at:'2026-10-28T11:59:58Z'},now),'just now');assert.match(runWhen({...run,state:'terminal',ended_at:'2026-10-26T11:59:58Z'},now,'en-US'),/Oct 26/);assert.equal(runWhen({...run,created_at:null},now),'Time unavailable');
});
test('capture failure is not success and uncertain reporters remain ahead of history',()=>{
 assert.deepEqual(runStatus({...run,state:'terminal',outcome:'log_failed',exit_code:0}),{label:'Log failed',tone:'warning'});
 const rows=[{...run,id:'finished',state:'terminal',created_at:'2026-10-28T11:59:00Z'},{...run,id:'lost',state:'interrupted',outcome:'reporter_lost',created_at:'2026-10-27T11:59:00Z'}];assert.deepEqual(orderRuns(rows).map(x=>x.id),['lost','finished']);assert.ok(!runWhen(rows[1],now).includes('elapsed'));
});

test('terminal start deadline rejection remains explicitly Rejected',()=>{assert.equal(runStatus({...run,state:'terminal',outcome:'rejected',exit_code:null}).label,'Rejected');});
