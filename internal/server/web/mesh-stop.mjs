import {jsonView} from './json-view.mjs';
import {reviewDuration} from './lease-reviews.mjs';

const hash = (value, size) => typeof value === 'string' && new RegExp(`^[a-f0-9]{${size}}$`).test(value);
const exactPositive = value => typeof value === 'bigint' ? value > 0n && value <= 9223372036854775807n : Number.isSafeInteger(value) && value > 0;
const exactNonnegative = value => typeof value === 'bigint' ? value >= 0n && value <= 9223372036854775807n : Number.isSafeInteger(value) && value >= 0;
const when = value => Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString() : 'Not reported';

// Presentation gates only. The destination rechecks the actual grant, scope,
// generation and exact authority completion support during preparation and admission.
export function nodeStopAvailability(entry) {
  if (!entry || entry.local) return {enabled:false, reason:'Use the local ARM stop workflow for this backend.'};
  if (entry.trust?.state !== 'active') return {enabled:false, reason:'Reconnect this exact node before reviewing a stop.'};
  if (!entry.scope_freshness?.known || entry.scope_freshness.stale) return {enabled:false, reason:'Refresh this node to read current stop support and permissions.'};
  const scope = entry.outbound_scope;
  if (!scope || scope.target_host !== entry.host || (!Array.isArray(scope.grant?.permissions)||!scope.grant.permissions.includes('node-stop'))) return {enabled:false, reason:'This node has not granted this source permission to stop ARM.'};
  const capability = scope.node_stop;
  if (capability?.supported !== true) return {enabled:false, reason:capability?.reason || 'This ARM backend does not report usable remote stop support.'};
  return {enabled:true, reason:capability.reason || 'Prepare a concrete review of this exact ARM backend.'};
}

export function stopDraft(host, reason, mode, timeoutSeconds = '') {
  if (!hash(host,32)) throw Error('Choose an exact destination host.');
  if (typeof reason !== 'string' || !reason.trim() || new TextEncoder().encode(reason).length > 4096) throw Error('Explain the stop in at most 4096 UTF-8 bytes.');
  if (!['refuse','wait','force'].includes(mode)) throw Error('Choose a stop policy.');
  const policy = {force:mode==='force',wait:mode==='wait'};
  const input = String(timeoutSeconds).trim();
  if (mode !== 'wait' && input && input !== '0') throw Error('A reclaim timeout is available only with Wait for work.');
  if (mode === 'wait' && input && input !== '0') {
    if (!/^[1-9][0-9]{0,9}$/.test(input)) throw Error('Use a positive whole number of seconds, or leave the timeout empty.');
    const ns = BigInt(input)*1000000000n;
    if (ns > 9223372036854775807n) throw Error('The reclaim timeout is too large.');
    policy.timeout_ns = ns <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(ns) : ns;
  }
  return {host,reason,policy};
}

export function stopPolicyLabel(policy) {
  if (!policy || typeof policy.force !== 'boolean' || typeof policy.wait !== 'boolean' || policy.force && policy.wait || !exactNonnegative(policy.timeout_ns ?? 0) || (policy.timeout_ns ?? 0) > 0 && !policy.wait) return 'Unrecognized stop policy';
  if (policy.force) return 'Reclaim ARM-owned work, then stop';
  if (policy.wait) return (policy.timeout_ns ?? 0) > 0 ? `Wait; reclaim ARM-owned work after ${reviewDuration(policy.timeout_ns)}` : 'Wait for existing work, then stop';
  return 'Refuse while work or cleanup remains';
}

