import { parseJSON, stringifyJSON } from './core.mjs';

// Leave room for the request envelope within the backend’s 64 KiB frame limit.
export const POOL_REQUEST_LIMIT = 60 * 1024;
export const MAX_COUNT = 9223372036854775807n,
  MIN_COUNT = -9223372036854775808n;
const copy = (value) => parseJSON(stringifyJSON(value));
export function poolInteger(value, { signed = false, nonzero = false } = {}) {
  if (typeof value === 'number' && !Number.isSafeInteger(value))
    throw Error('Use an exact base-10 integer.');
  const text = String(value ?? '').trim();
  if (!/^[+-]?\d{1,19}$/.test(text))
    throw Error('Use a whole base-10 count, without decimals or exponents.');
  const n = BigInt(text);
  if (n > MAX_COUNT || n < (signed ? MIN_COUNT : 0n) || (nonzero && n === 0n))
    throw Error(
      signed
        ? 'Enter a nonzero signed int64 adjustment.'
        : 'Capacity must be between 0 and 9223372036854775807.'
    );
  return n;
}
function text(value, limit, label, required = false, printable = false) {
  if (
    typeof value !== 'string' ||
    (required && !value.trim()) ||
    new TextEncoder().encode(value).length > limit ||
    /[\uD800-\uDFFF]/u.test(value) ||
    (printable && /\p{Cc}/u.test(value))
  )
    throw Error(
      `${label} must be ${required ? '1' : '0'}–${limit} UTF-8 bytes${printable ? ' without control characters' : ''}.`
    );
  return value;
}
export function preparePoolMutation(draft, view, authority, key) {
  const operation = draft.operation;
  if (
    !['create', 'set', 'adjust', 'drain', 'resume', 'delete'].includes(
      operation
    )
  )
    throw Error('Unsupported pool operation.');
  text(authority, 256, 'Authority', true);
  text(key, 128, 'Request key', true);
  const reason = text(draft.reason, 4096, 'Reason', true);
  const request = { operation, reason, idempotency_key: key };
  let before = null,
    capacity = 0n,
    reserved = 0n,
    state = 'active',
    scope = '',
    name = '',
    unit = 'count',
    queued = 0;
  if (operation === 'create') {
    name = text(String(draft.name ?? '').trim(), 128, 'Name', true, true);
    scope = draft.scope;
    if (scope !== 'host' && scope !== 'global')
      throw Error('Choose host or global scope.');
    unit = text(String(draft.unit ?? 'count').trim(), 64, 'Unit', true, true);
    request.name = name;
    request.scope = scope;
    request.unit = unit;
    const description = text(
      String(draft.description ?? ''),
      4096,
      'Description',
      false,
      true
    );
    if (description) request.description = description;
    request.capacity = poolInteger(draft.capacity);
    if (scope === 'host') request.host = authority;
    request.reviewed = { authority, consumers: [] };
    capacity = request.capacity;
  } else {
    if (
      !view?.pool?.id ||
      !view.review ||
      view.pool.authority !== authority ||
      view.review.authority !== authority
    )
      throw Error(
        'Refresh the pool from this local authority before reviewing a change.'
      );
    if (view.pool.state === 'deleted')
      throw Error('This pool is a retained historical definition.');
    const consumers = view.review.consumers;
    if (!Array.isArray(consumers) || consumers.length > 8192)
      throw Error(
        'The complete consumer review exceeds the dashboard limit (8192); scope was not truncated.'
      );
    const seen = new Set();
    for (const item of consumers) {
      if (!item.id || seen.has(item.id) || poolInteger(item.revision) <= 0n)
        throw Error('Invalid consumer review; refresh the pool.');
      seen.add(item.id);
    }
    before = copy(view);
    request.id = view.pool.id;
    request.revision = poolInteger(view.pool.revision);
    request.reviewed = copy(view.review);
    if (request.revision <= 0n || request.revision === MAX_COUNT)
      throw Error('Refresh a valid non-exhausted definition revision.');
    capacity = poolInteger(view.pool.capacity);
    reserved = poolInteger(view.reserved);
    state = view.pool.state;
    scope = view.pool.scope;
    name = view.pool.name;
    unit = view.pool.unit;
    queued = view.queued?.length ?? 0;
    if (operation === 'set') {
      request.capacity = poolInteger(draft.capacity);
      capacity = request.capacity;
    }
    if (operation === 'adjust') {
      request.delta = poolInteger(draft.delta, { signed: true, nonzero: true });
      capacity += request.delta;
      if (capacity < 0n || capacity > MAX_COUNT)
        throw Error(
          'This adjustment would make capacity negative or overflow int64.'
        );
    }
    if (operation === 'drain') state = 'draining';
    if (operation === 'resume') state = 'active';
    if (operation === 'delete') state = 'deleted';
  }
  if (
    new TextEncoder().encode(stringifyJSON(request)).length > POOL_REQUEST_LIMIT
  )
    throw Error(
      'The complete reviewed request exceeds the 60 KiB protocol budget. Nothing was truncated or sent; use the native CLI for this larger scope.'
    );
  const deficit = reserved > capacity ? reserved - capacity : 0n,
    available =
      state === 'deleted' ? 0n : capacity > reserved ? capacity - reserved : 0n;
  const paragraphs = [
    'Existing leases keep their quantities and deadlines. Capacity changes do not stop work or revoke leases.',
  ];
  if (scope === 'global')
    paragraphs.push(
      `Global scope uses one authoritative ledger on ${authority}; this does not create independent capacity on each host.`
    );
  if (deficit > 0n)
    paragraphs.push(
      `The pool will have a deficit of ${deficit} ${unit}. Existing obligations remain charged; new grants cannot exceed the revised capacity.`
    );
  if (operation === 'drain')
    paragraphs.push(
      'Refuse new requests and renewals. Already queued requests may still start when capacity permits; queued requests are not cancelled. Existing obligations remain charged; pausing does not reclaim capacity.'
    );
  if (operation === 'resume')
    paragraphs.push(
      'Allow normal admission again. Queued work may start under the backend scheduling policy.'
    );
  if (operation === 'delete')
    paragraphs.push(
      `Retain a deleted definition and its journal. The backend refuses deletion while any queued or consuming claim remains (snapshot: ${request.reviewed.consumers.length}). No files or processes are deleted.`
    );
  return {
    request,
    before,
    name,
    scope,
    authority,
    unit,
    capacity,
    reserved,
    available,
    deficit,
    state,
    queued,
    paragraphs,
  };
}
export const poolRequestCopy = copy;
function stable(value) {
  if (Array.isArray(value)) return value.map(stable);
  if (value && typeof value === 'object')
    return Object.fromEntries(
      Object.keys(value)
        .sort()
        .map((key) => [key, stable(value[key])])
    );
  return value;
}
export const samePoolRequest = (a, b) =>
  stringifyJSON(stable(a)) === stringifyJSON(stable(b));
