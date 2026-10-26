import {test} from 'node:test';
import assert from 'node:assert/strict';
import {emptyGrant,grantFromDraft,reviewIdentities,canReview,phaseLabel,operationAdvice,validHostID,validateMeshReason} from './mesh-model.mjs';
test('default directional grants are summary-only forward and zero reverse; execution requires bounded explicit claims',()=>{
 assert.deepEqual(grantFromDraft({mode:'summary'}).permissions,['observe-host']);assert.deepEqual(grantFromDraft({mode:'none'}),emptyGrant());
 const grant=grantFromDraft({mode:'run',cpu:'2',memory:'1.5',disk:'4',ttl:'60'});assert.deepEqual(grant.permissions,['observe-host','observe-own','lease-own','run']);assert.equal(grant.limits.memory_bytes,1610612736);assert.equal(grant.max_ttl_ns,3600e9);
 for(const change of [{cpu:'NaN'},{ttl:0},{ttl:10081},{memory:-1},{cpu:0,memory:0,disk:0}])assert.throws(()=>grantFromDraft({mode:'leases',cpu:1,memory:1,disk:1,ttl:10,...change}));
});
test('message access names exact scopes and cannot become an implicit broad broadcast grant',()=>{
 assert.throws(()=>grantFromDraft({mode:'summary',messages:true}));const grant=grantFromDraft({mode:'summary',messages:true,contacts:'alpha, beta alpha'});assert.deepEqual(grant.contact_ids,['alpha','beta']);assert.equal(grant.operator_inbox,false);assert.deepEqual(grant.permissions,['observe-host','message','contact-read']);
 assert.equal(grantFromDraft({mode:'summary',messages:true,operatorInbox:true}).operator_inbox,true);
});
test('screen access is a separate directional grant in the reviewed draft',()=>{
 assert.deepEqual(grantFromDraft({mode:'summary',screenView:true}).permissions,['observe-host','screen-view']);
 assert.equal(grantFromDraft({mode:'summary'}).permissions.includes('screen-view'),false);
 assert.equal(grantFromDraft({mode:'run',cpu:1,memory:1,disk:1,ttl:10}).permissions.includes('screen-view'),false);
});
test('concrete review uses prepared identities and approval requires live exact immutable review',()=>{
 const identities=reviewIdentities({local:{identity:{name:'before'}},local_prepared:{identity:{name:'actual'}}});assert.equal(identities[0].identity.name,'actual');
 const op={kind:'connect',phase:'awaiting_review',worker_live:true,review:{operation:'connect-direct-peers'},review_revision:2,review_sha256:'a'.repeat(64),review_expires_at:'2030-01-01T00:00:00Z'};assert.equal(canReview(op,0),true);
 for(const delta of [{worker_live:false},{review:null},{phase:'incomplete'},{review_sha256:'x'},{review_expires_at:null}])assert.equal(canReview({...op,...delta},0),false);
 assert.equal(canReview(op,Date.parse('2030-01-01')),false);
});
test('partial recovery and handoff are never described as cleanup or replay',()=>{
 assert.equal(phaseLabel({phase:'incomplete'}),'Needs attention');assert.match(operationAdvice({phase:'incomplete'}),/partial or unknown/);assert.match(operationAdvice({phase:'handed_off'}),/does not prove cleanup/);assert.match(operationAdvice({phase:'incomplete',resolved_by:'child'}),/original receipt is preserved/);
 assert.equal(validHostID('a'.repeat(32)),true);assert.equal(validHostID('localhost'),false);
});

test('audit reasons use the actual UTF-8 bound and reject whitespace-only purposes before preparation',()=>{assert.equal(validateMeshReason('build on this node'),'build on this node');assert.throws(()=>validateMeshReason('  \n\t'));assert.throws(()=>validateMeshReason('🙂'.repeat(1025)));assert.equal(validateMeshReason('🙂'.repeat(1024)).length,2048);});

test('recovery reviews keep exact bind addresses human-visible alongside advertised identities',()=>{const identities=reviewIdentities({operation:'verify-existing-connection',local:{identity:{host:'local',address:'10.0.0.1:1'}},remote:{identity:{host:'remote',address:'10.0.0.2:2'}},local_listen:'10.0.0.1:1',remote_listen:'10.0.0.2:2'});assert.equal(identities[0].listen,'10.0.0.1:1');assert.equal(identities[1].listen,'10.0.0.2:2');});

test('disconnect identity uses actual peer DTO and deliberate decline has a human outcome',()=>{
 const peer={host:'a'.repeat(32),name:'peer',owner:'501',owner_username:'tester',address:'10.0.0.2:1234',fingerprint:'b'.repeat(64),spki_sha256:'c'.repeat(64),expires_at:'2030-01-01T00:00:00Z'};
 const [item]=reviewIdentities({operation:'disconnect-direct-peer',host:peer});
 assert.equal(item.identity.owner_uid,'501');assert.equal(item.identity.certificate_sha256,peer.fingerprint);assert.equal(item.identity.public_key_sha256,peer.spki_sha256);assert.equal(item.identity.certificate_expires,peer.expires_at);
 assert.equal(phaseLabel({phase:'review_declined'}),'Declining review');assert.equal(phaseLabel({phase:'rejected',error:{code:'confirmation_declined'}}),'Declined');assert.equal(phaseLabel({phase:'rejected',error:{code:'review_expired'}}),'Not applied');
 assert.match(operationAdvice({phase:'awaiting_review',kind:'disconnect'}),/enrollment to revoke/);
});

test('a future provisioning review cannot inherit connect or disconnect approval controls',()=>{const op={kind:'provision',phase:'awaiting_review',worker_live:true,review:{operation:'install-arm'},review_revision:1,review_sha256:'a'.repeat(64),review_expires_at:'2030-01-01'};assert.equal(canReview(op,0),false);assert.equal(canReview({...op,kind:'connect'},0),false);assert.match(operationAdvice({...op,phase:'completed'}),/does not manage/);});
