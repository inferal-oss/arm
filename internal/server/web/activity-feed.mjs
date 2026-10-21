import {leaseStateLabel} from './lease-state.mjs';
import {ownerDisplay} from './core.mjs';

const text = (value, limit = 260) => typeof value === 'string' ? value.length > limit ? `${value.slice(0, limit)}…` : value : '';
const titleCase = value => value.replaceAll(/[._-]/g, ' ').replace(/^./, c => c.toUpperCase());
const stamp = value => Number.isFinite(Date.parse(value)) ? Date.parse(value) : 0;
const sequence = value => { try { return BigInt(value ?? 0); } catch { return 0n; } };
const owner = (value, current) => value?.owner_username ? text(value.owner_username, 80) : value?.owner ? ownerDisplay(value, current, value.host || current?.host) : '';

function messageStatus(message) {
  const states = new Map();
  for (const delivery of Object.values(message.deliveries ?? {})) {
    const label = delivery.receipt?.acknowledged_at ? 'acknowledged' : delivery.receipt?.received_at ? 'received'
      : ({available: 'in inbox', pending: 'delivery pending', queued: 'queued', responded: 'replied', unknown: 'delivery uncertain', failed: 'delivery failed'})[delivery.state] ?? text(delivery.state, 40);
    if (label) states.set(label, (states.get(label) ?? 0) + 1);
  }
  return [...states].map(([label, count]) => `${count > 1 ? `${count} ` : ''}${label}`).join(' · ');
}

