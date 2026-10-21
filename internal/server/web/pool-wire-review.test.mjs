import { test } from 'node:test';
import assert from 'node:assert/strict';
import { POOL_REQUEST_LIMIT, preparePoolMutation } from './pool-model.mjs';
import { createPoolOperations } from './pool-operations.mjs';

test('complete reviews respect the actual wire budget before sending or retaining a new operation', async () => {
  const consumers = Array.from({ length: 1200 }, (_, i) => ({
    id: String(i).padStart(32, '0'),
    revision: 1,
  }));
  const view = {
    pool: {
      id: 'pool',
      authority: 'local',
      state: 'active',
      scope: 'global',
      unit: 'permits',
      name: 'Slots',
      revision: 1,
      capacity: 1,
    },
    reserved: 0,
    queued: [],
    review: { authority: 'local', consumers },
  };
  assert.throws(
    () =>
      preparePoolMutation(
        { operation: 'set', capacity: '0', reason: 'Change' },
        view,
        'local',
        'key'
      ),
    /60 KiB/
  );
  assert.equal(consumers.length, 1200);
  const ops = createPoolOperations({
    call: () => assert.fail('oversized request crossed wire'),
    authority: () => 'local',
    storage: { getItem: () => null, setItem() {} },
  });
  await assert.rejects(
    ops.submit({
      reviewed: { authority: 'local' },
      idempotency_key: 'large',
      body: 'x'.repeat(POOL_REQUEST_LIMIT),
    }),
    /retention boundary/
  );
  assert.equal(ops.operations.length, 0);
});
