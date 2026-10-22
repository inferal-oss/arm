import {parseJSON, stringifyJSON, numeric, count, bytes, displayUnit, quantity, percent, mergeHistory, chartPaths, ownerDisplay} from './core.mjs';
import {leaseStateLabel, leaseVisible, isInactiveLease, readHideInactive, saveHideInactive} from './lease-state.mjs';
import {allDevices,selectedDeviceIDs,aggregateIO,aggregateIOWindow} from './io-devices.mjs';
import {createIOSelector} from './io-selector.mjs';
import {jsonView} from './json-view.mjs';
import {metricHelp} from './metric-help.mjs';
import {leaseActionPanel, leaseActionResult} from './lease-actions.mjs';
import {createDashboardHistory} from './route.mjs';
import {installDashboardShortcuts} from './dashboard-shortcuts.mjs';
import {createActivityFeed} from './activity-feed.mjs';
import {createOperatorMessaging} from './operator-messaging.mjs';
import {createPoolConsole} from './pool-console.mjs';
import {createConnectionLinkHandler, createDashboardAuthentication} from './connection-links.mjs';

const jsonExpansions = new Map();
function recordJSON(value, label, key, open = false) {
  if (!jsonExpansions.has(key)) {
    if (jsonExpansions.size >= 256) jsonExpansions.delete(jsonExpansions.keys().next().value);
    jsonExpansions.set(key, new Map());
  }
  return jsonView(value, {label, open, expansion: jsonExpansions.get(key)});
}

const $ = id => document.getElementById(id);
const node = (tag, className = '', text = null) => {
  const el = document.createElement(tag);
  el.className = className;
  if (text !== null) el.textContent = text;
  return el;
};
const svg = (tag, attrs) => {
  const el = document.createElementNS('http://www.w3.org/2000/svg', tag);
  for (const [key, value] of Object.entries(attrs)) el.setAttribute(key, String(value));
  return el;
};
const when = value => value ? new Date(value).toLocaleString() : '—';
const ownerLabel = (record, host = record?.host) => ownerDisplay(record, state.status, host);
const rate = value => value == null ? 'unavailable' : `${bytes(value)}/s`;
const empty = text => node('div', 'empty', text);
const badge = (text, kind = text) => { const el = node('span', 'badge', text); el.dataset.kind = kind; return el; };
const state = {status: null, statusReceived: 0, statusTime: null, messages: [], contacts: [], events: [], history: [], epoch: '', cursor: 0, monitor: null, received: 0, connected: false};
let leasePreferenceStorage; try { leasePreferenceStorage = localStorage; } catch { /* A blocked preference store keeps this session's choice in memory. */ }
let hideInactiveLeases = readHideInactive(leasePreferenceStorage);
let connection, view = 'monitor', leaseFilter = '', resourceFilter = '', detailRequest = 0;
let session = null, retry = 0, retryTimer, refreshTimer, refreshBusy = false, refreshAgain = false;
let detailPanel = null;
let poolConsole = null;
let detailLeaseID = '';
const pendingLeaseActions = new Set();
const navigation = createDashboardHistory({location, history, onRoute: applyDashboardRoute});
const activityFeed = createActivityFeed({root:$('live-feed'),toggle:$('feed-toggle'),drawer:$('feed-drawer'),onLease:id=>showLease(id),onMessages:()=>selectView('messages'),onActivity:()=>selectView('activity')});
let messageStorage; try { messageStorage = sessionStorage; } catch { /* Pending requests remain in memory. */ }
const operatorMessaging = createOperatorMessaging({root:$('messages'),call:(...args)=>connection.call(...args),connected:()=>state.connected,onLease:id=>showLease(id),storage:messageStorage});
function updateOperatorMessaging() { operatorMessaging.update({contacts:state.contacts,messages:state.messages,leases:state.status?.leases ?? []}); }
function updateActivityFeed() { activityFeed.update({events:state.events,messages:state.messages,leases:state.status?.leases ?? [],ownerContext:state.status,connected:state.connected}); }

function notice(text = '') { $('notice').textContent = text; $('notice').hidden = !text; }
function connectionState(text, live = false) {
  $('connection').textContent = text;
  $('connection').dataset.state = live ? 'live' : 'offline';
  state.connected = live;
  detailPanel?.updateConnection();
  updateActivityFeed();
  updateOperatorMessaging();
  poolConsole?.updateConnection();
}

