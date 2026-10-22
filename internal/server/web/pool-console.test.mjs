import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createPoolConsole } from './pool-console.mjs';
import { poolRequestCopy } from './pool-model.mjs';
class Element {
  constructor(tag, doc) {
    this.tagName = tag;
    this.ownerDocument = doc;
    this.children = [];
    this.parentNode = null;
    this.listeners = {};
    this.attributes = {};
    this.dataset = {};
    this._text = '';
    this.value = '';
    this.hidden = false;
    this.disabled = false;
  }
  append(...nodes) {
    for (const node of nodes) {
      node.remove();
      node.parentNode = this;
      this.children.push(node);
    }
  }
  remove() {
    if (this.parentNode) {
      this.parentNode.children = this.parentNode.children.filter(
        (n) => n !== this
      );
      this.parentNode = null;
    }
  }
  replaceChildren(...nodes) {
    for (const old of this.children) old.parentNode = null;
    this.children = [];
    this._text = '';
    this.append(...nodes);
  }
  set textContent(text) {
    this.replaceChildren();
    this._text = String(text);
  }
  get textContent() {
    return this._text + this.children.map((n) => n.textContent).join('');
  }
  setAttribute(k, v) {
    this.attributes[k] = String(v);
  }
  getAttribute(k) { return this.attributes[k] ?? null; }
  showModal() { this.open = true; }
  close() { this.open = false; }
  addEventListener(k, f) {
    (this.listeners[k] ||= []).push(f);
  }
  async fire(k, event = { preventDefault() {} }) {
    for (const f of this.listeners[k] || []) await f(event);
  }
  all() {
    return this.children.flatMap((n) => [n, ...n.all()]);
  }
  contains(n) {
    return this === n || this.all().includes(n);
  }
  querySelectorAll(selector) {
    return this.all().filter((n) =>
      selector.startsWith('[data-')
        ? Object.prototype.hasOwnProperty.call(
            n.dataset,
            selector
              .slice(6, -1)
              .replace(/-([a-z])/g, (_, c) => c.toUpperCase())
          )
        : selector.startsWith('.')
          ? n.className?.split(' ').includes(selector.slice(1))
          : n.tagName === selector
    );
  }
  querySelector(selector) {
    return this.querySelectorAll(selector)[0] || null;
  }
  focus() {
    this.ownerDocument.activeElement = this;
  }
  scrollIntoView() {}
}

