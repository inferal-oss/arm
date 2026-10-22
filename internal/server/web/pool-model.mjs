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
  if(view?.review?.unavailable) throw Error('This tree remains readable, but its complete review exceeds dashboard limits. No change can use a truncated scope.');

  const child = draft.operation === 'create-child';
  const operation = child ? 'create' : draft.operation;
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
    scope = child ? view?.pool?.scope : draft.scope;
    if (scope !== 'host' && scope !== 'global')
      throw Error('Choose host or global scope.');
    unit = text(String(child ? view?.pool?.unit ?? '' : draft.unit ?? 'count').trim(), 64, 'Unit', true, true);
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
    if (child) {
      if (!view?.pool?.id || view.pool.state !== 'active' || view.pool.authority !== authority || view.review?.authority !== authority || !Array.isArray(view.review?.consumers) || !validPoolHierarchy(view.review?.hierarchy, view.pool.id)) throw Error('Refresh an open parent with complete hierarchy evidence before carving a subpool.');
      if(request.capacity>poolInteger(view.available))throw Error('The child capacity exceeds this parent’s uncarved availability. Refresh or choose a smaller carve.');
      request.parent = view.pool.id;
      request.parent_revision = poolInteger(view.pool.revision);
      request.reviewed = copy(view.review);
    } else request.reviewed = { authority, consumers: [] };
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
    reserved = poolInteger(view.partition?.committed ?? view.reserved);
    if ((view.pool.parent || view.children?.length || view.review.hierarchy) && !validPoolHierarchy(view.review.hierarchy, view.pool.id)) throw Error('Refresh the complete hierarchy review before changing a partition.');
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
  if (child) paragraphs.push('The backend checks the complete hierarchy again at confirmation. Parent and child figures overlap; do not add them together.');
  if (!child && view?.partition) paragraphs.push(`Current backing: ${view.partition.direct_reserved} directly leased + ${view.partition.child_backing} held for children. Ancestor and descendant totals overlap; never add them together. Reductions keep promised backing until the obligations settle.`);
  if (!child && scope === 'global')
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
    parent: child ? copy(view) : null,
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
        change.after.host === (request.host || undefined) &&
        (change.after.parent || undefined) === (request.parent || undefined)
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

export function validPoolHierarchy(token, id) {
 return token?.schema_version === 1 && typeof token.pool === 'string' && token.pool === id && typeof token.root === 'string' && /^[A-Za-z0-9_.:-]{1,128}$/.test(token.root) && typeof token.pool_count === 'number' && Number.isInteger(token.pool_count) && token.pool_count >= 1 && token.pool_count <= 4096 && typeof token.consumer_count === 'number' && Number.isInteger(token.consumer_count) && token.consumer_count >= 0 && token.consumer_count <= 8192 && typeof token.snapshot_sha256 === 'string' && /^[0-9a-f]{64}$/.test(token.snapshot_sha256);
}
// The root-to-node presentation is an overlapping view, never another resource
// counter. Missing ancestry/cycles refuse rather than relabel an orphan as root.
export function poolTreeRows(views) {
 const nodes=new Map(), children=new Map();
 for(const view of views){const p=view.pool;if(!p?.id||nodes.has(p.id)) throw Error('Pool tree contains a missing or duplicate identity. Refresh the complete roster.');nodes.set(p.id,view);}
 for(const view of views){const p=view.pool,parent=p.parent||'';if(parent&&!nodes.has(parent))throw Error('Pool tree is incomplete; parent snapshot is missing. Refresh before changing it.');const list=children.get(parent)||[];list.push(p.id);children.set(parent,list);}
 for(const list of children.values())list.sort((a,b)=>a<b?-1:a>b?1:0);
 const out=[],seen=new Set();
 function walk(id,depth,path){if(depth>=16||seen.has(id))throw Error('Pool tree ancestry is invalid.');seen.add(id);const view=nodes.get(id);if(path.length){const parent=nodes.get(path.at(-1)).pool;for(const key of ['authority','scope','host','unit'])if((view.pool[key]||'')!==(parent[key]||''))throw Error('Pool child has conflicting inherited identity.');}out.push({view,depth,path:[...path,id]});for(const child of children.get(id)||[])walk(child,depth+1,[...path,id]);}
 for(const id of children.get('')||[])walk(id,0,[]);
 if(seen.size!==nodes.size)throw Error('Pool tree is cyclic or incomplete.');return out;
}

// The backend partition is authoritative. Child backing already includes its
// descendants, so resource cards show commitments once rather than summing rows.
export function poolBudgetFields(resource) {
 const a=resource.custom?.partition;
 return a ? [['TOTAL',a.configured],['COMMITTED',a.committed],['AVAILABLE',a.uncarved_available]] : [['POOL',resource.budget_total],['RESERVED',resource.reserved],['AVAILABLE',resource.available]];
}