class Backend {
  constructor(token) {
    this.pending = new Map(); this.next = 0;
    this.socket = new WebSocket(`ws://${location.host}/v1/ws`, ['arm.v1', `arm.auth.${token}`]);
    this.ready = new Promise((resolve, reject) => {
      const timer = setTimeout(() => { reject(Error('Connection timed out')); this.socket.close(); }, 10000);
      this.socket.addEventListener('open', () => { clearTimeout(timer); resolve(); }, {once: true});
      this.socket.addEventListener('close', () => { clearTimeout(timer); reject(Error('Connection refused')); }, {once: true});
    });
    this.socket.addEventListener('message', event => {
      if (connection !== this) return;
      try {
        const response = parseJSON(event.data);
        if (response.version !== 1) throw Error('Unsupported protocol version');
        if (response.event) { addEvent(response.event); scheduleRefresh(); }
        else if (response.telemetry && response.subscription === this.monitorID) acceptMonitor(response.telemetry);
        else if (response.id && this.pending.has(response.id)) {
          const pending = this.pending.get(response.id); this.pending.delete(response.id); clearTimeout(pending.timer);
          if (response.error) pending.reject(Object.assign(Error(`${response.error.code}: ${response.error.message}`), {code: response.error.code, definitive: true}));
          else pending.resolve(response.result);
        }
      } catch (error) { notice(error.message); this.socket.close(); }
    });
    this.socket.addEventListener('close', () => {
      for (const p of this.pending.values()) { clearTimeout(p.timer); p.reject(Error('Backend disconnected')); }
      this.pending.clear();
      if (connection === this) {
        connectionState('Disconnected · retrying');
        updateFreshness();
        if (Date.now() < Date.parse(session?.expires_at)) retryTimer = setTimeout(connect, Math.min(15000, 1000 * 2 ** Math.min(retry++, 4)));
        else notice('Dashboard session expired. Run arm dashboard for a new connection link.');
      }
    });
  }
  disconnect() {
    for (const pending of this.pending.values()) { clearTimeout(pending.timer); pending.reject(Error('Dashboard session changed; the prior request outcome may be unknown')); }
    this.pending.clear(); this.socket.close();
  }
  call(operation, params = {}) {
    if (this.socket.readyState !== WebSocket.OPEN) return Promise.reject(Error('Backend disconnected'));
    if (this.pending.size >= 4) return Promise.reject(Error('Dashboard request limit reached'));
    const id = String(++this.next);
    if (operation === 'monitor_subscribe') this.monitorID = id;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { this.pending.delete(id); reject(Error(`${operation} timed out`)); }, 15000);
      this.pending.set(id, {resolve, reject, timer});
      try { this.socket.send(stringifyJSON({version: 1, id, operation, params})); }
      catch (error) { clearTimeout(timer); this.pending.delete(id); reject(error); }
    });
  }
}

const authentication = createDashboardAuthentication({
  exchange: async (ticket, signal) => {
    const response = await fetch('/v1/browser/session', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({ticket}), credentials: 'omit', cache: 'no-store', redirect: 'error', signal});
    if (!response.ok) throw Error('Connection link expired or already used. Run arm dashboard again.');
    return response.json();
  },
  readSession: () => { try { return JSON.parse(sessionStorage.getItem('arm-session') || 'null'); } catch { return null; } },
  writeSession: value => { try { sessionStorage.setItem('arm-session', JSON.stringify(value)); } catch { /* In-memory credentials still work. */ } },
  clearSession: () => { try { sessionStorage.removeItem('arm-session'); } catch { /* Storage may be unavailable. */ } },
  onStart: fresh => {
    clearTimeout(retryTimer); clearTimeout(refreshTimer); refreshTimer = null; retry = 0;
    const previous = connection; connection = null; session = null;
    previous?.disconnect(); // Settle outstanding requests immediately, never replay.
    connectionState(fresh ? 'Authenticating' : 'Connecting'); notice();
  },
  onSession: async value => { session = value; await connect(); },
  onError: error => { connectionState('Not connected'); notice(error.message); },
});
const connectionLinks = createConnectionLinkHandler({location, navigation,
  onConnect: link => authentication.start(link), onStored: () => authentication.start()});

async function connect() {
  clearTimeout(retryTimer);
  connectionState(retry ? 'Reconnecting' : 'Connecting');
  const current = connection = new Backend(session.token);
  try {
    await current.ready;
    const status = await current.call('status');
    if (connection !== current) return;
    state.status = status; state.statusReceived = performance.now(); state.statusTime = new Date(); $('host').textContent = status.host;
    await current.call('subscribe', {after: status.cursor});
    acceptMonitor(await current.call('monitor_subscribe', {epoch: state.epoch, after: state.cursor}));
    const after = BigInt(status.cursor) > 100n ? BigInt(status.cursor) - 100n : 0;
    const events = await current.call('events', {after});
    for (const event of events ?? []) addEvent(event, false);
    state.messages = (await current.call('messages') ?? []).slice(-200);
    state.contacts = await current.call('agents') ?? [];
    if (connection !== current || current.socket.readyState !== WebSocket.OPEN) return;
    retry = 0; connectionState('● Connected', true); notice(); applyDashboardRoute(navigation.current); updateFreshness(); scheduleRefresh();
  } catch (error) {
    if (connection !== current) return;
    notice(`${error.message}. If the backend restarted, run arm dashboard for a fresh link.`);
    current.socket.close();
  }
}