const flush = () => new Promise((resolve) => setImmediate(resolve));
const snapshot = () => ({
  pool: {
    id: 'permits',
    name: 'Review permits',
    scope: 'global',
    authority: 'local-host',
    revision: 7,
    capacity: 9223372036854775807n,
    state: 'active',
    owner: '501',
    owner_username: 'alice',
    unit: 'permits',
  },
  reserved: 9223372036854775807n,
  available: 0,
  deficit: 0,
  users: [
    {
      lease: 'lease-a',
      quantity: 9223372036854775807n,
      reason: 'Retain build',
      owner: '501',
      state: 'active',
    },
  ],
  queued: [],
  review: {
    authority: 'local-host',
    consumers: [{ id: 'lease-a', revision: 2 }],
  },
});
function fixture() {
  const doc = {
      activeElement: null,
      createElement: (tag) => new Element(tag, doc),
    },
    root = doc.createElement('section'),
    calls = [];
  let currentRoute = {view: 'resources', pool: 'permits', poolAfter: '0', poolBefore: null};
  let live = true,
    view = snapshot(),
    cursor = 1,
    held = null,
    historyHandler = null,
    listHandler = null;
  const ui = createPoolConsole({
    root,
    document: doc,
    storage: { getItem: () => null, setItem() {} },
    key: () => 'review-key',
    onNavigate: route => { currentRoute = {view: 'resources', ...route}; update(); },
    connected: () => live,
    call: async (op, r) => {
      calls.push([op, poolRequestCopy(r)]);
      if (op === 'pool_list') return listHandler ? listHandler(r) : [poolRequestCopy(view)];
      if (op === 'pool_inspect') return poolRequestCopy(view);
      if (op === 'pool_history_page')
        return historyHandler ? historyHandler(r) : { pool: view.pool.id, entries: [], previous_before: null, next_after: null };
      if (op === 'pool_mutate') {
        if (held) return held;
        return {
          sequence: 1,
          idempotency_key: r.idempotency_key,
          operation: r.operation,
          reason: r.reason,
          before: poolRequestCopy(view.pool),
          after: {
            ...view.pool,
            revision: 8,
            capacity: r.capacity ?? view.pool.capacity,
          },
          actor: '501',
        };
      }
      throw Error(op);
    },
  });
  const update = () =>
    ui.update({
      route: currentRoute,
      status: {
        host: 'local-host',
        owner: '501',
        owner_username: 'alice',
        cursor: cursor++,
      },
    });
  const visible = (n) => !n.hidden && (!n.parentNode || visible(n.parentNode));
  const find = (text) =>
    root
      .all()
      .find(
        (n) => visible(n) && n.tagName === 'button' && (n.getAttribute('aria-label') || n.textContent) === text
      );
  return {
    ui,
    navigate(route) { currentRoute = {view: 'resources', pool: null, poolAfter: null, poolBefore: null, ...route}; update(); },
    getRoute: () => currentRoute,
    setHistory(handler) { historyHandler = handler; },
    setList(handler) { listHandler = handler; },
    root,
    doc,
    calls,
    find,
    update,
    setView(v) {
      view = v;
    },
    setLive(v) {
      live = v;
      ui.updateConnection();
    },
    hold(p) {
      held = p;
    },
  };
}
async function start() {
  const f = fixture();
  f.update();
  await flush();
  await flush();
  return f;
}
async function reviewSet(f) {
  await f.find('Change total capacity…').fire('click');
  const form = f.root.querySelector('form');
  form.all().find((n) => n.name === 'capacity').value = '0';
  form.all().find((n) => n.name === 'reason').value = 'Retain obligations';
  await form.fire('submit');
  return f.find('Set total capacity to 0');
}
test('pool component decline does not mutate, exact deficit review preserves complete scope and focus', async () => {
  const f = await start();
  assert.match(f.root.textContent, /9,223,372,036,854,775,807/);
  const confirm = await reviewSet(f);
  assert.match(f.root.textContent, /Deficit/);
  assert.match(f.root.textContent, /0 → 9223372036854775807/);
  assert.match(f.root.textContent, /lease-a · revision 2/);
  assert.equal(f.doc.activeElement.textContent, 'Cancel change');
  await f.find('Back to edit').fire('click');
  assert.equal(f.calls.filter((c) => c[0] === 'pool_mutate').length, 0);
  await f.root.querySelector('form').fire('submit');
  await f.find('Set total capacity to 0').fire('click');
  await flush();
  const mutations = f.calls.filter((c) => c[0] === 'pool_mutate');
  assert.equal(mutations.length, 1);
  assert.equal(mutations[0][1].capacity, 0);
  assert.equal(mutations[0][1].reviewed.authority, 'local-host');
  assert.equal(mutations[0][1].reviewed.consumers.length, 1);
  assert.equal(mutations[0][1].reviewed.consumers[0].id, 'lease-a');
  assert.equal(mutations[0][1].reviewed.consumers[0].revision, 2);
  assert.match(f.root.textContent, /Recorded/);
  assert.equal(f.root.contains(confirm), false);
});
test('pool component stale revision/consumer set disables confirmation and disconnect never replays', async () => {
  const f = await start();
  const confirm = await reviewSet(f);
  const keep = f.find('Back to edit');
  keep.focus();
  const changed = snapshot();
  changed.pool.revision = 8;
  changed.review.consumers.push({ id: 'new-consumer', revision: 1 });
  f.setView(changed);
  f.update();
  await flush();
  await flush();
  assert.equal(confirm.disabled, true);
  assert.equal(f.doc.activeElement, keep);
  await confirm.fire('click');
  assert.equal(f.calls.filter((c) => c[0] === 'pool_mutate').length, 0);
  f.setLive(false);
  assert.equal(confirm.disabled, true);
  f.setLive(true);
  assert.equal(f.calls.filter((c) => c[0] === 'pool_mutate').length, 0);
});
test('pool mutation result cannot erase a newer draft; in-flight exact request remains independent', async () => {
  const f = await start();
  let release;
  f.hold(
    new Promise((resolve) => {
      release = resolve;
    })
  );
  await reviewSet(f);
  await f.find('Set total capacity to 0').fire('click');
  await f.find('Create pool…').fire('click');
  const name = f.root
    .querySelector('form')
    .all()
    .find((n) => n.name === 'name');
  name.value = 'new unfinished draft';
  name.focus();
  const request = f.calls.find((c) => c[0] === 'pool_mutate')[1];
  release({
    sequence: 1,
    idempotency_key: request.idempotency_key,
    operation: 'set',
    reason: request.reason,
    before: snapshot().pool,
    after: { ...snapshot().pool, revision: 8, capacity: 0 },
    actor: '501',
  });
  await flush();
  await flush();
  assert.equal(name.value, 'new unfinished draft');
  assert.equal(f.doc.activeElement, name);
  assert.equal(f.root.querySelector('form').hidden, false);
  assert.equal(f.calls.filter((c) => c[0] === 'pool_mutate').length, 1);
});

