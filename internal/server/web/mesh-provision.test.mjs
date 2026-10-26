import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {provisionKey,provisionDraft,supportedProvisionReview,assertProvisionOperation,assertProvisionPreparation,provisionConfirmed,provisionLabel,provisionAdvice} from './mesh-provision.mjs';

function fixture(){
 const key='a'.repeat(64),image='b'.repeat(64),host='c'.repeat(32);
 const transfer={install_root:'/home/operator/.local/share/arm/provisioned',key,sha256:image,size_bytes:4096,expected_uid:'1000',os:'linux',arch:'arm64',expected_arm_version:'0.1.0-dev',expected_lifecycle_protocol:1,reason:'Build on a disposable node'};
 const startup={schema:1,resources:{cpu:2,memory_bytes:0,disk_bytes:0},listen:'127.0.0.1:0'};
 const request={connection_key:'connect-original',ssh_destination:'build-host',transfer,ssh_identity:{account:'operator',host_key:'ssh-ed25519 SHA256:pinned'},remote_home:'/home/operator/.local/share/arm',startup};
 const review={operation:'install-and-start-arm-node',key,request,artifact_source:'verified local catalog',final_binary:transfer.install_root+'/artifact-'+image+'/arm',effect:'Install these exact user-level bytes. Review mesh trust separately.'};
 const op={key,kind:'provision',revision:7,phase:'completed',approved:true,worker_live:false,provision_connection_ready:true,targets:['ssh:build-host'],review,review_revision:3,review_sha256:'d'.repeat(64)};
 op.result={provisioned:review.final_binary,key,connection_key:request.connection_key,verified_at:new Date().toISOString(),reused:false,receipt:{schema:1,request:structuredClone(transfer),phase:'started_verified',binary:review.final_binary,actual_arm_version:transfer.expected_arm_version,actual_lifecycle_protocol:1,started_host:host,start_home:request.remote_home,startup:structuredClone(startup),runtime:{host,uid:'1000',sha256:image,size_bytes:4096,arm_version:transfer.expected_arm_version,lifecycle_protocol:1,method:'kernel-self-executable',captured_at:new Date().toISOString()}}};
 return{op,review,request,transfer};
}

test('provision editor derives exact native key and sends only locator and purpose',async()=>{
 const expected=createHash('sha256').update('arm-node-provision:connection-original').digest('hex');
 assert.equal(await provisionKey('connection-original'),expected);
 assert.deepEqual(await provisionDraft('connection-original',{destination:'operator@build-host',reason:'Use spare capacity'}),{key:expected,provision:{connection_key:'connection-original',ssh_destination:'operator@build-host',reason:'Use spare capacity'}});
 assert.deepEqual((await provisionDraft('connection-original',{connector:'native-profile',reason:'Use spare capacity'})).provision,{connection_key:'connection-original',connector:'native-profile',reason:'Use spare capacity'});
 for(const draft of [{reason:'purpose'},{destination:'host',connector:'profile',reason:'purpose'},{destination:'-oProxyCommand=bad',reason:'purpose'},{destination:'host command',reason:'purpose'},{destination:'host',reason:' '},{destination:'host',reason:'x\nunsafe'}])await assert.rejects(provisionDraft('connection-original',draft));
 await assert.rejects(provisionKey(''));
});

test('installation review binds software platform paths startup and exact purpose',()=>{
 const{review}=fixture();assert.equal(supportedProvisionReview(review),true);
 for(const mutate of [p=>p.operation='execute-arbitrary-command',p=>p.final_binary='/other/arm',p=>p.request.transfer.key='e'.repeat(64),p=>p.request.transfer.os='unknown',p=>p.request.transfer.expected_uid='01000',p=>p.request.startup.listen='0.0.0.0:9000',p=>p.request.startup.resources.cpu=Infinity,p=>p.request.remote_home='/home/operator/../outside',p=>p.request.transfer.reason='']){const changed=structuredClone(review);mutate(changed);assert.equal(supportedProvisionReview(changed),false);}
});

test('companion seed requires the complete ordered platform set and matching selected image',()=>{
 const{review,transfer}=fixture();const platforms=[['darwin','amd64'],['darwin','arm64'],['linux','amd64'],['linux','arm64']];
 review.request.seed_catalog={schema:1,manifest_sha256:'e'.repeat(64),manifest:{schema:1,expected_arm_version:transfer.expected_arm_version,expected_lifecycle_protocol:1,integrity_scope:'reviewed local bytes',artifacts:platforms.map(([os,arch])=>({name:`arm-${os}-${arch}`,os,arch,go_version:'go1.27.3',size_bytes:4096,sha256:transfer.sha256,compressed_size_bytes:2048,compressed_sha256:'f'.repeat(64)}))}};
 assert.equal(supportedProvisionReview(review),true);
 for(const mutate of [p=>p.request.seed_catalog.manifest.artifacts.pop(),p=>p.request.seed_catalog.manifest.artifacts.reverse(),p=>p.request.seed_catalog.manifest.artifacts[3].sha256='0'.repeat(64),p=>p.request.seed_catalog.manifest.expected_arm_version='other']){const changed=structuredClone(review);mutate(changed);assert.equal(supportedProvisionReview(changed),false);}
});

