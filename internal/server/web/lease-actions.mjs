// Shared policy for the lease detail controls. The server remains authoritative:
// a reviewed revision never authorizes an action on a changed lease.
const SECOND = 1000000000n;
const units = {s: SECOND, m: 60n * SECOND, h: 3600n * SECOND, d: 86400n * SECOND};
const consuming = new Set(['active', 'ending', 'needs_attention', 'provisioning', 'reclaiming']);
export const MAX_TTL_NS = 7n * units.d;

export function parseLeaseTTL(text) {
  if (typeof text !== 'string' || text.length > 80) throw Error('Enter a duration such as 15m, 2h or 1d.');
  text = text.trim();
  let total = 0n, offset = 0;
  for (const part of text.matchAll(/(\d+)(?:\.(\d{1,9}))?([smhd])/g)) {
    if (part.index !== offset) throw Error('Use seconds, minutes, hours or days, for example 1h30m.');
    const fraction = BigInt((part[2] || '').padEnd(9, '0'));
    const scaled = (BigInt(part[1]) * SECOND + fraction) * units[part[3]];
    if (scaled % SECOND !== 0n) throw Error('Duration must be representable in nanoseconds.');
    total += scaled / SECOND;
    offset += part[0].length;
  }
  if (!offset || offset !== text.length || total <= 0n || total > MAX_TTL_NS) throw Error('Duration must be greater than zero and at most 7 days.');
  return Number(total); // Seven days in nanoseconds is exactly representable.
}

export function leaseActions(lease, now = Date.now()) {
  if (lease.state === 'queued') return ['cancel'];
  if (lease.state !== 'active') return [];
  // A serialized native deadline is a marker, not browser-computable time.
  // Only the backend can decide whether it remains live or an extension fits.
  return lease.elapsed_deadline != null || Number.isFinite(Date.parse(lease.expires_at)) && Date.parse(lease.expires_at) > now ? ['renew', 'release', 'cancel'] : ['release', 'cancel'];
}

export function attachedConsumers(lease, leases = []) {
  return leases.filter(item => (item.state === 'queued' || consuming.has(item.state)) &&
    (item.request?.storage_lease === lease.id || item.request?.port_lease === lease.id))
    .map(item => ({id: item.id, revision: item.revision})).sort((a, b) => a.id.localeCompare(b.id));
}

export function leaseActionReview(lease, action, {ttl = '15m', now = Date.now(), leases = []} = {}) {
  if (!leaseActions(lease, now).includes(action)) throw Error('This action is unavailable for the displayed lease state. Refresh the lease.');
  const params = {id: lease.id, revision: lease.revision, action};
  const paragraphs = [];
  let expires = null;
  if (action === 'renew') {
    params.ttl_ns = parseLeaseTTL(ttl);
    expires = new Date(now + params.ttl_ns / 1e6);
    if (lease.elapsed_deadline == null && expires.getTime() <= Date.parse(lease.expires_at)) throw Error('Choose a duration from now that extends the current expiry.');
    if (lease.elapsed_deadline != null) {
      if (!Number.isFinite(expires.getTime())) expires = null;
      paragraphs.push('ARM checks the native elapsed deadline and whether this duration extends it. A browser calendar estimate is audit-only, not remaining entitlement.');
    }
    paragraphs.push('The duration starts when ARM accepts this request. It is not added to the existing expiry.');
    paragraphs.push('ARM can deny an extension because of waiting work, memory pressure, pool limits or attached lease deadlines.');
  } else {
    params.consumers = attachedConsumers(lease, leases);
    if (lease.state === 'queued') paragraphs.push('Remove this request from the queue. It will not start or receive a resource grant.');
    else if (lease.request?.kind === 'storage') {
      paragraphs.push('End this storage lease and reclaim its managed files. Save any needed outputs before confirming.');
      if (lease.artifact?.path) paragraphs.push(`Managed location: ${lease.artifact.path}`);
      paragraphs.push('ARM will stop attached supervised jobs, then check ownership and activity before cleanup. Unresolved allocations remain charged.');
    } else if (lease.request?.kind === 'ports') {
      paragraphs.push('End this port lease, stop attached supervised jobs, and reclaim ARM-owned sockets. Any unresolved socket remains charged.');
    } else if (lease.pid || lease.process_identity) paragraphs.push('Stop the ARM-supervised process group. ARM requests termination, then can force termination after its grace period. Capacity stays charged until stopping is confirmed.');
    else paragraphs.push('End this reservation and return its resource claims. This does not authorize killing unrelated processes or deleting arbitrary files.');
    if (lease.request?.storage_lease) paragraphs.push(`Attached storage lease ${lease.request.storage_lease} keeps its own lifetime and cleanup policy.`);
    if (lease.request?.port_lease) paragraphs.push(`Attached port lease ${lease.request.port_lease} keeps its own lifetime.`);
    if (params.consumers.length) paragraphs.push(`Affected attached leases: ${params.consumers.map(item => item.id).join(', ')}.`);
    paragraphs.push(`Recorded outcome: ${action === 'cancel' ? 'cancelled' : 'released'}.`);
  }
  return {params, expires, ...(lease.elapsed_deadline != null && action === 'renew' ? {expiresAuditOnly: true} : {}), paragraphs, title: {renew: 'Extend lease', release: 'Release lease', cancel: 'Cancel lease'}[action]};
}

