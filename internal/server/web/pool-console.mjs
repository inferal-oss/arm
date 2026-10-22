import { stringifyJSON, count, ownerDisplay } from './core.mjs';
import { jsonView } from './json-view.mjs';
import { preparePoolMutation, poolRequestCopy, poolTreeRows } from './pool-model.mjs';
import { createPoolOperations } from './pool-operations.mjs';

export function createPoolConsole({
  root,
  call,
  connected,
  onLease = () => {},
  onChanged = () => {},
  onNavigate = () => {},
  storage,
  document = globalThis.document,
  key = () => crypto.randomUUID(),
}) {
  const el = (tag, cls = '', text = null) => {
    const item = document.createElement(tag);
    item.className = cls;
    if (text !== null) item.textContent = text;
    return item;
  };
  const button = (label, action, cls = '') => {
    const item = el('button', cls, label);
    item.type = 'button';
    item.addEventListener('click', action);
    return item;
  };
  const field = (label, input) => {
    const wrap = el('label', 'pool-field', label);
    wrap.append(input);
    return wrap;
  };
  let status = null,
    pools = [],
    selected = '',
    cursor = null,
    readGeneration = 0,
    historyGeneration = 0,
    historyReadKey = null,
    formGeneration = 0,
    loading = false,
    history = null,
    consumerPage = null, consumerAfter = '', consumerPrevious = [], consumerGeneration = 0,
    historyAfter = '0',
    historyBefore = null,
    review = null,
    reviewStale = false,
    draft = null,
    disposed = false,
    focusDetail = false,
    focusHistory = false;
  let journalItems = new Map();
  let opener = null, openerLabel = '';
  const editor = el('dialog', 'pool-editor'),
    editorHeader = el('header', 'pool-editor-heading'),
    editorStep = el('p', 'pool-step'),
    editorTitle = el('h2'),
    editorContext = el('p', 'pool-editor-context'),
    dialogFeedback = el('p', 'pool-dialog-feedback');
  dialogFeedback.setAttribute('role', 'alert');
  dialogFeedback.hidden = true;
  editorTitle.id = `pool-editor-title-${Math.random().toString(36).slice(2)}`;
  editor.setAttribute('aria-labelledby', editorTitle.id);
  editor.setAttribute('aria-modal', 'true');
  const heading = el('header', 'pool-heading'),
    tools = el('div', 'pool-tools'),
    filter = el('input'),
    feedback = el('p', 'pool-feedback'),
    table = el('table', 'pool-table'),
    body = el('tbody'),
    detail = el('section', 'pool-detail'),
    formBox = el('section', 'pool-form-box'),
    reviewBox = el('section', 'pool-review'),
    operationsBox = el('section', 'pool-operations'),
    journal = el('section', 'pool-journal');
  filter.type = 'search';
  filter.placeholder = 'Find custom pools';
  filter.setAttribute('aria-label', 'Find custom pools');
  feedback.setAttribute('role', 'status');
  heading.append(el('h2', '', 'Custom resource pools'));
  const createButton = button('Create pool…', () => begin('create'), 'primary'),
    refreshButton = button('Refresh pools', () => load(true));
  createButton.dataset.poolOnline = refreshButton.dataset.poolOnline = '';
  tools.append(filter, createButton, refreshButton);
  heading.append(tools);
  const head = el('thead'),
    headRow = el('tr');
  for (const label of [
    'Pool hierarchy',
    'State',
    'Total',
    'Committed',
    'Available / deficit',
  ])
    headRow.append(el('th', '', label));
  head.append(headRow);
  table.append(head, body);
  root.append(
    heading,
    el(
      'p',
      'measure-note',
      'Counted cooperative resources. Global pools use this one authority; physical resources retain their own policies.'
    ),
    feedback,
    table,
    detail,
    editor,
    operationsBox,
    journal
  );
  const dismissEditor = button('×', () => closeEditor(), 'pool-dialog-dismiss');
  dismissEditor.setAttribute('aria-label', 'Close without changes');
  editorHeader.append(editorStep, editorTitle, editorContext, dismissEditor);
  editor.append(editorHeader, dialogFeedback, formBox, reviewBox);
  editor.addEventListener('cancel', (event) => { event.preventDefault(); closeEditor(); });
  editor.hidden = true;
  detail.hidden = journal.hidden = formBox.hidden = reviewBox.hidden = true;
  function closeEditor(restore = true) {
    formGeneration++;
    draft = review = null;
    formBox.hidden = reviewBox.hidden = true;
    if (editor.open && typeof editor.close === 'function') editor.close();
    editor.hidden = true;
    const replacement = restore && openerLabel ? [...root.querySelectorAll('button')].find(b => (b.getAttribute?.('aria-label') || b.textContent) === openerLabel && !editor.contains(b)) : null;
    if (restore && opener && root.contains(opener)) opener.focus();
    else if (replacement) replacement.focus();
    else if (restore && selected) {
      const title = detail.querySelector('h3');
      if (title) { title.tabIndex = -1; title.focus(); }
    }
    opener = null;
    dialogFeedback.textContent = ''; dialogFeedback.hidden = true;
  }
  function showEditor(step, operation) {
    const names = {'create-child':'Create subpool',create:'Create resource pool',set:'Change total capacity',adjust:'Add or remove units',drain:'Pause new requests',resume:'Allow new requests',delete:'Delete empty pool'};
    editorStep.textContent = step === 1 ? '1 · EDIT CHANGE' : '2 · REVIEW & CONFIRM';
    editorTitle.textContent = names[operation];
    editorContext.textContent = operation === 'create-child' ? `Carve a named partition from ${current()?.pool.name || 'this parent'}.` : operation === 'create' ? 'Define a counted resource for agents to coordinate.' : current()?.pool.name || review?.name || 'Selected resource pool';
    editor.hidden = false;
    if (!editor.open) {
      if (typeof editor.showModal === 'function') editor.showModal();
      else editor.open = true;
    }
  }

  const rows = new Map();
  let ops,
    detailFingerprint = null;
  const current = () => pools.find((view) => view.pool.id === selected);
  const owner = (uid, name, authority) =>
    ownerDisplay({ owner: uid, owner_username: name }, status, authority);
  function fail(message) {
    feedback.textContent = message;
    if (editor.open) { dialogFeedback.textContent = message; dialogFeedback.hidden = !message; }
  }
  function setBusyState() {
    for (const item of root.querySelectorAll('[data-pool-online]'))
      item.disabled = !connected() || item.dataset.poolReadOnly === 'true';
    if (review) {
      reviewStale =
        !connected() ||
        review.request.reviewed.authority !== status?.host ||
        ((review.parent || review.before) &&
          (() => {
            const now = current();
            return (
              !now ||
              String(now.pool.revision) !== String(review.request.parent_revision ?? review.request.revision) ||
              stringifyJSON(now.review) !==
                stringifyJSON(review.request.reviewed)
            );
          })());
      const confirm = reviewBox.querySelector('[data-pool-confirm]');
      if (confirm) confirm.disabled = reviewStale;
      const note = reviewBox.querySelector('.pool-stale');
      if (note)
        note.textContent = reviewStale
          ? 'The pool snapshot or connection changed. Cancel this change, refresh the pool, then review again.'
          : '';
    }
  }
  const stateLabel = state => ({active:'Open for leases',draining:'New requests paused',deleted:'Historical pool'})[state] || state;
  function renderRows() {
    const visible = new Set(),
      search = filter.value.toLowerCase();
    let hierarchy;
    try { hierarchy=poolTreeRows(pools); } catch(error){fail(error.message);return;}
    for (const {view,depth,path} of hierarchy) {
      const p = view.pool;
      visible.add(p.id);
      let row = rows.get(p.id);
      if (!row) {
        const tr = el('tr'),
          cells = [];
        for (let i = 0; i < 5; i++) {
          const cell = el('td');
          cells.push(cell);
          tr.append(cell);
        }
        const pick = button('', () => select(p.id), 'link-button');
        cells[0].append(pick, el('small'));
        row = { tr, cells, pick };
        rows.set(p.id, row);
        body.append(tr);
      }
      row.pick.textContent = `${depth ? '↳ ' : ''}${p.name}`;
      row.cells[0].style?.setProperty('--pool-depth',String(depth));
      row.cells[0].className='pool-tree-cell';
      const parent = p.parent ? pools.find(v=>v.pool.id===p.parent)?.pool : null;
      row.pick.setAttribute('aria-label',parent ? `${p.name}, subpool of ${parent.name}` : p.name);
      body.append(row.tr);
      row.pick.setAttribute('aria-pressed', String(selected === p.id));
      row.cells[0].children[1].textContent = `${p.scope === 'global' ? 'Global · one ledger' : p.scope + ' · ' + p.host} · ${owner(p.owner, p.owner_username, p.authority)}${parent ? ' · Within ' + parent.name : ' · Root'}`;
      row.cells[1].textContent = stateLabel(p.state);
      row.cells[1].className = 'pool-state';
      row.cells[2].textContent = `${count(p.capacity)} ${p.unit}`;
      row.cells[3].textContent = count(view.partition?.committed ?? view.reserved);
      row.cells[4].textContent =
        view.deficit > 0
          ? `Deficit ${count(view.deficit)}`
          : count(view.available);
      row.tr.hidden = !stringifyJSON(view).toLowerCase().includes(search);
      row.tr.dataset.selected = String(selected === p.id);
    }
    for (const [id, row] of rows)
      if (!visible.has(id)) {
        row.tr.remove();
        rows.delete(id);
      }
    const emptyMessage = 'No custom pools yet. Create one to coordinate counted permits.';
    if (!pools.length && cursor !== null) fail(emptyMessage);
    else if (feedback.textContent === emptyMessage) fail('');
  }
  function renderDetail() {
    detail.hidden = !selected;
    const view = current(),
      fingerprint = stringifyJSON({
        view: view ?? null,
        consumerPage,
        selected,
        owner: status?.owner ?? null,
        username: status?.owner_username ?? null,
      });
    if (fingerprint === detailFingerprint) {
      setBusyState();
      return;
    }
    detailFingerprint = fingerprint;
    const focused = detail.contains(document.activeElement)
        ? document.activeElement?.textContent
        : null,
      opened = new Set(
        [...detail.querySelectorAll('details')]
          .filter((n) => n.open)
          .map((n) => n.querySelector('summary')?.textContent)
      );
    detail.replaceChildren();
    if (!view) {
      if (selected) detail.append(
        button('All pools', () => select(''), 'link-button'),
        el('h3', '', 'Pool unavailable'),
        el('p', 'measure-note', `No available pool snapshot for ${selected}. Refresh to check again; the selected address has been kept.`)
      );
      return;
    }
    const p = view.pool;
    detail.append(
      button('All pools', () => select(''), 'link-button'),
      el('h3', '', p.name),
      el(
        'p',
        'measure-note',
        `${p.scope === 'global' ? 'Global pool · one authority' : 'Pool on this host'} · ${stateLabel(p.state)} · ${owner(p.owner, p.owner_username, p.authority)}`
      )
    );
    const lineage=el('nav','pool-lineage');lineage.setAttribute('aria-label','Pool ancestry');
    for(const id of view.ancestors || [p.id]){const node=pools.find(v=>v.pool.id===id)?.pool;lineage.append(button(node?.name||id,()=>select(id),'link-button'));}
    detail.append(lineage);
    if (p.description) detail.append(el('p', '', p.description));
    const controls = el('div', 'pool-tools');
    if(view.review?.unavailable) detail.append(el('p','pool-deficit','Read only: this complete tree exceeds dashboard review limits. Browsing and existing obligations remain available; no change can use a truncated review.'));
    if (p.state !== 'deleted') {
      for (const [op, label, explanation] of [
        ...(p.state === 'active' ? [['create-child', 'Create subpool…', 'Carve existing capacity into a named child partition.']] : []),
        ['set', 'Change total capacity…', 'Replace the total with a specific number.'],
        ['adjust', 'Add or remove units…', 'Increase or decrease the current total.'],
        [
          p.state === 'draining' ? 'resume' : 'drain',
          p.state === 'draining' ? 'Allow new requests…' : 'Pause new requests…',
          p.state === 'draining' ? 'Reopen admission to this pool.' : 'Refuse new requests; already queued work may still start.',
        ],
        ['delete', 'Delete empty pool…', 'Retain its history; require no obligations.'],
      ]) {
        const b = button(
          label,
          () => begin(op),
          op === 'delete' ? 'pool-action danger' : 'pool-action'
        );
        b.setAttribute('aria-label', label);
        b.dataset.poolReadOnly=String(Boolean(view.review?.unavailable));b.disabled=Boolean(view.review?.unavailable)||!connected();
        b.append(el('small', '', explanation));
        b.dataset.poolOnline = '';
        controls.append(b);
      }
    }
    const budget = el('dl', 'pool-detail-budget');
    for (const [label, value] of [['Total capacity',p.capacity],['Directly leased',view.partition?.direct_reserved ?? view.reserved],['Backing for children',view.partition?.child_backing ?? 0],['Uncarved available',view.available],['Held in parent',p.parent ? view.partition?.held_backing ?? 0 : '—'],['Pending return',view.partition?.pending_return ?? 0]]) {
      const stat = el('div'); stat.append(el('dt','',label),el('dd','',`${value === '—' ? value : count(value)} ${value === '—' ? '' : p.unit}`)); budget.append(stat);
    }
    detail.append(budget);
    if(view.deficit > 0) detail.append(el('p','pool-deficit',`Over capacity by ${count(view.deficit)} ${p.unit}. Existing promises keep their backing; the parent cannot reuse it yet.`));
    detail.append(el('p','measure-note','Direct leases plus child backing are this node’s commitments. Parent and child figures overlap; do not add tree rows together.'), controls);
    if(view.children?.length){const branch=el('div','pool-child-links');branch.append(el('h4','','Child partitions'));for(const id of view.children){const child=pools.find(v=>v.pool.id===id);if(child)branch.append(button(`${child.pool.name} · ${count(child.partition?.held_backing ?? child.pool.capacity)} ${p.unit} held`,()=>select(id),'pool-child-link'));}detail.append(branch);}
    for (const [label, users] of [
      ['Direct active leases', view.users],
      ['Direct queued claims', view.queued],
    ]) {
      const block = el('details', 'pool-consumers'),
        summary = el('summary', '', `${label} · ${users?.length || 0}`);
      block.append(summary);
      for (const user of users || []) {
        const entry = el('div', 'consumer');
        entry.append(
          button(user.lease, () => onLease(user.lease), 'link-button'),
          el('p', '', user.reason),
          el(
            'small',
            '',
            `${owner(user.owner, user.owner_username, p.authority)} · ${count(user.quantity)} ${p.unit} · ${user.state}`
          )
        );
        block.append(entry);
      }
      detail.append(block);
    }
    const subtree=el('section','pool-subtree');subtree.append(el('h4','','Leases across this subtree'),el('p','measure-note','Each lease appears once. Its direct claims identify which child holds the units; tree totals are not summed.'));
    if(consumerPage?.pool===selected){
      for(const entry of consumerPage.entries || []){const u=entry.use,row=el('div','consumer');row.append(button(u.lease,()=>onLease(u.lease),'link-button'),el('p','',u.reason),el('small','',`${owner(u.owner,u.owner_username,p.authority)} · ${u.state}`));for(const claim of entry.direct_claims||[]){const node=pools.find(v=>v.pool.id===claim.pool)?.pool;row.append(el('p','',`${node?.name||claim.pool} · ${count(claim.quantity)} ${p.unit}`));}subtree.append(row);}
      if(!consumerPage.entries?.length)subtree.append(el('p','measure-note','No queued or consuming claims in this subtree.'));
      const nav=el('div','pool-tools'),back=button('Previous leases',()=>{const after=consumerPrevious.at(-1) ?? '';loadConsumers(after,'back');}),next=button('Next leases',()=>{loadConsumers(consumerPage.cursor,'next');});back.disabled=!consumerPrevious.length;next.disabled=!consumerPage.more;nav.append(back,next);subtree.append(nav);
    } else subtree.append(el('p','measure-note',connected()?'Loading current subtree claims…':'Reconnect to refresh subtree claims.'));
    detail.append(subtree);
    detail.append(
      jsonView(view, { label: 'Complete pool snapshot', document })
    );
    for (const d of detail.querySelectorAll('details'))
      if (opened.has(d.querySelector('summary')?.textContent)) d.open = true;
    if (focused) {
      const target = [...detail.querySelectorAll('button')].find(
        (b) => b.textContent === focused
      );
      target?.focus();
    }
    if (focusDetail) {
      const title = detail.querySelector('h3');
      if (title) {
        title.tabIndex = -1;
        title.focus();
        focusDetail = false;
      }
    }
    setBusyState();
  }
  async function load(force = false) {
    if (
      disposed ||
      !connected() ||
      loading ||
      (!force && cursor === status?.cursor)
    )
      return;
    loading = true;
    const generation = ++readGeneration,
      requestedCursor = status?.cursor,
      oldRevision = current()?.pool.revision;
    try {
      const result = await call('pool_list', { all: true });
      if (disposed || generation !== readGeneration) return;
      pools = result;
      cursor = requestedCursor;
      renderRows();
      renderDetail();
      if (
        selected &&
        (!history ||
          force ||
          String(oldRevision) !== String(current()?.pool.revision))
      )
        await loadHistory();
      if(selected) await loadConsumers(consumerAfter);
    } catch (error) {
      if (generation === readGeneration) fail(error.message);
    } finally {
      if (generation === readGeneration) loading = false;
    }
  }
  function select(id) {
    onNavigate({pool: id || null, poolAfter: id ? '0' : null, poolBefore: null});
  }
  function applyRoute(route) {
    const id = route?.view === 'resources' ? route.pool || '' : '',
      after = id ? route.poolAfter ?? '0' : '0',
      before = id ? route.poolBefore ?? null : null;
    if (id === selected && after === historyAfter && before === historyBefore) return;
    fail('');
    focusDetail = id !== selected && Boolean(id);
    focusHistory = id === selected && Boolean(id);
    selected = id;
    historyAfter = after;
    historyBefore = before;
    history = null;
    consumerPage=null;consumerAfter='';consumerPrevious=[];consumerGeneration++;
    historyGeneration++;
    historyReadKey = null;
    closeEditor(false);
    renderRows();
    renderDetail();
    renderHistory();
    loadHistory();
    loadConsumers();
  }
  async function loadConsumers(after='',direction='refresh') {
    if(!selected || !connected())return;
    const id=selected,generation=++consumerGeneration;
    try{const page=await call('pool_consumers',{id,descendants:true,after,limit:20});
      if(disposed||generation!==consumerGeneration||selected!==id)return;
      if(page.pool!==id||page.descendants!==true||!Array.isArray(page.entries))throw Error('Subtree claim page differs from the selected pool.');
      if(direction==='next')consumerPrevious.push(consumerAfter);else if(direction==='back')consumerPrevious.pop();
      consumerPage=page;consumerAfter=after;detailFingerprint=null;renderDetail();
    }catch(error){if(generation===consumerGeneration)fail(error.message);}
  }
  async function loadHistory() {
    if (!selected || !connected()) return;
    const signature = `${selected}:${historyBefore === null ? 'after:' + historyAfter : 'before:' + historyBefore}`;
    if (historyReadKey === signature) return;
    historyReadKey = signature;
    const generation = ++historyGeneration,
      id = selected,
      after = historyAfter,
      before = historyBefore;
    try {
      const page = await call('pool_history_page', {
        id,
        ...(before !== null ? {before: BigInt(before)} : {after: BigInt(after)}),
      });
      if (
        disposed || generation !== historyGeneration || id !== selected ||
        after !== historyAfter || before !== historyBefore
      ) return;
      history = page;
      renderHistory();
    } catch (error) {
      if (!disposed && generation === historyGeneration) fail(error.message);
    } finally {
      if (generation === historyGeneration) historyReadKey = null;
    }
  }
  function renderHistory() {
    const active = document.activeElement,
      restoreFocus = journal.contains(active),
      action = active?.dataset?.journalAction,
      previousItems = journalItems;
    journalItems = new Map();
    journal.hidden = !history;
    journal.replaceChildren();
    if (!history) return;
    journal.append(
      el('h3', '', 'Capacity-change journal'),
      el(
        'p',
        'measure-note',
        'Each page preserves the recorded actor, time, reason and exact before/after counts.'
      )
    );
    for (const change of history.entries) {
      const identity = String(change.sequence),
        fingerprint = stringifyJSON(change),
        saved = previousItems.get(identity),
        item = saved?.fingerprint === fingerprint ? saved.item : el('article', 'pool-change'),
        p = change.after;
      if (saved?.fingerprint !== fingerprint) item.append(
        el(
          'h4',
          '',
          `${change.operation} · ${new Date(change.at).toLocaleString()}`
        ),
        el('p', '', owner(change.actor, change.actor_username, p.authority)),
        el('p', '', change.reason),
        el(
          'p',
          '',
          `${change.before?.capacity ?? '—'} → ${p.capacity} ${p.unit} · change ${change.delta} · revision ${change.before?.revision ?? '—'} → ${p.revision}`
        ),
        jsonView(change, {
          label: `Journal entry ${change.sequence}`,
          document,
        })
      );
      journalItems.set(identity, {fingerprint, item});
      journal.append(item);
    }
    if (!history.entries.length)
      journal.append(el('p', 'measure-note', 'No entries at this journal boundary.'));
    const nav = el('div', 'pool-tools'),
      start = button('First page', () => onNavigate({pool: selected, poolAfter: '0', poolBefore: null})),
      back = button('Previous page', () => onNavigate({pool: selected, poolAfter: null, poolBefore: String(history.previous_before)})),
      next = button('Next page', () => onNavigate({pool: selected, poolAfter: String(history.next_after), poolBefore: null}));
    start.disabled = historyBefore === null && historyAfter === '0';
    back.disabled = history.previous_before == null;
    next.disabled = history.next_after == null;
    start.dataset.journalAction = 'first';
    back.dataset.journalAction = 'previous';
    next.dataset.journalAction = 'next';
    nav.append(start, back, next);
    journal.append(nav);
    if (focusHistory || restoreFocus) {
      const title = journal.querySelector('h3');
      title.tabIndex = -1;
      const replacement = [start, back, next].find(b => b.dataset.journalAction === action && !b.disabled);
      (focusHistory ? title : journal.contains(active) ? active : replacement || title).focus();
      focusHistory = false;
    }
  }
  function begin(operation) {
    if (!connected() || operation !== 'create' && !current()) return;
    fail('');
    opener = document.activeElement;
    openerLabel = opener?.getAttribute?.('aria-label') || opener?.textContent || '';
    draft = { operation };
    showEditor(1, operation);
    review = null;
    formBox.replaceChildren();
    reviewBox.hidden = true;
    formBox.hidden = false;
    const form = el('form', 'pool-form'),
      inputs = {};
    const descriptions = {
      'create-child': `A child partitions ${current()?.pool.name}. Scope and count units are inherited. It does not add capacity; all ancestors must have enough backing.`,
      create: 'Choose a name, scope and total count. Agents can lease units from this pool.',
      set: `Current total: ${count(current()?.pool.capacity)} ${current()?.pool.unit}. Enter the replacement total; existing leases remain charged.`,
      adjust: 'Use a positive number to add units, or a negative number to remove units. Existing leases remain charged.',
      drain: 'New requests and renewals will be refused. Existing leases keep their obligations. Already queued requests may still start when capacity permits; queued requests are not cancelled.',
      resume: 'New requests and renewals can be admitted again when capacity and other policies permit.',
      delete: 'Only an empty pool with no queued or charged leases can be deleted. Its definition and journal remain available as history.',
    };
    form.append(el('p', 'pool-form-explainer', descriptions[operation]));
    function input(name, label, value = '', textarea = false) {
      const item = el(textarea ? 'textarea' : 'input');
      item.value = value;
      item.name = name;
      if (!textarea) item.type = 'text';
      item.setAttribute('aria-label', label);
      inputs[name] = item;
      form.append(field(label, item));
      return item;
    }
    if (operation === 'create' || operation === 'create-child') {
      input('name', operation === 'create-child' ? 'Subpool name' : 'Pool name');
      if(operation === 'create-child') form.append(el('p','pool-inherited',`Parent · ${current()?.pool.name} · ${current()?.pool.scope} · ${current()?.pool.unit}. ${count(current()?.available)} uncarved units in this snapshot.`));
      if(operation === 'create') {
      const scope = el('select');
      scope.name = 'scope';
      scope.setAttribute('aria-label', 'Pool scope');
      for (const [value, label] of [
        ['host', 'This host'],
        ['global', 'Global · one authority'],
      ]) {
        const option = el('option', '', label);
        option.value = value;
        scope.append(option);
      }
      scope.value = 'host';
      inputs.scope = scope;
      form.append(field('Scope', scope));
      input('unit', 'Count unit', 'count');
      }
      input('capacity', 'Initial capacity', '0').inputMode = 'numeric';
      input('description', 'Description (optional)', '', true);
    } else if (operation === 'set')
      input(
        'capacity',
        'New total capacity',
        String(current()?.pool.capacity ?? 0)
      ).inputMode = 'numeric';
    else if (operation === 'adjust')
      input('delta', 'Units to add (+) or remove (−)', '1').inputMode = 'text';
    input('reason', 'Why are you making this change?', '', true).required = true;
    inputs.reason.placeholder = 'This reason will be recorded in the pool journal.';
    const controls = el('footer', 'pool-dialog-actions'),
      keep = button('Cancel', () => closeEditor()),
      reviewButton = el('button', 'primary', 'Review change');
    reviewButton.type = 'submit';
    controls.append(keep, reviewButton);
    form.append(controls);
    formBox.append(form);
    const generation = ++formGeneration;
    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      if (!connected() || generation !== formGeneration) return;
      fail('');
      const data = { operation };
      for (const [name, input] of Object.entries(inputs))
        data[name] = input.value;
      for (const input of Object.values(inputs)) input.disabled = true;
      reviewButton.disabled = true;
      try {
        const snapshot =
          operation === 'create'
            ? null
            : await call('pool_inspect', { id: selected });
        if (disposed || generation !== formGeneration) return;
        review = preparePoolMutation(data, snapshot, status?.host, key());
        if (snapshot) {
          const index = pools.findIndex((v) => v.pool.id === snapshot.pool.id);
          if (index >= 0) pools[index] = snapshot;
        }
        formBox.hidden = true;
        renderReview();
      } catch (error) {
        if (generation === formGeneration) fail(error.message);
      } finally {
        if (generation === formGeneration) {
          for (const input of Object.values(inputs)) input.disabled = false;
          reviewButton.disabled = false;
        }
      }
    });
    Object.values(inputs)[0]?.focus();
  }
  function renderReview() {
    reviewBox.replaceChildren();
    if (!review) return;
    reviewBox.hidden = false;
    const plan = review;
    showEditor(2, plan.request.parent ? 'create-child' : plan.request.operation);
    reviewBox.append(
      el('h3', '', 'Proposed change'),
      el(
        'p',
        '',
        `${plan.scope === 'global' ? 'Global pool · single authority' : 'Pool on this host'} · ${plan.name}`
      ),
      el('p', 'pool-reviewed-reason', `Reason · ${plan.request.reason}`)
    );
    const addEffects=(rows)=>{const effects=el('dl','pool-effects');for(const [label,before,after]of rows)effects.append(el('dt','',label),el('dd','',`${before} → ${after}`));reviewBox.append(effects);};
    if(plan.parent) {
      const parent=plan.parent,a=parent.partition,committed=BigInt(a?.committed??parent.reserved),available=BigInt(parent.available);
      reviewBox.append(el('h4','','New child · '+plan.name),el('p','pool-inherited',`Within ${parent.pool.name}. Inherits ${parent.pool.scope} scope and ${plan.unit} units. Initial capacity: ${plan.capacity} ${plan.unit}.`));
      reviewBox.append(el('h4','','Parent · '+parent.pool.name),el('p','measure-note',`Exact parent revision ${plan.request.parent_revision}. Its total capacity stays ${parent.pool.capacity} ${plan.unit}; ${plan.capacity} units move into child backing.`));
      addEffects([['Committed backing',committed,committed+plan.capacity],['Uncarved available',available,available-plan.capacity]]);
    } else addEffects([
      ['Capacity',plan.before?.pool.capacity??'New',plan.capacity],
      ['State',stateLabel(plan.before?.pool.state??'New'),stateLabel(plan.state)],
      ['Committed backing',plan.before?.partition?.committed??plan.before?.reserved??0,plan.reserved],
      ['Available',plan.before?.available??0,plan.available],['Deficit',plan.before?.deficit??0,plan.deficit],
    ]);
    for (const paragraph of plan.paragraphs)
      reviewBox.append(el('p', '', paragraph));
    const scope = el('details', 'pool-review-scope');
    scope.append(
      el(
        'summary',
        '',
        `Complete root scope guard · ${plan.request.reviewed.hierarchy?.pool_count ?? 1} pools · ${plan.request.reviewed.hierarchy?.consumer_count ?? plan.request.reviewed.consumers.length} leases`
      )
    );
    if(plan.request.reviewed.hierarchy) scope.append(el('p','measure-note',`Root ${plan.request.reviewed.hierarchy.root}. Exact definitions, lineage, backing and all queued/consuming claims are bound by this snapshot. Direct selected-pool claims are listed below; subtree browsing shows their owners and reasons.`));
    let scopePage = 0;
    const scopeRows = el('div'),
      scopeNav = el('div', 'pool-tools');
    function renderScope() {
      scopeRows.replaceChildren();
      const consumers = plan.request.reviewed.consumers;
      for (const c of consumers.slice(scopePage * 50, scopePage * 50 + 50))
        scopeRows.append(el('p', '', `${c.id} · revision ${c.revision}`));
      scopeBack.disabled = scopePage === 0;
      scopeNext.disabled = (scopePage + 1) * 50 >= consumers.length;
      scopeLabel.textContent = `${scopePage * 50 + Math.min(consumers.length, 1)}–${Math.min(consumers.length, (scopePage + 1) * 50)} of ${consumers.length}`;
    }
    const scopeBack = button('Earlier obligations', () => {
        scopePage--;
        renderScope();
      }),
      scopeNext = button('Later obligations', () => {
        scopePage++;
        renderScope();
      }),
      scopeLabel = el('span');
    scopeNav.append(scopeBack, scopeLabel, scopeNext);
    scope.append(scopeRows, scopeNav);
    renderScope();
    reviewBox.append(scope);
    const technical = el('details', 'pool-technical');
    technical.append(el('summary', '', 'Technical review details'),
      jsonView(plan.before ?? { authority: plan.authority }, {
        label: 'Reviewed snapshot and obligations',
        document,
      }),
      jsonView(plan.request, {
        label: 'Exact request and complete consumer revisions',
        document,
      })
    );
    reviewBox.append(technical, el('p', 'pool-stale'));
    const controls = el('footer', 'pool-dialog-actions'),
      cancel = button('Cancel change', () => closeEditor()),
      keep = button('Back to edit', () => {
        review = null;
        reviewBox.hidden = true;
        formBox.hidden = false;
        showEditor(1, plan.request.operation);
        formBox.querySelector('textarea')?.focus();
      }),
      confirm = button(
        ({create:plan.request.parent?'Create subpool':'Create pool',set:`Set total capacity to ${count(plan.capacity)}`,adjust:plan.request.delta > 0 ? `Add ${count(plan.request.delta)} units` : `Remove ${count(-plan.request.delta)} units`,drain:'Pause new requests',resume:'Allow new requests',delete:'Delete empty pool'})[plan.request.operation],
        () => {
          if (!review || reviewStale || !connected()) return;
          const request = poolRequestCopy(review.request);
          closeEditor();
          ops
            .submit(request)
            .then(() => {
              onChanged();
              load(true);
            })
            .catch((error) => fail(error.message));
        },
        plan.request.operation === 'delete' ? 'danger' : 'primary'
      );
    confirm.dataset.poolConfirm = '';
    controls.append(cancel, keep, confirm);
    reviewBox.append(controls);
    setBusyState();
    cancel.focus();
  }
  function renderOperations() {
    const focused = operationsBox.contains(document.activeElement);
    operationsBox.replaceChildren();
    if (ops.warning)
      operationsBox.append(el('p', 'pool-feedback', ops.warning));
    for (const op of [...ops.operations].reverse()) {
      const item = el('article', 'pool-operation');
      item.append(
        el(
          'h4',
          '',
          `${({create:op.request.parent?'Create subpool':'Create pool',set:'Change total capacity',adjust:'Add or remove units',drain:'Pause new requests',resume:'Allow new requests',delete:'Delete empty pool'})[op.request.operation] || op.request.operation} · ${op.state === 'recorded' ? 'Recorded' : op.state === 'rejected' ? 'Rejected' : op.busy ? 'In flight' : 'Outcome uncertain'}`
        ),
        el('p', '', op.request.reason),
        el('small', 'pool-operation-key', `Operation key: ${op.request.idempotency_key}`),
        el('p', '', op.error || ''),
        jsonView(op.request, { label: 'Retained exact request', document })
      );
      if (op.change)
        item.append(
          jsonView(op.change, { label: 'Recorded change', document })
        );
      if (op.state === 'unknown') {
        const inspect = button('Check stored result', () => ops.lookup(op)),
          retry = button('Retry exact request', () => ops.retry(op));
        inspect.disabled = retry.disabled = op.busy || !connected();
        item.append(
          el(
            'p',
            'measure-note',
            'Lookup is read-only. A missing result cannot prove the original request will not commit. Retry retains this exact request/key.'
          ),
          inspect,
          retry
        );
      }
      operationsBox.append(item);
    }
    if (focused) {
      operationsBox.tabIndex = -1;
      operationsBox.focus();
    }
  }
  ops = createPoolOperations({
    call,
    authority: () => status?.host,
    storage,
    onChange: renderOperations,
  });
  renderOperations();
  filter.addEventListener('input', renderRows);
  return {
    update({ status: next, route }) {
      status = next;
      applyRoute(route);
      setBusyState();
      load();
    },
    updateConnection() {
      if (!connected()) {
        readGeneration++;
        historyGeneration++;
        historyReadKey = null;
        loading = false;
        cursor = null;
      }
      setBusyState();
      renderOperations();
      if (connected()) load(true);
    },
    dispose() {
      closeEditor(false);
      disposed = true;
      readGeneration++;
      historyGeneration++;
      formGeneration++;
    },
    select,
    applyRoute,
    focusLease(id) {
      const target = [...detail.querySelectorAll('button')].find(button => button.textContent === id) || detail.querySelector('h3');
      if (!target) return false;
      for (let parent = target.parentNode; parent && parent !== detail; parent = parent.parentNode)
        if (parent.tagName?.toLowerCase() === 'details') parent.open = true;
      target.tabIndex = -1;
      target.focus();
      return true;
    },
  };
}
