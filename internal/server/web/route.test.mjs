import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readDashboardRoute, dashboardRouteURL, createDashboardHistory} from './route.mjs';

const route=(view='monitor',lease=null,pool=null,poolAfter=null,poolBefore=null)=>({view,lease,pool,poolAfter,poolBefore});
const id = '0123456789abcdef0123456789abcdef';
const origin = 'http://127.0.0.1:1234/';

test('public view and lease state survive canonical navigation URLs', () => {
  assert.deepEqual(readDashboardRoute(origin), route());
  assert.deepEqual(readDashboardRoute(origin + '?view=resources'), route('resources'));
  assert.deepEqual(readDashboardRoute(origin + '?lease=' + id), route('leases',id));
  const target = route('resources',id);
  assert.deepEqual(readDashboardRoute(new URL(dashboardRouteURL(target, origin), origin).href), target);
});

test('unknown, repeated and excessive routes are bounded without trusting URL content', () => {
  for (const query of ['?view=leases&view=host', '?lease=' + id + '&lease=' + id, '?view=' + 'x'.repeat(1025)]) assert.deepEqual(readDashboardRoute(origin + query), route());
  for (const lease of ['<script>', '../etc/passwd', 'x'.repeat(10000), 'ABCDEF0123456789ABCDEF0123456789AB']) assert.equal(readDashboardRoute(origin + '?lease=' + encodeURIComponent(lease)).lease, null);
  assert.equal(readDashboardRoute(origin + '?view=unknown').view, 'monitor');
  assert.deepEqual(readDashboardRoute('not a URL'), route());
});

test('canonical URLs contain no credentials, fragments, unknown query fields or free-form data', () => {
  const url = dashboardRouteURL(route('leases',id), origin + '?token=secret&reason=private#connect=secret');
  assert.equal(url, '/?view=leases&lease=' + id);
  assert.doesNotMatch(url, /secret|connect|reason|token/);
});

function fixture(address) {
  let url = new URL(address), entries = [address], index = 0, callbacks = [];
  const location = {get href() { return url.href; }, get pathname() { return url.pathname; }, get search() { return url.search; }, get hash() { return url.hash; }};
  const history = {
    replaceState(state, title, target) { assert.equal(state, null); url = new URL(target, url); entries[index] = url.href; },
    pushState(state, title, target) { assert.equal(state, null); url = new URL(target, url); entries = entries.slice(0, index + 1); entries.push(url.href); index++; },
  };
  const routes = createDashboardHistory({location, history, onRoute: route => callbacks.push(route)});
  return {routes, callbacks, get entries() { return entries; }, back() { url = new URL(entries[--index]); routes.restore(); }, forward() { url = new URL(entries[++index]); routes.restore(); }};
}

test('bootstrap sanitization replaces its credential-bearing entry before navigation', () => {
  const f = fixture(origin + '?view=resources#connect=one-use-secret');
  assert.equal(f.routes.current.view, 'resources');
  f.routes.sanitize();
  assert.deepEqual(f.entries, [origin + '?view=resources']);
  assert.equal(f.callbacks.length, 0);
  f.routes.navigate(route('leases',id));
  assert.ok(f.entries.every(entry => !entry.includes('secret')));
});

test('back/forward restore view and modal identifiers without writing another history entry', () => {
  const f = fixture(origin); f.routes.sanitize();
  f.routes.navigate(route('resources'));
  f.routes.navigate(route('resources',id));
  f.routes.navigate(route('messages'));
  assert.equal(f.entries.length, 4);
  f.back(); assert.deepEqual(f.callbacks.at(-1), route('resources',id));
  f.back(); assert.deepEqual(f.callbacks.at(-1), route('resources'));
  f.forward(); assert.deepEqual(f.callbacks.at(-1), route('resources',id));
  assert.equal(f.entries.length, 4);
  f.routes.navigate(route('resources',id));
  assert.equal(f.entries.length, 4, 'reselecting current state duplicates history');
});

const pool='pool_'+id;
test('pool selection, exact journal boundaries and lease context are public canonical routes',()=>{
 assert.deepEqual(readDashboardRoute(origin+'?pool='+pool),route('resources',null,pool,'0'));
 const after=route('resources',id,pool,'9223372036854775807');
 assert.deepEqual(readDashboardRoute(new URL(dashboardRouteURL(after,origin),origin).href),after);
 const before=route('resources',null,pool,null,'9223372036854775807');
 assert.deepEqual(readDashboardRoute(new URL(dashboardRouteURL(before,origin),origin).href),before);
 assert.equal(dashboardRouteURL(route('resources',null,pool,'0000'),origin),'/?view=resources&pool='+pool);
 for(const input of ['-1','1.2','1e3','9223372036854775808','x'.repeat(50)])assert.equal(readDashboardRoute(origin+'?pool='+pool+'&pool_after='+input).poolAfter,'0');
 assert.equal(readDashboardRoute(origin+'?view=messages&pool='+pool).pool,null);
 assert.equal(readDashboardRoute(origin+'?pool='+pool+'&pool_after=1&pool_before=2').pool,null);
 assert.equal(readDashboardRoute(origin+'?pool='+pool+'&pool='+pool).pool,null);
 assert.equal(readDashboardRoute(origin+'?pool=../bad').pool,null);
 assert.doesNotMatch(dashboardRouteURL(after,origin+'?token=secret#connect=secret'),/secret|token|connect/);
});
test('pool journal Back/Forward and reload restore exact scope without writing another entry',()=>{
 const f=fixture(origin);f.routes.sanitize();const first=route('resources',null,pool,'0'),next=route('resources',null,pool,'1234567890123456789'),overlay={...next,lease:id};f.routes.navigate(first);f.routes.navigate(next);f.routes.navigate(overlay);f.back();assert.deepEqual(f.callbacks.at(-1),next);f.back();assert.deepEqual(f.callbacks.at(-1),first);f.forward();assert.deepEqual(f.callbacks.at(-1),next);assert.deepEqual(readDashboardRoute(f.entries[2]),next);assert.equal(f.entries.length,4);
});