test('pool route controls selection and pages without silently choosing the first row', async () => {
  const f = fixture();
  f.navigate({}); await flush(); await flush();
  assert.equal(f.find('Change total capacity…'), undefined);
  assert.equal(f.calls.filter(([op]) => op === 'pool_history_page').length, 0);
  f.setHistory(async r => ({pool:r.id, entries:[], previous_before:7n, next_after:9223372036854775807n}));
  await f.find('Review permits').fire('click'); await flush(); await flush();
  assert.equal(f.getRoute().pool, 'permits');
  assert.ok(f.find('Change total capacity…'));
  assert.doesNotMatch(f.root.textContent,/No custom pools yet/);
  await f.find('Next page').fire('click'); await flush();
  assert.equal(f.getRoute().poolAfter, '9223372036854775807');
  assert.equal(f.calls.filter(([op]) => op === 'pool_history_page').at(-1)[1].after,9223372036854775807n);
  await f.find('Previous page').fire('click'); await flush();
  assert.equal(f.getRoute().poolBefore, '7');
  assert.equal(f.calls.filter(([op]) => op === 'pool_history_page').at(-1)[1].before,7);
  f.navigate({pool:'missing',poolAfter:'0'}); await flush(); await flush();
  assert.match(f.root.textContent,/Pool unavailable/);
  assert.equal(f.getRoute().pool,'missing');
  assert.equal(f.find('Change total capacity…'),undefined);
  await f.find('All pools').fire('click'); await flush();
  assert.equal(f.getRoute().pool,null);
  assert.equal(f.calls.filter(([op])=>op==='pool_mutate'||op==='pool_lookup').length,0);
});

test('pool journal A to B to A ignores the original late A read and reconnect is read-only', async () => {
  const f = fixture(), pending=[];
  f.setHistory(r=>new Promise(resolve=>pending.push({r,resolve})));
  f.navigate({pool:'permits',poolAfter:'0'}); await flush(); await flush();
  f.navigate({pool:'other',poolBefore:'17'}); await flush();
  f.navigate({pool:'permits',poolAfter:'23'}); await flush();
  const latest=pending.at(-1);
  latest.resolve({pool:'permits',entries:[],previous_before:22,next_after:null}); await flush();
  for(const p of pending.slice(0,-1))p.resolve({pool:p.r.id,entries:[],previous_before:null,next_after:999});
  await flush();
  assert.equal(f.find('Next page').disabled,true);
  assert.equal(f.find('Previous page').disabled,false);
  f.setLive(false);
  f.navigate({pool:'permits',poolAfter:'42'}); const before=f.calls.length;
  await flush(); assert.equal(f.calls.length,before);
  f.setLive(true); await flush(); await flush();
  assert.equal(pending.at(-1).r.after,42n);
  assert.equal(f.calls.filter(([op])=>op==='pool_mutate'||op==='pool_lookup').length,0);
});

