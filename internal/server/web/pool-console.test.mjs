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
  let live = true,
    view = snapshot(),
    cursor = 1,
    held = null;
  const ui = createPoolConsole({
    root,
    document: doc,
    storage: { getItem: () => null, setItem() {} },
    key: () => 'review-key',
    connected: () => live,
    call: async (op, r) => {
      calls.push([op, poolRequestCopy(r)]);
      if (op === 'pool_list') return [poolRequestCopy(view)];
      if (op === 'pool_inspect') return poolRequestCopy(view);
      if (op === 'pool_history')
        return { pool: view.pool.id, entries: [], cursor: 0, more: false };
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
        (n) => visible(n) && n.tagName === 'button' && n.textContent === text
      );
  return {
    ui,
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
  await f.find('Set capacity').fire('click');
  const form = f.root.querySelector('form');
  form.all().find((n) => n.name === 'capacity').value = '0';
  form.all().find((n) => n.name === 'reason').value = 'Retain obligations';
  await form.fire('submit');
  return f.find('Confirm reviewed change');
}
test('pool component decline does not mutate, exact deficit review preserves complete scope and focus', async () => {
  const f = await start();
  assert.match(f.root.textContent, /9,223,372,036,854,775,807/);
  const confirm = await reviewSet(f);
  assert.match(f.root.textContent, /Deficit/);
  assert.match(f.root.textContent, /0 → 9223372036854775807/);
  assert.match(f.root.textContent, /lease-a · revision 2/);
  assert.equal(f.doc.activeElement.textContent, 'Keep unchanged');
  await f.find('Keep unchanged').fire('click');
  assert.equal(f.calls.filter((c) => c[0] === 'pool_mutate').length, 0);
  await f.root.querySelector('form').fire('submit');
  await f.find('Confirm reviewed change').fire('click');
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
  const keep = f.find('Keep unchanged');
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
  await f.find('Confirm reviewed change').fire('click');
  await f.find('Create pool').fire('click');
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
