import { stringifyJSON, count, ownerDisplay } from '../core.mjs';
import { preparePoolMutation, poolRequestCopy, poolTreeRows } from '../pool-model.mjs';
import { createPoolOperations } from '../pool-operations.mjs';

const stateLabel = state => ({active:'Open for leases',draining:'New requests paused',deleted:'Historical pool'})[state] || state;
const operationLabel = (operation, child = false) => ({create:child?'Create subpool':'Create pool','create-child':'Create subpool',set:'Change total capacity',adjust:'Add or remove units',drain:'Pause new requests',resume:'Allow new requests',delete:'Delete empty pool'})[operation] || operation;

/**
 * The pool host effect uses the existing exact-count/review/intent models. Every
 * visible node and control is a Semblance component: no DOM template or CSS is
 * retained here. `ui` writes component triples and routes RDF events to effects.
 */
export function createPoolConsole({ui,root,call,connected,onLease=()=>{},onChanged=()=>{},onNavigate=()=>{},storage,key=()=>crypto.randomUUID()}) {
  let status=null,pools=[],selected='',cursor=null,loading=false,disposed=false;
  let readGeneration=0,historyGeneration=0,consumerGeneration=0,formGeneration=0;
  let history=null,historyAfter='0',historyBefore=null,historyReadKey=null;
  let consumerPage=null,consumerAfter='',consumerPrevious=[];
  let review=null,reviewStale=true,detailFingerprint=null,editorClosing=false;
  let opener=null,editorOperation='',inputs={},confirm=null,reviewMessage=null;
  let focusDetail=false,focusHistory=false,focusAfterEdit=null;
  let detailDisclosures=[],leaseControls=new Map(),actionControls=[];
  let detailFocusTargets=new Map(),journalFocusTargets=new Map(),journalItems=new Map();
  const allOnline=new Map();
  const feedback=ui.text('',{role:'status',hidden:true});
  const filter=ui.field('Find custom pools',{type:'search',placeholder:'Find custom pools'},()=>renderRows());
  const rows=ui.stack();
  const detail=ui.stack([],{hidden:true});
  const journal=ui.stack([],{hidden:true});
  const operations=ui.stack();
  const formBox=ui.stack([],{hidden:true});
  const reviewBox=ui.stack([],{hidden:true});
  const editorStep=ui.text('');
  const editorContext=ui.text('');
  const dialogFeedback=ui.text('',{role:'alert',hidden:true});
  const editor=ui.dialog('Change resource pool',[editorStep,editorContext,dialogFeedback,formBox,reviewBox]);
  editor.on('close',()=>{if(!editorClosing&&editorOperation)closeEditor();});
  const createButton=online(ui.button('Create pool…',()=>begin('create',createButton)));
  const refreshButton=online(ui.button('Refresh pools',()=>load(true)));
  root.append(ui.heading('Custom resource pools'),ui.stack([filter,createButton,refreshButton],{row:true}),
    ui.text('Counted cooperative resources. Global pools use this one authority; physical resources retain their own policies.'),
    feedback,rows,detail,operations,journal);

  function online(handle,readOnly=false) {
    allOnline.set(handle,readOnly);
    handle.disabled=!connected()||readOnly;
    return handle;
  }
  function forgetOnline(handles) { for(const handle of handles)allOnline.delete(handle); }
  function current() {return pools.find(view=>view.pool.id===selected);}
  function owner(uid,name,authority) {return ownerDisplay({owner:uid,owner_username:name},status,authority);}
  function fail(message='') {
    feedback.set({label:message,hidden:!message});
    if(editor.open)dialogFeedback.set({label:message,hidden:!message});
  }
  function closeEditor(restore=true) {
    return ui.batch(()=>{
    const operation=editorOperation;editorOperation='';
    formGeneration++;
    review=null;reviewStale=true;confirm=reviewMessage=null;inputs={};
    formBox.hidden=reviewBox.hidden=true;formBox.replace();reviewBox.replace();
    editorClosing=true;editor.close();editorClosing=false;
    dialogFeedback.set({label:'',hidden:true});
    if(restore) {
      if(opener?.isConnected)opener.focus();
      else if(detailFocusTargets.get('action:'+operation)?.isConnected)detailFocusTargets.get('action:'+operation).focus();
      else if(focusAfterEdit?.isConnected)focusAfterEdit.focus();
      else createButton.focus();
    }
    opener=null;
    });
  }
  function showEditor(step,operation) {
    return ui.batch(()=>{
    editorOperation=operation;
    editor.set({label:operationLabel(operation)});
    editorStep.set({label:step===1?'1 · Edit change':'2 · Review & confirm'});
    editorContext.set({label:operation==='create-child'?`Carve a named partition from ${current()?.pool.name||'this parent'}.`:operation==='create'?'Define a counted resource for agents to coordinate.':current()?.pool.name||review?.name||'Selected resource pool'});
    if(!editor.open)editor.showModal();
    });
  }
  function setBusyState() {
    return ui.batch(()=>{
    for(const [handle,readOnly]of allOnline)handle.disabled=!connected()||readOnly;
    if(review) {
      const now=current(),request=review.request;
      reviewStale=!connected()||request.reviewed.authority!==status?.host||Boolean((review.parent||review.before)&&
        (!now||String(now.pool.revision)!==String(request.parent_revision??request.revision)||stringifyJSON(now.review)!==stringifyJSON(request.reviewed)));
      if(confirm)confirm.disabled=reviewStale;
      reviewMessage?.set({label:reviewStale?'The pool snapshot or connection changed. Cancel this change, refresh the pool, then review again.':'',hidden:!reviewStale});
    }
    });
  }
  function renderRows() {
    return ui.batch(()=>{
    let hierarchy;
    try {hierarchy=poolTreeRows(pools);}catch(error){fail(error.message);return;}
    const search=filter.value.toLowerCase();
    const cards=hierarchy.filter(({view})=>stringifyJSON(view).toLowerCase().includes(search)).map(({view,depth})=>{
      const p=view.pool,parent=p.parent?pools.find(v=>v.pool.id===p.parent)?.pool:null;
      const pick=ui.button(`${depth?'↳ '.repeat(depth):''}${p.name}`,()=>select(p.id),{pressed:selected===p.id});
      return ui.card(p.name,[pick,ui.text(`${p.scope==='global'?'Global · one ledger':p.scope+' · '+p.host} · ${owner(p.owner,p.owner_username,p.authority)}${parent?' · Within '+parent.name:' · Root'}`),
        ui.badge(stateLabel(p.state)),ui.text(`Total ${count(p.capacity)} ${p.unit} · Committed ${count(view.partition?.committed??view.reserved)} · ${view.deficit>0?'Deficit '+count(view.deficit):'Available '+count(view.available)}`)]);
    });
    const emptyMessage='No custom pools yet. Create one to coordinate counted permits.';
    rows.replace(...(cards.length?cards:cursor!==null?[ui.text(pools.length?'No pools match this filter.':emptyMessage)]:[]));
    });
  }
  function renderDetail() {
    return ui.batch(()=>{
    const view=current(),fingerprint=stringifyJSON({view:view??null,consumerPage,selected,owner:status?.owner??null,username:status?.owner_username??null});
    if(fingerprint===detailFingerprint){setBusyState();return;}
    detailFingerprint=fingerprint;
    const focusedKey=[...detailFocusTargets].find(([,handle])=>handle===ui.activeComponent)?.[0];
    detailFocusTargets=new Map();
    const opened=new Set(detailDisclosures.filter(({handle})=>handle.open).map(({key})=>key));
    detailDisclosures=[];leaseControls=new Map();forgetOnline(actionControls);actionControls=[];
    detail.hidden=!selected;
    if(!view) {
      const title=ui.heading('Pool unavailable',3);focusAfterEdit=title;detailFocusTargets.set('title',title);
      detail.replace(...(selected?[ui.button('All pools',()=>select('')),title,ui.text(`No available pool snapshot for ${selected}. Refresh to check again; the selected address has been kept.`)]:[]));
      return;
    }
    const p=view.pool,title=ui.heading(p.name,3);focusAfterEdit=title;detailFocusTargets.set('title',title);
    const content=[ui.button('All pools',()=>select('')),title,ui.text(`${p.scope==='global'?'Global pool · one authority':'Pool on this host'} · ${stateLabel(p.state)} · ${owner(p.owner,p.owner_username,p.authority)}`),
      ui.stack((view.ancestors||[p.id]).map(id=>ui.button(pools.find(v=>v.pool.id===id)?.pool.name||id,()=>select(id))),{row:true})];
    if(p.description)content.push(ui.text(p.description));
    if(view.review?.unavailable)content.push(ui.text('Read only: this complete tree exceeds dashboard review limits. Browsing and existing obligations remain available; no change can use a truncated review.',{tone:'warning'}));
    content.push(ui.table(['Budget','Exact count'],[
      ['Total capacity',`${count(p.capacity)} ${p.unit}`],['Directly leased',`${count(view.partition?.direct_reserved??view.reserved)} ${p.unit}`],
      ['Backing for children',`${count(view.partition?.child_backing??0)} ${p.unit}`],['Uncarved available',`${count(view.available)} ${p.unit}`],
      ['Held in parent',p.parent?`${count(view.partition?.held_backing??0)} ${p.unit}`:'—'],['Pending return',`${count(view.partition?.pending_return??0)} ${p.unit}`]
    ]));
    if(view.deficit>0)content.push(ui.text(`Over capacity by ${count(view.deficit)} ${p.unit}. Existing promises keep their backing; the parent cannot reuse it yet.`,{tone:'warning'}));
    content.push(ui.text('Direct leases plus child backing are this node’s commitments. Parent and child figures overlap; do not add tree rows together.'));
    if(p.state!=='deleted') {
      const controls=[];
      for(const [operation,label,help]of [
        ...(p.state==='active'?[['create-child','Create subpool…','Carve existing capacity into a named child partition.']]:[]),
        ['set','Change total capacity…','Replace the total with a specific number.'],['adjust','Add or remove units…','Increase or decrease the current total.'],
        [p.state==='draining'?'resume':'drain',p.state==='draining'?'Allow new requests…':'Pause new requests…',p.state==='draining'?'Reopen admission to this pool.':'Refuse new requests; already queued work may still start.'],
        ['delete','Delete empty pool…','Retain its history; require no obligations.']]) {
        const button=online(ui.button(label,()=>begin(operation,button),{help,tone:operation==='delete'?'danger':undefined}),Boolean(view.review?.unavailable));
        actionControls.push(button);controls.push(button);detailFocusTargets.set('action:'+operation,button);
      }
      content.push(ui.stack(controls,{row:true}));
    }
    if(view.children?.length)content.push(ui.card('Child partitions',view.children.flatMap(id=>{
      const child=pools.find(v=>v.pool.id===id);return child?[ui.button(`${child.pool.name} · ${count(child.partition?.held_backing??child.pool.capacity)} ${p.unit} held`,()=>select(id))]:[];
    })));
    for(const [label,users]of [['Direct active leases',view.users],['Direct queued claims',view.queued]]) {
      const list=(users||[]).map(user=>{
        const button=ui.button(user.lease,()=>onLease(user.lease));leaseControls.set(user.lease,{button});detailFocusTargets.set('lease:'+user.lease,button);
        return ui.stack([button,ui.text(user.reason),ui.text(`${owner(user.owner,user.owner_username,p.authority)} · ${count(user.quantity)} ${p.unit} · ${user.state}`)]);
      });
      const handle=ui.disclosure(`${label} · ${users?.length||0}`,list,{open:opened.has(label)});
      for(const user of users||[])leaseControls.get(user.lease).disclosure=handle;
      detailDisclosures.push({key:label,handle});detailFocusTargets.set('disclosure:'+label,handle);content.push(handle);
    }
    const subtree=[ui.text('Each lease appears once. Its direct claims identify which child holds the units; tree totals are not summed.')];
    if(consumerPage?.pool===selected) {
      for(const entry of consumerPage.entries||[]) {
        const user=entry.use,button=ui.button(user.lease,()=>onLease(user.lease));leaseControls.set(user.lease,{button});detailFocusTargets.set('lease:'+user.lease,button);
        subtree.push(ui.stack([button,ui.text(user.reason),ui.text(`${owner(user.owner,user.owner_username,p.authority)} · ${user.state}`),
          ...(entry.direct_claims||[]).map(claim=>ui.text(`${pools.find(v=>v.pool.id===claim.pool)?.pool.name||claim.pool} · ${count(claim.quantity)} ${p.unit}`))]));
      }
      if(!consumerPage.entries?.length)subtree.push(ui.text('No queued or consuming claims in this subtree.'));
      subtree.push(ui.stack([
        ui.button('Previous leases',()=>loadConsumers(consumerPrevious.at(-1)??'','back'),{disabled:!consumerPrevious.length||!connected()}),
        ui.button('Next leases',()=>loadConsumers(consumerPage.cursor,'next'),{disabled:!consumerPage.more||!connected()})
      ],{row:true}));
    }else subtree.push(ui.text(connected()?'Loading current subtree claims…':'Reconnect to refresh subtree claims.'));
    content.push(ui.card('Leases across this subtree',subtree));
    const technical=ui.json(view,'Complete pool snapshot');technical.open=opened.has('snapshot');
    detailDisclosures.push({key:'snapshot',handle:technical});detailFocusTargets.set('snapshot',technical);content.push(technical);
    detail.replace(...content);setBusyState();
    if(focusDetail){title.focus();focusDetail=false;}else if(focusedKey){(detailFocusTargets.get(focusedKey)||title).focus();}
    });
  }
  async function load(force=false) {
    if(disposed||!connected()||loading||(!force&&cursor===status?.cursor))return;
    loading=true;
    const generation=++readGeneration,requestedCursor=status?.cursor,oldRevision=current()?.pool.revision;
    try {
      const result=await call('pool_list',{all:true});
      if(disposed||generation!==readGeneration)return;
      pools=result;cursor=requestedCursor;
      renderRows();renderDetail();
      if(selected&&(!history||force||String(oldRevision)!==String(current()?.pool.revision)))await loadHistory();
      if(selected)await loadConsumers(consumerAfter);
    }catch(error){if(generation===readGeneration)fail(error.message);}
    finally {if(generation===readGeneration)loading=false;}
  }
  function select(id) {if(disposed)return;onNavigate({pool:id||null,poolAfter:id?'0':null,poolBefore:null});}
  function applyRoute(route) {
    return ui.batch(()=>{
    if(disposed)return;
    const id=route?.view==='resources'?route.pool||'':'',after=id?route.poolAfter??'0':'0',before=id?route.poolBefore??null:null;
    if(id===selected&&after===historyAfter&&before===historyBefore)return;
    fail();focusDetail=id!==selected&&Boolean(id);focusHistory=id===selected&&Boolean(id);
    selected=id;historyAfter=after;historyBefore=before;history=null;
    consumerPage=null;consumerAfter='';consumerPrevious=[];consumerGeneration++;
    historyGeneration++;historyReadKey=null;closeEditor(false);
    renderRows();renderDetail();renderHistory();loadHistory();loadConsumers();
    });
  }
  async function loadConsumers(after='',direction='refresh') {
    if(disposed||!selected||!connected())return;
    const id=selected,generation=++consumerGeneration;
    try {
      const page=await call('pool_consumers',{id,descendants:true,after,limit:20});
      if(disposed||generation!==consumerGeneration||selected!==id)return;
      if(page.pool!==id||page.descendants!==true||!Array.isArray(page.entries))throw Error('Subtree claim page differs from the selected pool.');
      if(direction==='next')consumerPrevious.push(consumerAfter);else if(direction==='back')consumerPrevious.pop();
      consumerPage=page;consumerAfter=after;detailFingerprint=null;renderDetail();
    }catch(error){if(generation===consumerGeneration)fail(error.message);}
  }
  async function loadHistory() {
    if(disposed||!selected||!connected())return;
    const signature=`${selected}:${historyBefore===null?'after:'+historyAfter:'before:'+historyBefore}`;
    if(historyReadKey===signature)return;
    historyReadKey=signature;
    const id=selected,generation=++historyGeneration,after=historyAfter,before=historyBefore;
    try {
      const page=await call('pool_history_page',{id,...(before!==null?{before:BigInt(before)}:{after:BigInt(after)})});
      if(disposed||generation!==historyGeneration||id!==selected||after!==historyAfter||before!==historyBefore)return;
      history=page;renderHistory();
    }catch(error){if(!disposed&&generation===historyGeneration)fail(error.message);}
    finally {if(generation===historyGeneration)historyReadKey=null;}
  }
  function renderHistory() {
    return ui.batch(()=>{
    const active=ui.activeComponent,focusedKey=[...journalFocusTargets].find(([,handle])=>handle===active)?.[0];
    journalFocusTargets=new Map();
    journal.hidden=!history;
    if(!history){journal.replace();journalItems.clear();return;}
    const title=ui.heading('Capacity-change journal',3),nextItems=new Map();
    journalFocusTargets.set('title',title);
    const entries=history.entries.map(change=>{
      const identity=String(change.sequence),fingerprint=stringifyJSON(change),saved=journalItems.get(identity);
      const item=saved?.fingerprint===fingerprint?saved:{fingerprint,
        technical:ui.json(change,`Journal entry ${change.sequence}`)};
      if(!item.handle)item.handle=ui.card(`${change.operation} · ${new Date(change.at).toLocaleString()}`,[
        ui.text(owner(change.actor,change.actor_username,change.after.authority)),ui.text(change.reason),
        ui.text(`${change.before?.capacity??'—'} → ${change.after.capacity} ${change.after.unit} · change ${change.delta} · revision ${change.before?.revision??'—'} → ${change.after.revision}`),item.technical]);
      nextItems.set(identity,item);journalFocusTargets.set('entry:'+identity,item.technical);return item.handle;
    });
    journalItems=nextItems;
    const start=ui.button('First page',()=>onNavigate({pool:selected,poolAfter:'0',poolBefore:null}),{disabled:historyBefore===null&&historyAfter==='0'}),
      back=ui.button('Previous page',()=>onNavigate({pool:selected,poolAfter:null,poolBefore:String(history.previous_before)}),{disabled:history.previous_before==null}),
      next=ui.button('Next page',()=>onNavigate({pool:selected,poolAfter:String(history.next_after),poolBefore:null}),{disabled:history.next_after==null});
    journalFocusTargets.set('first',start);journalFocusTargets.set('previous',back);journalFocusTargets.set('next',next);
    journal.replace(title,ui.text('Each page preserves the recorded actor, time, reason and exact before/after counts.'),...entries,
      ...(!history.entries.length?[ui.text('No entries at this journal boundary.')]:[]),ui.stack([start,back,next],{row:true}));
    if(focusHistory){title.focus();focusHistory=false;}
    else if(focusedKey){const replacement=journalFocusTargets.get(focusedKey);(replacement&&!replacement.disabled?replacement:title).focus();}
    });
  }
  function begin(operation,source) {
    return ui.batch(()=>{
    if(disposed||!connected()||operation!=='create'&&(!current()||current().review?.unavailable))return;
    fail();opener=source;review=null;reviewStale=true;inputs={};
    formBox.hidden=false;reviewBox.hidden=true;
    const descriptions={
      'create-child':`A child partitions ${current()?.pool.name}. Scope and count units are inherited. It does not add capacity; all ancestors must have enough backing.`,
      create:'Choose a name, scope and total count. Agents can lease units from this pool.',
      set:`Current total: ${count(current()?.pool.capacity)} ${current()?.pool.unit}. Enter the replacement total; existing leases remain charged.`,
      adjust:'Use a positive number to add units, or a negative number to remove units. Existing leases remain charged.',
      drain:'New requests and renewals will be refused. Existing leases keep their obligations. Already queued requests may still start when capacity permits; queued requests are not cancelled.',
      resume:'New requests and renewals can be admitted again when capacity and other policies permit.',
      delete:'Only an empty pool with no queued or charged leases can be deleted. Its definition and journal remain available as history.'
    };
    const content=[ui.text(descriptions[operation])];
    const input=(name,label,value='',area=false,props={})=>{
      const handle=(area?ui.area.bind(ui):ui.field.bind(ui))(label,{value,...props});inputs[name]=handle;content.push(handle);return handle;
    };
    if(operation==='create'||operation==='create-child') {
      input('name',operation==='create-child'?'Subpool name':'Pool name');
      if(operation==='create-child')content.push(ui.text(`Parent · ${current().pool.name} · ${current().pool.scope} · ${current().pool.unit}. ${count(current().available)} uncarved units in this snapshot.`));
      else {
        inputs.scope=ui.select('Scope',[{value:'host',label:'This host'},{value:'global',label:'Global · one authority'}],{value:'host'});
        content.push(inputs.scope);input('unit','Count unit','count');
      }
      input('capacity','Initial capacity','0',false,{inputMode:'numeric'});input('description','Description (optional)','',true);
    }else if(operation==='set')input('capacity','New total capacity',String(current().pool.capacity),false,{inputMode:'numeric'});
    else if(operation==='adjust')input('delta','Units to add (+) or remove (−)','1');
    input('reason','Why are you making this change?','',true,{required:true,placeholder:'This reason will be recorded in the pool journal.'});
    const generation=++formGeneration;
    const reviewButton=ui.button('Review change',async()=>{
      if(disposed||!connected()||generation!==formGeneration)return;
      fail();const data={operation};for(const [name,handle]of Object.entries(inputs))data[name]=handle.value;
      for(const handle of Object.values(inputs))handle.disabled=true;reviewButton.disabled=true;
      try {
        const snapshot=operation==='create'?null:await call('pool_inspect',{id:selected});
        if(disposed||generation!==formGeneration)return;
        review=preparePoolMutation(data,snapshot,status?.host,key());
        if(snapshot){const index=pools.findIndex(v=>v.pool.id===snapshot.pool.id);if(index>=0)pools[index]=snapshot;}
        formBox.hidden=true;renderReview();
      }catch(error){if(generation===formGeneration)fail(error.message);}
      finally {if(generation===formGeneration){for(const handle of Object.values(inputs))handle.disabled=false;reviewButton.disabled=false;}}
    });
    content.push(ui.stack([ui.button('Cancel',()=>closeEditor()),reviewButton],{row:true}));
    formBox.replace(...content);showEditor(1,operation);Object.values(inputs)[0]?.focus();
    });
  }
  function renderReview() {
    return ui.batch(()=>{
    if(!review)return;
    const plan=review;reviewBox.hidden=false;showEditor(2,plan.request.parent?'create-child':plan.request.operation);
    const content=[ui.heading('Proposed change',3),ui.text(`${plan.scope==='global'?'Global pool · single authority':'Pool on this host'} · ${plan.name}`),ui.text(`Reason · ${plan.request.reason}`)];
    if(plan.parent) {
      const parent=plan.parent,committed=BigInt(parent.partition?.committed??parent.reserved),available=BigInt(parent.available);
      content.push(ui.heading('New child · '+plan.name,4),ui.text(`Within ${parent.pool.name}. Inherits ${parent.pool.scope} scope and ${plan.unit} units. Initial capacity: ${plan.capacity} ${plan.unit}.`),
        ui.heading('Parent · '+parent.pool.name,4),ui.text(`Exact parent revision ${plan.request.parent_revision}. Its total capacity stays ${parent.pool.capacity} ${plan.unit}; ${plan.capacity} units move into child backing.`),
        ui.table(['Effect','Before','After'],[['Committed backing',String(committed),String(committed+plan.capacity)],['Uncarved available',String(available),String(available-plan.capacity)]]));
    }else content.push(ui.table(['Effect','Before','After'],[
      ['Capacity',String(plan.before?.pool.capacity??'New'),String(plan.capacity)],['State',stateLabel(plan.before?.pool.state??'New'),stateLabel(plan.state)],
      ['Committed backing',String(plan.before?.partition?.committed??plan.before?.reserved??0),String(plan.reserved)],['Available',String(plan.before?.available??0),String(plan.available)],['Deficit',String(plan.before?.deficit??0),String(plan.deficit)]
    ]));
    content.push(...plan.paragraphs.map(label=>ui.text(label)));
    let scopePage=0;
    const consumers=plan.request.reviewed.consumers,scopeRows=ui.stack(),scopeLabel=ui.text('');
    const scopeBack=ui.button('Earlier obligations',()=>{scopePage--;renderScope();});
    const scopeNext=ui.button('Later obligations',()=>{scopePage++;renderScope();});
    function renderScope() {
      scopeRows.replace(...consumers.slice(scopePage*50,scopePage*50+50).map(c=>ui.text(`${c.id} · revision ${c.revision}`)));
      scopeBack.disabled=scopePage===0;scopeNext.disabled=(scopePage+1)*50>=consumers.length;
      scopeLabel.set({label:`${scopePage*50+Math.min(consumers.length,1)}–${Math.min(consumers.length,(scopePage+1)*50)} of ${consumers.length}`});
    }
    renderScope();
    content.push(ui.disclosure(`Complete root scope guard · ${plan.request.reviewed.hierarchy?.pool_count??1} pools · ${plan.request.reviewed.hierarchy?.consumer_count??consumers.length} leases`,[
      ...(plan.request.reviewed.hierarchy?[ui.text(`Root ${plan.request.reviewed.hierarchy.root}. Exact definitions, lineage, backing and all queued/consuming claims are bound by this snapshot. Direct selected-pool claims are listed below; subtree browsing shows their owners and reasons.`)]:[]),
      scopeRows,ui.stack([scopeBack,scopeLabel,scopeNext],{row:true})]));
    content.push(ui.disclosure('Technical review details',[ui.json(plan.before??{authority:plan.authority},'Reviewed snapshot and obligations'),ui.json(plan.request,'Exact request and complete consumer revisions')]));
    reviewMessage=ui.text('',{hidden:true,role:'alert'});content.push(reviewMessage);
    const cancel=ui.button('Cancel change',()=>closeEditor());
    const back=ui.button('Back to edit',()=>{review=null;reviewBox.hidden=true;formBox.hidden=false;showEditor(1,plan.request.parent?'create-child':plan.request.operation);inputs.reason?.focus();});
    const label=({create:plan.request.parent?'Create subpool':'Create pool',set:`Set total capacity to ${count(plan.capacity)}`,adjust:plan.request.delta>0?`Add ${count(plan.request.delta)} units`:`Remove ${count(-plan.request.delta)} units`,drain:'Pause new requests',resume:'Allow new requests',delete:'Delete empty pool'})[plan.request.operation];
    confirm=ui.button(label,()=>{
      setBusyState();if(disposed||!review||reviewStale||!connected())return;
      const request=poolRequestCopy(review.request);closeEditor();
      ops.submit(request).then(()=>{if(!disposed){onChanged();load(true);}}).catch(error=>{if(!disposed)fail(error.message);});
    },{tone:plan.request.operation==='delete'?'danger':undefined});
    content.push(ui.stack([cancel,back,confirm],{row:true}));reviewBox.replace(...content);setBusyState();cancel.focus();
    });
  }
  function renderOperations() {
    return ui.batch(()=>{
    if(disposed)return;
    const content=[];
    if(ops.warning)content.push(ui.text(ops.warning,{role:'status'}));
    for(const operation of [...ops.operations].reverse()) {
      const state=operation.state==='recorded'?'Recorded':operation.state==='rejected'?'Rejected':operation.busy?'In flight':'Outcome uncertain';
      const children=[ui.text(operation.request.reason),ui.text(`Operation key: ${operation.request.idempotency_key}`),ui.text(operation.error||''),ui.json(operation.request,'Retained exact request')];
      if(operation.change)children.push(ui.json(operation.change,'Recorded change'));
      if(operation.state==='unknown')children.push(ui.text('Lookup is read-only. A missing result cannot prove the original request will not commit. Retry retains this exact request/key.'),ui.stack([
        ui.button('Check stored result',()=>{if(connected())return ops.lookup(operation);},{disabled:operation.busy||!connected()}),
        ui.button('Retry exact request',()=>{if(connected())return ops.retry(operation);},{disabled:operation.busy||!connected()})
      ],{row:true}));
      content.push(ui.card(`${operationLabel(operation.request.operation,Boolean(operation.request.parent))} · ${state}`,children));
    }
    operations.replace(...content);
    });
  }
  const ops=createPoolOperations({call,authority:()=>status?.host,storage,onChange:renderOperations});
  renderOperations();
  return {
    update({status:next,route}) {if(disposed)return;status=next;applyRoute(route);setBusyState();load();},
    updateConnection() {
      if(disposed)return;
      if(!connected()){readGeneration++;historyGeneration++;consumerGeneration++;historyReadKey=null;loading=false;cursor=null;}
      setBusyState();renderOperations();if(connected())load(true);
    },
    dispose() {if(disposed)return;closeEditor(false);disposed=true;readGeneration++;historyGeneration++;consumerGeneration++;formGeneration++;allOnline.clear();editor.dispose();root.replace();},
    select,applyRoute,
    focusLease(id) {const target=leaseControls.get(id);if(!target){if(!focusAfterEdit?.isConnected)return false;focusAfterEdit.focus();return true;}if(target.disclosure)target.disclosure.open=true;target.button.focus();return true;}
  };
}