export function matchingPoolChange(request, change) {
  try {
    if (
      !change ||
      change.idempotency_key !== request.idempotency_key ||
      change.operation !== request.operation ||
      change.reason !== request.reason ||
      change.after?.authority !== request.reviewed.authority ||
      !change.after?.id ||
      poolInteger(change.sequence) <= 0n
    )
      return false;
    if (request.operation === 'create')
      return (
        change.after.name === request.name &&
        change.after.scope === request.scope &&
        change.after.unit === request.unit &&
        poolInteger(change.after.capacity) === poolInteger(request.capacity) &&
        poolInteger(change.after.revision) === 1n &&
        change.after.host === (request.host || undefined)
      );
    if (
      change.after.id !== request.id ||
      change.before?.id !== request.id ||
      poolInteger(change.before.revision) !== poolInteger(request.revision) ||
      poolInteger(change.after.revision) !== poolInteger(request.revision) + 1n
    )
      return false;
    if (request.operation === 'set')
      return (
        poolInteger(change.after.capacity) === poolInteger(request.capacity)
      );
    if (request.operation === 'adjust')
      return (
        poolInteger(change.after.capacity) ===
        poolInteger(change.before.capacity) +
          poolInteger(request.delta, { signed: true, nonzero: true })
      );
    return (
      change.after.state ===
      { drain: 'draining', resume: 'active', delete: 'deleted' }[
        request.operation
      ]
    );
  } catch {
    return false;
  }
}
