// Preserve exact int64 pool quantities, revisions and cursors. Never turn a
// rounded JavaScript Number back into an ARM command or cursor.
export function parseJSON(text) {
  if (typeof text !== 'string' || text.length > 4 * 1024 * 1024) throw Error('Response exceeds 4 MiB');
  let i = 0;
  const fail = () => { throw Error('Invalid JSON response'); };
  const space = () => { while (/[\x20\t\n\r]/.test(text[i] ?? '\0')) i++; };
  function value(depth) {
    if (depth > 64) fail();
    space();
    const c = text[i];
    if (c === '"') {
      const start = i++;
      while (i < text.length) {
        if (text[i] === '\\') { i += 2; continue; }
        if (text[i++] === '"') return JSON.parse(text.slice(start, i));
      }
      fail();
    }
    if (c === '[' || c === '{') {
      i++;
      const array = c === '[', end = array ? ']' : '}', result = array ? [] : Object.create(null);
      space();
      if (text[i] === end) { i++; return result; }
      for (;;) {
        space();
        if (array) result.push(value(depth + 1));
        else {
          if (text[i] !== '"') fail();
          const key = value(depth + 1);
          space();
          if (text[i++] !== ':') fail();
          result[key] = value(depth + 1);
        }
        space();
        if (text[i] === end) { i++; return result; }
        if (text[i++] !== ',') fail();
      }
    }
    for (const [word, literal] of [['true', true], ['false', false], ['null', null]]) {
      if (text.startsWith(word, i)) { i += word.length; return literal; }
    }
    const match = /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/.exec(text.slice(i));
    if (!match) fail();
    i += match[0].length;
    if (!/[.eE]/.test(match[0])) {
      const n = BigInt(match[0]);
      if (n > BigInt(Number.MAX_SAFE_INTEGER) || n < BigInt(Number.MIN_SAFE_INTEGER)) return n;
    }
    const n = Number(match[0]);
    if (!Number.isFinite(n)) fail();
    return n;
  }
  const result = value(0);
  space();
  if (i !== text.length) fail();
  return result;
}

export function stringifyJSON(value) {
  function encode(v, depth) {
    if (depth > 64) throw Error('JSON nesting limit');
    if (typeof v === 'bigint') return String(v);
    if (v === null || typeof v === 'string' || typeof v === 'boolean') return JSON.stringify(v);
    if (typeof v === 'number' && Number.isFinite(v)) return JSON.stringify(v);
    if (Array.isArray(v)) return '[' + v.map(x => encode(x, depth + 1)).join(',') + ']';
    if (v && typeof v === 'object') return '{' + Object.entries(v).map(([k, x]) => JSON.stringify(k) + ':' + encode(x, depth + 1)).join(',') + '}';
    throw Error('Unsupported JSON value');
  }
  return encode(value, 0);
}

export const numeric = value => value === null || value === undefined ? null : Number(value);
export function count(value) {
  return value === null || value === undefined ? 'unavailable' : value.toLocaleString('en-US', {maximumFractionDigits: 2});
}
export function bytes(value) {
  if (value === null || value === undefined) return 'unavailable';
  let n = Number(value), unit = 0;
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB', 'EiB'];
  while (Math.abs(n) >= 1024 && unit < units.length - 1) { n /= 1024; unit++; }
  return `${n.toLocaleString('en-US', {maximumFractionDigits: unit ? 1 : 0})} ${units[unit]}`;
}
export const displayUnit = unit => ({'logical-core-equivalents': 'cores', 'socket-binding': 'bindings'})[unit] || unit;
export const quantity = (v, unit) => unit === 'bytes' ? bytes(v) : count(v) + (unit ? ' ' + displayUnit(unit) : '');
export const percent = value => value == null ? 'unavailable' : Number(value).toFixed(1) + '%';

// Keep only the collector's bounded window. Epoch changes and explicit gaps
// discard the old window; a missing interval breaks paths rather than joining.
export function mergeHistory(previous, page, epoch) {
  const points = epoch !== page.epoch || page.gap ? [] : previous;
  const bySequence = new Map(points.map(p => [String(p.sequence), p]));
  for (const p of page.history ?? []) bySequence.set(String(p.sequence), p);
  return [...bySequence.values()].sort((a, b) => a.sequence < b.sequence ? -1 : a.sequence > b.sequence ? 1 : 0).slice(-60);
}
export function chartPaths(points, get, ceiling = null, cadence = 2000) {
  const samples = points.map(p => ({value: numeric(get(p)), at: Date.parse(p.at)}));
  const max = ceiling ?? Math.max(1, ...samples.map(p => p.value ?? 0));
  const paths = [];
  let current = '', last = null;
  samples.forEach((p, i) => {
    if (p.value === null || !Number.isFinite(p.value) || !Number.isFinite(p.at)) { if (current) paths.push(current); current = ''; last = null; return; }
    if (last !== null && (p.at - last > cadence * 1.5 || p.at <= last)) { if (current) paths.push(current); current = ''; }
    const x = i * 600 / Math.max(1, samples.length - 1), y = 90 - Math.min(1, Math.max(0, p.value / max)) * 80;
    current += `${current ? 'L' : 'M'}${x.toFixed(2)},${y.toFixed(2)} `;
    last = p.at;
  });
  if (current) paths.push(current);
  return paths;
}

// Historical records keep their original UID/name snapshot. A current account
// name may supplement a missing snapshot only on the same selected authority.
export function ownerDisplay(record, current = null, host = record?.host) {
  const uid = record?.owner;
  if (uid == null || uid === '') return 'Unknown owner';
  if (record.owner_username) return `${record.owner_username} (UID ${uid})`;
  if (host && host === current?.host && uid === current?.owner && current.owner_username) {
    return `${current.owner_username} (UID ${uid}; current account)`;
  }
  return `UID ${uid}`;
}

// Use the backend Resources wire names; these are promises, not usage.
export function hostResourceSummary(resources = {}) {
  const cpu = Number(resources.cpu ?? 0);
  return `${cpu === 0 ? '0' : cpu.toPrecision(3)} cores · ${bytes(resources.memory_bytes ?? 0)} RAM · ${bytes(resources.disk_bytes ?? 0)} disk`;
}