test('pool route changes clear unsubmitted reviews but retain independently uncertain operations', async () => {
  const f=await start(); let finish;
  f.hold(new Promise((_, reject)=>{finish=reject;}));
  await reviewSet(f); await f.find('Set total capacity to 0').fire('click');
  finish(Error('connection lost')); await flush(); await flush();
  f.navigate({pool:'missing',poolBefore:'99'}); await flush(); await flush();
  assert.ok(f.find('Check stored result'));
  assert.equal(f.calls.filter(([op])=>op==='pool_mutate').length,1);
  assert.equal(f.calls.filter(([op])=>op==='pool_lookup').length,0);
});

test('leaving Resources clears an unsubmitted pool review and closing lease restores its consumer', async () => {
  const f=await start(); await reviewSet(f);
  f.ui.applyRoute({view:'messages'});
  assert.equal(f.find('Set total capacity to 0'),undefined);
  f.navigate({pool:'permits',poolAfter:'0'}); await flush();
  assert.equal(f.ui.focusLease('lease-a'),true);
  assert.equal(f.doc.activeElement.textContent,'lease-a');
  assert.equal(f.doc.activeElement.parentNode.parentNode.open,true);
  assert.equal(f.calls.filter(([op])=>op==='pool_mutate').length,0);
});

test('deleted pool routes retain their journal while background refresh preserves deliberate focus', async () => {
  const f=fixture(), deleted=snapshot(); deleted.pool.state='deleted';
  f.setView(deleted); f.update(); await flush(); await flush();
  assert.match(f.root.textContent,/Capacity-change journal/);
  for(const label of ['Change total capacity…','Add or remove units…','Pause new requests…','Delete empty pool…'])assert.equal(f.find(label),undefined);
  const refresh=f.find('Refresh pools'); refresh.focus();
  f.update(); await flush(); await flush();
  assert.equal(f.doc.activeElement,refresh);
  assert.ok(f.find('First page'));
  assert.equal(f.calls.filter(([op])=>op==='pool_mutate').length,0);
});

