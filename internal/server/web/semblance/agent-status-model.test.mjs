import test from 'node:test';
import assert from 'node:assert/strict';
import {agentResources,sameAgentIdentity,participationLabel} from './agent-status-model.mjs';
import {createAgentReportWidget} from './agent-report-widget.mjs';

test('recycled labels and foreign hosts do not confer ownership',()=>{
 const a={host:'local',id:'builder',session:'first',registration:'one'},b={...a,session:'second',registration:'two'};
 assert.equal(sameAgentIdentity(a,b),false);
 const result=agentResources(a,{leases:[{id:'legacy',host:'local',request:{agent:'builder'}},{id:'other',host:'foreign',request:{agent:'builder'}},{id:'injected',host:'local',request:{agent:'builder',agent_identity:a}}],runs:[{id:'run',host:'local',agent:'builder',agent_identity:b}]});
 assert.deepEqual(result.leases.map(x=>[x.record.id,x.association]),[['legacy','legacy'],['injected','legacy']]);
 assert.deepEqual(result.runs.map(x=>[x.record.id,x.association]),[['run','legacy']]);
});
test('expiry is uncertain activity, never proof of exit',()=>{
 assert.match(participationLabel({state:'awaiting_reply',reply_deadline:'2020-01-01'},Date.now()),/activity unknown/);
});
test('report island has no sandbox powers and revokes every owned blob',()=>{
 const revoked=[],attrs=new Map(),element={ownerDocument:{createElement:()=>({setAttribute:(k,v)=>attrs.set(k,v),removeAttribute:k=>attrs.delete(k),remove(){}})},append(frame){this.frame=frame;}};
 let serial=0;const report=createAgentReportWidget('<p>One</p>',{URL:{createObjectURL:()=>`blob:${++serial}`,revokeObjectURL:url=>revoked.push(url)},Blob:class {}}),mounted=report.widget.mount(element);
 assert.equal(attrs.get('sandbox'),'');assert.equal(attrs.get('allow'),'');assert.equal(element.frame.src,'blob:1');
 report.update('<p>Two</p>');assert.deepEqual(revoked,['blob:1']);
 mounted.destroy();assert.deepEqual(revoked,['blob:1','blob:2']);
});
