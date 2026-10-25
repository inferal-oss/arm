import {jsonView} from './json-view.mjs';
import {bytes, count} from './core.mjs';
import {parseLeaseTTL} from './lease-actions.mjs';

export const REVIEW_CAPABILITY = 'local-lease-reviews-v1';
const SECOND_NS = 1000000000n, MINUTE_NS = 60n * SECOND_NS;
function durationNanos(value) {
  if (typeof value === 'number' && Number.isSafeInteger(value)) value = BigInt(value);
  return typeof value === 'bigint' && value >= 0n && value <= 9223372036854775807n ? value : null;
}
function durationSeconds(seconds) {
  let text = '';
  for (const [unit, scale] of [['d',86400n],['h',3600n],['m',60n],['s',1n]]) {
    const amount = seconds / scale; seconds %= scale; if (amount) text += amount + unit;
  }
  return text || '0s';
}
function durationFraction(value, unit) {
  const whole = value / 1000n, fraction = (value % 1000n).toString().padStart(3,'0').replace(/0+$/, '');
  return whole + (fraction ? '.' + fraction : '') + unit;
}
// Presentation only: never feed a rounded label back into a request.
export function reviewDuration(value) {
  if (value === null || value === undefined) return 'Unavailable';
  const ns = durationNanos(value); if (ns === null) return 'Invalid duration'; if (!ns) return '0s';
  if (ns >= MINUTE_NS) return durationSeconds((ns + SECOND_NS/2n)/SECOND_NS);
  if (ns >= SECOND_NS) { const ms=(ns+500000n)/1000000n; return ms>=60000n ? durationSeconds(ms/1000n) : durationFraction(ms,'s'); }
  if (ns >= 1000000n) { const us=(ns+500n)/1000n; return us>=1000000n ? '1s' : durationFraction(us,'ms'); }
  if (ns >= 1000n) return durationFraction(ns,'µs');
  return ns+'ns';
}
// Exact, lossless form text. The protocol still carries the original integer ns.
export function reviewDurationInput(value) {
  const ns=durationNanos(value); if (ns === null) return ''; if (!ns) return '0s';
  const whole=ns/SECOND_NS, fraction=(ns%SECOND_NS).toString().padStart(9,'0').replace(/0+$/,'');
  let text=durationSeconds(whole); if (text==='0s') text='';
  if (fraction) { const seconds=whole%60n; if (seconds) text=text.slice(0,-(seconds.toString().length+1)); text+=(seconds.toString()+'.'+fraction+'s'); }
  return text;
}
const duration = reviewDuration;
const when = value => value ? new Date(value).toLocaleString() : '—';
export function reviewConfiguration(lease, policy, enabled, values = {}) {
  if (!Number.isSafeInteger(lease.revision) || lease.revision < 1 || !Number.isSafeInteger(policy.policy_generation) || policy.policy_generation < 0 || policy.lease !== lease.id) throw Error('Refresh: the exact lease revision or policy generation cannot be represented safely.');
  const params = {lease: lease.id, lease_revision: lease.revision, policy_generation: policy.policy_generation, enabled};
  if (!enabled) return params;
  if (lease.state !== 'active' || !lease.request?.agent) throw Error('Check-ins require an active lease attributed to a registered agent.');
  params.interval_ns = parseLeaseTTL(values.interval ?? '10m');
  params.reply_timeout_ns = parseLeaseTTL(values.timeout ?? '1m');
  params.contention_interval_ns = values.contention?.trim() && values.contention.trim() !== '0s' ? parseLeaseTTL(values.contention) : 0;
  if (params.interval_ns < 15e9 || params.reply_timeout_ns < 5e9 || params.reply_timeout_ns > 3600e9 || (params.contention_interval_ns && (params.contention_interval_ns < 15e9 || params.contention_interval_ns > params.interval_ns))) throw Error('Use an interval of 15s–7d, a reply window of 5s–1h, and a contention interval of 0s or 15s–the regular interval.');
  return params;
}