export function supportedStopReview(review) {
  const p = review?.plan;
  return review?.operation === 'stop-arm-node' && hash(review.review_sha256,64) && p?.schema === 'arm.node-stop.plan/v1' && typeof p.key === 'string' && p.key === review.key &&
    hash(p.target?.host,32) && hash(p.target?.backend_generation,32) && hash(p.target?.certificate_sha256,64) && hash(p.target?.spki_sha256,64) &&
    typeof p.target?.owner === 'string' && /^[0-9]+$/.test(p.target.owner) && typeof p.target.home === 'string' && p.target.home.startsWith('/') &&
    hash(p.actor?.source_host,32) && hash(p.actor?.certificate_sha256,64) && hash(p.actor?.spki_sha256,64) && hash(p.actor?.pair,64) && exactPositive(p.actor?.grant_revision) && exactPositive(p.desired_state_revision) &&
    stopPolicyLabel(p.policy) !== 'Unrecognized stop policy' && typeof p.reason === 'string' && p.reason.trim().length > 0 &&
    p.impact?.complete === true && hash(p.impact?.scope_sha256,64) && Array.isArray(p.impact.obligations) && p.impact.obligations.length <= 64 &&
    Array.isArray(p.impact.blockers) && p.impact.blockers.length === 0 && Array.isArray(p.impact.affected_peers) && p.impact.affected_peers.length <= 64 &&
    p.impact.outstanding === p.impact.obligations.length && (p.policy.wait||p.policy.force||p.impact.outstanding===0) && Number.isFinite(Date.parse(p.observed_at)) && Date.parse(p.expires_at)-Date.parse(p.observed_at)===120000 &&
    review.completion_check?.available === true;
}

export function stopReviewIssue(review) {
 const p=review?.plan;
 if(p?.impact?.complete!==true)return 'The work census is incomplete. Unknown work cannot be treated as an empty backend.';
 if(p.impact.blockers?.length)return 'Unresolved work prevents this stop. Force never discards unknown work or missing cleanup evidence.';
 if(!p.policy?.wait&&!p.policy?.force&&p.impact.outstanding>0)return 'Existing work prevents the default stop. Keep ARM running, then prepare a new review to wait or reclaim ARM-owned work.';
 if(review.completion_check?.available!==true)return review.completion_check?.detail||'ARM cannot currently confirm this backend stopped. Approval is unavailable.';
 if(!supportedStopReview(review))return 'The exact plan is incomplete or unsupported. Refresh its receipt; approval is unavailable.';
 return '';
}

export function stopOperationTarget(op) {
 if(op?.kind!=='stop'||!Array.isArray(op.targets)||op.targets.length!==1)return null;
 const match=/^host:([a-f0-9]{32})$/.exec(op.targets[0]);return match?match[1]:null;
}
export function assertStopOperation(op,key,host=null) {
 const target=stopOperationTarget(op);
 if(op?.key!==key||!exactPositive(op.revision)||!target||(host!==null&&target!==host)||typeof op.lookup_only!=='boolean'||typeof op.can_prepare_fresh!=='boolean')throw Error('The Stop receipt does not match the exact requested key and host. Keep the key and inspect its source record.');
 if(op.review&&(op.review.operation!=='stop-arm-node'||op.review.key!==key||op.review.plan?.key!==key||op.review.plan?.target?.host!==target))throw Error('The Stop review changed the requested target. No decision was sent.');
 return op;
}
function sameScalar(a,b){if(typeof a==='bigint'||typeof b==='bigint')return (typeof a==='bigint'||Number.isSafeInteger(a))&&(typeof b==='bigint'||Number.isSafeInteger(b))&&BigInt(a)===BigInt(b);return a===b;}
function sameStopTarget(a,b){return Boolean(a&&b)&&['host','backend_generation','home','owner','certificate_sha256','spki_sha256','startup_contract'].every(k=>a[k]===b[k])&&['device','inode','owner','born_seconds','born_nanoseconds','birth_known'].every(k=>sameScalar(a.directory?.[k],b.directory?.[k]));}
function sameStopActor(a,b){return Boolean(a&&b)&&['source_host','certificate_sha256','spki_sha256','pair'].every(k=>a[k]===b[k])&&sameScalar(a.grant_revision,b.grant_revision);}

export function stopAdvice(op) {
  if (op?.kind !== 'stop') return null;
  if (stopResultConfirmed(op.result,op.key,op.review??{})) return 'The original ARM backend stopped. This receipt remains available; starting the node is a separate reviewed action.';
  if (op.lookup_only === true || op.approved === true) return 'This stop may already be admitted. Look up this exact operation; it will never be resent or resumed here.';
  if (op.can_prepare_fresh === true) return 'This attempt ended before a stop was admitted. You may prepare a new review with a new key; this receipt is retained.';
  if (op.phase === 'awaiting_review') return 'ARM is still running. Review the exact backend, affected work and connections before deciding.';
  return 'Read the exact recorded outcome. A closed connection does not prove that ARM stopped.';
}