function scheduleRefresh() {
  if (refreshTimer) return;
  refreshTimer = setTimeout(() => { refreshTimer = null; refresh(); }, 200);
}
async function refresh() {
  if (refreshBusy) { refreshAgain = true; return; }
  if (!state.connected) return;
  refreshBusy = true;
  const current = connection;
  try {
    const status = await current.call('status');
    const messages = await current.call('messages');
    const contacts = await current.call('agents');
    if (connection !== current) return;
    state.status = status; state.statusReceived = performance.now(); state.statusTime = new Date(); state.messages = (messages ?? []).slice(-200); state.contacts = contacts ?? [];
    updateOperatorMessaging();
    updateActivityFeed();
    if (view !== 'monitor') renderCurrent();
  } catch (error) { if (connection === current) notice(error.message); }
  finally { refreshBusy = false; if (refreshAgain) { refreshAgain = false; scheduleRefresh(); } }
}
function addEvent(event, render = true) {
  const events = new Map(state.events.map(e => [String(e.sequence), e]));
  events.set(String(event.sequence), event);
  state.events = [...events.values()].sort((a, b) => a.sequence < b.sequence ? -1 : a.sequence > b.sequence ? 1 : 0).slice(-200);
  if (render && view === 'activity') renderActivity();
  if (render) updateActivityFeed();
}
function acceptMonitor(page) {
  state.history = mergeHistory(state.history, page, state.epoch);
  state.epoch = page.epoch; state.cursor = page.cursor; state.monitor = page; state.received = performance.now();
  if (view === 'monitor') renderMonitor();
  scheduleRefresh(); // Live memory pool changes do not create durable events.
  updateFreshness();
}
function updateFreshness() {
  if (view !== 'monitor' && state.status) {
    const age = Math.max(0, Math.floor((performance.now() - state.statusReceived) / 1000));
    $('freshness').textContent = `${state.connected ? '' : 'STALE · '}state updated ${age}s ago`;
    $('freshness').title = `Last state snapshot: ${when(state.statusTime)}`;
    $('cadence').textContent = 'Snapshot + durable events';
    return;
  }
  const page = state.monitor;
  if (!page) return;
  const age = Number(page.age_ns) / 1e6 + performance.now() - state.received;
  const stale = !state.connected || page.stale || age > Number(page.stale_after_ns) / 1e6;
  $('freshness').textContent = `${stale ? 'STALE · ' : ''}${$('monitor').dataset.paused ? 'view paused · ' : ''}sample ${Math.max(0, Math.floor(age / 1000))}s ago`;
  $('freshness').title = `Last sample: ${when(page.latest?.at)}`;
  $('cadence').textContent = `${Number(page.cadence_ns) / 1e9}s cadence · ${state.history.length}/60 history points`;
  $('monitor').setAttribute('aria-label', stale ? 'Host monitor: stale observations' : 'Host monitor: live observations');
}

const titles = {
  monitor: ['Host monitor', 'Physical observations · reservation budgets are in Resources'],
  resources: ['Resource budgets', 'Capacity, availability, consumers and the reasons behind each claim'],
  leases: ['Leases', 'Current and historical claims · select a lease to inspect its exact state'],
  messages: ['Operator messages', 'Exact session delivery, reviewed broadcasts and agent replies'],
  activity: ['Activity', 'Durable operational events · latest 200 loaded entries'],
  host: ['Host & capabilities', 'Local authority, infrastructure allocations and supported capabilities'],
};
function renderView(name) {
  if (!titles[name]) return;
  view = name;
  for (const button of document.querySelectorAll('nav button')) {
    if (button.dataset.view === name) button.setAttribute('aria-current', 'page'); else button.removeAttribute('aria-current');
  }
  for (const [key] of Object.entries(titles)) $(key === 'host' ? 'host-view' : key).hidden = key !== name;
  [$('title').textContent, $('subtitle').textContent] = titles[name];
  renderCurrent(); updateFreshness();
}
function applyDashboardRoute(route) {
  const previousView = view, previousLease = detailLeaseID, hadDetail = Boolean(detailLeaseID) || $('detail').open;
  renderView(route.view);
  poolConsole?.applyRoute(route);
  if (route.lease) loadLease(route.lease);
  else {
    detailRequest++; detailLeaseID = ''; detailPanel?.dispose(); detailPanel = null;
    if ($('detail').open) $('detail').close();
    if (hadDetail && route.view === 'resources' && route.pool && poolConsole?.focusLease(previousLease)) return;
    if (hadDetail || previousView !== route.view) document.querySelector('nav button[aria-current]')?.focus();
  }
}
function selectView(name) { navigation.navigate({view: name, lease: null}); }
window.addEventListener('popstate', connectionLinks.change);
window.addEventListener('hashchange', connectionLinks.change);
function renderCurrent() {
  ({monitor: renderMonitor, resources: renderResources, leases: renderLeases, messages: renderMessages, activity: renderActivity, host: renderHost})[view]();
}
for (const [index, button] of [...document.querySelectorAll('nav button')].entries()) {
  button.setAttribute('aria-keyshortcuts', String(index + 1));
  button.addEventListener('click', () => selectView(button.dataset.view));
}
installDashboardShortcuts({document, selectView});

