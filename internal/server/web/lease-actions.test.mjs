import {test} from 'node:test';
import assert from 'node:assert/strict';
import {parseLeaseTTL, leaseActions, attachedConsumers, leaseActionReview, leaseActionResult, leaseActionPanel} from './lease-actions.mjs';

const now = Date.parse('2026-10-21T10:00:00Z');
const active = {id: 'parent', revision: 3, state: 'active', expires_at: '2026-10-21T10:05:00Z', request: {reason: '<script>literal reason</script>', resources: {cpu: 1}}};

test('durations preserve nanoseconds, allow combined units and reject invalid or excessive input', () => {
  assert.equal(parseLeaseTTL('1h30m'), 5400000000000);
  assert.equal(parseLeaseTTL('0.000000001s'), 1);
  assert.equal(parseLeaseTTL('7d'), 604800000000000);
  for (const value of ['', '0m', '-1h', '8d', '168h1s', '1 h', '1H', '1e3s', '0.0000000001s', '1m junk', '9'.repeat(100) + 's']) assert.throws(() => parseLeaseTTL(value));
});

test('state policy distinguishes queued, active, expired and unresolved allocations', () => {
  assert.deepEqual(leaseActions(active, now), ['renew', 'release', 'cancel']);
  assert.deepEqual(leaseActions({...active, state: 'queued'}, now), ['cancel']);
  assert.deepEqual(leaseActions(active, now + 600000), ['release', 'cancel']);
  for (const state of ['ending', 'needs_attention', 'terminal', 'provisioning']) assert.deepEqual(leaseActions({...active, state}, now), []);
});

test('extension preview uses duration from now and refuses shortening the existing expiry', () => {
  const review = leaseActionReview(active, 'renew', {now, ttl: '15m'});
  assert.equal(review.params.revision, 3);
  assert.equal(review.expires.toISOString(), '2026-10-21T10:15:00.000Z');
  assert.equal(review.params.ttl_ns, 900000000000);
  assert.match(review.paragraphs.join(' '), /not added/);
  assert.throws(() => leaseActionReview(active, 'renew', {now, ttl: '1m'}), /extends/);
});

test('destructive review lists exact live dependencies and storage path without authorizing unrelated files', () => {
  const storage = {...active, request: {reason: 'scratch', kind: 'storage'}, artifact: {path: '/managed/<literal>'}};
  const leases = [
    {id: 'child-b', revision: 9, state: 'queued', request: {storage_lease: 'parent'}},
    {id: 'child-a', revision: 4, state: 'ending', request: {port_lease: 'parent'}},
    {id: 'finished', revision: 2, state: 'terminal', request: {storage_lease: 'parent'}},
    {id: 'other', revision: 2, state: 'active', request: {storage_lease: 'other'}},
  ];
  const review = leaseActionReview(storage, 'release', {now, leases});
  assert.deepEqual(review.params.consumers, [{id: 'child-a', revision: 4}, {id: 'child-b', revision: 9}]);
  assert.match(review.paragraphs.join(' '), /reclaim its managed files/);
  assert.match(review.paragraphs.join(' '), /\/managed\/<literal>/);
  assert.match(review.paragraphs.join(' '), /child-a, child-b/);
  assert.deepEqual(attachedConsumers(active), []);
  assert.match(leaseActionResult({...active, state: 'ending'}), /not yet been confirmed free/);
});

class Element {
  constructor(tag) { this.tagName = tag; this.children = []; this.events = {}; this.attributes = {}; this._text = ''; this.hidden = false; this.disabled = false; }
  append(...items) { this.children.push(...items); }
  replaceChildren(...items) { this.children = items; this._text = ''; }
  set textContent(value) { this._text = String(value); this.children = []; }
  get textContent() { return this._text + this.children.map(item => item.textContent).join(''); }
  setAttribute(key, value) { this.attributes[key] = value; }
  addEventListener(type, fn) { (this.events[type] ||= []).push(fn); }
  async fire(type) { for (const fn of this.events[type] || []) await fn(); }
  focus() { this.focused = true; }
}
const document = {createElement: tag => new Element(tag)};
const all = root => [root, ...root.children.flatMap(all)];
const button = (panel, text) => all(panel.element).find(item => item.tagName === 'button' && item.textContent === text);
function setup(t, options = {}) {
  let live = true, calls = [], results = [], pending = [];
  const panel = leaseActionPanel({lease: active, connected: () => live, now: () => now, document,
    call: async (...args) => { calls.push(args); return {...active, revision: 4}; },
    onResult: result => results.push(result), onRefresh() {}, onPending: value => pending.push(value), ...options});
  t.after(() => panel.dispose());
  return {panel, calls, results, pending, offline() { live = false; panel.updateConnection(); }, online() { live = true; panel.updateConnection(); }};
}

test('review and decline never send a mutation; confirmation sends exactly the displayed revision', async t => {
  const {panel, calls, pending} = setup(t);
  await button(panel, 'Release').fire('click');
  assert.equal(calls.length, 0);
  assert.match(panel.element.textContent, /revision 3/);
  await button(panel, 'Keep lease').fire('click');
  assert.equal(calls.length, 0);
  assert.equal(button(panel, 'Release').focused, true);
  await button(panel, 'Cancel').fire('click');
  await button(panel, 'Confirm cancellation').fire('click');
  assert.deepEqual(calls, [['lease_action', {id: 'parent', revision: 3, action: 'cancel', consumers: []}]]);
  assert.deepEqual(pending, [true, false]);
  assert.equal(button(panel, 'Cancel').disabled, true);
  assert.equal(all(panel.element).some(item => item.tagName === 'script'), false);
});

