import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseJSON, stringifyJSON } from './core.mjs';
import {
  poolInteger,
  preparePoolMutation,
  MAX_COUNT,
  matchingPoolChange,
} from './pool-model.mjs';
import { createPoolOperations } from './pool-operations.mjs';
const view = () => ({
  pool: {
    id: 'pool-fixture',
    name: 'Slots',
    scope: 'global',
    authority: 'host-a',
    unit: 'permits',
    capacity: MAX_COUNT,
    revision: 7,
    state: 'active',
  },
  reserved: MAX_COUNT,
  available: 0,
  deficit: 0,
  users: [],
  queued: [],
  review: { authority: 'host-a', consumers: [{ id: 'lease-a', revision: 4 }] },
});
const plan = () =>
  preparePoolMutation(
    {
      operation: 'set',
      capacity: '0',
      reason: 'Capacity temporarily unavailable',
    },
    view(),
    'host-a',
    'original-key'
  );
const result = (r) => ({
  sequence: 1,
  idempotency_key: r.idempotency_key,
  operation: r.operation,
  reason: r.reason,
  before: { ...view().pool },
  after: { ...view().pool, capacity: r.capacity ?? MAX_COUNT, revision: 8 },
});
function storage() {
  let value = null;
  return {
    getItem: () => value,
    setItem: (_, v) => {
      value = v;
    },
  };
}
test('pool reviews preserve full int64 counts, explicit deficit and immutable complete scope', () => {
  const source = view(),
    p = preparePoolMutation(
      { operation: 'set', capacity: '0', reason: 'needed' },
      source,
      'host-a',
      'key'
    );
  assert.equal(p.deficit, MAX_COUNT);
  assert.equal(p.reserved, MAX_COUNT);
  assert.equal(p.capacity, 0n);
  assert.match(p.paragraphs.join(' '), /Existing leases keep/);
  assert.match(p.paragraphs.join(' '), /one authoritative ledger/);
  source.review.consumers.length = 0;
  assert.equal(p.request.reviewed.consumers.length, 1);
  const encoded = stringifyJSON(p);
  assert.match(encoded, /9223372036854775807/);
  assert.equal(parseJSON(encoded).reserved, MAX_COUNT);
  for (const invalid of [
    '1.1',
    '1e3',
    '0x10',
    '9223372036854775808',
    '-1',
    '',
    Number(MAX_COUNT),
  ])
    assert.throws(() => poolInteger(invalid));
  assert.equal(
    poolInteger('-9223372036854775808', { signed: true, nonzero: true }),
    -9223372036854775808n
  );
  assert.throws(() =>
    preparePoolMutation(
      { operation: 'adjust', delta: '1', reason: 'overflow' },
      view(),
      'host-a',
      'key'
    )
  );
});
test('create normalizes exact canonical wire fields and rejects incomplete/foreign/oversize reviews', () => {
  const p = preparePoolMutation(
    {
      operation: 'create',
      name: '  New slots ',
      scope: 'host',
      unit: 'count',
      capacity: String(MAX_COUNT),
      reason: 'new',
    },
    null,
    'host-a',
    'key'
  );
  assert.equal(p.request.host, 'host-a');
  assert.equal(p.request.name, 'New slots');
  assert.equal(p.request.description, undefined);
  assert.equal(p.request.capacity, MAX_COUNT);
  assert.deepEqual(p.request.reviewed.consumers, []);
  for (const altered of [
    { ...view(), review: null },
    { ...view(), review: { authority: 'other', consumers: [] } },
    {
      ...view(),
      review: {
        authority: 'host-a',
        consumers: Array.from({ length: 8193 }, (_, i) => ({
          id: String(i),
          revision: 1,
        })),
      },
    },
  ])
    assert.throws(() =>
      preparePoolMutation(
        { operation: 'set', capacity: '0', reason: 'change' },
        altered,
        'host-a',
        'key'
      )
    );
});
test('unknown pool operation survives reload; lookup no-result and same-key retry never change request', async () => {
  const calls = [],
    s = storage(),
    ops = createPoolOperations({
      storage: s,
      authority: () => 'host-a',
      call: async (op, r) => {
        calls.push([op, r]);
        throw Error('lost acknowledgement');
      },
    });
  const p = plan(),
    op = await ops.submit(p.request);
  assert.equal(op.state, 'unknown');
  assert.equal(calls.length, 1);
  p.request.reason = 'caller edited';
  assert.notEqual(op.request.reason, p.request.reason);
  const nextCalls = [],
    restored = createPoolOperations({
      storage: s,
      authority: () => 'host-a',
      call: async (method, r) => {
        nextCalls.push([method, r]);
        return method === 'pool_lookup' ? { found: false } : result(r);
      },
    });
  const restoredOp = restored.operations[0];
  assert.equal(nextCalls.length, 0);
  await restored.lookup(restoredOp);
  assert.equal(restoredOp.state, 'unknown');
  await restored.retry(restoredOp);
  assert.equal(restoredOp.state, 'recorded');
  assert.equal(nextCalls[1][1].idempotency_key, 'original-key');
  assert.equal(nextCalls[1][1].capacity, 0);
  assert.equal(nextCalls.length, 2);
});
test('pending send/lookup bounded, prior uncertainty survives preflight error, foreign authority cannot replay', async () => {
  let finish;
  const calls = [],
    ops = createPoolOperations({
      storage: storage(),
      authority: () => 'host-a',
      call: (method, r) => {
        calls.push([method, r]);
        return new Promise((resolve) => (finish = resolve));
      },
    });
  const pending = ops.submit(plan().request),
    op = ops.operations[0];
  await ops.lookup(op);
  await ops.retry(op);
  assert.equal(calls.length, 1);
  finish(result(op.request));
  await pending;
  assert.equal(op.state, 'recorded');
  const unknown = createPoolOperations({
    storage: storage(),
    authority: () => 'host-a',
    call: async () => {
      throw Object.assign(Error('stale'), { code: 'review_conflict' });
    },
  });
  const rejected = await unknown.submit(plan().request);
  assert.equal(rejected.state, 'rejected');
  const s = storage(),
    first = createPoolOperations({
      storage: s,
      authority: () => 'host-a',
      call: async () => {
        throw Error('lost');
      },
    });
  await first.submit(plan().request);
  const restored = createPoolOperations({
    storage: s,
    authority: () => 'host-a',
    call: async () => {
      throw Object.assign(Error('backend stopping'), {
        code: 'backend_stopping',
      });
    },
  });
  await restored.retry(restored.operations[0]);
  assert.equal(restored.operations[0].state, 'unknown');
  const foreign = createPoolOperations({
    storage: s,
    authority: () => 'host-b',
    call: () => assert.fail('cross-authority replay'),
  });
  await foreign.lookup(foreign.operations[0]);
  await foreign.retry(foreign.operations[0]);
  assert.match(foreign.operations[0].error, /saved pool authority/);
});
test('lookup requires exact stored request and matching result, never accepts a mismatched key or capacity', async () => {
  const s = storage(),
    ops = createPoolOperations({
      storage: s,
      authority: () => 'host-a',
      call: async () => {
        throw Error('lost');
      },
    });
  await ops.submit(plan().request);
  const restored = createPoolOperations({
    storage: s,
    authority: () => 'host-a',
    call: async () => ({
      found: true,
      request: { ...plan().request, reason: 'other' },
      change: result(plan().request),
    }),
  });
  await restored.lookup(restored.operations[0]);
  assert.equal(restored.operations[0].state, 'unknown');
  const r = plan().request;
  assert.equal(
    matchingPoolChange(r, { ...result(r), idempotency_key: 'wrong' }),
    false
  );
  assert.equal(
    matchingPoolChange(r, {
      ...result(r),
      after: { ...result(r).after, capacity: 1 },
    }),
    false
  );
  assert.equal(matchingPoolChange(r, result(r)), true);
});
