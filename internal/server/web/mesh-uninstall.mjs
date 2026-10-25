import {bytes,stringifyJSON} from './core.mjs';
import {jsonView} from './json-view.mjs';
import {nodeStopAvailability,stopDraft,stopPolicyLabel} from './mesh-stop.mjs';

const hash=(v,n)=>typeof v==='string'&&new RegExp(`^[a-f0-9]{${n}}$`).test(v);
const key=v=>typeof v==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(v);
const positive=v=>typeof v==='bigint'?v>0n&&v<=9223372036854775807n:Number.isSafeInteger(v)&&v>0;
const nativeIdentity=v=>v?.birth_known===true&&positive(v.inode)&&['device','owner','born_seconds','born_nanoseconds'].every(k=>typeof v[k]==='bigint'||Number.isSafeInteger(v[k]));
const preserved=['database','node identity and grants','configuration and startup policy','lease and operation history','backups and retained artifacts','companion catalog configuration','recovery helper and uninstall receipt'];
const same=(a,b)=>stringifyJSON(a)===stringifyJSON(b);

export function nodeUninstallAvailability(entry){
 const stop=nodeStopAvailability(entry);
 if(!stop.enabled)return stop;
 if(!entry.outbound_scope.grant.permissions.includes('node-uninstall'))return{enabled:false,reason:'This node has not separately granted permission to uninstall its ARM-managed executable.'};
 return{enabled:true,reason:'Choose an original managed installation, then review exact removal and preserved data.'};
}
export function uninstallDraft(host,provisionKey,reason,mode='refuse',timeout=''){
 if(!key(provisionKey))throw Error('Choose an exact original provisioning receipt.');
 return{...stopDraft(host,reason,mode,timeout),provision_key:provisionKey};
}
export function uninstallOperationTarget(op){
 if(op?.kind!=='uninstall'||!Array.isArray(op.targets)||op.targets.length!==1)return null;
 return /^host:([a-f0-9]{32})$/.exec(op.targets[0])?.[1]??null;
}
export function assertUninstallOperation(op,expectedKey,host=null,review=null){
 const target=uninstallOperationTarget(op);
 if(!key(expectedKey)||!key(op?.key)||op.key!==expectedKey||!positive(op.revision)||!target||host!==null&&target!==host||typeof op.lookup_only!=='boolean'||op.can_prepare_fresh!==false)throw Error('The uninstall receipt does not match the exact requested key and host. Retain this key and inspect it.');
 if(op.review&&(op.review.schema!=='arm.node-uninstall/v1'||op.review.key!==expectedKey||op.review.stop?.plan?.target?.host!==target))throw Error('The uninstall review changed its exact operation or destination. No decision was sent.');
 // run() may expose the immutable plan while preparation is still active;
 // awaitReview then assigns its decision revision. Only that provisional
 // metadata may advance: the original key, host, plan and hash stay fixed.
 const provisionalAdvance=review?.provisional===true&&positive(review.revision)&&positive(op.review_revision)&&BigInt(op.review_revision)>BigInt(review.revision)&&BigInt(op.review_revision)<=BigInt(op.revision);
 if(review&&(!same(review.plan,op.review)||review.hash!==op.review_sha256||review.revision!==op.review_revision&&!provisionalAdvance))throw Error('The immutable uninstall review changed. Retain its exact key; no action was repeated.');
 return op;
}
export function supportedUninstallReview(p){
 const s=p?.stop?.plan,q=p?.installation,o=p?.ownership,t=s?.target,a=s?.actor,impact=s?.impact;
 return p?.schema==='arm.node-uninstall/v1'&&key(p.key)&&typeof p.reason==='string'&&p.reason.trim().length>0&&new TextEncoder().encode(p.reason).length<=4096&&
  hash(q?.key,64)&&hash(q.sha256,64)&&positive(q.size_bytes)&&BigInt(q.size_bytes)<=268435456n&&typeof q.install_root==='string'&&q.install_root.startsWith('/')&&q.expected_uid===t?.owner&&['darwin','linux'].includes(q.os)&&['arm64','amd64'].includes(q.arch)&&
  o?.schema===1&&same(o.transfer,q)&&nativeIdentity(o.root_identity)&&nativeIdentity(o.artifact_identity)&&nativeIdentity(o.binary_identity)&&!o.uninstall_key&&Array.isArray(o.referenced_homes)&&o.referenced_homes.length===1&&o.referenced_homes[0]===t?.home&&
  typeof p.retained_recovery_helper==='string'&&p.retained_recovery_helper.startsWith(`${q.install_root}/recovery-`)&&/^[a-f0-9]{64}\/arm$/.test(p.retained_recovery_helper.slice(`${q.install_root}/recovery-`.length))&&same(p.preserved,preserved)&&
  s?.schema==='arm.node-stop.plan/v1'&&/^uninstall-[a-f0-9]{64}$/.test(s.key??'')&&s.reason===p.reason&&hash(p.stop.review_sha256,64)&&hash(t?.host,32)&&hash(t.backend_generation,32)&&hash(t.certificate_sha256,64)&&hash(t.spki_sha256,64)&&/^[0-9]+$/.test(t.owner??'')&&t.home?.startsWith('/')&&
  hash(a?.source_host,32)&&hash(a.certificate_sha256,64)&&hash(a.spki_sha256,64)&&hash(a.pair,64)&&positive(a.grant_revision)&&positive(s.desired_state_revision)&&stopPolicyLabel(s.policy)!=='Unrecognized stop policy'&&
  impact?.complete===true&&hash(impact.scope_sha256,64)&&Array.isArray(impact.obligations)&&impact.obligations.length<=64&&impact.outstanding===impact.obligations.length&&Array.isArray(impact.blockers)&&impact.blockers.length===0&&Array.isArray(impact.affected_peers)&&impact.affected_peers.length<=64&&(s.policy.wait||s.policy.force||impact.outstanding===0)&&
  Number.isFinite(Date.parse(s.observed_at))&&Date.parse(s.expires_at)-Date.parse(s.observed_at)===120000;
}
export function uninstallConfirmed(op){
 const r=op?.result,c=r?.receipt;
 return op?.approved===true&&key(op.key)&&hash(op.review_sha256,64)&&supportedUninstallReview(op.review)&&uninstallOperationTarget(op)===op.review.stop.plan.target.host&&op.review.key===op.key&&c?.schema==='arm.node-uninstall/v1'&&c.phase==='removed'&&c.plan?.key===op.key&&c.review_sha256===op.review_sha256&&same(c.plan,op.review)&&nativeIdentity(c.recovery_identity)&&r.original_stop_observed===true&&r.artifact_absent===true;
}
export function uninstallLabel(op){
 if(uninstallConfirmed(op))return 'Managed ARM executable removed';
 if(op?.phase==='rejected'&&op.error?.code==='confirmation_declined')return 'Uninstall declined · kept installed';
 if(op?.phase==='awaiting_review'&&!op.approved)return 'Review managed uninstall';
 if(op?.phase==='preparing')return 'Preparing uninstall review';
 return 'Uninstall outcome needs inspection';
}
export function uninstallAdvice(op){
 if(uninstallConfirmed(op))return 'The exact managed executable was removed. Its data, identity, history, ownership records and recovery helper remain.';
 if(op.approved||op.lookup_only)return 'This uninstall may already have effects. Inspect this exact key; Stop, copying and removal are never automatically repeated.';
 return 'Review one original ARM-managed executable. Keeping it installed is the default. The host operating system and retained data are preserved.';
}
export function uninstallFinishAllowed(op){
 return op?.kind==='uninstall'&&op.approved===true&&key(op.key)&&hash(op.review_sha256,64)&&supportedUninstallReview(op.review)&&op.review.key===op.key&&uninstallOperationTarget(op)===op.review.stop.plan.target.host&&!op.worker_live&&!uninstallConfirmed(op)&&op.result?.original_stop_observed===true&&op.result.artifact_absent===false&&op.result.receipt?.schema==='arm.node-uninstall/v1'&&op.result.receipt.plan?.key===op.key&&op.result.receipt.review_sha256===op.review_sha256&&same(op.result.receipt.plan,op.review)&&nativeIdentity(op.result.receipt.recovery_identity)&&['accepted','stop-admitted','stop-unknown','remove-intent'].includes(op.result.receipt.phase);
}
export function uninstallReviewView(p,{document=globalThis.document}={}){
 const el=(tag,cls='',text=null)=>{const n=document.createElement(tag);n.className=cls;if(text!==null)n.textContent=String(text);return n;};
 const root=el('section','mesh-uninstall-review'),s=p?.stop?.plan,q=p?.installation;
 if(!s||!q){root.append(el('p','mesh-note warning','The exact installation review is unavailable. Approval is disabled.'));return root;}
 const heading=el('div','mesh-stop-heading');heading.append(el('span','mesh-stop-symbol','−'),el('div'));heading.children[1].append(el('h3','','Remove this managed ARM executable'),el('p','','Stop the reviewed ARM backend, then remove one owned executable. Keep all retained data.'));root.append(heading);
 const grid=el('div','mesh-stop-grid');
 for(const [title,rows]of [['EXACT INSTALLATION',[['Stable host',s.target?.host],['Run as',p.stop.owner_username?`${p.stop.owner_username} (UID ${s.target?.owner})`:`UID ${s.target?.owner}`],['ARM home',s.target?.home],['Managed executable',`${q.install_root}/artifact-${q.sha256}/arm`],['Platform',`${q.os} / ${q.arch}`],['Binary size',bytes(q.size_bytes)],['Original transfer',q.key]]],['SEPARATE AUTHORITY',[['Source host',s.actor?.source_host],['Enrollment',s.actor?.pair],['Grant revision',s.actor?.grant_revision],['Access required','Both node-stop and node-uninstall'],['Backend generation',s.target?.backend_generation],['Stop policy',stopPolicyLabel(s.policy)]]]]){
  const box=el('section','mesh-stop-card'),facts=el('dl','mesh-facts');box.append(el('h4','',title));for(const[label,value]of rows){const row=el('div','mesh-fact');row.append(el('dt','',label),el('dd','mesh-mono',value??'Not reported'));facts.append(row);}box.append(facts);grid.append(box);
 }
 root.append(grid);
 const keep=el('section','mesh-uninstall-preserved');keep.append(el('h4','','WHAT STAYS'),el('p','','Uninstall removes the reviewed executable only. It does not purge the ARM home or stop the host.'));const list=el('ul');for(const name of p.preserved??[])list.append(el('li','',name));keep.append(list);root.append(keep);
 const recovery=el('details','mesh-stop-completion');recovery.append(el('summary','','Retained recovery helper and executable identity'),el('code','mesh-stop-completion-detail',`${p.retained_recovery_helper}\nExecutable SHA-256: ${q.sha256}`));root.append(recovery);
 const work=el('section','mesh-stop-impact');work.append(el('h4','','WORK AND CONNECTIONS AFFECTED'));const impact=s.impact;work.append(el('p','mesh-stop-count',`${impact?.outstanding??'Unknown'} outstanding obligations · ${impact?.affected_peers?.length??0} affected peers`));
 for(const item of impact?.obligations??[]){const box=el('section','mesh-stop-card');box.append(el('strong','mesh-mono',item.id),el('p','',`${item.kind} · ${item.state} · revision ${item.revision}`));if(item.lease_details==='permitted'&&item.lease){const v=item.lease;box.append(el('p','',`${v.owner_username??'UID '+v.owner} · ${v.reason||'Purpose not shared in preview'}`));}work.append(box);}
 for(const b of impact?.blockers??[])work.append(el('p','mesh-note warning',b.detail));root.append(work);
 root.append(el('h4','','OPERATOR REASON'),el('p','mesh-stop-reason',p.reason),el('p','mesh-note',`Review expires ${Number.isFinite(Date.parse(s.expires_at))?new Date(s.expires_at).toLocaleString():'not reported'}. A changed review requires a new decision.`));
 if(!supportedUninstallReview(p))root.append(el('p','mesh-note warning','This installation, work census or removal policy cannot currently be approved. Keep it installed and inspect the exact receipt.'));
 root.append(jsonView(p,{label:'Full immutable installation, ownership and Stop review',document}));return root;
}