let helpKey = '';
function helpButton(key, content) {
  const button = node('button', 'info-button', '?');
  button.type = 'button'; button.dataset.helpKey = key;
  button.setAttribute('aria-label', `About ${content.title.toLowerCase()}`);
  button.setAttribute('aria-haspopup', 'dialog'); button.setAttribute('aria-controls', 'metric-help');
  button.addEventListener('blur', () => queueMicrotask(() => {
    if (!$('metric-help').open && view === 'monitor') renderMonitor();
  }));
  button.addEventListener('click', () => {
    helpKey = key;
    $('metric-help-title').textContent = content.title;
    const body = $('metric-help-body'); body.replaceChildren();
    for (const paragraph of content.paragraphs) body.append(node('p', '', paragraph));
    if (content.technical.length) {
      const details = node('details', 'metric-technical'), list = node('dl');
      details.append(node('summary', '', 'Technical details'));
      for (const [label, value] of content.technical) list.append(node('dt', '', label), node('dd', '', value));
      details.append(list); body.append(details);
    }
    $('metric-help').showModal();
  });
  return button;
}
$('close-metric-help').addEventListener('click', () => $('metric-help').close());
$('metric-help').addEventListener('click', event => {
  const dialog = $('metric-help'), rect = dialog.getBoundingClientRect();
  if (event.target === dialog && (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom)) dialog.close();
});
$('metric-help').addEventListener('close', () => {
  // Telemetry can replace the original card while the explanation stays open.
  const source = [...document.querySelectorAll('[data-help-key]')].find(b => b.dataset.helpKey === helpKey);
  source?.focus(); helpKey = '';
});
function panel(title, tag, span, help = null, key = title) {
  const box = node('article', `panel ${span}`), head = node('div', 'panel-head'), body = node('div', 'panel-body');
  const tools = node('div', 'panel-tools'); tools.append(node('span', 'panel-tag', tag));
  if (help) tools.append(helpButton(key, help));
  head.append(node('h2', '', title), tools); box.append(head, body);
  return {box, body};
}
function bar(value, total = 100) {
  const el = svg('svg', {viewBox: '0 0 100 5', preserveAspectRatio: 'none', class: 'bar', 'aria-hidden': 'true'});
  el.append(svg('rect', {width: 100, height: 5, class: 'bar-bg'}));
  if (value != null && total > 0) el.append(svg('rect', {width: Math.min(100, Math.max(0, Number(value) / Number(total) * 100)), height: 5, class: 'bar-fill'}));
  return el;
}
function chart(get, ceiling = null, second = null) {
  const el = svg('svg', {viewBox: '0 0 600 100', preserveAspectRatio: 'none', class: 'chart', role: 'img', 'aria-label': 'Recent history; gaps indicate unavailable observations'});
  for (const y of [10, 50, 90]) el.append(svg('line', {x1: 0, x2: 600, y1: y, y2: y, class: 'chart-grid'}));
  const cadence = Number(state.monitor?.cadence_ns ?? 2e9) / 1e6;
  if (second && ceiling === null) ceiling = Math.max(1, ...state.history.flatMap(p => [numeric(get(p)) ?? 0, numeric(second(p)) ?? 0]));
  for (const d of chartPaths(state.history, get, ceiling, cadence)) el.append(svg('path', {d, class: 'chart-line'}));
  if (second) for (const d of chartPaths(state.history, second, ceiling, cadence)) el.append(svg('path', {d, class: 'chart-line secondary'}));
  return el;
}
function chartLabels(parent, right = 'now') {
  const labels = node('div', 'chart-labels');
  const seconds = state.history.length > 1 ? Math.round((Date.parse(state.history.at(-1).at) - Date.parse(state.history[0].at)) / 1000) : 0;
  labels.append(node('span', '', `${seconds}s ago`), node('span', '', right)); parent.append(labels);
}
function metric(parent, value, note) {
  const line = node('div', 'metric-line'); line.append(node('div', 'metric accent', value), node('span', 'submetric', note)); parent.append(line);
}
function stat(parent, label, value) { const row = node('div', 'stat-row'); row.append(node('span', '', label), node('span', '', value)); parent.append(row); }
function renderMonitor() {
  const root = $('monitor'), sample = state.monitor?.latest;
  if (!sample) { root.replaceChildren(empty('Waiting for the shared host collector…')); return; }
  // Keep a focused help source stable until its dialog opens. I/O selectors
  // have persistent cards and update in place while telemetry continues.
  if (root.contains(document.activeElement) && document.activeElement.classList.contains('info-button')) {
    root.dataset.paused = 'true';
    return;
  }
  delete root.dataset.paused;
  const cards = [];
  let grid = root.querySelector('.grid');
  if (!grid) { grid = node('div', 'grid'); root.replaceChildren(grid); }
  const cpu = panel('CPU activity', sample.cpu_busy_percent.value == null ? 'UNAVAILABLE' : 'ALL CORES', 'span8', metricHelp('cpu', {measurements: [['CPU', sample.cpu_busy_percent]]}));
  metric(cpu.body, percent(sample.cpu_busy_percent.value), `${sample.cores?.length ?? 0} logical cores`);
  cpu.body.append(chart(p => p.cpu_busy_percent, 100)); chartLabels(cpu.body);
  const cores = node('div', 'cores');
  for (const core of sample.cores ?? []) {
    const box = node('div', 'core'), labels = node('div', 'core-label');
    labels.append(node('span', '', core.id), node('span', '', percent(core.busy_percent.value))); box.append(labels, bar(core.busy_percent.value)); cores.append(box);
  }
  cpu.body.append(cores); cards.push(cpu.box);
  const memory = panel('Memory & swap', sample.memory.used.value == null ? 'UNAVAILABLE' : 'HOST MEMORY', 'span4', metricHelp('memory', {measurements: [['RAM', sample.memory.used], ['Available RAM', sample.memory.available], ['Swap', sample.swap.used]]}));
  metric(memory.body, bytes(sample.memory.used.value), 'memory used');
  memory.body.append(chart(p => p.memory_used_bytes, numeric(sample.memory.total.value))); chartLabels(memory.body);
  stat(memory.body, 'Total', bytes(sample.memory.total.value)); stat(memory.body, 'Available', bytes(sample.memory.available.value));
  memory.body.append(node('div', 'divider'));
  stat(memory.body, 'Swap used', bytes(sample.swap.used.value)); memory.body.append(bar(sample.swap.used.value, numeric(sample.swap.total.value)));
  stat(memory.body, 'Swap total', bytes(sample.swap.total.value)); cards.push(memory.box);
  const fs = panel('Storage space', sample.filesystem_stale ? 'STALE' : sample.filesystem.available.value == null ? 'UNAVAILABLE' : 'ON THIS DISK', 'span4', metricHelp('filesystem', {measurements: [['Available space', sample.filesystem.available]], filesystem: sample.filesystem.filesystem}));
  metric(fs.body, bytes(sample.filesystem.available.value), 'available');
  fs.body.append(chart(p => p.filesystem_available_bytes, numeric(sample.filesystem.total.value))); chartLabels(fs.body);
  stat(fs.body, 'Total', bytes(sample.filesystem.total.value)); stat(fs.body, 'Used', bytes(sample.filesystem.used.value));
  cards.push(fs.box);
  cards.push(ioPanel('Disk I/O', 'disk_io', sample.disk_io ?? [], sample.disk_quality, sample.disk_detail));
  cards.push(ioPanel('Network', 'network', sample.network ?? [], sample.network_quality, sample.network_detail));
  const support = node('div', 'support span12');
  support.append(node('span', '', 'GPU monitoring unavailable'), helpButton('gpu', metricHelp('gpu', {measurements: [['GPU', sample.gpu_busy_percent]]})));
  cards.push(support);
  for (const [index, card] of cards.entries()) {
    const current=grid.children[index];
    if (current!==card) { if(current) current.replaceWith(card); else grid.append(card); }
  }
  for (const card of ioPanels.values()) card.selector.reposition();
}
const ioPanels = new Map();
function ioPanel(title, key, devices, quality, detail) {
  let card=ioPanels.get(key);
  if (!card) {
    const help=metricHelp(key),p=panel(title,'REPORTED SUM','span4 io-panel',help),heading=node('div','io-heading'),qualityBadge=badge(''),summary=node('p','io-summary'),legend=node('div','legend'),read=node('span'),write=node('span'),graph=node('div','io-graph'),note=node('p','io-note');
    card={p,help,qualityBadge,summary,read,write,graph,note,selection:allDevices()};
    card.selector=createIOSelector({kind:key,title,onChange:selection=>{card.selection=selection;renderMonitor();}});
    heading.append(card.selector.element,qualityBadge);legend.append(read,write);p.body.append(heading,summary,legend,graph,note);ioPanels.set(key,card);
  }
  const {p,selector,selection,help}=card,ids=selectedDeviceIDs(selection,devices),totals=aggregateIO(devices,ids);
  selector.update(devices);
  Object.assign(help,metricHelp(key,{measurements:[['Inventory',{quality,detail}],...devices.filter(d=>ids.includes(d.id)).flatMap(d=>[[`${d.name} · Read / receive`,d.read_bytes_per_second],[`${d.name} · Write / send`,d.write_bytes_per_second]])]}));
  card.qualityBadge.hidden=quality==='reported';card.qualityBadge.textContent=({partial:'Partial inventory',estimated:'Estimate',unavailable:'Unavailable'})[quality]||'Reading incomplete';
  card.summary.textContent=`Sum of ${ids.length} selected ${key==='network'?'interface':'device'} ${ids.length===1?'report':'reports'}`;
  card.read.textContent=`${key==='network'?'↓ Receive':'↓ Read'} ${rate(totals.read)}`;card.write.textContent=`${key==='network'?'↑ Send':'↑ Write'} ${rate(totals.write)}`;
  card.note.textContent=!ids.length?(devices.length?'Select devices to see combined activity.':'No device reports are available.'):totals.present<totals.selected?'A selected device is unavailable. Totals are unknown until it reports again.':totals.read==null||totals.write==null?'A selected reading is unavailable. Gaps preserve unknown or reset counters.':'';
  card.note.hidden=!card.note.textContent;
  const window=aggregateIOWindow(state.history,key,selection,devices),bySequence=new Map(window.map(point=>[String(point.sequence),point]));
  card.graph.replaceChildren(chart(point=>bySequence.get(String(point.sequence))?.read??null,null,point=>bySequence.get(String(point.sequence))?.write??null));chartLabels(card.graph);
  return p.box;
}

