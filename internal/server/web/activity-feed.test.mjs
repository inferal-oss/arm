import {test} from 'node:test';
import assert from 'node:assert/strict';
import {activityItems, activityChanges, createActivityFeed} from './activity-feed.mjs';

const at = '2026-10-21T12:00:00Z';
const leaseEvent = (sequence, reason = 'Compile release binary') => ({sequence, at, type: 'lease.created', entity: `lease-${sequence}`, data: {id: `lease-${sequence}`, owner: '501', owner_username: 'alice', state: 'active', request: {reason, agent: 'builder'}}});

test('activity explains attribution and reason, keeps exact ordering and bounds recent history', () => {
  const events = Array.from({length: 205}, (_, i) => leaseEvent(9223372036854775000n + BigInt(i)));
  const items = activityItems({events});
  assert.equal(items.length, 80);
  assert.equal(items[0].sequence, events.at(-1).sequence);
  assert.equal(items[0].title, 'Lease reserved');
  assert.equal(items[0].actor, 'builder · alice');
  assert.equal(items[0].body, 'Compile release binary');
  assert.equal(items[0].lease, events.at(-1).data.id);
  const legacy = activityItems({events: [{...leaseEvent(1), data: {...leaseEvent(1).data, owner_username: undefined}}]})[0];
  assert.equal(legacy.actor, 'builder · UID 501');
  assert.deepEqual(activityChanges(items, items), []);
  const next = activityItems({events: [...events, leaseEvent(9223372036854775999n)]});
  assert.equal(activityChanges(items, next).length, 1);
});

test('message snapshots and delivery/receipt events share a safe human-readable row', () => {
  const message = {id: 'message', sender: 'builder', audience: 'operator', body: '<script>literal, not markup</script>', lease: 'build', created_at: at, deliveries: {builder: {state: 'available'}}};
  const events = [
    {sequence: 2, at, entity: message.id, type: 'message.created', data: message},
    {sequence: 3, at, entity: message.id, type: 'message.delivery', data: message},
    {sequence: 4, at, entity: message.id, type: 'message.receipt', data: {recipient: 'builder', receipt: {acknowledged_at: at}}},
  ];
  const items = activityItems({events, messages: [message]});
  assert.equal(items.length, 1); assert.equal(items[0].key, 'message:message');
  assert.equal(items[0].body, message.body); assert.equal(items[0].title, 'Message to operator');
  assert.equal(items[0].actor, 'builder → Operator'); assert.equal(items[0].status, 'acknowledged');
  assert.equal(items[0].sequence, 4n);
  const snapshot = {...message, deliveries: {builder: {state: 'available', receipt: {acknowledged_at: at}}}};
  const received = activityItems({events: [{sequence: 5, at, entity: message.id, type: 'message.receipt', data: {recipient: 'builder', receipt: {received_at: at}}}], messages: [snapshot]});
  assert.equal(received[0].status, 'acknowledged', 'an older received event must not erase a hydrated acknowledgement');
  assert.equal(activityItems({events: [{sequence: 9, type: 'monitor.sample', at, data: {}}]}).length, 0);
});

// Minimal DOM fixture for renderer/focus contracts; rendered browser acceptance
// is separate. No browser, browser driver or OS collector is emulated here.
class Element {
  constructor(tag, doc) { this.tagName = tag; this.ownerDocument = doc; this.children = []; this.parentNode = null; this.attributes = {}; this.dataset = {}; this.listeners = {}; this._text = ''; this.scrollTop = 0; this.hidden = false; this.open = false; }
  append(...nodes) { for (const n of nodes) { n.remove(); this.children.push(n); n.parentNode = this; } }
  insertBefore(n, before) { n.remove(); const index = before ? this.children.indexOf(before) : this.children.length; this.children.splice(index, 0, n); n.parentNode = this; }
  remove() { if (this.parentNode) this.parentNode.children = this.parentNode.children.filter(n => n !== this); this.parentNode = null; }
  contains(n) { return this === n || this.children.some(c => c.contains(n)); }
  querySelector(selector) { return this.children.flatMap(n => [n, ...n.all()]).find(n => selector === n.tagName || selector.startsWith('[') && n.attributes[selector.slice(1, -1)] !== undefined) ?? null; }
  querySelectorAll() { return this.all().filter(n => n.tagName === 'button' || n.attributes.tabindex === '0'); }
  getClientRects() { return [{}]; }
  all() { return this.children.flatMap(n => [n, ...n.all()]); }
  setAttribute(k, v) { this.attributes[k] = String(v); }
  addEventListener(k, fn) { (this.listeners[k] ||= []).push(fn); }
  removeEventListener(k, fn) { this.listeners[k] = (this.listeners[k] || []).filter(f => f !== fn); }
  fire(k, event = {}) { for (const fn of this.listeners[k] ?? []) fn(event); }
  set textContent(v) { this._text = v; this.children = []; }
  get textContent() { return this._text + this.children.map(n => n.textContent).join(''); }
  focus() { this.ownerDocument.activeElement = this; }
  showModal() { this.open = true; }
  close() { this.open = false; this.fire('close'); }
}
function fixture(compact = false) {
  const doc = {createElement: tag => new Element(tag, doc), activeElement: null};
  const root = doc.createElement('section'), home = doc.createElement('aside'), toggle = doc.createElement('button'), drawer = doc.createElement('dialog');
  home.append(root);
  const media = {matches: compact, addEventListener(k, fn) { this.listener = fn; }, removeEventListener() {}, change(matches) { this.matches = matches; this.listener(); }};
  const controls = {};
  for (const [name, tag] of [['list', 'ol'], ['scroll', 'div'], ['latest', 'button'], ['empty', 'p'], ['count', 'span'], ['connection', 'span'], ['close', 'button'], ['history', 'button']]) {
    const n = doc.createElement(tag); n.setAttribute(`data-feed-${name}`, ''); controls[name] = n;
  }
  controls.scroll.append(controls.list, controls.empty);
  root.append(doc.createElement('h2'), controls.close, controls.connection, controls.latest, controls.scroll, controls.count, controls.history);
  const unread = doc.createElement('span'); unread.setAttribute('data-feed-unread', ''); toggle.append(unread);
  const selected = [];
  const feed = createActivityFeed({root, toggle, drawer, document: doc, media, onLease: id => selected.push(id), onMessages: () => selected.push('messages'), onActivity: () => selected.push('activity')});
  return {root, home, toggle, drawer, doc, media, controls, unread, selected, feed};
}