export function leaseActionResult(lease) {
  if (lease.state === 'ending' || lease.state === 'reclaiming') return 'Request accepted. Stopping or cleanup is still in progress; capacity has not yet been confirmed free.';
  if (lease.state === 'needs_attention') return 'Request accepted, but reclamation needs attention. Unresolved allocations remain charged.';
  if (lease.state === 'terminal') return `Lease ${lease.outcome || 'ended'}.`;
  return 'Lease updated.';
}

// Small DOM component kept independent from websocket/reconnect machinery so a
// declined confirmation and an uncertain transport result can be tested directly.
export function leaseActionPanel({lease, leases = [], call, connected, onResult, onRefresh, busy = () => false, onPending = () => {}, document = globalThis.document, now = Date.now}) {
  const el = (tag, className = '', text = null) => { const item = document.createElement(tag); item.className = className; if (text !== null) item.textContent = text; return item; };
  const root = el('section', 'lease-actions'), bar = el('div', 'lease-action-bar'), review = el('div', 'lease-action-review'), feedback = el('p', 'lease-action-feedback');
  feedback.setAttribute('role', 'status'); review.hidden = true;
  root.append(el('h3', '', 'Lease controls'), bar, review, feedback);
  let disposed = false, sending = false, finished = false, chosen = '', confirm, declineControl, timer, ttl, preview, validation;
  const actionButtons = [], actionSources = new Map();
  const refresh = el('button', 'lease-refresh', 'Refresh lease'); refresh.type = 'button'; refresh.addEventListener('click', () => onRefresh()); root.append(refresh);
  const unavailable = {terminal: 'This lease has ended.', ending: 'Stopping or cleanup is in progress.', reclaiming: 'Reclamation is in progress.', needs_attention: 'Reclamation needs attention. Inspect the record and use ARM’s reconciliation or storage commands.'};
  const idleFeedback = !leaseActions(lease, now()).length ? unavailable[lease.state] || 'No controls are available for this lease state.' : '';
  feedback.textContent = idleFeedback;
  const updateConnection = () => {
    const disabled = !connected() || sending || finished || busy();
    for (const button of actionButtons) button.disabled = disabled;
    if (confirm) confirm.disabled = disabled || Boolean(validation);
    if (declineControl) declineControl.disabled = sending;
    if (ttl) ttl.disabled = sending || finished;
    refresh.disabled = !connected() || sending || busy();
    if (!connected() && !sending && !finished) feedback.textContent = 'Reconnect before changing this lease.';
    else if (connected() && feedback.textContent === 'Reconnect before changing this lease.') feedback.textContent = idleFeedback;
    if (connected() && busy() && !sending && !finished) feedback.textContent = 'Another request for this lease is still pending.';
    else if (!busy() && feedback.textContent === 'Another request for this lease is still pending.') feedback.textContent = idleFeedback;
  };
  function updatePreview() {
    validation = '';
    if (chosen === 'renew') {
      try {
        const plan = leaseActionReview(lease, chosen, {ttl: ttl.value, now: now(), leases});
        preview.textContent = plan.expires ? (plan.expiresAuditOnly ? `Browser calendar estimate (audit only): ${plan.expires.toLocaleString()}. The backend decides the native deadline.` : `Estimated new expiry: ${plan.expires.toLocaleString()}. The accepted expiry will be shown after confirmation.`) : 'Browser calendar estimate unavailable. The backend decides the native deadline.';
      } catch (error) { validation = error.message; preview.textContent = validation; }
    }
    updateConnection();
  }
  function decline() { const source = actionSources.get(chosen); chosen = ''; review.hidden = true; clearInterval(timer); timer = null; if (!disposed) source?.focus(); }
  function choose(action) {
    if (disposed || sending || finished || busy() || !connected()) return;
    if (!leaseActions(lease, now()).includes(action)) { feedback.textContent = 'This lease changed or expired. Refresh it before reviewing another action.'; refresh.hidden = false; return; }
    chosen = action; review.replaceChildren(); review.hidden = false;
    review.append(el('h4', '', {renew: 'Extend lease', release: 'Release lease?', cancel: 'Cancel lease?'}[action]), el('p', 'action-scope', `Lease ${lease.id} · revision ${lease.revision}`), el('p', '', lease.request?.reason || ''));
    ttl = null; preview = null;
    if (action === 'renew') {
      const label = el('label', '', 'New duration from now'); ttl = el('input'); ttl.type = 'text'; ttl.value = '15m'; ttl.setAttribute('aria-label', 'New duration from now'); ttl.autocomplete = 'off'; ttl.spellcheck = false; label.append(ttl);
      preview = el('p', 'action-expiry'); review.append(label, el('p', 'muted', 'Examples: 15m, 2h, 1d or 1h30m. Maximum: 7 days.'), el('p', '', `${lease.elapsed_deadline != null ? 'Recorded calendar expiry (audit only)' : 'Current expiry'}: ${new Date(lease.expires_at).toLocaleString()}`), preview);
      ttl.addEventListener('input', updatePreview);
    }
    // The static consequences do not depend on the duration selected below.
    const plan = action === 'renew' ? {paragraphs: ['The duration starts when ARM accepts this request. It is not added to the existing expiry.', 'Extensions can be denied because of waiting work, memory pressure, pool limits or attached lease deadlines.']} : leaseActionReview(lease, action, {now: now(), leases});
    for (const paragraph of plan.paragraphs) review.append(el('p', '', paragraph));
    const controls = el('div', 'lease-action-bar'), declineButton = el('button', '', 'Keep lease'); declineButton.type = 'button'; declineButton.addEventListener('click', () => { if (!sending) decline(); }); declineControl = declineButton;
    confirm = el('button', action === 'renew' ? 'primary' : 'danger', {renew: 'Confirm extension', release: 'Confirm release', cancel: 'Confirm cancellation'}[action]); confirm.type = 'button'; confirm.addEventListener('click', submit);
    controls.append(declineButton, confirm); review.append(controls);
    clearInterval(timer); if (action === 'renew') timer = setInterval(updatePreview, 1000);
    updatePreview(); (ttl || declineButton).focus();
  }
  async function submit() {
    if (disposed || sending || finished || busy() || !connected()) return;
    let plan;
    try { plan = leaseActionReview(lease, chosen, {ttl: ttl?.value, now: now(), leases}); }
    catch (error) { validation = error.message; if (preview) preview.textContent = validation; updateConnection(); return; }
    sending = true; clearInterval(timer); onPending(true); updateConnection();
    feedback.textContent = 'Sending the reviewed request…';
    try {
      const result = await call('lease_action', plan.params);
      finished = true;
      if (!disposed) { decline(); feedback.textContent = leaseActionResult(result); refresh.hidden = false; }
      onResult(result);
    } catch (error) {
      finished = true;
      if (!disposed) {
        decline(); refresh.hidden = false;
        feedback.textContent = error.definitive ? `${error.message}. Refresh the lease before reviewing another action.` : 'The outcome is unknown because no response was received. The request may have been applied. Refresh the lease and inspect its state before taking another action.';
      }
    } finally { sending = false; onPending(false); if (!disposed) { updateConnection(); if (finished && !refresh.hidden) refresh.focus(); } }
  }
  for (const action of leaseActions(lease, now())) { const button = el('button', action === 'renew' ? 'primary' : '', {renew: 'Extend', release: 'Release', cancel: 'Cancel'}[action]); button.type = 'button'; button.addEventListener('click', () => choose(action)); actionButtons.push(button); actionSources.set(action, button); bar.append(button); }
  updateConnection();
  return {element: root, updateConnection, dispose() { disposed = true; clearInterval(timer); }};
}
