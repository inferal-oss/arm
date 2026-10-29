import {leaseStateLabel} from '../lease-state.mjs';
import {bytes,count,stringifyJSON} from '../core.mjs';
const hostID=value=>typeof value==='string'&&/^[a-f0-9]{32}$/.test(value);
const nonempty=value=>typeof value==='string'&&value.trim()?value.trim():'';
// Cosmetic names are used only on records bound to the exact host ID. A remote
// selection or a matching machine name cannot establish local authority.
export function hostPresentation(host,{localHost='',hosts=[]}={}){
 const record=hostID(host)?hosts.find(row=>row?.host===host):null;
 const name=nonempty(record?.display_name)||nonempty(record?.name);
 const local=hostID(host)&&hostID(localHost)&&host===localHost;
 return {name:name&&name!=='localhost'?name:record?'Unnamed host':'Unknown host',local,label:(name&&name!=='localhost'?name:record?'Unnamed host':'Unknown host')+(local?' · Local':''),id:host||'Unavailable'};
}
export function detailTime(value,fallback='Not recorded',locale=undefined){
 const at=typeof value==='string'&&value?new Date(value):null;
 if(!at||!Number.isFinite(at.getTime())||at.getUTCFullYear()<=1)return fallback;
 return at.toLocaleString(locale,{year:'numeric',month:'short',day:'numeric',hour:'numeric',minute:'2-digit',second:'2-digit'});
}
export function displayCommand(command){return (Array.isArray(command)?command:[]).map(value=>/^[a-zA-Z0-9_./:@%+=,-]+$/.test(value)?value:JSON.stringify(value)).join(' ')||'Command unavailable';}
export function createLeaseSummary({ui,lease,owner,hostContext={},resultNote=''}){
 const typed=(h,name)=>{ui.put?.(h.iri,'http://www.w3.org/1999/02/22-rdf-syntax-ns#type','https://arm.local/ui#'+name,true);return h;},host=hostPresentation(lease.host,{...hostContext,hosts:[...(hostContext.hosts??[]),{host:lease.host,display_name:lease.host_display_name}]}),claims=lease.request?.resources??{},resources=[];
 if(claims.cpu)resources.push(count(claims.cpu)+' CPU');if(claims.memory_bytes)resources.push(bytes(claims.memory_bytes)+' memory');if(claims.disk_bytes)resources.push(bytes(claims.disk_bytes)+' disk');if(lease.request?.pools?.length)resources.push(lease.request.pools.length+' pool claims');if(lease.ports?.length)resources.push(lease.ports.length+' ports');
 const state=ui.badge(leaseStateLabel(lease));
 const header=ui.stack([ui.heading(lease.request?.reason||'Lease',3),ui.stack([state,...(host.local?[ui.badge('Local')]:[])],{row:true})]);
 const facts=ui.descriptions([['Resources',resources.join(' · ')||'No resource claims'],['Host',host.label],['Expires',detailTime(lease.expires_at,'No expiry recorded')],...(lease.exit_code!=null?[['Exit code',String(lease.exit_code)]]:[])]);
 const context=ui.disclosure('Owner & context',[ui.descriptions([['Owner',owner],['Agent',lease.request?.agent||'Unattributed'],['Project',lease.request?.project||'Not specified'],['Lease name',lease.display_name||'Unnamed lease'],['Host ID',host.id],['Lease ID',lease.id],['Revision',String(lease.revision)]])]);
 const summary=typed(ui.stack([header,facts,context,...(resultNote?[ui.text(resultNote,{role:'status'})]:[])]),'LeaseSummary');
 return summary;
}
export function exactRunRecord(run){return stringifyJSON(run);}
