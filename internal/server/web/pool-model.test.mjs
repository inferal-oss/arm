import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseJSON, stringifyJSON } from './core.mjs';
import {
  poolInteger,
  preparePoolMutation,
  MAX_COUNT,
  matchingPoolChange, poolBudgetFields, validPoolHierarchy,
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

test('pause review distinguishes new requests from already queued work', () => {
  const p = preparePoolMutation({operation:'drain', reason:'Pause new demand'}, view(), 'host-a', 'pause-key');
  assert.equal(p.state, 'draining');
  assert.match(p.paragraphs.join(' '), /Refuse new requests and renewals/);
  assert.match(p.paragraphs.join(' '), /Already queued requests may still start when capacity permits/);
  assert.match(p.paragraphs.join(' '), /queued requests are not cancelled/);
});

test('subpool creation inherits identity and binds parent/tree review; reduction charges child backing once',async()=>{
 const {poolTreeRows}=await import('./pool-model.mjs');
 const parent=view();parent.pool.capacity=10;parent.reserved=1;parent.available=5;parent.partition={direct_reserved:1,child_backing:4,committed:5,held_backing:10,pending_return:0};parent.review.hierarchy={schema_version:1,pool:parent.pool.id,root:parent.pool.id,pool_count:2,consumer_count:1,snapshot_sha256:'a'.repeat(64)};
 const p=preparePoolMutation({operation:'create-child',name:'Compiler slots',capacity:'3',reason:'Dedicated build partition'},parent,'host-a','child-key');
 assert.equal(p.request.operation,'create');assert.equal(p.request.parent,parent.pool.id);assert.equal(p.request.parent_revision,7n);assert.equal(p.request.scope,'global');assert.equal(p.request.unit,'permits');assert.equal(p.request.reviewed.hierarchy.snapshot_sha256,'a'.repeat(64));assert.equal(p.reserved,0n);assert.match(p.paragraphs.join(' '),/overlap/);
 parent.children=['child'];const reduced=preparePoolMutation({operation:'set',capacity:'2',reason:'retain backing'},parent,'host-a','reduce-key');assert.equal(reduced.reserved,5n);assert.equal(reduced.deficit,3n);
 const child={...parent,pool:{...parent.pool,id:'child',name:'Child',parent:parent.pool.id},children:[]};
 const tree=poolTreeRows([child,parent]);assert.deepEqual(tree.map(r=>[r.view.pool.id,r.depth]),[[parent.pool.id,0],['child',1]]);
 assert.throws(()=>poolTreeRows([child]),/incomplete/);assert.throws(()=>poolTreeRows([{...child,pool:{...child.pool,parent:'child'}}]),/cyclic/);
 assert.throws(()=>preparePoolMutation({operation:'create-child',name:'No guard',capacity:'1',reason:'x'},{...parent,review:{authority:'host-a',consumers:[]}},'host-a','k'),/complete hierarchy/);
 assert.equal(matchingPoolChange(p.request,{...result(p.request),before:null,after:{...parent.pool,id:'new',name:p.request.name,unit:p.request.unit,scope:p.request.scope,capacity:p.request.capacity,revision:1,parent:'wrong'}}),false);
});

test('resource cards use backend child commitments without adding overlapping tree totals',()=>{
 for(const a of [{configured:12,direct_reserved:1,child_backing:7,committed:8,uncarved_available:4},{configured:2,direct_reserved:3,child_backing:2,committed:5,uncarved_available:0},{configured:2,direct_reserved:2,child_backing:0,committed:2,uncarved_available:0}])assert.deepEqual(poolBudgetFields({custom:{partition:a},reserved:a.direct_reserved}),[['TOTAL',a.configured],['COMMITTED',a.committed],['AVAILABLE',a.uncarved_available]]);
 assert.equal(validPoolHierarchy({schema_version:1,pool:'p',root:'p',pool_count:1.5,consumer_count:0,snapshot_sha256:'a'.repeat(64)},'p'),false);
});

test('malformed hierarchy scalar coercions cannot enter an exact child review',()=>{
 const parent=view();parent.available=10;parent.review.hierarchy={schema_version:1,pool:parent.pool.id,root:parent.pool.id,pool_count:1,consumer_count:0,snapshot_sha256:'a'.repeat(64)};
 for(const [field,value] of [['consumer_count',null],['consumer_count',false],['pool_count',true],['snapshot_sha256',['a'.repeat(64)]],['root',['p']]]){
 const v=structuredClone(parent);v.review.hierarchy[field]=value;assert.equal(validPoolHierarchy(v.review.hierarchy,v.pool.id),false);assert.throws(()=>preparePoolMutation({operation:'create-child',name:'Child',capacity:'1',reason:'bounded review'},v,'host-a','key'),/complete hierarchy/);}
 parent.review.unavailable='hierarchy_review_too_large';assert.throws(()=>preparePoolMutation({operation:'set',capacity:'1',reason:'no truncated fallback'},parent,'host-a','key'),/remains readable/);
});
test('child carve respects exact uncarved availability through MaxInt64',()=>{
 const parent=view();parent.available=MAX_COUNT;parent.review.hierarchy={schema_version:1,pool:parent.pool.id,root:parent.pool.id,pool_count:1,consumer_count:0,snapshot_sha256:'a'.repeat(64)};
 const draft={operation:'create-child',name:'Exact boundary',capacity:String(MAX_COUNT),reason:'Carve existing permits'};
 assert.equal(preparePoolMutation(draft,parent,'host-a','key').capacity,MAX_COUNT);
 parent.available=MAX_COUNT-1n;assert.throws(()=>preparePoolMutation(draft,parent,'host-a','key'),/uncarved availability/);
 parent.available=0;draft.capacity='1';assert.throws(()=>preparePoolMutation(draft,parent,'host-a','key'),/uncarved availability/);
 draft.capacity='0';assert.equal(preparePoolMutation(draft,parent,'host-a','key').capacity,0n);
});
