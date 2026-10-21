import {test} from 'node:test';
import assert from 'node:assert/strict';
import {parseJSON, stringifyJSON, count, bytes, mergeHistory, chartPaths} from './core.mjs';

test('exact signed int64 values survive parsing, display and serialization', () => {
  const text = '{"capacity":9223372036854775807,"delta":-9223372036854775808,"safe":9007199254740991,"fraction":0.25,"string":"9223372036854775807"}';
  const value = parseJSON(text);
  assert.equal(value.capacity, 9223372036854775807n);
  assert.equal(value.delta, -9223372036854775808n);
  assert.equal(typeof value.safe, 'number');
  assert.equal(count(value.capacity), '9,223,372,036,854,775,807');
  assert.equal(stringifyJSON(value), text);
});
test('JSON grammar, strings, escaped content and prototype keys are data', () => {
  const values = [null, true, false, ['x', null, {}], {a: '\\"}\n\u0000', b: -5.5e20}];
  for (const value of values) assert.equal(stringifyJSON(parseJSON(JSON.stringify(value))), JSON.stringify(value));
  const data = parseJSON('{"__proto__":{"polluted":true},"constructor":3}');
  assert.equal(Object.getPrototypeOf(data), null);
  assert.equal({}.polluted, undefined);
  for (const text of ['', 'null true', '[1,]', '{"a":1,}', '01', '1e999', '"bad\nstring"', '"\\x"', '{a:1}', '+1', '1.', '1e', '['.repeat(66)+']'.repeat(66)]) assert.throws(() => parseJSON(text), text);
  assert.throws(() => stringifyJSON({a: NaN}));
});
test('history is bounded, cursor-ordered and cleared on epoch/gap', () => {
  const prior = Array.from({length: 60}, (_, i) => ({sequence: i+1}));
  const page = {epoch: 'one', history: [{sequence: 60}, {sequence: 61}]};
  const points = mergeHistory(prior, page, 'one');
  assert.equal(points.length, 60); assert.equal(points[0].sequence, 2); assert.equal(points.at(-1).sequence, 61);
  assert.deepEqual(mergeHistory(prior, page, 'two'), page.history);
  assert.deepEqual(mergeHistory(prior, {...page, gap: true}, 'one'), page.history);
});
test('unknown samples, missing intervals and device resets break graphs', () => {
  const point = (seconds, value) => ({at: new Date(seconds*1000).toISOString(), value});
  assert.equal(chartPaths([point(0, 1), point(2, null), point(4, 2)], p => p.value).length, 2);
  assert.equal(chartPaths([point(0, 1), point(6, 2)], p => p.value).length, 2);
  assert.equal(chartPaths([point(0, 1), point(2, 2)], p => p.value).length, 1);
  assert.equal(bytes(null), 'unavailable');
  assert.equal(bytes(1024), '1 KiB');
});
