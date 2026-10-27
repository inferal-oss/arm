// Cosmetic name references preserve the exact identity and port incarnation.
const hostID=value=>typeof value==='string'&&/^[a-f0-9]{32}$/.test(value);
export const sameNameBinding=(a,b)=>Boolean(a&&b&&a.kind===b.kind&&a.authority_host===b.authority_host&&a.id===b.id&&a.binding_lease===b.binding_lease);
export const nameBindingKey=ref=>JSON.stringify([ref.kind,ref.authority_host,ref.id,ref.binding_lease]);
export const namedLabel=(record,fallback)=>typeof record?.display_name==='string'&&record.display_name.trim()?record.display_name:fallback;
export function nameReference(kind,record,authority) {
 if(!hostID(authority)||!record)return null;
 const ref={kind,authority_host:authority,id:kind==='host'?record.host:record.id,binding_lease:''};
 if(kind==='host')return hostID(ref.id)&&ref.id===authority?ref:null;
 if(kind==='lease')return hostID(ref.id)&&record.host===authority?ref:null;
 if(kind==='pool')return /^pool_[a-f0-9]{32}$/.test(ref.id)&&(!record.authority||record.authority===authority)?ref:null;
 if(kind!=='resource'||typeof ref.id!=='string'||record.host&&record.host!==authority)return null;
 if(record.kind==='custom')return nameReference('pool',{id:ref.id,authority:record.custom?.pool?.authority},authority);
 if(['cpu','memory','disk'].includes(record.kind))return ref.id===authority+'/'+record.kind?ref:null;
 if(record.kind!=='port'||!ref.id.startsWith(authority+'/ports/'))return null;
 const owners=new Set((record.users??[]).filter(use=>use.relationship==='reservation').map(use=>use.lease));
 if(owners.size!==1||!hostID([...owners][0]))return null;
 ref.binding_lease=[...owners][0];return ref;
}
export function loadedNameTargets({status,nodes=[],pools=[]}={}) {
 const result=new Map(),add=(ref,label)=>{if(ref)result.set(nameBindingKey(ref),{...ref,label:typeof label==='string'?label:ref.id});};
 if(hostID(status?.host)) {
  add(nameReference('host',status,status.host),namedLabel(status,'Local host'));
  for(const lease of status.leases??[])add(nameReference('lease',lease,status.host),namedLabel(lease,lease.id));
  for(const resource of status.resources??[]){const ref=nameReference('resource',resource,status.host);if(resource.kind==='port'){const duplicates=(status.resources??[]).filter(row=>row.id===resource.id);if(!ref||duplicates.some(row=>!sameNameBinding(nameReference('resource',row,status.host),ref)))continue;}add(ref,namedLabel(resource,resource.custom?.pool?.name||resource.kind));}
 }
 for(const node of nodes)add(nameReference('host',node,node.host),namedLabel(node,node.name||node.host));
 for(const view of pools){const pool=view.pool??view;add(nameReference('pool',pool,pool.authority),namedLabel(view,pool.name||pool.id));}
 return [...result.values()];
}
export function validCustomName(value) {
 return typeof value==='string'&&/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(value)&&value.toLowerCase()!=='localhost'&&!/^(?:pool_)?[a-f0-9]{32}$/i.test(value);
}
export function validNameSnapshot(snapshot,source) {
 const revision=snapshot?.revision,validRevision=typeof revision==='bigint'?revision>=0n&&revision<=9223372036854775807n:Number.isSafeInteger(revision)&&revision>=0;
 if(snapshot?.version!==1||snapshot.host!==source||!hostID(source)||!validRevision||!Array.isArray(snapshot.entries)||snapshot.entries.length>1024||snapshot.naming_diagnostic||typeof snapshot.owner!=='string'||! /^(?:0|[1-9][0-9]{0,9})$/.test(snapshot.owner))return false;
 const names=new Set(),active=new Set();
 for(const entry of snapshot.entries){
  if(!entry||Object.keys(entry).sort().join(',')!=='active,authority_host,binding_lease,id,kind,name'||!hostID(entry.authority_host)||typeof entry.active!=='boolean'||!validCustomName(entry.name))return false;
  const authority=entry.authority_host,id=entry.id;
  if(entry.kind==='host'){if(id!==authority||entry.binding_lease!=='')return false;}
  else if(entry.kind==='lease'){if(!hostID(id)||entry.binding_lease!=='')return false;}
  else if(entry.kind==='pool'){if(!/^pool_[a-f0-9]{32}$/.test(id)||entry.binding_lease!=='')return false;}
  else if(entry.kind==='resource'){
   if(typeof id!=='string'||id.length>256)return false;
   if(['cpu','memory','disk'].some(kind=>id===authority+'/'+kind)){if(entry.binding_lease!=='')return false;}
   else if(!id.startsWith(authority+'/ports/')||!hostID(entry.binding_lease))return false;
  }else return false;
  const key=entry.kind+'\0'+(entry.kind==='host'?'':authority)+'\0'+entry.name.toLowerCase(),binding=nameBindingKey(entry);
  if(names.has(key)||entry.active&&active.has(binding))return false;names.add(key);if(entry.active)active.add(binding);
 }
 return !(BigInt(revision)===0n&&snapshot.entries.length);
}
export function fixedNamingDiagnostic(diagnostic) {
 if(!diagnostic)return null;
 return diagnostic.code==='name_metadata_invalid'?'Custom name metadata is invalid. Exact IDs remain available; inspect the database without repairing or dropping evidence.':'Custom names are unavailable. Exact IDs remain available; read a fresh naming snapshot.';
}