test('installation observations retain key destination plan hash and ready decision revision',()=>{
 const{op}=fixture();assert.equal(assertProvisionOperation(op,op.key),op);
 const reference={plan:structuredClone(op.review),hash:op.review_sha256,revision:op.review_revision};
 for(const mutate of [o=>o.kind='connect',o=>o.key='e'.repeat(64),o=>o.targets=['ssh:other-host'],o=>o.review_sha256='e'.repeat(64),o=>o.review.request.ssh_identity.host_key='another key',o=>o.review_revision++]){const changed=structuredClone(op);mutate(changed);assert.throws(()=>assertProvisionOperation(changed,op.key,reference));}
 const ready=structuredClone(op);ready.review_revision++;assert.equal(assertProvisionOperation(ready,op.key,{...reference,provisional:true}),ready);
});

test('source completion cannot borrow another artifact runtime or an unresolved owner',()=>{
 const{op}=fixture();assert.equal(provisionConfirmed(op),true);assert.equal(provisionLabel(op),'ARM installation completed');assert.match(provisionAdvice(op),/Installation grants no peer trust/);
 for(const mutate of [o=>o.approved=false,o=>o.phase='incomplete',o=>o.worker_live=true,o=>o.provision_connection_ready=false,o=>o.error={code:'source_owner_unresolved'},o=>o.result.connection_key='other',o=>o.result.receipt.request.sha256='e'.repeat(64),o=>o.result.receipt.runtime.host='e'.repeat(32),o=>o.result.receipt.runtime.uid='0',o=>o.result.receipt.runtime.sha256='e'.repeat(64),o=>o.result.receipt.runtime.size_bytes++,o=>o.result.receipt.runtime.method='unverified-path',o=>o.result.receipt.phase='published']){const changed=structuredClone(op);mutate(changed);assert.equal(provisionConfirmed(changed),false);assert.equal(provisionLabel(changed),'Installation outcome needs inspection');}
});

test('decline and uncertain installation descriptions never imply a trusted connection',()=>{
 const{op}=fixture();op.phase='rejected';op.approved=false;op.error={code:'confirmation_declined'};assert.equal(provisionLabel(op),'Installation declined · unchanged');
 op.approved=true;op.lookup_only=true;op.phase='incomplete';assert.match(provisionAdvice(op),/never automatically repeated/);assert.equal(provisionConfirmed(op),false);
});

test('installation completion requires bounded recorded source and decision revisions',()=>{
 const{op}=fixture();for(const mutate of [o=>delete o.revision,o=>o.revision=0,o=>o.revision=Number.MAX_SAFE_INTEGER+1,o=>delete o.review_revision,o=>o.review_revision=0,o=>o.review_revision=o.revision+1,o=>o.review_revision=Number.MAX_SAFE_INTEGER+1]){const changed=structuredClone(op);mutate(changed);assert.equal(provisionConfirmed(changed),false);assert.throws(()=>assertProvisionOperation(changed,op.key));}
});

test('installation preparation and its later review retain original intent before a plan exists',()=>{
 const{op,request}=fixture(),q={key:op.key,provision:{connection_key:request.connection_key,ssh_destination:request.ssh_destination,reason:request.transfer.reason}};assert.equal(assertProvisionPreparation(op,q),op);
 for(const change of [d=>d.provision.ssh_destination='another-host',d=>d.provision.connection_key='another-key',d=>d.provision.reason='another purpose']){const altered=structuredClone(q);change(altered);assert.throws(()=>assertProvisionPreparation(op,altered));}
 const waiting={...op,phase:'preparing',approved:false,review:null};assert.equal(assertProvisionOperation(op,op.key,{target:waiting.targets[0],plan:null}),op);const moved=structuredClone(op);moved.targets=['ssh:another-host'];moved.review.request.ssh_destination='another-host';assert.throws(()=>assertProvisionOperation(moved,op.key,{target:waiting.targets[0],plan:null}));
});

test('history summaries and in-flight installation labels distinguish recorded results from current verification',()=>{
 const{op}=fixture();const summary={...op,has_result:true};delete summary.review;delete summary.result;assert.equal(provisionConfirmed(summary),false);assert.equal(provisionLabel(summary),'Installation result recorded');
 const applying={...op,phase:'applying',worker_live:true};assert.equal(provisionLabel(applying),'Installing reviewed ARM node');assert.match(provisionAdvice(applying),/workflow is running/);assert.equal(provisionConfirmed(applying),false);
 assert.equal(provisionLabel({...applying,error:{code:'unknown'}}),'Installation outcome needs inspection');assert.equal(provisionLabel({...applying,approved:false,phase:'review_declined'}),'Keeping host unchanged');
 assert.doesNotMatch(provisionLabel(op),/connection needs review/);assert.doesNotMatch(provisionAdvice(op),/node is available/);
});