function filterToolbar(root, placeholder, value, onInput, label) {
  const bar = node('div', 'toolbar'), input = node('input'); input.type = 'search'; input.placeholder = placeholder; input.value = value; input.setAttribute('aria-label', placeholder);
  input.addEventListener('input', () => onInput(input.value)); bar.append(input, node('small', '', label)); root.append(bar); return bar;
}
function renderResources() {
  const root = $('resources');
  if (!state.status) { poolConsole?.dispose(); poolConsole=null; root.replaceChildren(empty('Waiting for resource budgets…')); return; }
  if (!root.querySelector('input')) { root.replaceChildren(); filterToolbar(root, 'Filter resources, owners or reasons…', resourceFilter, value => { resourceFilter = value; resourceCards(); }, 'Cooperative reservations · exact counts'); }
  resourceCards();
  if (!poolConsole) {
    const slot = node('section', 'pool-console');
    root.append(slot);
    poolConsole = createPoolConsole({
      root: slot, call: (...args) => connection.call(...args),
      connected: () => state.connected, onLease: id => showLease(id),
      onNavigate: route => navigation.navigate({view: 'resources', lease: null, ...route}),
      onChanged: scheduleRefresh, storage: messageStorage,
    });
  }
  poolConsole.update({status: state.status, route: navigation.current});
}
function resourceCards() {
  const root = $('resources'); root.querySelector('.resource-grid')?.remove();
  const grid = node('div', 'resource-grid'), filter = resourceFilter.toLowerCase();
  const byLease = new Map((state.status.leases ?? []).map(lease => [lease.id, lease]));
  for (const r of state.status.resources ?? []) {
    if (!stringifyJSON(r).toLowerCase().includes(filter)) continue;
    const custom = r.custom?.pool;
    const label = custom?.name || ({cpu: 'CPU', memory: 'Memory', disk: 'Disk', port: 'Port'})[r.kind] || r.kind;
    const p = panel(label, `${custom?.scope || 'host'} / ${displayUnit(r.unit)}`, '', metricHelp(custom ? 'resource-custom' : `resource-${r.kind}`, {accounting: r.accounting, memory: r.kind === 'memory' ? state.status.memory_budget : null, measurements: [['Physical reading', r.measurement?.used]]}), `resource:${r.id}`);
    const budget = node('div', 'budget');
    for (const [label, value] of [['POOL', r.budget_total], ['RESERVED', r.reserved], ['AVAILABLE', r.available]]) {
      const item = node('div'); item.append(node('small', '', label), node('strong', label === 'AVAILABLE' ? 'accent' : '', quantity(value, r.unit))); item.title = `${label}: ${String(value)} ${r.unit}`; budget.append(item);
    }
    p.body.append(budget, bar(r.reserved, numeric(r.budget_total)));
    if(r.kind==='memory' && state.status.memory_budget) {
      const memory=state.status.memory_budget;
      stat(p.body,'Physical memory',bytes(memory?.observation.total_bytes));
      if(memory?.ceiling_bytes!=null) stat(p.body,'Operator ceiling',bytes(memory.ceiling_bytes));
      if(memory?.stale || memory?.observation.available_bytes==null) p.body.append(node('p','measure-note','New memory grants paused: waiting for an availability estimate.'));
      if(r.deficit>0) p.body.append(node('p','',`Memory shortfall: ${bytes(r.deficit)}`));
    }
    if (r.custom?.deficit > 0) p.body.append(node('p', '', `Deficit: ${count(r.custom.deficit)} ${r.unit}`));
    for (const u of r.users ?? []) {
      const user = node('div', 'consumer'); if (u.lease) user.append(leaseButton(u.lease)); user.append(node('p', '', u.reason), node('small', '', `${u.agent ? u.agent + ' · ' : ''}${ownerLabel(u, r.host)} · ${quantity(u.quantity, r.unit)} · ${leaseStateLabel(byLease.get(u.lease) ?? u)}${u.project ? ' · ' + u.project : ''}`)); p.body.append(user);
    }
    if (!r.users?.length) p.body.append(node('p', 'measure-note', 'No current consumers'));
    grid.append(p.box);
  }
  if (!grid.childElementCount) grid.append(empty('No matching resources')); const poolSlot=root.querySelector('.pool-console');if(poolSlot)root.insertBefore(grid,poolSlot);else root.append(grid);
}
function leaseButton(id) { const b = node('button', 'link-button', id); b.addEventListener('click', () => showLease(id)); return b; }
function renderLeases() {
  const root = $('leases');
  if (!state.status) { root.replaceChildren(empty('Waiting for leases…')); return; }
  if (!root.querySelector('input[type=search]')) {
    root.replaceChildren();
    const bar = filterToolbar(root, 'Filter leases, owners or reasons…', leaseFilter, value => { leaseFilter = value; leaseTable(); }, '');
    bar.classList.add('lease-toolbar');
    const label = node('label', 'lease-visibility'), toggle = node('input'); toggle.type = 'checkbox'; toggle.checked = hideInactiveLeases;
    const note = bar.querySelector('small'); note.classList.add('lease-count-note'); note.setAttribute('role', 'status');
    label.append(toggle, node('span', '', 'Hide inactive leases')); bar.insertBefore(label, note);
    toggle.addEventListener('change', () => {
      hideInactiveLeases = toggle.checked;
      const saved = saveHideInactive(leasePreferenceStorage, hideInactiveLeases);
      label.title = saved ? 'Remembered in this browser.' : 'Applied for this session. Browser storage is unavailable.';
      leaseTable();
    });
  }
  leaseTable();
}
function leaseTable() {
  const root = $('leases'); root.querySelector('.panel')?.remove();
  const box = node('div', 'panel table-wrap'), table = node('table'), head = node('thead'), row = node('tr');
  for (const title of ['LEASE / OWNER', 'STATE', 'REASON', 'CLAIMS', 'EXPIRES']) row.append(node('th', '', title)); head.append(row); table.append(head);
  const body = node('tbody');
  const all = state.status.leases ?? [], query = leaseFilter.toLowerCase();
  const matches = [...all].reverse().filter(l => leaseVisible(l, hideInactiveLeases) && `${leaseStateLabel(l)} ${stringifyJSON(l)}`.toLowerCase().includes(query));
  const leases = matches.slice(0, 200), hidden = hideInactiveLeases ? all.filter(isInactiveLease).length : 0;
  const note = root.querySelector('.lease-count-note'), caption = `${matches.length} matching${matches.length > 200 ? ' · showing 200' : ''}${hideInactiveLeases ? ` · ${hidden} inactive hidden` : ' · includes history'}`;
  if (note && note.textContent !== caption) note.textContent = caption;
  for (const l of leases) {
    const row = node('tr'), id = node('td'); id.append(leaseButton(l.id), node('p', 'muted', `${l.request.agent ? l.request.agent + ' · ' : ''}${ownerLabel(l)}`));
    const status = node('td'); status.append(badge(leaseStateLabel(l), l.state));
    if (l.exit_code != null) status.append(node('small', 'lease-exit-code', `Exit code ${l.exit_code}`));
    const claims = l.request.resources ?? {}, summary = [];
    if (claims.cpu) summary.push(`${count(claims.cpu)} cores`);
    if (claims.memory_bytes) summary.push(bytes(claims.memory_bytes) + ' memory');
    if (claims.disk_bytes) summary.push(bytes(claims.disk_bytes) + ' disk');
    if (l.ports?.length) summary.push(`${l.ports.length} ports`);
    if (l.request.pools?.length) summary.push(`${l.request.pools.length} pool claims`);
    row.append(id, status, node('td', 'reason', l.request.reason), node('td', '', summary.join(' · ') || '—'), node('td', '', when(l.expires_at))); body.append(row);
  }
  table.append(body); box.append(table); if (!leases.length) box.append(empty('No matching leases')); root.append(box);
}
function showLease(id) { navigation.navigate({...navigation.current, view, lease: id}); }
async function loadLease(id) {
  const request = ++detailRequest;
  detailLeaseID = id;
  detailPanel?.dispose(); detailPanel = null;
  $('detail-body').replaceChildren(empty('Loading lease…')); if (!$('detail').open) $('detail').showModal();
  if (!state.connected) { $('detail-body').replaceChildren(empty('Waiting for a connection to inspect this lease…')); return; }
  try {
    const current = connection, lease = await current.call('inspect', {id});
    const status = await current.call('status');
    if (request !== detailRequest || !$('detail').open || current !== connection) return;
    renderLeaseDetail(lease, status.leases ?? [], request);
  } catch (error) { if (request === detailRequest && $('detail').open) $('detail-body').replaceChildren(empty(error.message)); }
}
function renderLeaseDetail(lease, leases, request, resultNote = '') {
  detailPanel?.dispose();
  const body = $('detail-body'), dl = node('dl');
  for (const [label, value] of [['ID', lease.id], ['State', leaseStateLabel(lease)], ['Reason', lease.request.reason], ['Owner', ownerLabel(lease)], ['Agent', lease.request.agent || '—'], ['Project', lease.request.project || '—'], ['Expires', when(lease.expires_at)]]) dl.append(node('dt', '', label), node('dd', '', value));
  if (lease.exit_code != null) dl.append(node('dt', '', 'Exit code'), node('dd', '', String(lease.exit_code)));
  const current = connection;
  detailPanel = leaseActionPanel({lease, leases, call: (...args) => current.call(...args), connected: () => state.connected && connection === current,
    busy: () => pendingLeaseActions.has(lease.id), onPending: pending => {
      if (pending) pendingLeaseActions.add(lease.id); else pendingLeaseActions.delete(lease.id);
      detailPanel?.updateConnection();
      // Reopening a detail during a request must not leave its pre-response
      // snapshot actionable when that request finishes with an uncertain result.
      if (!pending && request !== detailRequest && detailLeaseID === lease.id && $('detail').open) loadLease(lease.id);
    },
    onRefresh: () => loadLease(lease.id), onResult: result => {
      scheduleRefresh();
      if (request === detailRequest && $('detail').open) renderLeaseDetail(result, leases, request, `${leaseActionResult(result)}${result.state === 'active' ? ' Expires: ' + when(result.expires_at) : ''}`);
    }});
  body.replaceChildren(dl);
  if (resultNote) { const note = node('p', 'lease-action-feedback', resultNote); note.setAttribute('role', 'status'); body.append(note); }
  body.append(detailPanel.element, recordJSON(lease, 'Full lease record', `lease:${lease.id}`, true));
  $('close-detail').focus();
}
$('close-detail').addEventListener('click', () => $('detail').close());
$('detail').addEventListener('close', () => {
  // A queued close event from Back must not tear down a dialog reopened by a
  // quick Forward. The URL is already authoritative for history-driven closes.
  if ($('detail').open) return;
  if (navigation.current.lease) { navigation.navigate({...navigation.current, view, lease: null}); return; }
  detailRequest++; detailLeaseID = ''; detailPanel?.dispose(); detailPanel = null;
});
function renderMessages() { updateOperatorMessaging(); }

