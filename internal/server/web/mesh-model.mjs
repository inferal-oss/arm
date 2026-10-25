import {supportedStopReview,stopAdvice,stopResultLabel,stopOperationTarget} from './mesh-stop.mjs';
// Public UI descriptions. These never authorize a request; the destination
// enforces current grants and the coordinator binds its immutable review.
export const meshPreparationNotice = 'Preparation may start ordinary ARM backends and restore previously approved listeners. New endpoints and trust wait for your confirmation. Cancelling keeps ordinary backends, identities and existing connections.';
export const meshPermissions = Object.freeze({
  'observe-host':'Host summary', 'observe-own':'Our leases', 'observe-all':'All leases',
  'lease-own':'Create and manage our leases', run:'Run programs', 'node-stop':'Stop this ARM backend',
  message:'Send messages', 'contact-read':'Read allowed agent contacts',
});
export const validHostID = value => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
export function phaseLabel(op) {
  if(op.kind==='stop'){if(op.result)return stopOperationTarget(op)?stopResultLabel(op.result,op.key,op.review??{}):'Stop outcome unknown';if(op.lookup_only||op.approved)return op.has_result?'Stop result recorded':'Stop decision recorded';if(op.can_prepare_fresh)return 'Stop not applied';if(op.phase==='awaiting_review')return 'Waiting for stop review';if(op.phase==='preparing')return 'Preparing stop review';return 'Recorded stop operation';}
  if (op.resolved_by) return 'Resolved by recovery';
  if (op.cancel_requested && op.worker_live) return 'Cancellation requested';
  if (op.phase === 'rejected' && op.error?.code === 'confirmation_declined') return 'Declined';
  return ({preparing:'Preparing review',awaiting_review:'Waiting for your review',review_approved:'Review approved',review_declined:'Declining review',applying:'Applying reviewed change',completed:'Completed',incomplete:'Needs attention',rejected:'Not applied',declined:'Declined',cancelled:'Cancelled',handed_off:'Handed off · inspect independently'})[op.phase] ?? op.phase ?? 'Unknown';
}
export function operationAdvice(op) {
  if(op.kind==='stop')return stopAdvice(op);
  if (op.resolved_by) return `Recovery ${op.resolved_by} recorded the outcome. The original receipt is preserved.`;
  if (op.phase === 'awaiting_review') return op.kind === 'disconnect' ? 'Review the exact enrollment to revoke. Existing leases and their cleanup obligations remain.' : 'Nothing new is trusted yet. Check the exact identities, endpoints and permissions before deciding.';
  if (op.phase === 'completed' && !['connect','reconnect','disconnect'].includes(op.kind)) return 'This operation recorded a result. Inspect its receipt using the CLI; this dashboard does not manage this operation type.';
  if (op.phase === 'completed') return op.kind === 'disconnect' ? 'This enrollment is disconnected. Existing leases and cleanup obligations keep their lifetimes.' : 'The reviewed connection is established. Permissions remain directional and destination-enforced.';
  if (op.phase === 'incomplete' || (!op.worker_live && !['completed','rejected','declined','cancelled','handed_off'].includes(op.phase))) return 'The outcome may be partial or unknown. Inspect this exact receipt. Reconnecting or closing this dialog never retries it.';
  if (op.phase === 'handed_off') return 'Workflow exclusion was released. This does not prove cleanup, revoke trust, or release leases.';
  return 'Closing this dialog keeps the recorded operation discoverable. No action is replayed on reconnect.';
}
export function emptyGrant() { return {permissions:[],limits:{cpu:0,memory_bytes:0,disk_bytes:0},max_ttl_ns:0,contact_ids:[],operator_inbox:false}; }
export function grantFromDraft(draft) {
  const grant = emptyGrant();
  if (draft.mode === 'none') return grant;
  grant.permissions = ['observe-host'];
  if (draft.mode === 'leases' || draft.mode === 'run') {
    grant.permissions.push('observe-own','lease-own');
    if (draft.mode === 'run') grant.permissions.push('run');
    const cpu = Number(draft.cpu), memory = Number(draft.memory), disk = Number(draft.disk), ttl = Number(draft.ttl);
    if (![cpu,memory,disk,ttl].every(Number.isFinite) || cpu < 0 || cpu > 65536 || memory < 0 || memory > 1048576 || disk < 0 || disk > 1048576 || ttl <= 0 || ttl > 10080 || cpu + memory + disk <= 0) throw Error('Choose positive resource ceilings and a lease duration of at most 7 days.');
    grant.limits = {cpu,memory_bytes:Math.round(memory * 1073741824),disk_bytes:Math.round(disk * 1073741824)};
    grant.max_ttl_ns = Math.round(ttl * 60e9);
  }
  if (draft.messages) {
    grant.permissions.push('message'); grant.operator_inbox = Boolean(draft.operatorInbox);
    grant.contact_ids = [...new Set(String(draft.contacts || '').split(/[\s,]+/).filter(Boolean))];
    if (grant.contact_ids.length > 64 || grant.contact_ids.some(id => id.length > 128)) throw Error('Use at most 64 exact contact IDs, each at most 128 characters.');
    if (!grant.operator_inbox && !grant.contact_ids.length) throw Error('Select the operator inbox or name exact allowed contacts for messaging.');
    if (grant.contact_ids.length) grant.permissions.push('contact-read');
  }
  return grant;
}
export function grantDescription(grant) {
  const permissions = grant?.permissions ?? [];
  return permissions.length ? permissions.map(p => meshPermissions[p] ?? p).join(' · ') : 'No access';
}
export function reviewIdentities(review) {
  if (!review) return [];
  // Connect previews and disconnect's actual mesh.Identity use distinct DTOs.
  const normalize = identity => identity && ({...identity,owner_uid:identity.owner_uid??identity.owner,certificate_sha256:identity.certificate_sha256??identity.fingerprint,public_key_sha256:identity.public_key_sha256??identity.spki_sha256,certificate_expires:identity.certificate_expires??identity.expires_at});
  const identity = (side) => normalize(review[side + '_prepared']?.identity ?? review[side]?.identity);
  if (review.operation === 'disconnect-direct-peer') return [{label:'Disconnect this enrollment',identity:normalize(review.host)}].filter(x=>x.identity);
  return [{label:'This node',identity:identity('local'),listen:review.local_prepared?.listen??review.local_listen},{label:'Destination node',identity:identity('remote'),listen:review.remote_prepared?.listen??review.remote_listen}].filter(x => x.identity);
}
export function supportedReview(op){
 if(op?.kind==='stop')return stopOperationTarget(op)===op.review?.plan?.target?.host&&op.review?.key===op.key&&op.review?.plan?.key===op.key&&supportedStopReview(op.review);
 if(op?.kind==='reconnect'){
  if(op.review?.operation==='verify-existing-connection')return true;
  const r=op.review?.request?.reconnect,p=r?.binding?.previous_enrollment;
  return op.review?.operation==='reconnect-direct-peers'&&validHostID(r?.draft?.host)&&p?.identity?.host===r.draft.host&&p.state==='revoked'&&/^[a-f0-9]{64}$/.test(p.pair??'')&&Number.isInteger(p.revision)&&p.revision>0;
 }
 return (op?.kind==='connect'&&['connect-direct-peers','restore-existing-connection','verify-existing-connection'].includes(op.review?.operation))||(op?.kind==='disconnect'&&op.review?.operation==='disconnect-direct-peer');
}
export function canReview(op, now = Date.now()) {
  if(op?.kind==='stop'&&(op.lookup_only===true||op.approved===true||now<Date.parse(op.review?.plan?.observed_at)||now>=Date.parse(op.review?.plan?.expires_at)))return false;
  return supportedReview(op) && op?.phase === 'awaiting_review' && op.worker_live && Boolean(op.review) && Number(op.review_revision) > 0 && /^[a-f0-9]{64}$/.test(op.review_sha256 ?? '') && now < Date.parse(op.review_expires_at);
}

export function validateMeshReason(reason){if(typeof reason!=='string'||!reason.trim()||new TextEncoder().encode(reason).length>4096)throw Error('A meaningful reason of at most 4096 UTF-8 bytes is required.');return reason;}
