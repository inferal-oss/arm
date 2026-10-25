import {test} from 'node:test';
import assert from 'node:assert/strict';
import {nodeStopAvailability,stopDraft,stopPolicyLabel,supportedStopReview,stopResultConfirmed,stopResultLabel,stopAdvice} from './mesh-stop.mjs';
import {canReview} from './mesh-model.mjs';
import {parseJSON,stringifyJSON} from './core.mjs';
const clone=value=>parseJSON(stringifyJSON(value));
function entry(){return {host:'b'.repeat(32),local:false,trust:{state:'active'},scope_freshness:{known:true,stale:false},outbound_scope:{target_host:'b'.repeat(32),grant:{permissions:['observe-host','node-stop']},node_stop:{supported:true,reason:'Original backend completion is available.'}}};}
function review(){return {operation:'stop-arm-node',key:'stop-ui',review_sha256:'a'.repeat(64),owner_username:'operator',history_used:4,history_capacity:128,completion_check:{available:true,detail:'The fixed completion check inspects only the original backend.'},plan:{schema:'arm.node-stop.plan/v1',key:'stop-ui',target:{host:'b'.repeat(32),owner:'501',home:'/home/operator/arm',backend_generation:'c'.repeat(32),certificate_sha256:'d'.repeat(64),spki_sha256:'e'.repeat(64),directory:{owner:501,inode:42,device:7},startup_contract:'arm-desired-state-gated-v1'},actor:{source_host:'a'.repeat(32),pair:'f'.repeat(64),grant_revision:7,certificate_sha256:'1'.repeat(64),spki_sha256:'2'.repeat(64)},desired_state_revision:9,policy:{force:false,wait:true},reason:'Finish maintenance',impact:{complete:true,scope_sha256:'3'.repeat(64),outstanding:1,obligations:[{id:'compile-lease',kind:'lease',state:'active',revision:4}],blockers:[],affected_peers:['a'.repeat(32)]},observed_at:'2026-10-25T00:00:00Z',expires_at:'2026-10-25T00:02:00Z',supervisor_scope:'The host stays running. External service-manager ownership is unverified.'}};}
function stopped(){const r=review();const value={operation:'stop-arm-node',key:r.key,status:'stopped',observation:{server_joined:true,original_generation_absent:true,record:{schema:'arm.node-stop.record/v1',phase:'drain-ready',plan:r.plan,review_sha256:r.review_sha256,desired_state_revision:10},desired_state:{schema:'arm.node-stop.state/v1',target:clone(r.plan.target),stop_key:r.key,stop_sha256:r.review_sha256,desired_running:false,revision:10}}};return value;}