function renderActivity() {
  const root = $('activity'); root.replaceChildren();
  for (const ev of [...state.events].reverse()) {
    const item = node('article', 'item'), heading = node('div', 'item-heading'); heading.append(node('span', '', `#${ev.sequence} / ${ev.type}`), node('span', '', when(ev.at)));
    item.append(heading, node('p', '', ev.entity), recordJSON(ev, 'Event data', `event:${ev.sequence}`)); root.append(item);
  }
  if (!root.childElementCount) root.append(empty('No operational events in the loaded window'));
}
function renderHost() {
  const root = $('host-view'), status = state.status;
  if (!status) { root.replaceChildren(empty('Waiting for the local authority…')); return; }
  const p = panel('Local authority', 'CAPABILITIES', ''); const dl = node('dl');
  for (const [label, value] of [['Host', status.host], ['Owner', ownerLabel(status)], ['Event cursor', count(status.cursor)], ['Capabilities', (status.capabilities ?? []).join(', ')]]) dl.append(node('dt', '', label), node('dd', '', value));
  p.body.append(dl, node('h3', '', 'Infrastructure ports'), recordJSON(status.infrastructure_ports ?? [], 'Port bindings', 'infrastructure', true), node('p', 'measure-note', 'Inspect local state and extend, release or cancel leases from their detail view. Reviewed custom pool controls and their journal are in Resources. Explicit cleanup recovery remains in the CLI. Reviewed agent messaging is available in Messages. Use arm dashboard terminal for the terminal dashboard.'));
  root.replaceChildren(p.box);
}

setInterval(updateFreshness, 1000);
connectionLinks.start();