test('new entries preserve reading scroll/focus; explicit latest resumes and keeps DOM bounded', () => {
  const f = fixture();
  f.feed.update({events: [leaseEvent(1)], connected: true});
  const original = f.controls.list.children[0], button = original.all().find(n => n.tagName === 'button');
  button.focus();
  f.feed.update({events: [leaseEvent(1), leaseEvent(2)], connected: true});
  assert.equal(f.doc.activeElement, button); assert.equal(f.controls.list.children.length, 1);
  assert.equal(f.controls.latest.hidden, false); assert.match(f.controls.latest.textContent, /1 recent update/);
  f.controls.latest.fire('click');
  assert.equal(f.controls.list.children.length, 2); assert.equal(f.doc.activeElement, f.controls.scroll);
  f.controls.scroll.scrollTop = 200;
  f.feed.update({events: [leaseEvent(1), leaseEvent(2), leaseEvent(3)], connected: false});
  assert.equal(f.controls.scroll.scrollTop, 200); assert.equal(f.controls.list.children.length, 2);
  assert.match(f.controls.connection.textContent, /Disconnected.*paused/);
  f.controls.latest.fire('click');
  assert.equal(f.controls.scroll.scrollTop, 0); assert.equal(f.controls.list.children.length, 3);
  assert.equal(f.controls.list.children.find(n => n.dataset.feedKey === 'event:1'), original);
  const literal = '<img src=x onerror=alert(1)>';
  f.doc.activeElement = null;
  f.feed.update({events: Array.from({length: 200}, (_, i) => leaseEvent(i, literal)), connected: true});
  assert.equal(f.controls.list.children.length, 80);
  assert.match(f.controls.list.textContent, /<img src=x/);
  assert.equal(f.controls.list.all().some(n => n.tagName === 'img'), false);
  f.feed.dispose();
});

test('compact feed tracks recent updates, uses one modal panel, and restores focus', () => {
  const f = fixture(true);
  f.feed.update({events: [leaseEvent(1)], connected: true});
  assert.equal(f.unread.hidden, true);
  f.feed.update({events: [leaseEvent(1), leaseEvent(2)], connected: true});
  assert.equal(f.unread.textContent, '1'); assert.match(f.toggle.attributes['aria-label'], /1 recent update$/);
  f.toggle.fire('click');
  assert.equal(f.drawer.open, true); assert.equal(f.root.parentNode, f.drawer); assert.equal(f.unread.hidden, true);
  f.controls.close.focus(); let prevented = false;
  f.drawer.fire('keydown', {key: 'Tab', shiftKey: true, preventDefault() { prevented = true; }});
  assert.equal(prevented, true); assert.equal(f.doc.activeElement, f.controls.history);
  f.controls.close.fire('click');
  assert.equal(f.drawer.open, false); assert.equal(f.root.parentNode, f.home); assert.equal(f.doc.activeElement, f.toggle);
  f.toggle.fire('click');
  const lease = f.controls.list.children[0].all().find(n => n.tagName === 'button'); lease.fire('click');
  assert.equal(f.drawer.open, false); assert.deepEqual(f.selected, ['lease-2']);
  f.toggle.fire('click'); f.media.change(false);
  assert.equal(f.drawer.open, false); assert.equal(f.root.parentNode, f.home);
  f.feed.dispose();
});

test('a queued close event cannot remove a reopened drawer or steal its focus', () => {
  const f = fixture(true), pending = [];
  f.drawer.close = () => { f.drawer.open = false; pending.push(() => f.drawer.fire('close')); };
  f.feed.update({events: [leaseEvent(1)], connected: true});
  f.toggle.fire('click'); f.controls.close.fire('click');
  f.toggle.fire('click'); f.controls.close.focus();
  pending.shift()();
  assert.equal(f.drawer.open, true); assert.equal(f.root.parentNode, f.drawer);
  assert.equal(f.toggle.attributes['aria-expanded'], 'true'); assert.equal(f.doc.activeElement, f.controls.close);
  f.feed.dispose();
});

test('legacy feed attribution supplements the same-host UID with an explicitly current account name', () => {
  const event = leaseEvent(1);delete event.data.owner_username;event.data.host='local';
  const ownerContext={host:'local',owner:'501',owner_username:'current-user'};
  assert.equal(activityItems({events:[event],ownerContext})[0].actor,'builder · current-user (UID 501; current account)');
  assert.equal(activityItems({events:[{...event,data:{...event.data,host:'remote'}}],ownerContext})[0].actor,'builder · UID 501');
  assert.equal(event.data.owner_username,undefined);
});