test('Stop entry needs explicit fresh destination permission and usable completion capability',()=>{
 assert.equal(nodeStopAvailability(entry()).enabled,true);
 for(const change of [e=>{e.local=true;},e=>{e.trust.state='revoked';},e=>{e.scope_freshness.stale=true;},e=>{e.scope_freshness.known=false;},e=>{e.outbound_scope.grant.permissions=['run'];},e=>{e.outbound_scope.target_host='e'.repeat(32);},e=>{delete e.outbound_scope.node_stop;}]){const e=entry();change(e);assert.equal(nodeStopAvailability(e).enabled,false);}
 const e=entry();e.outbound_scope.node_stop={supported:false,reason:'Current Serve has no completion helper.'};assert.match(nodeStopAvailability(e).reason,/no completion helper/);
});
test('Stop drafts default to refusal and preserve exact timeout without widening to force',()=>{
 const host='b'.repeat(32);assert.deepEqual(stopDraft(host,'maintenance','refuse'),{host,reason:'maintenance',policy:{force:false,wait:false}});
 assert.deepEqual(stopDraft(host,'drain','wait','120').policy,{force:false,wait:true,timeout_ns:120e9});
 const maximum=stopDraft(host,'long wait','wait','9223372036');assert.equal(maximum.policy.timeout_ns,9223372036000000000n);assert.equal(clone(maximum).policy.timeout_ns,maximum.policy.timeout_ns);
 for(const [mode,timeout] of [['force','1'],['refuse','1'],['wait','-1'],['wait','1.5'],['wait','1e2'],['wait','01'],['wait','9223372037']])assert.throws(()=>stopDraft(host,'review',mode,timeout));
 assert.throws(()=>stopDraft('display-name','review','force'));assert.throws(()=>stopDraft(host,' ','refuse'));assert.throws(()=>stopDraft(host,'é'.repeat(2049),'refuse'));
 assert.equal(stopPolicyLabel({force:false,wait:true,timeout_ns:120e9}),'Wait; reclaim ARM-owned work after 2m');assert.match(stopPolicyLabel({force:true,wait:true}),/Unrecognized/);
});
test('Stop approval requires exact complete review and current nested expiry in addition to operation expiry',()=>{
 const r=review();assert.equal(supportedStopReview(r),true);
 const op={key:r.key,kind:'stop',targets:['host:'+r.plan.target.host],phase:'awaiting_review',worker_live:true,approved:false,lookup_only:false,review_revision:3,review_sha256:'a'.repeat(64),review_expires_at:'2026-10-25T00:05:00Z',review:r};
 assert.equal(canReview(op,Date.parse('2026-10-25T00:01:00Z')),true);assert.equal(canReview(op,Date.parse('2026-10-24T23:59:59Z')),false);assert.equal(canReview(op,Date.parse(r.plan.expires_at)),false);
 for(const change of [v=>{v.key='other';},v=>{v.plan.policy.wait=false;},v=>{v.plan.impact.complete=false;},v=>{v.plan.impact.outstanding=2;},v=>{v.plan.impact.blockers=[{class:'unknown',id:'work',detail:'Keep unresolved work.'}];},v=>{v.completion_check.available=false;},v=>{delete v.plan.actor.pair;},v=>{v.plan.policy.force=true;v.plan.policy.wait=true;}]){const v=clone(r);change(v);assert.equal(supportedStopReview(v),false);}
 for(const field of ['lookup_only','approved'])assert.equal(canReview({...op,[field]:true},Date.parse('2026-10-25T00:01:00Z')),false);
});
test('Stopped label needs matching original-generation authority observations rather than completed phase or disconnect',()=>{
 const r=stopped();assert.equal(stopResultConfirmed(r),true);assert.equal(stopResultLabel(r),'Original ARM backend stopped');
 for(const change of [v=>{delete v.observation;},v=>{v.observation.server_joined=false;},v=>{v.observation.original_generation_absent=false;},v=>{v.observation.desired_state.target.backend_generation='0'.repeat(32);},v=>{v.observation.desired_state.stop_key='other';},v=>{v.observation.desired_state.desired_running=true;},v=>{v.observation.desired_state.revision=11;}]){const v=clone(r);change(v);assert.equal(stopResultConfirmed(v),false);assert.equal(stopResultLabel(v),'Stop outcome unknown');}
 const restarted=stopped();restarted.observation.desired_state.desired_running=true;restarted.observation.desired_state.revision=11;assert.equal(stopResultConfirmed(restarted),true);assert.match(stopResultLabel(restarted),/restart requested/);
 assert.equal(stopResultLabel({status:'unknown'}),'Stop outcome unknown');assert.match(stopAdvice({kind:'stop',phase:'completed'}),/closed connection does not prove/);
});
test('Fresh Stop review is a backend-declared no-effect state while approved outcomes remain lookup-only',()=>{
 assert.match(stopAdvice({kind:'stop',can_prepare_fresh:true}),/new key/);assert.match(stopAdvice({kind:'stop',approved:true,can_prepare_fresh:true}),/never be resent/);assert.match(stopAdvice({kind:'stop',key:'stop-ui',review:review(),result:stopped(),lookup_only:true}),/original ARM backend stopped/);
});

test('Positive result is bound to the displayed reviewed generation rather than another self-consistent record',()=>{
 const expected=review(),other=stopped();other.observation.record.plan.target.backend_generation='9'.repeat(32);other.observation.desired_state.target.backend_generation='9'.repeat(32);
 assert.equal(stopResultConfirmed(other),true);assert.equal(stopResultConfirmed(other,expected.key,expected),false);assert.equal(stopResultLabel(other,expected.key,expected),'Stop outcome unknown');
});

test('Stopped result uses one canonical observation record and refuses ambiguous dual forms',()=>{
 const r=stopped();assert.equal(stopResultConfirmed(r),true);
 r.record=clone(r.observation.record);assert.equal(stopResultConfirmed(r),false);assert.equal(stopResultLabel(r),'Stop outcome unknown');
 r.record=null;assert.equal(stopResultConfirmed(r),false);
});