// One bounded, read-only view over the dashboard's existing event/message data.
// Message snapshots and delivery events share one row rather than duplicating
// every notification and receipt. Exact sequence integers are never narrowed.
export function activityItems({events = [], messages = [], leases = [], ownerContext = null} = {}, limit = 80) {
  const bound = Math.max(1, Math.min(200, limit)), records = new Map(), byLease = new Map(leases.map(l => [l.id, l]));
  const byMessage = new Map(messages.filter(m => typeof m.id === 'string').map(m => [m.id, {...m}]));
  for (const event of [...events].sort((a, b) => sequence(a.sequence) < sequence(b.sequence) ? -1 : sequence(a.sequence) > sequence(b.sequence) ? 1 : 0)) {
    const kind = text(event.type, 100), data = event.data && typeof event.data === 'object' ? event.data : {};
    if (kind.startsWith('message.')) {
      const id = text(data.id || event.entity, 160);
      if (!id) continue;
      const existing = byMessage.get(id), message = typeof data.body === 'string' ? {...data, ...existing} : existing;
      if (!message) continue;
      if (kind === 'message.receipt' && data.recipient && data.receipt) {
        message.deliveries = {...message.deliveries, [data.recipient]: {...message.deliveries?.[data.recipient], receipt: {...message.deliveries?.[data.recipient]?.receipt, ...data.receipt}}};
      }
      byMessage.set(id, message);
      records.set(`message:${id}`, {message, at: event.at, sequence: sequence(event.sequence)});
      continue;
    }
    if (!/^(lease|storage|ports|incident|request|pool|shutdown|agent|leases)\./.test(kind)) continue;
    const leaseID = typeof data.request === 'object' ? data.id : text(data.lease, 160), lease = data.request ? data : byLease.get(leaseID || event.entity);
    const captions = {
      'lease.created': data.state === 'queued' ? 'Lease queued' : 'Lease reserved', 'lease.granted': 'Lease granted', 'lease.started': 'Work started',
      'lease.renewed': 'Lease extended', 'lease.ending': 'Work stopping', 'lease.released': 'Lease released', 'lease.cancelled': 'Lease cancelled',
      'lease.completed': 'Work completed', 'lease.failed': 'Work failed', 'lease.expired': 'Lease expired', 'lease.finished': 'Work finished', 'lease.recovery_required': 'Work needs review', 'lease.launch_failed': 'Work could not start',
      'lease.exited': 'Work finished', 'lease.revoked': 'Lease revoked', 'lease.reconciled': 'Lease checked',
      'storage.needs_attention': 'Storage needs review', 'storage.handed_off': 'Storage handed back', 'ports.needs_attention': 'Port needs review',
      'storage.created': 'Build storage ready', 'storage.cleanup_planned': 'Storage cleanup started', 'storage.cleanup_progress': 'Storage cleanup in progress',
      'pool.create': 'Resource pool created', 'pool.adjust': 'Resource capacity changed', 'pool.set': 'Resource capacity changed',
      'pool.drain': 'Resource pool paused', 'pool.resume': 'Resource pool resumed', 'pool.delete': 'Resource pool removed', 'agent.registered': 'Agent registered',
      'incident.warning': 'Usage warning', 'incident.revocation_requested': 'Lease revocation requested', 'request.cancelled': 'Request cancelled',
      'leases.bulk_started': 'Bulk lease action started', 'shutdown.started': 'Backend draining', 'shutdown.queue_cancelled': 'Waiting requests cancelled',
      'shutdown.interrupted': 'Shutdown interrupted', 'shutdown.blocked': 'Shutdown needs review', 'shutdown.ready': 'Backend stopping',
    };
    const title = captions[kind] ?? titleCase(kind);
    const attribution = lease ?? data, agent = text(attribution.request?.agent || attribution.agent, 80);
    const actor = [agent, owner(attribution, ownerContext) || text(data.actor_username, 80) || (data.actor ? `UID ${data.actor}` : '')].filter(Boolean).join(' · ');
    const reason = text(lease?.request?.reason || data.reason || data.detail || data.description || data.after?.description);
    records.set(`event:${event.sequence}`, {key: `event:${event.sequence}`, kind: /attention|warning|failed|recovery|interrupted|blocked|revocation/.test(kind) ? 'attention' : 'lease',
      title, body: reason || (kind.startsWith('agent.') ? `Contact ${text(event.entity, 80)}` : ''), actor,
      status: lease?.state || data.state || data.after?.state ? leaseStateLabel(lease ?? (data.state ? data : data.after)) : '', at: event.at, sequence: sequence(event.sequence), lease: lease?.id || leaseID || '', message: false});
  }
  for (const [id, message] of byMessage) {
    const row = records.get(`message:${id}`);
    const sender = message.sender === 'local-client' ? 'Local client' : message.sender === 'operator' ? 'Operator' : text(message.sender, 80);
    const audience = message.audience === 'operator' ? 'Operator' : message.audience === 'broadcast' ? 'All registered agents' : text(message.audience, 80);
    records.set(`message:${id}`, {key: `message:${id}`, kind: 'message', title: message.audience === 'operator' ? 'Message to operator' : message.audience === 'broadcast' ? 'Operator broadcast' : 'Message to agent',
      body: text(message.body), actor: `${sender || 'Agent'} → ${audience || 'Recipient'}`, status: messageStatus(message),
      at: row?.at || message.created_at, sequence: row?.sequence ?? 0n, lease: text(message.lease, 160), message: true});
  }
  return [...records.values()].sort((a, b) => stamp(b.at) - stamp(a.at) || (a.sequence < b.sequence ? 1 : a.sequence > b.sequence ? -1 : a.key.localeCompare(b.key))).slice(0, bound);
}

const fingerprint = item => JSON.stringify({...item, sequence: String(item.sequence)});
export function activityChanges(previous, next) {
  const before = new Map(previous.map(item => [item.key, fingerprint(item)]));
  return next.filter(item => before.get(item.key) !== fingerprint(item)).map(item => item.key);
}

