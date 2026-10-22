export const dashboardViews = Object.freeze([
  'monitor', 'resources', 'leases', 'messages', 'activity', 'host',
]);
const validLease = value => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
const validPool = value => typeof value === 'string' && /^pool_[a-f0-9]{32}$/.test(value);
const emptyRoute = () => ({
  view: 'monitor', lease: null, pool: null, poolAfter: null, poolBefore: null,
});
export function journalBoundary(value, positive = false) {
  if (typeof value === 'number' && !Number.isSafeInteger(value)) return null;
  if (!['string', 'number', 'bigint'].includes(typeof value)) return null;
  const text = String(value);
  if (!/^\d{1,19}$/.test(text)) return null;
  const integer = BigInt(text);
  return integer <= 9223372036854775807n && (!positive || integer > 0n)
    ? String(integer) : null;
}
export function readDashboardRoute(address) {
  try {
    const url = new URL(address), params = url.searchParams;
    if (
      url.search.length > 1024 ||
      ['view', 'lease', 'pool', 'pool_after', 'pool_before'].some(key => params.getAll(key).length > 1) ||
      (params.has('pool_after') && params.has('pool_before'))
    ) return emptyRoute();
    const lease = validLease(params.get('lease')) ? params.get('lease') : null;
    const pool = validPool(params.get('pool')) ? params.get('pool') : null;
    const requested = params.get('view') ?? (pool ? 'resources' : lease ? 'leases' : 'monitor');
    const view = dashboardViews.includes(requested) ? requested : 'monitor';
    const route = {...emptyRoute(), view, lease};
    if (view === 'resources' && pool) {
      route.pool = pool;
      route.poolBefore = journalBoundary(params.get('pool_before'), true);
      route.poolAfter = route.poolBefore === null ? journalBoundary(params.get('pool_after')) ?? '0' : null;
    }
    return route;
  } catch { return emptyRoute(); }
}
export function dashboardRouteURL(route, address) {
  const url = new URL(address), params = new URLSearchParams();
  const pool = validPool(route.pool) ? route.pool : null;
  const lease = validLease(route.lease) ? route.lease : null;
  const view = dashboardViews.includes(route.view) ? route.view : pool ? 'resources' : lease ? 'leases' : 'monitor';
  if (view !== 'monitor' || lease) params.set('view', view);
  if (view === 'resources' && pool) {
    params.set('pool', pool);
    const before = journalBoundary(route.poolBefore, true);
    const after = journalBoundary(route.poolAfter);
    if (before !== null && after === null) params.set('pool_before', before);
    else if (before === null && after !== null && after !== '0') params.set('pool_after', after);
  }
  if (lease) params.set('lease', lease);
  // Only public navigation identifiers survive. Never copy credentials, drafts,
  // unknown query fields or untrusted history.state into another entry.
  const search = params.toString();
  return url.pathname + (search ? '?' + search : '');
}
export function createDashboardHistory({location, history, onRoute}) {
  let current = readDashboardRoute(location.href);
  const canonical = route => dashboardRouteURL(route, location.href);
  const relative = () => location.pathname + location.search + location.hash;
  return {
    get current() { return {...current}; },
    sanitize() { history.replaceState(null, '', canonical(current)); },
    navigate(route) {
      const target = canonical(route);
      const next = readDashboardRoute(new URL(target, location.href).href);
      if (canonical(current) === target && relative() === target) return;
      history.pushState(null, '', target);
      current = next;
      onRoute({...current});
    },
    restore() {
      current = readDashboardRoute(location.href);
      if (relative() !== canonical(current)) history.replaceState(null, '', canonical(current));
      onRoute({...current});
    },
  };
}