// Only operator read/configure operations live here. Agent reply authority stays
// in the existing exact-session polling/CLI path; this component never replies.
export function leaseReviewPanel({lease, call, connected, supported, busy = () => false, onPending = () => {}, onRefresh = () => {}, document = globalThis.document, renderJSON = (value, label) => jsonView(value, {label, document})}) {
  const el = (tag, cls = '', text) => { const e = document.createElement(tag); e.className = cls; if (text !== undefined) e.textContent = text; return e; };
  const root = el('section', 'review-console'), heading = el('div', 'review-console-heading'), status = el('span', 'review-status', supported ? 'Loading' : 'Unavailable');
  heading.append(el('h3', '', 'Agent check-ins'), status);
  const feedback = el('p', 'lease-action-feedback'); feedback.setAttribute('role', 'status');
  const pendingBox = el('div', 'review-pending'), summary = el('div', 'review-summary'), timeline = el('div', 'review-timeline'), editor = el('div', 'lease-action-review'); editor.hidden = true;
  const bar = el('div', 'lease-action-bar'), configure = el('button', '', 'Configure check-ins'), refresh = el('button', '', 'Refresh check-ins'), more = el('button', '', 'Next history page');
  for (const b of [configure, refresh, more]) b.type = 'button'; more.hidden = true;
  root.append(heading, el('p', 'review-principle', 'Ask the owning agent whether these resources are still needed. Replies and silence never renew, release or resize a lease.'), summary, bar, editor, feedback, pendingBox, timeline, more);
  bar.append(configure, refresh);
  let disposed = false, loading = false, sending = false, finished = false, policy, cursor = 0, request = 0, selectedPlan, confirm, decline;
  const fields = {};
  const updateConnection = () => {
    configure.disabled = !supported || !connected() || loading || sending || finished || busy() || !policy;
    refresh.disabled = !supported || !connected() || loading || sending || busy();
    more.disabled = refresh.disabled;
    if (confirm) confirm.disabled = configure.disabled || !selectedPlan;
    if (decline) decline.disabled = sending;
    for (const field of Object.values(fields)) field.disabled = sending || finished;
  };
  function showRecord(record, target = timeline) {
    const card = el('article', 'review-checkpoint'), top = el('div', 'review-checkpoint-heading');
    top.append(el('strong', '', record.agent), el('span', `review-status review-state-${record.state}`, ({pending:'Awaiting agent',replied:'Agent replied',timed_out:'No reply',superseded:'Context changed'})[record.state] || record.state));
    card.append(top, el('p', 'muted', `${when(record.created_at)} · reply by ${when(record.reply_deadline_at)}`));
    for (const context of record.leases ?? []) {
      const block = el('div', 'review-context'), metrics = el('dl', 'review-metrics');
      block.append(el('h4', '', context.purpose || context.lease));
      for (const [label, value] of [['Lease', `${context.lease} · revision ${context.lease_revision}`], ['Claims', `${count(context.resources?.cpu ?? 0)} CPU · ${bytes(context.resources?.memory_bytes ?? 0)} memory · ${bytes(context.resources?.disk_bytes ?? 0)} disk`], ['Time remaining at capture', duration(context.remaining_ns)], ['Waiting requests', `${context.queued_demand ?? 0} with overlapping resource kinds`], ['Usage quality', `${context.usage?.cpu_quality || 'unknown'} CPU · ${context.usage?.memory_quality || 'unknown'} memory${context.usage?.stale ? ' · stale' : ''}${context.usage?.complete ? '' : ' · incomplete'}`], ['Observed CPU / memory', `${count(context.usage?.cpu_cores)} cores / ${bytes(context.usage?.memory_bytes)}`]]) metrics.append(el('dt', '', label), el('dd', '', String(value)));
      for (const [label, values] of [['Custom claims',context.pools],['Ports',context.ports]]) if (values?.length) { block.append(renderJSON(values, label, `review:${record.id}:${context.lease}:${label}`)); }
      block.append(metrics); card.append(block);
    }
    if (record.reply) card.append(el('p', 'review-intent', `Agent intent: ${record.reply.intent}`), el('blockquote', 'review-explanation', record.reply.explanation), el('p', 'muted', 'This explanation is a conversation record. Use Lease controls above to review an actual change.'));
    if (record.detail) card.append(el('p', 'muted', record.detail));
    card.append(renderJSON(record, 'Exact session and complete checkpoint', `review:${record.id}`)); target.append(card);
  }
  async function load(append = false) {
    if (disposed || loading || sending || !supported || !connected() || busy()) return;
    loading = true; updateConnection(); const generation = ++request;
    try {
      // Read policy before displaying a configuration. A response from an old
      // connection/detail cannot turn into fresh authority after disposal.
      const nextPolicy = append ? policy : await call('lease_review_policy', {lease: lease.id});
      if (disposed || generation !== request) return;
      const pendingPage = append ? null : await call('lease_review_list', {lease: lease.id, pending: true});
      const page = await call('lease_review_list', {lease: lease.id, after: append ? cursor : 0});
      if (disposed || generation !== request) return;
      if (!Number.isSafeInteger(page.cursor) || page.cursor < 0 || (append && (page.more || page.items?.length) && page.cursor <= cursor)) throw Error('Invalid check-in cursor; refresh before continuing.');
      policy = nextPolicy; cursor = page.cursor; finished = false; editor.hidden = true;
      status.textContent = policy.enabled ? 'Enabled' : 'Off';
      summary.replaceChildren(el('p', '', policy.enabled ? `Every ${duration(policy.interval_ns)} · reply window ${duration(policy.reply_timeout_ns)}${policy.contention_interval_ns ? ` · ${duration(policy.contention_interval_ns)} while work is waiting` : ''}` : 'Optional and off by default. Delivery uses the agent’s durable ARM inbox.'), el('p', 'muted', `Policy generation ${policy.policy_generation}${policy.enabled ? ` · next scheduled ${when(policy.next_at)}` : ''}`));
      if (pendingPage) { pendingBox.replaceChildren(el('h4', '', 'Awaiting the owning agent')); for (const record of pendingPage.items ?? []) showRecord(record, pendingBox); if (!pendingPage.items?.length) pendingBox.append(el('p', 'muted', 'No reply is currently requested.')); }
      timeline.replaceChildren(el('h4', '', 'Conversation history'));
      for (const record of page.items ?? []) if (record.state !== 'pending') showRecord(record);
      if (!append && !page.items?.length) timeline.append(el('p', 'muted', 'No check-ins recorded for this lease.'));
      more.hidden = !page.more;
      feedback.textContent = page.scheduler_error ? `Check-in scheduling needs attention: ${page.scheduler_error}. Lease deadlines still apply.` : '';
    } catch (error) { if (!disposed) { finished = true; feedback.textContent = error.message; } }
    finally { loading = false; if (!disposed) updateConnection(); }
  }
  function choose() {
    if (configure.disabled || disposed) return;
    editor.replaceChildren(); editor.hidden = false; selectedPlan = null;
    editor.append(el('h4', '', 'Review check-in settings'), el('p', 'action-scope', `Lease ${lease.id} · revision ${lease.revision} · policy generation ${policy.policy_generation}`));
    const enabled = el('input'); enabled.type = 'checkbox'; enabled.checked = policy.enabled; fields.enabled = enabled;
    const enabledLabel = el('label', 'review-toggle', 'Enable periodic check-ins'); enabledLabel.append(enabled); editor.append(enabledLabel);
    for (const [name, label, value] of [['interval','Regular interval', policy.interval_ns ? reviewDurationInput(policy.interval_ns) : '10m'],['contention','Interval while work is waiting (0s disables acceleration)', reviewDurationInput(policy.contention_interval_ns)],['timeout','Agent reply window',policy.reply_timeout_ns ? reviewDurationInput(policy.reply_timeout_ns) : '1m']]) {
      const input = el('input'); input.type = 'text'; input.value = value; input.autocomplete = 'off'; input.spellcheck = false; input.setAttribute('aria-label', label); fields[name] = input; const labelElement = el('label', '', label); labelElement.append(input); editor.append(labelElement);
    }
    const preview = el('p', 'review-preview'), controls = el('div', 'lease-action-bar');
    confirm = el('button', 'primary', 'Save reviewed settings'); decline = el('button', '', 'Keep current settings'); confirm.type = decline.type = 'button';
    const validate = () => { try { selectedPlan = reviewConfiguration(lease, policy, enabled.checked, {interval:fields.interval.value,contention:fields.contention.value,timeout:fields.timeout.value}); preview.textContent = enabled.checked ? 'Send questions to the exact registered owner through ARM polling. No change to the lease expiry or resource claims.' : 'Stop future questions. Existing conversation history remains; no lease is ended.'; } catch (error) { selectedPlan = null; preview.textContent = error.message; } updateConnection(); };
    for (const input of Object.values(fields)) input.addEventListener('input', validate);
    decline.addEventListener('click', () => { if (!sending) { editor.hidden = true; configure.focus(); } });
    confirm.addEventListener('click', async () => {
      if (disposed || sending || finished || !connected() || busy() || !selectedPlan) return;
      const exact = {...selectedPlan}; sending = true; onPending(true); updateConnection(); feedback.textContent = 'Saving the reviewed settings…';
      try { await call('lease_review_configure', exact); finished = true; if (!disposed) { editor.hidden = true; feedback.textContent = 'Settings accepted. Refresh to inspect the current schedule.'; } }
      catch (error) { finished = true; if (!disposed) { editor.hidden = true; feedback.textContent = error.definitive ? `${error.message}. Refresh the lease and policy before another change.` : 'Outcome unknown: the settings may have been saved. Refresh the lease and policy before another change. Nothing will be retried automatically.'; } }
      finally { sending = false; onPending(false); if (!disposed) updateConnection(); }
    });
    controls.append(decline, confirm); editor.append(preview, controls); validate(); enabled.focus();
  }
  configure.addEventListener('click', choose); refresh.addEventListener('click', () => { onRefresh(); }); more.addEventListener('click', () => load(true));
  if (!supported) feedback.textContent = 'Check-ins are available only on a local backend advertising this feature. Remote node controls remain unavailable.';
  updateConnection(); const ready = supported ? load() : Promise.resolve();
  return {element: root, ready, updateConnection, dispose() { disposed = true; request++; }};
}
