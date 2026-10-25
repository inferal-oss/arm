import {test} from 'node:test';
import assert from 'node:assert/strict';
import {nodeUninstallAvailability,uninstallDraft,supportedUninstallReview,assertUninstallOperation,uninstallConfirmed,uninstallFinishAllowed,uninstallLabel} from './mesh-uninstall.mjs';
import {canReview} from './mesh-model.mjs';

function fixture(){
 const now=Date.now(),id={device:7,inode:42,owner:501,born_seconds:1700000000,born_nanoseconds:5,birth_known:true},host='b'.repeat(32),key='original-uninstall';
 const installation={install_root:'/private/install',key:'1'.repeat(64),sha256:'2'.repeat(64),size_bytes:1234,expected_uid:'501',os:'darwin',arch:'arm64',expected_arm_version:'dev',expected_lifecycle_protocol:1,reason:'original installation'};
 const plan={schema:'arm.node-stop.plan/v1',key:'uninstall-'+'3'.repeat(64),reason:'Remove the original managed binary',target:{host,owner:'501',home:'/private/state',backend_generation:'4'.repeat(32),certificate_sha256:'5'.repeat(64),spki_sha256:'6'.repeat(64)},actor:{source_host:'a'.repeat(32),certificate_sha256:'7'.repeat(64),spki_sha256:'8'.repeat(64),pair:'9'.repeat(64),grant_revision:3},policy:{force:false,wait:false},desired_state_revision:1,impact:{complete:true,scope_sha256:'a'.repeat(64),outstanding:0,obligations:[],blockers:[],affected_peers:[]},observed_at:new Date(now).toISOString(),expires_at:new Date(now+120000).toISOString()};
 const review={schema:'arm.node-uninstall/v1',key,reason:plan.reason,installation,ownership:{schema:1,transfer:structuredClone(installation),root_identity:id,artifact_identity:{...id,inode:43},binary_identity:{...id,inode:44},referenced_homes:[plan.target.home]},stop:{plan,review_sha256:'b'.repeat(64),owner_username:'operator'},retained_recovery_helper:'/private/install/recovery-'+'c'.repeat(64)+'/arm',preserved:['database','node identity and grants','configuration and startup policy','lease and operation history','backups and retained artifacts','companion catalog configuration','recovery helper and uninstall receipt']};
 const op={key,kind:'uninstall',targets:['host:'+host],revision:4,phase:'awaiting_review',worker_live:true,approved:false,lookup_only:false,can_prepare_fresh:false,review_revision:3,review_sha256:'d'.repeat(64),review_expires_at:plan.expires_at,review};
 const node={host,local:false,trust:{state:'active'},scope_freshness:{known:true,stale:false},outbound_scope:{target_host:host,grant:{permissions:['node-stop','node-uninstall']},node_stop:{supported:true}}};
 return{op,node,id,now};
}
test('uninstall requires separate fresh destination Stop and uninstall grants',()=>{
 const{node}=fixture();assert.equal(nodeUninstallAvailability(node).enabled,true);node.outbound_scope.grant.permissions=['node-stop'];assert.equal(nodeUninstallAvailability(node).enabled,false);node.outbound_scope.grant.permissions=['node-uninstall'];assert.equal(nodeUninstallAvailability(node).enabled,false);node.outbound_scope.grant.permissions=['node-stop','node-uninstall'];node.scope_freshness.stale=true;assert.equal(nodeUninstallAvailability(node).enabled,false);
});
test('uninstall draft selects exact provisioning key and retains safe Stop policy',()=>{
 const{node}=fixture();assert.deepEqual(uninstallDraft(node.host,'original','purpose'),{host:node.host,provision_key:'original',reason:'purpose',policy:{force:false,wait:false}});assert.throws(()=>uninstallDraft(node.host,'','purpose'));assert.throws(()=>uninstallDraft(node.host,'original',' '));assert.throws(()=>uninstallDraft(node.host,'original','purpose','force','5'));assert.equal(uninstallDraft(node.host,'original','purpose','wait','9007199').policy.timeout_ns,9007199000000000);
});
test('uninstall review requires exact ownership, one home, preservation and complete work census',()=>{
 const{op,now}=fixture();assert.equal(supportedUninstallReview(op.review),true);assert.equal(canReview(op,now),true);
 for(const mutate of [p=>p.ownership.referenced_homes.push('/private/another'),p=>p.ownership.binary_identity.birth_known=false,p=>p.ownership.transfer.sha256='f'.repeat(64),p=>p.preserved.pop(),p=>p.stop.plan.impact.complete=false,p=>p.installation.expected_uid='502',p=>p.retained_recovery_helper='/other/recovery-'+'c'.repeat(64)+'/arm']){const p=structuredClone(op.review);mutate(p);assert.equal(supportedUninstallReview(p),false);}
 op.review.stop.plan.impact.obligations=[{id:'lease:one'}];op.review.stop.plan.impact.outstanding=1;assert.equal(canReview(op,now),false);op.review.stop.plan.policy.wait=true;assert.equal(canReview(op,now),true);op.approved=true;assert.equal(canReview(op,now),false);
});
test('uninstall responses cannot replace exact host key or immutable review',()=>{
 const{op,node}=fixture();assert.equal(assertUninstallOperation(op,op.key,node.host),op);assert.throws(()=>assertUninstallOperation(op,'other',node.host));assert.throws(()=>assertUninstallOperation(op,op.key,'f'.repeat(32)));const snapshot={plan:structuredClone(op.review),hash:op.review_sha256,revision:op.review_revision};op.review.stop.plan.target.backend_generation='e'.repeat(32);assert.throws(()=>assertUninstallOperation(op,op.key,node.host,snapshot));op.can_prepare_fresh=true;assert.throws(()=>assertUninstallOperation(op,op.key,node.host));
});
test('provisional review metadata can advance without changing its immutable commitment',()=>{
 const{op,node}=fixture();const snapshot={plan:structuredClone(op.review),hash:op.review_sha256,revision:op.review_revision,provisional:true};op.revision+=2;op.review_revision++;
 assert.equal(assertUninstallOperation(op,op.key,node.host,snapshot),op);
 for(const mutate of [o=>o.review_sha256='f'.repeat(64),o=>o.review.reason='changed purpose',o=>o.review_revision=1,o=>o.review_revision=o.revision+1]){const changed=structuredClone(op);mutate(changed);assert.throws(()=>assertUninstallOperation(changed,op.key,node.host,snapshot));}
 assert.throws(()=>assertUninstallOperation(op,op.key,node.host,{...snapshot,provisional:false}));
});
test('uninstall completion requires exact removed receipt and independent positive observations',()=>{
 const{op,id}=fixture();op.approved=true;op.lookup_only=true;op.phase='incomplete';op.result={receipt:{schema:'arm.node-uninstall/v1',plan:structuredClone(op.review),review_sha256:op.review_sha256,phase:'removed',recovery_identity:{...id,inode:45}},original_stop_observed:true,artifact_absent:true};assert.equal(uninstallConfirmed(op),true);assert.equal(uninstallLabel(op),'Managed ARM executable removed');
 for(const mutate of [o=>delete o.key,o=>delete o.review,o=>delete o.review_sha256,o=>o.approved=false,o=>o.review.stop.plan.target.host='f'.repeat(32),o=>o.result.receipt.phase='remove-intent',o=>o.result.original_stop_observed=false,o=>o.result.artifact_absent=false,o=>o.result.receipt.review_sha256='e'.repeat(64),o=>o.result.receipt.plan.stop.plan.target.backend_generation='f'.repeat(32),o=>o.result.receipt.recovery_identity.birth_known=false]){const other=structuredClone(op);mutate(other);assert.equal(uninstallConfirmed(other),false);}
});
test('explicit finish requires an approved settled source and verified original recovery identity',()=>{
 const{op,id}=fixture();op.approved=true;op.lookup_only=true;op.phase='incomplete';op.worker_live=false;op.result={receipt:{schema:'arm.node-uninstall/v1',plan:structuredClone(op.review),review_sha256:op.review_sha256,phase:'stop-unknown',recovery_identity:id},original_stop_observed:true,artifact_absent:false};assert.equal(uninstallFinishAllowed(op),true);const malformed=structuredClone(op);delete malformed.result.receipt.schema;assert.equal(uninstallFinishAllowed(malformed),false);delete malformed.key;assert.equal(uninstallFinishAllowed(malformed),false);op.result.artifact_absent=true;assert.equal(uninstallFinishAllowed(op),false);op.result.artifact_absent=false;op.worker_live=true;assert.equal(uninstallFinishAllowed(op),false);op.worker_live=false;op.result.receipt.phase='copy-unknown';assert.equal(uninstallFinishAllowed(op),false);op.result.receipt.phase='accepted';op.result.receipt.recovery_identity={};assert.equal(uninstallFinishAllowed(op),false);
});