test('background journal refresh preserves page controls and expanded entry focus', async () => {
  const f = fixture(), entries = [{sequence:1, operation:'create', at:'2026-10-21T00:00:00Z', actor:'501', reason:'Keep the audit readable', before:null, after:snapshot().pool, delta:1}];
  f.setHistory(async r => ({pool:r.id, entries, previous_before:null, next_after:1}));
  f.update(); await flush(); await flush();
  const oldNext = f.find('Next page'); oldNext.focus();
  let view = snapshot(); view.pool.revision++; f.setView(view);
  f.update(); await flush(); await flush();
  assert.notEqual(f.find('Next page'), oldNext);
  assert.equal(f.doc.activeElement, f.find('Next page'));
  const entry = f.root.querySelector('.pool-change'), branch = entry.querySelector('details'), summary = branch.querySelector('summary');
  branch.open = true; await branch.fire('toggle'); summary.focus();
  entries.push({...entries[0],sequence:2,reason:'Another journal record'});
  view = snapshot(); view.pool.revision += 2; f.setView(view);
  f.update(); await flush(); await flush();
  assert.equal(f.root.querySelector('.pool-change'), entry);
  assert.equal(branch.open, true);
  assert.equal(f.doc.activeElement, summary);
  assert.equal(f.root.contains(summary), true);
  assert.equal(f.calls.filter(([op]) => op === 'pool_mutate').length, 0);
});
test('pool modal separates editing and review; Escape discards draft and restores action focus', async () => {
  const f = await start();
  const opener = f.find('Change total capacity…'); opener.focus();
  await opener.fire('click');
  const dialog = f.root.querySelector('dialog');
  assert.equal(dialog.open, true);
  assert.equal(dialog.getAttribute('aria-modal'), 'true');
  assert.match(dialog.textContent, /1 · EDIT CHANGE/);
  assert.match(dialog.textContent, /replacement total/);
  assert.equal(f.root.querySelector('.pool-review').hidden, true);
  let prevented = false;
  await dialog.fire('cancel', {preventDefault() { prevented = true; }});
  assert.equal(prevented, true);
  assert.equal(dialog.hidden, true);
  assert.equal(dialog.open, false);
  assert.equal(f.doc.activeElement, opener);
  assert.equal(f.calls.filter(([op]) => op === 'pool_mutate').length, 0);
  await reviewSet(f);
  assert.match(dialog.textContent, /2 · REVIEW & CONFIRM/);
  assert.equal(f.doc.activeElement.textContent, 'Cancel change');
  await f.find('Cancel change').fire('click');
  assert.equal(dialog.hidden, true);
  assert.equal(f.calls.filter(([op]) => op === 'pool_mutate').length, 0);
});
test('pause wording preserves queued obligations and confirmation names the exact action', async () => {
  const f = await start();
  await f.find('Pause new requests…').fire('click');
  const form = f.root.querySelector('form');
  assert.match(form.textContent, /queued requests are not cancelled/);
  assert.match(form.textContent, /Already queued requests may still start when capacity permits/);
  form.all().find(n => n.name === 'reason').value = 'Operator maintenance window';
  await form.fire('submit');
  assert.ok(f.find('Pause new requests'));
  assert.match(f.root.querySelector('.pool-review').textContent, /Already queued requests may still start when capacity permits/);
  assert.equal(f.doc.activeElement.textContent, 'Cancel change');
  await f.find('Back to edit').fire('click');
  assert.match(f.root.querySelector('dialog').textContent, /1 · EDIT CHANGE/);
  assert.equal(f.calls.filter(([op]) => op === 'pool_mutate').length, 0);
});
test('validation errors are visible inside the modal and clear before a corrected review', async () => {
  const f = await start(); await f.find('Change total capacity…').fire('click');
  const form = f.root.querySelector('form');
  const capacity = form.all().find(n => n.name === 'capacity');
  capacity.value = '1.5'; form.all().find(n => n.name === 'reason').value = 'Correct exact count';
  await form.fire('submit');
  const error = f.root.querySelector('.pool-dialog-feedback');
  assert.equal(error.hidden, false); assert.equal(error.getAttribute('role'), 'alert');
  assert.match(error.textContent, /whole base-10/);
  assert.equal(f.calls.filter(([op]) => op === 'pool_mutate').length, 0);
  capacity.value = '2'; await form.fire('submit');
  assert.equal(error.hidden, true); assert.ok(f.find('Set total capacity to 2'));
});
test('closing after a live detail refresh restores the replacement action without stealing journal focus', async () => {
  const f = await start(); const opener = f.find('Change total capacity…'); opener.focus(); await opener.fire('click');
  const changed = snapshot(); changed.pool.revision = 8; f.setView(changed); f.update(); await flush(); await flush();
  assert.equal(f.root.contains(opener), false);
  await f.find('Close without changes').fire('click');
  assert.equal(f.doc.activeElement, f.find('Change total capacity…'));
  assert.equal(f.calls.filter(([op]) => op === 'pool_mutate').length, 0);
});

test('closing after a pool becomes unavailable restores focus to a focusable detail heading', async () => {
  const f = await start(); const opener = f.find('Change total capacity…'); opener.focus(); await opener.fire('click');
  f.setList(() => []); f.update(); await flush(); await flush();
  assert.equal(f.root.contains(opener), false);
  await f.find('Close without changes').fire('click');
  const title = f.root.querySelector('.pool-detail').querySelector('h3');
  assert.equal(title.textContent, 'Pool unavailable');
  assert.equal(title.tabIndex, -1);
  assert.equal(f.doc.activeElement, title);
  assert.equal(f.calls.filter(([op]) => op === 'pool_mutate').length, 0);
});