export function stopResultConfirmed(result, expectedKey = null, expectedReview = null) {
  const o=result?.observation,record=o?.record,state=o?.desired_state,target=record?.plan?.target;
  if ((expectedKey!==null&&result?.key!==expectedKey)||result?.operation!=='stop-arm-node'||result.status!=='stopped'||o?.server_joined!==true||o.original_generation_absent!==true||
    result.record!==undefined||record?.schema!=='arm.node-stop.record/v1'||state?.schema!=='arm.node-stop.state/v1'||record.plan?.key!==result.key||state.stop_key!==result.key||
    record.phase!=='drain-ready'||!hash(record.review_sha256,64)||state.stop_sha256!==record.review_sha256||
    !exactPositive(record.desired_state_revision)||!exactPositive(state.revision)||typeof state.desired_running!=='boolean'||!hash(target?.host,32)||!hash(target?.backend_generation,32)) return false;
  if(expectedReview!==null&&(expectedReview.key!==result.key||expectedReview.plan?.key!==result.key||expectedReview.review_sha256!==record.review_sha256||!sameStopTarget(target,expectedReview.plan?.target)||!sameStopActor(record.plan.actor,expectedReview.plan?.actor)))return false;
  if(!sameStopTarget(target,state.target))return false;
  const revision=BigInt(record.desired_state_revision)+(state.desired_running?1n:0n);
  return revision<=9223372036854775807n&&BigInt(state.revision)===revision&&['host','backend_generation','home','owner','certificate_sha256','spki_sha256'].every(key=>state.target?.[key]===target[key]);
}
export function stopResultLabel(result, expectedKey = null, expectedReview = null) {
  // The destination/coordinator owns the positive-generation observation. UI
  // never substitutes a transport disconnect or generic completed phase.
  if (result?.status==='stopped' && !stopResultConfirmed(result,expectedKey,expectedReview)) return 'Stop outcome unknown';
  return ({accepted:'Stop accepted',draining:'Waiting for work and cleanup',stopped:result?.observation?.desired_state?.desired_running?'Original ARM backend stopped · restart requested':'Original ARM backend stopped',unknown:'Stop outcome unknown',blocked:'Stop needs attention'})[result?.status] || 'Recorded stop outcome';
}

