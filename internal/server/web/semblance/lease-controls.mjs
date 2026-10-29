import {detailTime} from './detail-presentation.mjs';
import {leaseActions, leaseActionReview, leaseActionResult} from '../lease-actions.mjs';
import {reviewConfiguration, reviewDuration, reviewDurationInput, REVIEW_CAPABILITY} from '../lease-reviews.mjs';
import {bytes, count} from '../core.mjs';
export {REVIEW_CAPABILITY};
const when = value => detailTime(value);

// Only stock component instances are created here. State transitions and exact
// request policies remain the same shared functions used by the previous UI.
export function createLeaseActions({ui, root, lease, leases = [], call, connected,
  onResult = () => {}, onRefresh = () => {}, busy = () => false, onPending = () => {}, now = Date.now}) {
  let disposed = false, sending = false, finished = false, chosen = '', confirm, declineControl, timer, ttl, preview, validation = '';
  const actionButtons = [], actionSources = new Map();
  const review = ui.stack([], {hidden:true}), feedback = ui.text('');
  const refresh = ui.button('Refresh lease', () => onRefresh(),{emphasis:'Quiet'});
  const unavailable = {terminal:'This lease has ended.', ending:'Stopping or cleanup is in progress.', reclaiming:'Reclamation is in progress.', needs_attention:'Reclamation needs attention. Inspect the record and use ARM’s reconciliation or storage commands.'};
  const idleFeedback = !leaseActions(lease, now()).length ? unavailable[lease.state] || 'No controls are available for this lease state.' : '';
  feedback.set({label:idleFeedback});
  function updateConnection() {
    if(disposed)return;
    const disabled = !connected() || sending || finished || busy();
    for(const button of actionButtons)button.disabled=disabled;
    if(confirm)confirm.disabled=disabled||Boolean(validation);
    if(declineControl)declineControl.disabled=sending;
    if(ttl)ttl.disabled=sending||finished;
    refresh.disabled=!connected()||sending||busy();
    if(!connected()&&!sending&&!finished)feedback.set({label:'Reconnect before changing this lease.'});
    else if(connected()&&!sending&&!finished)feedback.set({label:busy()?'Another request for this lease is still pending.':idleFeedback});
  }
  function updatePreview() {
    if(disposed)return;
    validation='';
    if(chosen==='renew')try {
      const plan=leaseActionReview(lease,chosen,{ttl:ttl.value,now:now(),leases});
      preview.set({label:plan.expires ? (plan.expiresAuditOnly ? `Browser calendar estimate (audit only): ${plan.expires.toLocaleString()}. The backend decides the native deadline.` : `Estimated new expiry: ${plan.expires.toLocaleString()}. The accepted expiry will be shown after confirmation.`) : 'Browser calendar estimate unavailable. The backend decides the native deadline.'});
    } catch(error) {validation=error.message;preview.set({label:validation});}
    updateConnection();
  }
  function decline() {const source=actionSources.get(chosen);chosen='';review.hidden=true;clearInterval(timer);timer=null;if(!disposed)source?.focus();}
  function choose(action) {
    if(disposed||sending||finished||busy()||!connected())return;
    if(!leaseActions(lease,now()).includes(action)){feedback.set({label:'This lease changed or expired. Refresh it before reviewing another action.'});return;}
    chosen=action;ttl=null;preview=null;validation='';
    const children=[ui.heading({renew:'Extend lease',release:'Release lease?',cancel:'Cancel lease?'}[action],4),ui.text(`Lease ${lease.id} · revision ${lease.revision}`),ui.text(lease.request?.reason||'')];
    if(action==='renew') {
      ttl=ui.field('New duration from now',{value:'15m',help:'Examples: 15m, 2h, 1d or 1h30m. Maximum: 7 days.'},updatePreview);
      preview=ui.text('');children.push(ttl,ui.text(`${lease.elapsed_deadline != null ? 'Recorded calendar expiry (audit only)' : 'Current expiry'}: ${when(lease.expires_at)}`),preview);
    }
    const plan=action==='renew'?{paragraphs:['The duration starts when ARM accepts this request. It is not added to the existing expiry.','Extensions can be denied because of waiting work, memory pressure, pool limits or attached lease deadlines.']}:leaseActionReview(lease,action,{now:now(),leases});
    children.push(...plan.paragraphs.map(text=>ui.text(text)));
    declineControl=ui.button('Keep lease',()=>{if(!sending)decline();});
    confirm=ui.button({renew:'Confirm extension',release:'Confirm release',cancel:'Confirm cancellation'}[action],submit,{tone:action==='renew'?'info':'danger'});
    children.push(ui.stack([declineControl,confirm],{row:true}));review.replace(...children);review.hidden=false;
    clearInterval(timer);if(action==='renew')timer=setInterval(updatePreview,1000);
    updatePreview();(ttl||declineControl).focus();
  }
  async function submit() {
    if(disposed||sending||finished||busy()||!connected())return;
    let plan;
    try{plan=leaseActionReview(lease,chosen,{ttl:ttl?.value,now:now(),leases});}
    catch(error){validation=error.message;preview?.set({label:validation});updateConnection();return;}
    sending=true;clearInterval(timer);onPending(true);updateConnection();feedback.set({label:'Sending the reviewed request…'});
    try {
      const result=await call('lease_action',plan.params);finished=true;
      if(!disposed){decline();feedback.set({label:leaseActionResult(result)});}onResult(result);
    } catch(error) {
      finished=true;if(!disposed){decline();feedback.set({label:error.definitive?`${error.message}. Refresh the lease before reviewing another action.`:'The outcome is unknown because no response was received. The request may have been applied. Refresh the lease and inspect its state before taking another action.'});}
    } finally{sending=false;onPending(false);if(!disposed){updateConnection();refresh.focus();}}
  }
  for(const action of leaseActions(lease,now())) {
    const button=ui.button({renew:'Extend',release:'Release',cancel:'Cancel'}[action],()=>choose(action),{emphasis:action==='renew'?'Strong':'Quiet',...(action==='release'||action==='cancel'?{tone:'danger'}:{})});
    actionButtons.push(button);actionSources.set(action,button);
  }
  ui.put?.(root.iri,'http://www.w3.org/1999/02/22-rdf-syntax-ns#type','https://arm.local/ui#LeaseSection',true);
  root.append(ui.heading('Lease controls',4),ui.toolbar('Lease actions',[...actionButtons,refresh]),review,feedback);updateConnection();
  return {element:root,updateConnection,dispose(){disposed=true;clearInterval(timer);}};
}