test('extension duration validation blocks shortening and updates the request after editing', async t => {
  const {panel, calls} = setup(t);
  await button(panel, 'Extend').fire('click');
  const input = all(panel.element).find(item => item.tagName === 'input');
  input.value = '1m'; await input.fire('input');
  assert.equal(button(panel, 'Confirm extension').disabled, true);
  await button(panel, 'Confirm extension').fire('click'); // Guard even synthetic events.
  assert.equal(calls.length, 0);
  input.value = '2h'; await input.fire('input');
  assert.equal(button(panel, 'Confirm extension').disabled, false);
  await button(panel, 'Confirm extension').fire('click');
  assert.equal(calls[0][1].ttl_ns, 7200000000000);
});

test('pending actions are serialized and closing the detail does not resend or discard the result', async t => {
  let resolve, calls = 0, result;
  const {panel} = setup(t, {call: () => { calls++; return new Promise(done => { resolve = done; }); }, onResult: value => { result = value; }});
  await button(panel, 'Release').fire('click');
  const sending = button(panel, 'Confirm release').fire('click');
  assert.equal(button(panel, 'Keep lease').disabled, true);
  await button(panel, 'Keep lease').fire('click');
  assert.match(panel.element.textContent, /Sending the reviewed request/);
  await button(panel, 'Confirm release').fire('click');
  assert.equal(calls, 1);
  assert.equal(button(panel, 'Release').disabled, true);
  panel.dispose(); resolve({...active, state: 'terminal', outcome: 'released'}); await sending;
  assert.equal(result.outcome, 'released');
  assert.equal(calls, 1);
});

test('disconnect disables confirmation and reconnect does not send an action', async t => {
  const {panel, calls, offline, online} = setup(t);
  await button(panel, 'Release').fire('click'); offline();
  assert.equal(button(panel, 'Confirm release').disabled, true);
  await button(panel, 'Confirm release').fire('click'); assert.equal(calls.length, 0);
  online(); assert.equal(button(panel, 'Confirm release').disabled, false); assert.equal(calls.length, 0);
});

test('rejected and unknown outcomes require inspection, never automatic retries', async t => {
  for (const definitive of [false, true]) {
    let calls = 0, refreshed = 0;
    const {panel} = setup(t, {call: async () => { calls++; throw Object.assign(Error('revision_conflict: changed'), {definitive}); }, onRefresh: () => refreshed++});
    await button(panel, 'Release').fire('click'); await button(panel, 'Confirm release').fire('click');
    assert.equal(calls, 1);
    assert.match(panel.element.textContent, definitive ? /Refresh the lease/ : /outcome is unknown/);
    assert.equal(button(panel, 'Release').disabled, true);
    assert.equal(button(panel, 'Refresh lease').focused, true);
    await button(panel, 'Release').fire('click'); assert.equal(calls, 1);
    await button(panel, 'Refresh lease').fire('click'); assert.equal(refreshed, 1); assert.equal(calls, 1);
  }
});

test('terminal explanation survives an outstanding request settling after the detail rerenders', t => {
  let pending = true;
  const {panel} = setup(t, {lease: {...active, state: 'terminal'}, busy: () => pending});
  assert.match(panel.element.textContent, /still pending/);
  pending = false; panel.updateConnection();
  assert.match(panel.element.textContent, /This lease has ended/);
});

test('native deadline marker defers forward and backward wall renewal decisions to the backend',()=>{
  const elapsed_deadline=new Proxy({}, {get(){throw Error('Browser must not read or derive native remaining time');}});
  const lease={...active,elapsed_deadline};
  for(const shifted of [now+2*86400000,now-2*86400000]){
    assert.deepEqual(leaseActions(lease,shifted),['renew','release','cancel']);
    const plan=leaseActionReview(lease,'renew',{now:shifted,ttl:'15m'});
    assert.deepEqual(plan.params,{id:'parent',revision:3,action:'renew',ttl_ns:900000000000});
    assert.equal(plan.expiresAuditOnly,true);assert.match(plan.paragraphs.join(' '),/native elapsed deadline.*audit-only/);
    assert.equal(plan.expires.getTime(),shifted+900000);
    for(const ttl of ['0m','8d'])assert.throws(()=>leaseActionReview(lease,'renew',{now:shifted,ttl}));
    for(const state of ['ending','needs_attention','terminal','provisioning'])assert.deepEqual(leaseActions({...lease,state},shifted),[]);
  }
  assert.deepEqual(leaseActions(active,now+2*86400000),['release','cancel']);
  assert.throws(()=>leaseActionReview(active,'renew',{now:now-2*86400000,ttl:'15m'}),/extends/);
  assert.equal(leaseActionReview(lease,'renew',{now:NaN,ttl:'15m'}).expires,null);
});

test('native renewal calendar review cannot bypass backend refusal, changed revision or unknown outcome',async t=>{
  for(const failure of [{code:'expired',definitive:true},{code:'revision_conflict',definitive:true},{code:'lost-response',definitive:false}]){
    let calls=0;const lease={...active,elapsed_deadline:{authority:'native fixture'}};
    const {panel}=setup(t,{lease,now:()=>now+2*86400000,call:async(operation,params)=>{
      calls++;assert.equal(operation,'lease_action');assert.equal(params.revision,3);assert.equal(params.ttl_ns,900000000000);
      assert.equal(Object.hasOwn(params,'elapsed_deadline'),false);throw Object.assign(Error(failure.code),failure);
    }});
    await button(panel,'Extend').fire('click');assert.match(panel.element.textContent,/audit only/);
    await button(panel,'Confirm extension').fire('click');assert.equal(calls,1);
    assert.match(panel.element.textContent,failure.definitive?/Refresh the lease/:/outcome is unknown/);
    await button(panel,'Confirm extension')?.fire('click');assert.equal(calls,1);
    assert.equal(button(panel,'Extend').disabled,true);
  }
});