export function stopReviewView(review, {document = globalThis.document, inspectLease = null} = {}) {
  const el = (tag, cls='', text=null) => {const n=document.createElement(tag);n.className=cls;if(text!==null)n.textContent=String(text);return n;};
  const root = el('section','mesh-stop-review');
  const note = (text,warning=false) => el('p',warning?'mesh-note warning':'mesh-note',text);
  const fact = (parent,label,value,mono=false) => {const row=el('div','mesh-fact');row.append(el('dt','',label),el('dd',mono?'mesh-mono':'',value??'Not reported'));parent.append(row);};
  const card = (label, facts) => {const node=el('section','mesh-stop-card');node.append(el('h4','',label),facts);return node;};
  const p = review?.plan;
  if (!p) {root.append(note('The exact stop plan is unavailable. Refresh its receipt; approval is unavailable.',true));return root;}
  const heading=el('div','mesh-stop-heading');heading.append(el('span','mesh-stop-symbol','◼'),el('div','',null));heading.children[1].append(el('h3','','Stop ARM on this node'),el('p','','Stops this ARM backend and its connections. The host remains running.'));root.append(heading);
  const target=el('dl','mesh-facts'),actor=el('dl','mesh-facts');
  fact(target,'Stable host',p.target?.host,true);fact(target,'Run as',review.owner_username?`${review.owner_username} (UID ${p.target?.owner})`:`UID ${p.target?.owner??'not reported'} · name not included in review`);
  fact(target,'ARM home',p.target?.home,true);fact(target,'Backend generation',p.target?.backend_generation,true);fact(target,'Desired-state revision',p.desired_state_revision?.toString());
  fact(actor,'Source host',p.actor?.source_host,true);fact(actor,'Exact enrollment',p.actor?.pair,true);fact(actor,'Grant revision',p.actor?.grant_revision?.toString());fact(actor,'Access used','Separate destination-granted node-stop permission');
  const identity=el('div','mesh-stop-grid');identity.append(card('EXACT BACKEND',target),card('ACCESS USED',actor));root.append(identity);
  const policy=el('div','mesh-stop-policy');policy.append(el('span','eyebrow','WHAT WILL HAPPEN'),el('strong','',stopPolicyLabel(p.policy)));root.append(policy);
  root.append(note(p.supervisor_scope||'Supervisor scope is not reported.',true));
  if (review.completion_check?.available !== true) root.append(note(review.completion_check?.detail||'ARM cannot currently confirm that this backend has stopped. Approval is unavailable.',true));
  else {
    root.append(note('A completion check is available for this exact ARM backend.'));
    if (review.completion_check.detail) {
      const check=el('details','mesh-stop-completion');
      check.append(el('summary','','How ARM checks completion'),el('code','mesh-stop-completion-detail',review.completion_check.detail));
      root.append(check);
    }
  }
  const impact=p.impact,section=el('section','mesh-stop-impact');section.append(el('h4','','AFFECTED WORK AND CONNECTIONS'));
  if (!impact || impact.complete !== true) section.append(note('The work census is incomplete. It cannot be treated as an empty backend; approval is unavailable.',true));
  else section.append(el('p','mesh-stop-count',`${impact.outstanding} outstanding ${impact.outstanding===1?'obligation':'obligations'} · ${impact.affected_peers?.length??0} affected peers`));
  if (Array.isArray(impact?.obligations) && impact.obligations.length) {
    const wrap=el('div','mesh-stop-table-wrap'),table=el('table','mesh-stop-table'),head=el('thead'),tr=el('tr');for (const name of ['Work','Who / why','State','Revision']) tr.append(el('th','',name));head.append(tr);table.append(head);const body=el('tbody');
    const inspector=el('section','mesh-stop-card mesh-stop-lease-inspector');inspector.setAttribute('aria-live','polite');let readVersion=0;
    for (const item of impact.obligations.slice(0,64)) {
      const row=el('tr'),work=el('td'),attribution=el('td');work.append(el('strong','mesh-mono',item.id),el('p','mesh-note',item.kind));
      if(item.kind==='lease'){
        const v=item.lease,permitted=item.lease_details==='permitted'&&v;
        if(permitted){attribution.append(el('strong','',v.owner_username?`${v.owner_username} (UID ${v.owner})`:`UID ${v.owner} · ${v.truncated?'name omitted from preview for size':'recorded name unavailable'}`),el('p','',v.reason|| (v.truncated?'Purpose omitted from this preview for size.':'Purpose not recorded.')));if(v.agent||v.project)attribution.append(el('p','mesh-note',[v.agent&&`Agent ${v.agent}`,v.project&&`Project ${v.project}`].filter(Boolean).join(' · ')));if(v.truncated)attribution.append(note('Shortened preview. Open lease details for the complete purpose.'));
          if(inspectLease){const open=el('button','mesh-lease-inspect','View lease details');open.type='button';open.setAttribute('aria-label',`View lease details for ${item.id}`);open.addEventListener('click',async()=>{const version=++readVersion;inspector.replaceChildren(el('h4','','LEASE DETAILS · READ ONLY'),note('Reading the exact destination lease…'));open.disabled=true;try{const detail=await inspectLease(item);if(version===readVersion)inspector.replaceChildren(stopLeaseDetailView(detail,{document}));}catch(e){if(version===readVersion)inspector.replaceChildren(el('h4','','LEASE DETAILS UNAVAILABLE'),note(`${e.message} The Stop review is unchanged; no lease or Stop action was sent.`,true));}finally{open.disabled=false;}});work.append(open);}
        }else attribution.append(note('Details not shared by this destination. Node-stop permission does not include private lease owners or purposes.'));
      }else attribution.append(el('p','mesh-note','ARM connection or owned work interval'));
      row.append(work,attribution,el('td','',item.state??'Not reported'),el('td','',item.revision?.toString()??'Not reported'));body.append(row);
    }table.append(body);wrap.append(table);section.append(wrap,inspector);
  }
  if (Array.isArray(impact?.blockers)) for (const blocker of impact.blockers.slice(0,64)) section.append(note(`${blocker.class} · ${blocker.id}: ${blocker.detail}`,true));
  if (Array.isArray(impact?.affected_peers) && impact.affected_peers.length) {const peers=el('details','mesh-stop-peers'),list=el('ul');peers.append(el('summary','',`${impact.affected_peers.length} connections affected`));for (const host of impact.affected_peers.slice(0,64)) list.append(el('li','mesh-mono',host));peers.append(list);section.append(peers);}
  root.append(section);
  const reason=el('section','mesh-stop-reason');reason.append(el('h4','','OPERATOR REASON'),el('p','',p.reason??'Not reported'));root.append(reason);
  const reviewFacts=el('dl','mesh-facts mesh-stop-review-facts');fact(reviewFacts,'Review expires',when(p.expires_at));fact(reviewFacts,'Stop history',Number.isInteger(review.history_used)&&Number.isInteger(review.history_capacity)?`${review.history_used} / ${review.history_capacity} retained records`:'Not reported');root.append(reviewFacts);
  const issue=stopReviewIssue(review);if(issue)root.append(note(issue,true));
  root.append(jsonView(review,{label:'Full immutable stop review and identity fingerprints',document}));return root;
}