export function createLeaseReviews({ui, root, lease, call, connected, supported,
  busy = () => false, onPending = () => {}, onRefresh = () => {}}) {
  let disposed=false,loading=false,sending=false,finished=false,policy,cursor=0,request=0,selectedPlan,confirm,decline;
  const fields={};
  const status=ui.badge(supported?'Loading':'Unavailable'),feedback=ui.text(''),pendingBox=ui.stack(),summary=ui.stack(),timeline=ui.stack(),editor=ui.stack([],{hidden:true});
  const configure=ui.button('Configure check-ins',choose,{emphasis:'Quiet'}),refresh=ui.button('Refresh check-ins',()=>onRefresh(),{emphasis:'Quiet'}),more=ui.button('Next history page',()=>load(true),{hidden:true});
  const schedule=ui.text(''),policySource=ui.stack();
  const history=ui.disclosure('Conversation history',[timeline,more],{open:false});
  const advanced=ui.disclosure('Check-in policy & safety',[ui.text('Ask the owning agent whether these resources are still needed. Replies and silence never renew, release or resize a lease.'),ui.text('Optional and off by default. Delivery uses the agent’s durable ARM inbox.'),schedule,policySource,history],{open:false});
  ui.put?.(root.iri,'http://www.w3.org/1999/02/22-rdf-syntax-ns#type','https://arm.local/ui#LeaseSection',true);
  root.append(ui.stack([ui.heading('Agent check-ins',4),status],{row:true}),summary,ui.toolbar('Check-in controls',[configure,refresh]),editor,feedback,pendingBox,advanced);
  function updateConnection() {
    if(disposed)return;
    configure.disabled=!supported||!connected()||loading||sending||finished||busy()||!policy;
    refresh.disabled=!supported||!connected()||loading||sending||busy();more.disabled=refresh.disabled;
    if(confirm)confirm.disabled=configure.disabled||!selectedPlan;
    if(decline)decline.disabled=sending;
    for(const field of Object.values(fields))field.disabled=sending||finished;
  }
  function recordComponent(record) {
    const children=[ui.badge(({pending:'Awaiting agent',replied:'Agent replied',timed_out:'No reply',superseded:'Context changed'})[record.state]||record.state),ui.text(`${when(record.created_at)} · reply by ${when(record.reply_deadline_at)}`)];
    for(const context of record.leases??[]) {
      const rows=[['Lease',`${context.lease} · revision ${context.lease_revision}`],['Claims',`${count(context.resources?.cpu??0)} CPU · ${bytes(context.resources?.memory_bytes??0)} memory · ${bytes(context.resources?.disk_bytes??0)} disk`],['Time remaining at capture',reviewDuration(context.remaining_ns)],['Waiting requests',`${context.queued_demand??0} with overlapping resource kinds`],['Usage quality',`${context.usage?.cpu_quality||'unknown'} CPU · ${context.usage?.memory_quality||'unknown'} memory${context.usage?.stale?' · stale':''}${context.usage?.complete?'':' · incomplete'}`],['Observed CPU / memory',`${count(context.usage?.cpu_cores)} cores / ${bytes(context.usage?.memory_bytes)}`]];
      const details=rows.map(([label,value])=>ui.text(`${label}: ${value}`));
      for(const [label,values]of [['Custom claims',context.pools],['Ports',context.ports]])if(values?.length)details.push(ui.json(values,label));
      children.push(ui.card(context.purpose||context.lease,details));
    }
    if(record.reply)children.push(ui.text(`Agent intent: ${record.reply.intent}`),ui.text(record.reply.explanation),ui.text('This explanation is a conversation record. Use Lease controls above to review an actual change.'));
    if(record.detail)children.push(ui.text(record.detail));children.push(ui.json(record,'Exact session and complete checkpoint'));
    return ui.card(record.agent||'Agent check-in',children);
  }
  async function load(append=false) {
    if(disposed||loading||sending||!supported||!connected()||busy())return;
    loading=true;updateConnection();const generation=++request;
    try {
      const nextPolicy=append?policy:await call('lease_review_policy',{lease:lease.id});if(disposed||generation!==request)return;
      const pendingPage=append?null:await call('lease_review_list',{lease:lease.id,pending:true});
      const page=await call('lease_review_list',{lease:lease.id,after:append?cursor:0});if(disposed||generation!==request)return;
      if(!Number.isSafeInteger(page.cursor)||page.cursor<0||(append&&(page.more||page.items?.length)&&page.cursor<=cursor))throw Error('Invalid check-in cursor; refresh before continuing.');
      policy=nextPolicy;cursor=page.cursor;finished=false;editor.hidden=true;status.set({label:policy.enabled?'Enabled':'Off'});
      summary.replace(ui.text(policy.enabled?`Every ${reviewDuration(policy.interval_ns)} · reply window ${reviewDuration(policy.reply_timeout_ns)}${policy.contention_interval_ns?` · ${reviewDuration(policy.contention_interval_ns)} while work is waiting`:''}`:'No automatic questions.'));
      schedule.set({label:`Policy generation ${policy.policy_generation}${policy.enabled?` · next scheduled ${when(policy.next_at)}`:''}`});policySource.replace(ui.json(policy,'Exact check-in policy'));
      if(pendingPage)pendingBox.replace(...(pendingPage.items?.length?[ui.heading('Awaiting the owning agent',4),...pendingPage.items.map(recordComponent)]:[ui.text('No reply is currently requested.')]));
      const records=(page.items??[]).filter(record=>record.state!=='pending').map(recordComponent);
      // "Next" deliberately replaces the bounded history page; prior records
      // remain durable and inspectable through the backend, not an unbounded UI.
      timeline.replace(...records,...(!append&&!page.items?.length?[ui.text('No check-ins recorded for this lease.')]:[]));
      more.hidden=!page.more;feedback.set({label:page.scheduler_error?`Check-in scheduling needs attention: ${page.scheduler_error}. Lease deadlines still apply.`:''});
    } catch(error){if(!disposed){finished=true;status.set({label:'Unavailable',tone:'warning'});feedback.set({label:error.message});}}
    finally{loading=false;if(!disposed)updateConnection();}
  }
  function choose() {
    if(configure.disabled||disposed)return;selectedPlan=null;editor.hidden=false;
    for(const key of Object.keys(fields))delete fields[key];
    fields.enabled=ui.check('Enable periodic check-ins',{checked:policy.enabled},validate);
    for(const [name,label,value]of [['interval','Regular interval',policy.interval_ns?reviewDurationInput(policy.interval_ns):'10m'],['contention','Interval while work is waiting (0s disables acceleration)',reviewDurationInput(policy.contention_interval_ns)],['timeout','Agent reply window',policy.reply_timeout_ns?reviewDurationInput(policy.reply_timeout_ns):'1m']])fields[name]=ui.field(label,{value},validate);
    const preview=ui.text('');
    function validate() {
      try{selectedPlan=reviewConfiguration(lease,policy,fields.enabled.checked,{interval:fields.interval.value,contention:fields.contention.value,timeout:fields.timeout.value});preview.set({label:fields.enabled.checked?'Send questions to the exact registered owner through ARM polling. No change to the lease expiry or resource claims.':'Stop future questions. Existing conversation history remains; no lease is ended.'});}
      catch(error){selectedPlan=null;preview.set({label:error.message});}updateConnection();
    }
    decline=ui.button('Keep current settings',()=>{if(!sending){editor.hidden=true;configure.focus();}});
    confirm=ui.button('Save reviewed settings',async()=>{
      if(disposed||sending||finished||!connected()||busy()||!selectedPlan)return;
      const exact={...selectedPlan};sending=true;onPending(true);updateConnection();feedback.set({label:'Saving the reviewed settings…'});
      try{await call('lease_review_configure',exact);finished=true;if(!disposed){editor.hidden=true;feedback.set({label:'Settings accepted. Refresh to inspect the current schedule.'});}}
      catch(error){finished=true;if(!disposed){editor.hidden=true;feedback.set({label:error.definitive?`${error.message}. Refresh the lease and policy before another change.`:'Outcome unknown: the settings may have been saved. Refresh the lease and policy before another change. Nothing will be retried automatically.'});}}
      finally{sending=false;onPending(false);if(!disposed)updateConnection();}
    });
    editor.replace(ui.heading('Review check-in settings',4),ui.text(`Lease ${lease.id} · revision ${lease.revision} · policy generation ${policy.policy_generation}`),...Object.values(fields),preview,ui.stack([decline,confirm],{row:true}));validate();fields.enabled.focus();
  }
  if(!supported)feedback.set({label:'Check-ins are available only on a local backend advertising this feature. Remote node controls remain unavailable.'});
  updateConnection();const ready=supported?load():Promise.resolve();
  return {element:root,ready,updateConnection,dispose(){disposed=true;request++;}};
}
