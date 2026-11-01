// A display label never proves a current registration owns a historical run.
export function agentIdentityKey(identity) {
  return JSON.stringify([identity?.host??'',identity?.id??'',identity?.session??'',identity?.registration??'']);
}
export function sameAgentIdentity(a,b) {
  return Boolean(a?.host&&a?.id&&a?.session&&a?.registration)&&agentIdentityKey(a)===agentIdentityKey(b);
}
export function agentResources(identity,{leases=[],runs=[]}={}) {
  const sameHost=record=>record.host===identity.host;
  return {
    leases:leases.filter(l=>sameHost(l)&&l.request?.agent===identity.id).map(record=>({record,association:'legacy'})),
    runs:runs.filter(r=>sameHost(r)&&r.agent===identity.id).map(record=>({record,association:'legacy'})),
  };
}
export function participationLabel(request,now=Date.now()) {
  if(!request)return 'No contribution requested';
  if(request.state==='awaiting_reply'&&Date.parse(request.reply_deadline)<=now)return 'Reply window ended · activity unknown';
  return ({pending_delivery:'Delivery pending',awaiting_reply:'Awaiting agent contribution',contributed:'Agent contributed · ready to summarize',late_reply:'Late agent contribution',ready:'Summary available',generating:'Summarizer running',failed:'Attempt failed',cancelled:'Attempt cancelled',superseded:'Registration replaced',timed_out:'Reply window ended · activity unknown',unknown:'Outcome uncertain'})[request.state]??'State unavailable';
}
export function contributionFreshness(request,now=Date.now()) {
  const at=Date.parse(request?.generated_at??'');
  return Number.isFinite(at)?{stale:now-at>15*60*1000,at:new Date(at).toLocaleString()}:{stale:true,at:'No generated report'};
}
