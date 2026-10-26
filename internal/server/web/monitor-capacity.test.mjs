import {test} from 'node:test';
import assert from 'node:assert/strict';
import {monitorStale, remainingValue} from './monitor-capacity.mjs';

test('remaining uses reported availability, preserving zero and unknown', () => {
  const available = {value: 37};
  assert.equal(remainingValue(available), 37);
  assert.equal(remainingValue({value: 0}), 0);
  assert.equal(remainingValue(available, true), null);
  for (const value of [null, undefined, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
    assert.equal(remainingValue({value}), null);
  }
  assert.equal(remainingValue(null), null);
});

test('held or disconnected monitor pages cannot present current remaining capacity', () => {
  const page = {age_ns: 1e9, stale_after_ns: 6e9, stale: false};
  assert.equal(monitorStale(page, true, 1000), false);
  assert.equal(monitorStale(page, true, 5001), true);
  assert.equal(monitorStale(page, false, 0), true);
  assert.equal(monitorStale({...page, stale: true}, true, 0), true);
  assert.equal(monitorStale(null, true, 0), true);
});