// Deliberately a read-only allowlisted view; no generic local lease navigation
// or actions are attached to remote Stop obligations.
export function stopLeaseDetailView(detail,{document=globalThis.document}={}) {
 const el=(tag,cls='',text=null)=>{const n=document.createElement(tag);n.className=cls;if(text!==null)n.textContent=String(text);return n;};
 const root=el('section','mesh-stop-lease-detail'),facts=el('dl','mesh-facts');
 root.append(el('h4','','LEASE DETAILS · READ ONLY'));
 const account=detail.owner_username?`${detail.owner_username} (UID ${detail.owner})`:`UID ${detail.owner} · recorded name unavailable`;
 const resources=detail.resources??{};
 for(const [label,value]of [['Lease',detail.id],['Destination',detail.host],['Owner',account],['Purpose',detail.reason],['Agent',detail.agent||'Not recorded'],['Project',detail.project||'Not recorded'],['Origin',detail.origin_peer||'Destination-local'],['State',detail.outcome?`${detail.state} · ${detail.outcome}`:detail.state],['Revision',`${detail.revision} · reviewed ${detail.reviewed_revision}`],['Lease kind',detail.kind||'Resource reservation'],['CPU cores',resources.cpu],['Memory bytes',resources.memory_bytes],['Disk bytes',resources.disk_bytes],['Created',when(detail.created_at)],['Started',detail.started_at?when(detail.started_at):'Not started'],['Expires',detail.expires_at?when(detail.expires_at):'Not set'],['Ended',detail.ended_at?when(detail.ended_at):'Not ended']]){const row=el('div','mesh-fact');row.append(el('dt','',label),el('dd','',value??'Not reported'));facts.append(row);}
 for(const pool of detail.pools??[]){const row=el('div','mesh-fact');row.append(el('dt','','Custom pool'),el('dd','',`${pool.pool} · ${pool.quantity} units`));facts.append(row);}
 for(const port of detail.ports??[]){const row=el('div','mesh-fact');row.append(el('dt','','Port claim'),el('dd','',`${port.name?port.name+' · ':''}${port.network}://${port.address}:${port.port}${port.range_end?'-'+port.range_end:''} · ${port.mode} · ${port.state}`));facts.append(row);}
 if(detail.changed_since_review)root.append(el('p','mesh-note warning','This lease changed since the Stop review. These current details do not update or approve that review; destination admission rechecks the complete scope.'));
 root.append(facts,el('p','mesh-note','Read using the destination’s current lease-read permission. Your original Stop review and Keep running choice are retained.'));return root;
}
