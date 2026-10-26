import test from 'node:test';
import assert from 'node:assert/strict';
import {createPeerPollGate} from './peer-poll.mjs';
test('first connected resource view polls immediately and coalesces', () => {
 const gate=createPeerPollGate(), a={};gate.reset(a);
 assert.equal(gate.begin(a,1),true);assert.equal(gate.begin(a,2),false);
 assert.equal(gate.finish(a,5),true);assert.equal(gate.begin(a,10004),false);assert.equal(gate.begin(a,10005),true);
});
test('old session completion cannot release or delay replacement request', () => {
 const gate=createPeerPollGate(), a={},b={};gate.reset(a);assert.equal(gate.begin(a,1),true);
 gate.reset(b);assert.equal(gate.begin(b,2),true);assert.equal(gate.finish(a,500),false);
 assert.equal(gate.begin(b,501),false);assert.equal(gate.finish(b,502),true);
 assert.equal(gate.begin(a,20000),false);assert.equal(gate.begin(b,10502),true);
});
