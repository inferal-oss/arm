import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readDashboardRoute, dashboardRouteURL, createDashboardHistory} from './route.mjs';

const id = '0123456789abcdef0123456789abcdef';
const origin = 'http://127.0.0.1:1234/';

test('public view and lease state survive canonical navigation URLs', () => {
  assert.deepEqual(readDashboardRoute(origin), {view: 'monitor', lease: null});
  assert.deepEqual(readDashboardRoute(origin + '?view=resources'), {view: 'resources', lease: null});
  assert.deepEqual(readDashboardRoute(origin + '?lease=' + id), {view: 'leases', lease: id});
  const route = {view: 'resources', lease: id};
  assert.deepEqual(readDashboardRoute(new URL(dashboardRouteURL(route, origin), origin).href), route);
});

test('unknown, repeated and excessive routes are bounded without trusting URL content', () => {
  for (const query of ['?view=leases&view=host', '?lease=' + id + '&lease=' + id, '?view=' + 'x'.repeat(1025)]) assert.deepEqual(readDashboardRoute(origin + query), {view: 'monitor', lease: null});
  for (const lease of ['<script>', '../etc/passwd', 'x'.repeat(10000), 'ABCDEF0123456789ABCDEF0123456789AB']) assert.equal(readDashboardRoute(origin + '?lease=' + encodeURIComponent(lease)).lease, null);
  assert.equal(readDashboardRoute(origin + '?view=unknown').view, 'monitor');
  assert.deepEqual(readDashboardRoute('not a URL'), {view: 'monitor', lease: null});
});

test('canonical URLs contain no credentials, fragments, unknown query fields or free-form data', () => {
  const url = dashboardRouteURL({view: 'leases', lease: id}, origin + '?token=secret&reason=private#connect=secret');
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
  f.routes.navigate({view: 'leases', lease: id});
  assert.ok(f.entries.every(entry => !entry.includes('secret')));
});

test('back/forward restore view and modal identifiers without writing another history entry', () => {
  const f = fixture(origin); f.routes.sanitize();
  f.routes.navigate({view: 'resources', lease: null});
  f.routes.navigate({view: 'resources', lease: id});
  f.routes.navigate({view: 'messages', lease: null});
  assert.equal(f.entries.length, 4);
  f.back(); assert.deepEqual(f.callbacks.at(-1), {view: 'resources', lease: id});
  f.back(); assert.deepEqual(f.callbacks.at(-1), {view: 'resources', lease: null});
  f.forward(); assert.deepEqual(f.callbacks.at(-1), {view: 'resources', lease: id});
  assert.equal(f.entries.length, 4);
  f.routes.navigate({view: 'resources', lease: id});
  assert.equal(f.entries.length, 4, 'reselecting current state duplicates history');
});
