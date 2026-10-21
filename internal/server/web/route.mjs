export const dashboardViews = Object.freeze(['monitor', 'resources', 'leases', 'messages', 'activity', 'host']);
const validLease = value => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);

export function readDashboardRoute(address) {
  try {
    const url = new URL(address);
    if (url.search.length > 1024 || url.searchParams.getAll('view').length > 1 || url.searchParams.getAll('lease').length > 1) return {view: 'monitor', lease: null};
    const lease = url.searchParams.get('lease');
    const view = url.searchParams.get('view') ?? (validLease(lease) ? 'leases' : 'monitor');
    return {view: dashboardViews.includes(view) ? view : 'monitor', lease: validLease(lease) ? lease : null};
  } catch { return {view: 'monitor', lease: null}; }
}

export function dashboardRouteURL(route, address) {
  const url = new URL(address), params = new URLSearchParams();
  const view = dashboardViews.includes(route.view) ? route.view : 'monitor';
  const lease = validLease(route.lease) ? route.lease : null;
  if (view !== 'monitor' || lease) params.set('view', view);
  if (lease) params.set('lease', lease);
  const search = params.toString();
  // Only public navigation identifiers survive. Never copy connection fragments,
  // unknown query fields or untrusted history.state into a new history entry.
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
      const target = canonical(route), next = readDashboardRoute(new URL(target, location.href).href);
      if (canonical(current) === target && relative() === target) return;
      history.pushState(null, '', target); current = next; onRoute({...current});
    },
    restore() {
      current = readDashboardRoute(location.href);
      if (relative() !== canonical(current)) history.replaceState(null, '', canonical(current));
      onRoute({...current});
    },
  };
}
