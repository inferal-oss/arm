import test from 'node:test';
import assert from 'node:assert/strict';
import {leaseStateLabel, isInactiveLease, leaseVisible, readHideInactive, saveHideInactive, HIDE_INACTIVE_KEY} from './lease-state.mjs';

test('terminal lifecycle uses recorded outcome without guessing from process exit', () => {
  for (const [outcome, label] of Object.entries({released:'Released',cancelled:'Cancelled',expired:'Expired',completed:'Completed',failed:'Failed',launch_failed:'Failed',admission_failed:'Failed',revoked:'Revoked',handed_off:'Handed off',interrupted:'Interrupted',reconciled:'Reconciled'})) {
    assert.equal(leaseStateLabel({state:'terminal',outcome}),label);
  }
  for (const exit_code of [0,1,-1]) assert.equal(leaseStateLabel({state:'terminal',outcome:'released',exit_code}),'Released');
  assert.equal(leaseStateLabel({state:'terminal'}),'Inactive');
  assert.equal(leaseStateLabel({state:'terminal',outcome:'future_outcome'}),'Future outcome');
});

test('only terminal records are hidden; outcomes cannot hide unsettled or unknown lifecycles', () => {
  for (const state of ['active','queued','provisioning','ending','reclaiming','needs_attention','future_state','constructor','toString','__proto__','',null,undefined]) {
    const lease={state,outcome:'released'};
    assert.equal(isInactiveLease(lease),false);
    assert.equal(leaseVisible(lease,true),true);
    assert.notEqual(leaseStateLabel(lease),'Released');
  }
  assert.equal(leaseVisible(null,true),true);
  assert.equal(leaseStateLabel(null),'Unknown state');
  assert.equal(leaseVisible({state:'terminal',outcome:'released'},true),false);
  assert.equal(leaseVisible({state:'terminal'},false),true);
  assert.equal(leaseStateLabel({state:'needs_attention',outcome:'expired'}),'Needs attention');
});

test('hide-inactive preference survives a fresh reader and fails safely when storage is unavailable', () => {
  const values=new Map(),storage={getItem:key=>values.get(key),setItem:(key,value)=>values.set(key,value)};
  assert.equal(readHideInactive(storage),true);
  assert.equal(saveHideInactive(storage,true),true);
  assert.equal(values.get(HIDE_INACTIVE_KEY),'true');
  assert.equal(readHideInactive(storage),true);
  assert.equal(saveHideInactive(storage,false),true);
  assert.equal(readHideInactive(storage),false);
  for (const value of ['TRUE','1','yes','broken',null]) { values.set(HIDE_INACTIVE_KEY,value); assert.equal(readHideInactive(storage),true); }
  const blocked={getItem(){throw Error('blocked')},setItem(){throw Error('quota')}};
  assert.equal(readHideInactive(blocked),true);
  assert.equal(saveHideInactive(blocked,true),false);
  assert.equal(readHideInactive(),true);
  assert.equal(saveHideInactive(undefined,true),false);
});
