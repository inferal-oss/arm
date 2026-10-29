import {stringifyJSON} from '../core.mjs';
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
export function displayCommand(command=[]){return command.map(value=>/^[a-zA-Z0-9_./:@%+=,-]+$/.test(value)?value:JSON.stringify(value)).join(' ')||'Command unavailable';}
export function exactRunRecord(run){return stringifyJSON(run,null,2);}