export function createActivityFeed({root, toggle, drawer, onLease, onMessages, onActivity, document = globalThis.document, media = document.defaultView.matchMedia('(max-width:1199px), (max-height:560px)')} = {}) {
  const home = root.parentNode, list = root.querySelector('[data-feed-list]'), scroll = root.querySelector('[data-feed-scroll]');
  const latest = root.querySelector('[data-feed-latest]'), empty = root.querySelector('[data-feed-empty]'), count = root.querySelector('[data-feed-count]');
  const connection = root.querySelector('[data-feed-connection]'), close = root.querySelector('[data-feed-close]'), history = root.querySelector('[data-feed-history]');
  const heading = root.querySelector('h2'), unread = toggle.querySelector('[data-feed-unread]'), rows = new Map(), unseen = new Set();
  let items = [], rendered = [], initialized = false, compact = media.matches, returnFocus = true, wasConnected = false;
  const el = (tag, className, value = '') => { const n = document.createElement(tag); n.className = className; n.textContent = value; return n; };
  const setText = (node, value) => { if (node.textContent !== value) node.textContent = value; };
  function refreshCounts() {
    const changed = activityChanges(rendered, items).length;
    latest.hidden = changed === 0;
    setText(latest, `${changed} recent ${changed === 1 ? 'update' : 'updates'} · View latest`);
    unread.hidden = unseen.size === 0; setText(unread, String(unseen.size));
    toggle.setAttribute('aria-label', `Open live activity${unseen.size ? `, ${unseen.size} recent ${unseen.size === 1 ? 'update' : 'updates'}` : ''}`);
  }
  function entry(item) {
    let row = rows.get(item.key);
    if (!row) {
      const box = el('li', 'feed-entry'), head = el('div', 'feed-entry-heading'), title = el('h3', ''), time = el('time', '');
      const body = el('p', 'feed-entry-body'), actor = el('p', 'feed-entry-actor'), status = el('p', 'feed-entry-status'), links = el('div', 'feed-entry-links');
      head.append(title, time); box.append(head, body, actor, status, links); box.dataset.feedKey = item.key;
      row = {box, title, time, body, actor, status, links}; rows.set(item.key, row);
    }
    row.box.dataset.kind = item.kind;
    for (const key of ['title', 'body', 'actor', 'status']) { setText(row[key], item[key]); row[key].hidden = !item[key]; }
    const date = new Date(item.at), valid = Number.isFinite(date.getTime());
    setText(row.time, valid ? date.toLocaleTimeString([], {hour: '2-digit', minute: '2-digit'}) : '—');
    row.time.setAttribute('datetime', valid ? date.toISOString() : ''); row.time.title = valid ? date.toLocaleString() : 'Time unavailable';
    if (item.lease && !row.lease) {
      row.lease = el('button', 'link-button'); row.lease.type = 'button'; row.links.append(row.lease);
      row.lease.addEventListener('click', () => navigate(() => onLease?.(row.leaseID)));
    }
    row.leaseID = item.lease;
    if (row.lease) { row.lease.hidden = !item.lease; setText(row.lease, `Lease ${item.lease.slice(0, 8)} ↗`); row.lease.setAttribute('aria-label', `Inspect lease ${item.lease}`); }
    if (item.message && !row.message) {
      row.message = el('button', 'link-button', 'Open messages ↗'); row.message.type = 'button'; row.links.append(row.message);
      row.message.addEventListener('click', () => navigate(() => onMessages?.()));
    }
    return row.box;
  }
  function render(force = false) {
    if (!force && (scroll.scrollTop > 8 || list.contains(document.activeElement))) { refreshCounts(); return; }
    empty.hidden = items.length > 0;
    setText(empty, initialized ? 'No lease changes or messages in the loaded window yet.' : 'Waiting for local activity…');
    setText(count, `${items.length} recent ${items.length === 1 ? 'entry' : 'entries'}`);
    if (!force && rendered.length === items.length && rendered.every((item, i) => item.key === items[i].key) && activityChanges(rendered, items).length === 0) { refreshCounts(); return; }
    const keep = new Set(items.map(item => item.key));
    for (const [key, row] of rows) if (!keep.has(key)) { row.box.remove(); rows.delete(key); }
    // No focused entry is moved: updates pause while focus is inside the list.
    for (const [i, item] of items.entries()) { const row = entry(item); if (list.children[i] !== row) list.insertBefore(row, list.children[i] ?? null); }
    rendered = items;
    if (force) scroll.scrollTop = 0;
    refreshCounts();
  }
  function restoreHome() { if (root.parentNode !== home) home.append(root); }
  function navigate(action) { returnFocus = false; if (drawer.open) drawer.close(); restoreHome(); action(); }
  function openDrawer() { if (!compact) return; unseen.clear(); returnFocus = true; drawer.append(root); drawer.showModal(); toggle.setAttribute('aria-expanded', 'true'); refreshCounts(); }
  function closeDrawer() { drawer.close(); }
  function drawerClosed() { if (drawer.open) return; restoreHome(); toggle.setAttribute('aria-expanded', 'false'); if (returnFocus && compact) toggle.focus(); returnFocus = true; }
  function modeChanged() {
    compact = media.matches;
    if (!compact && drawer.open) { returnFocus = false; drawer.close(); restoreHome(); heading.focus(); }
    else if (compact && root.contains(document.activeElement)) toggle.focus();
    if (!compact) unseen.clear(); refreshCounts();
  }
  function showLatest() { unseen.clear(); render(true); scroll.focus(); }
  function resume() { if (scroll.scrollTop <= 8 && !list.contains(document.activeElement)) render(); }
  function blur() { queueMicrotask(resume); }
  function outside(event) { const r = drawer.getBoundingClientRect(); if (event.target === drawer && (event.clientX < r.left || event.clientX > r.right || event.clientY < r.top || event.clientY > r.bottom)) drawer.close(); }
  function containKeyboard(event) {
    if (event.key !== 'Tab' || !drawer.open) return;
    const controls = [...drawer.querySelectorAll('button,[tabindex="0"]')].filter(el => !el.hidden && !el.disabled && el.getClientRects().length > 0);
    const first = controls[0], last = controls.at(-1), active = document.activeElement;
    if (first && (event.shiftKey && active === first || !event.shiftKey && active === last || !drawer.contains(active))) {
      event.preventDefault(); (event.shiftKey ? last : first).focus();
    }
  }
  function openHistory() { navigate(() => onActivity?.()); }
  toggle.addEventListener('click', openDrawer); close.addEventListener('click', closeDrawer); drawer.addEventListener('close', drawerClosed); drawer.addEventListener('click', outside);
  drawer.addEventListener('keydown', containKeyboard);
  latest.addEventListener('click', showLatest); history.addEventListener('click', openHistory); scroll.addEventListener('scroll', resume); list.addEventListener('focusout', blur); media.addEventListener('change', modeChanged);
  return {
    update(snapshot) {
      const next = activityItems(snapshot);
      if (initialized && compact && !drawer.open) for (const key of activityChanges(items, next)) unseen.add(key);
      const present = new Set(next.map(item => item.key)); for (const key of unseen) if (!present.has(key)) unseen.delete(key);
      items = next; initialized ||= next.length > 0 || snapshot.connected;
      wasConnected ||= snapshot.connected;
      setText(connection, snapshot.connected ? 'Live · newest first' : wasConnected ? 'Disconnected · updates paused' : 'Connecting to local backend');
      connection.dataset.live = String(Boolean(snapshot.connected));
      render();
    },
    dispose() {
      returnFocus = false; if (drawer.open) drawer.close(); restoreHome();
      toggle.removeEventListener('click', openDrawer); close.removeEventListener('click', closeDrawer); drawer.removeEventListener('close', drawerClosed); drawer.removeEventListener('click', outside);
      drawer.removeEventListener('keydown', containKeyboard);
      latest.removeEventListener('click', showLatest); history.removeEventListener('click', openHistory); scroll.removeEventListener('scroll', resume); list.removeEventListener('focusout', blur); media.removeEventListener('change', modeChanged);
    },
  };
}
