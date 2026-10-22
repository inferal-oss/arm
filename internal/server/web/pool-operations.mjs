import { parseJSON, stringifyJSON } from './core.mjs';
import {
  poolRequestCopy,
  samePoolRequest,
  matchingPoolChange,
  POOL_REQUEST_LIMIT,
} from './pool-model.mjs';
const bytes = (value) => new TextEncoder().encode(value).length;
const freeze = (value) => {
  if (value && typeof value === 'object') {
    for (const child of Object.values(value)) freeze(child);
    Object.freeze(value);
  }
  return value;
};
const key = 'arm-pool-operations-v1',
  limit = POOL_REQUEST_LIMIT,
  maximum = 8;
const definite = new Set([
  'invalid_request',
  'invalid_contact',
  'pool_not_found',
  'pool_deleted',
  'pool_exists',
  'pool_in_use',
  'revision_conflict',
  'review_conflict',
  'hierarchy_review_too_large',
  'hierarchy_review_required',
  'revision_exhausted',
  'authority_unavailable',
  'host_unavailable',
]);
export function createPoolOperations({
  call,
  authority,
  storage,
  onChange = () => {},
}) {
  let operations = [],
    warning = '';
  try {
    const raw = storage?.getItem(key);
    if (raw) {
      if (bytes(raw) > maximum * (limit + 64)) throw Error('oversize');
      const saved = parseJSON(raw);
      if (!Array.isArray(saved) || saved.length > maximum)
        throw Error('invalid');
      operations = saved.map(({ request }) => {
        if (
          !request?.idempotency_key ||
          request.idempotency_key.length > 128 ||
          !request.reviewed?.authority ||
          bytes(stringifyJSON(request)) > limit
        )
          throw Error('invalid');
        return {
          request: freeze(request),
          state: 'unknown',
          busy: false,
          error: 'Restored request. Check its stored result before retrying.',
        };
      });
    }
  } catch {
    warning =
      'Saved pool operations could not be read. Inspect the pool journal before making a replacement change.';
  }
  function changed() {
    try {
      storage?.setItem(
        key,
        stringifyJSON(
          operations
            .filter((op) => op.busy || op.state === 'unknown')
            .map((op) => ({ request: op.request }))
        )
      );
      if (!storage) throw Error('unavailable');
    } catch {
      warning =
        'Pending pool requests cannot be saved across reloads. Keep this page open and copy the exact request/key.';
    }
    onChange();
  }
  function accept(op, change) {
    try {
      if (!matchingPoolChange(op.request, change)) throw Error('mismatch');
      op.change = change;
      op.state = 'recorded';
      op.error = '';
    } catch {
      op.state = 'unknown';
      op.error =
        'The result did not match the exact reviewed request. Retain this key and inspect the journal.';
    }
  }
  function validAuthority(op) {
    if (op.request.reviewed.authority !== authority()) {
      op.error =
        'Connect to the saved pool authority before looking up or retrying this request.';
      onChange();
      return false;
    }
    return true;
  }
  async function send(op, priorUnknown = false) {
    if (op.busy || !validAuthority(op)) return;
    op.busy = true;
    op.state = 'unknown';
    op.error = 'Request in flight; no automatic retry.';
    changed();
    try {
      accept(op, await call('pool_mutate', poolRequestCopy(op.request)));
    } catch (error) {
      op.state =
        !priorUnknown && definite.has(error.code) ? 'rejected' : 'unknown';
      op.error =
        (priorUnknown ? 'The earlier outcome remains uncertain. ' : '') +
        (error.message || 'No response; the operation may have committed.');
    } finally {
      op.busy = false;
      changed();
    }
  }
  return {
    get operations() {
      return operations;
    },
    get warning() {
      return warning;
    },
    async submit(request) {
      if (
        operations.filter((op) => op.busy || op.state === 'unknown').length >=
        maximum
      )
        throw Error('Resolve a pending pool operation first (eight maximum).');
      if (
        operations.some(
          (op) =>
            (op.busy || op.state === 'unknown') &&
            op.request.reviewed?.authority === request.reviewed?.authority &&
            (request.id
              ? op.request.id === request.id
              : !op.request.id &&
                op.request.name === request.name &&
                op.request.scope === request.scope)
        )
      )
        throw Error(
          'Resolve the uncertain operation on this pool before creating a replacement change.'
        );
      if (
        operations.some(
          (op) => op.request.idempotency_key === request.idempotency_key
        )
      )
        throw Error(
          'This key already has an operation; use its existing controls.'
        );
      if (bytes(stringifyJSON(request)) > limit)
        throw Error('Request exceeds retention boundary.');
      operations = operations
        .filter((op) => op.busy || op.state === 'unknown')
        .concat(
          operations
            .filter((op) => !op.busy && op.state !== 'unknown')
            .slice(-7)
        );
      const op = {
        request: freeze(poolRequestCopy(request)),
        busy: false,
        state: 'unknown',
      };
      operations.push(op);
      await send(op);
      return op;
    },
    async retry(op) {
      if (op.state === 'unknown') await send(op, true);
    },
    async lookup(op) {
      if (op.busy || !validAuthority(op)) return;
      op.busy = true;
      onChange();
      try {
        const result = await call('pool_lookup', {
          idempotency_key: op.request.idempotency_key,
        });
        if (result.found) {
          if (!samePoolRequest(result.request, op.request))
            throw Error('Stored request differs from this exact request/key.');
          accept(op, result.change);
        } else
          op.error =
            'No stored result yet. The original request may still commit; only an explicit retry of this same request/key is safe.';
      } catch (error) {
        op.error = error.message || 'Unable to inspect stored result.';
      } finally {
        op.busy = false;
        changed();
      }
    },
  };
}
