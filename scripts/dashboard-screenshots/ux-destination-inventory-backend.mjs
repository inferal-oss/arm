// Strict synthetic destination read facade. These DTOs are observations, never grants.
import {parseJSON,stringifyJSON} from '../../internal/server/web/core.mjs';
import {validHostID} from '../../internal/server/web/mesh-model.mjs';
import {validCustomName} from '../../internal/server/web/resource-names.mjs';
const seed=window.__destinationSeed,stamp=new Date().toISOString(),wide=9007199254740993n;
const exact=(q,keys)=>q&&typeof q==='object'&&!Array.isArray(q)&&Object.keys(q).sort().join(',')===[...keys].sort().join(',');
const copy=value=>parseJSON(stringifyJSON(value));
const label=(kind,n)=>(kind+'-'+n+'-'+ 'longvalidlabel'.repeat(12)).slice(0,128);
const f=window.destinationInventoryFixture={bound:seed.context,stamp,mode:'dense',permission:'observe-own',inspectMode:'ready',mismatch:null,readState:'ready',pendingOps:[],pending:[],active:0,calls:[],forbidden:[],connections:[],completed:[],lastStatus:null,lastMetadata:null,lastLease:null,holdReconnect:false,serial:0};
const id=n=>(BigInt('0x'+seed.context.host)+BigInt(n)).toString(16).slice(-32).padStart(32,'0');
f.ids=Array.from({length:8},(_,i)=>id(i+1));f.foreign=id(99);f.hostLabel=label('Synthetic-destination',0);
const permissions=()=>['observe-host',...(f.permission==='observe-host'?[]:[f.permission])];
f.scope=()=>({source_host:f.bound.source_host,target_host:f.bound.host,pair:f.bound.pair,revision:f.bound.remote_revision,grant:{permissions:permissions(),limits:{cpu:16,memory_bytes:wide,disk_bytes:wide},max_ttl_ns:900000000000,contact_ids:[],operator_inbox:false},observed_at:stamp});
f.contextReport=()=>{const report={context:{...f.bound},access_scope:f.scope(),read_only:true,unavailable:['live-monitor','event-stream','contacts','messages','resource-administration','lease-mutations','mesh-administration']};
 if(f.mismatch){const[field,value]=f.mismatch;if(field==='scope.revision')report.access_scope.revision=value;else if(field==='scope.source_host')report.access_scope.source_host=value;else if(field==='read_only')report.read_only=value;else report.context[field]=value;}return report;};
const budget=(kind)=>{const unknown=f.mode==='unknown',stale=f.mode==='stale',aging=f.mode==='aging',base={observation_at:stamp,quality:'synthetic-recent',known:!unknown,stale,age_ns:aging?28000000000:0,stale_after_ns:30000000000};
 if(kind==='cpu')return{...base,eligible_cpus:16,observed_capacity_cores:16,busy_cores:8,observed_available_cores:8,capacity_cores:8,reserved_cores:8,managed_credit_cores:0,available_cores:0,ceiling_cores:16,deficit_cores:0};
 const {known,...memoryBase}=base;return{...(kind==='memory'?memoryBase:base),total_bytes:wide,...(kind==='memory'?{estimated_available_bytes:unknown?null:wide}:{reported_available_bytes:unknown?null:wide}),capacity_bytes:wide,reserved_bytes:wide,available_bytes:0,ceiling_bytes:wide,deficit_bytes:0};};
f.lease=(leaseID,index=f.ids.indexOf(leaseID))=>({id:leaseID,host:f.bound.host,display_name:label('Synthetic-lease',index),owner:'1000',owner_username:'synthetic-account',state:['active','released','queued','expired'][Math.max(0,index)%4],created_at:stamp,expires_at:'2099-01-01T00:00:00Z',request:{host:f.bound.host,reason:('Synthetic reservation reason '+index+' '+ 'bounded context accounting details '.repeat(index===0?32:3)).trim(),origin_peer:index===7?f.foreign:f.bound.source_host,origin_certificate:seed.sourceCertificate,origin_grant_revision:f.bound.remote_revision,origin_key:id(100+index),resources:{cpu:2,memory_bytes:wide,disk_bytes:1073741824},ttl_ns:900000000000,idempotency_key:id(200+index),queue:false}});
f.status=()=>{const native=['cpu','memory','disk'].map(kind=>({id:f.bound.host+'/'+kind,host:f.bound.host,kind,unit:kind==='cpu'?'logical-core-equivalents':'bytes',display_name:{cpu:'CPU',memory:'Memory',disk:'Disk'}[kind],budget_total:kind==='cpu'?8:wide,reserved:kind==='cpu'?8:wide,available:0,...(kind!=='memory'?{availability_known:f.mode!=='unknown',stale:f.mode==='stale',observation_at:stamp}:{}),deficit:0}));
 const others=Array.from({length:3},(_,i)=>({id:i===0?f.bound.host+'/ports/'+id(300)+'/tcp4/127.0.0.1:43210':'pool_'+id(301+i),host:f.bound.host,kind:i===0?'port':'custom',unit:i===0?'socket-binding':'permits',display_name:label('Synthetic-scoped-resource',i),budget_total:i===0?1:wide,reserved:i===0?1:wide,available:0}));
 const all=f.ids.map((v,i)=>f.lease(v,i)),leases=f.mode==='empty'?[]:f.permission==='observe-all'||f.mode==='foreign-inventory'?all:f.permission==='observe-own'?all.filter(l=>l.request.origin_peer===f.bound.source_host):[];
 return{host:f.mode==='wrong-status-host'?f.foreign:f.bound.host,display_name:f.hostLabel,capacity:{cpu:16,memory_bytes:wide,disk_bytes:wide},reserved:{cpu:8,memory_bytes:wide,disk_bytes:wide},leases,resources:f.mode==='empty'?[]:[...native,...others],capabilities:['reported-node-metadata','durable-events'],cpu:budget('cpu'),memory:budget('memory'),disk:budget('disk'),reported_at:stamp};};
f.metadata=()=>({schema_version:1,host:f.mode==='wrong-metadata-host'?f.foreign:f.bound.host,hostname:('synthetic-destination-'+ 'segment'.repeat(5))+'.example',platform:{os:'linux',kernel_release:'synthetic-kernel-'+ 'release'.repeat(18),process_architecture:'arm64'},build:{arm_version:'synthetic-development-'+ 'revision'.repeat(18),go_version:'synthetic-go-version'},inventory:{logical_cpus:16,host_memory:{total_bytes:wide,observed_at:stamp,known:true,stale:false,age_ns:0,stale_after_ns:30000000000},state_filesystem:{total_bytes:wide,observed_at:stamp,known:true,stale:false,age_ns:0,stale_after_ns:30000000000}},capabilities:['reported-node-metadata',...Array.from({length:18},(_,i)=>'synthetic-capability-'+i+'-'+ 'segment'.repeat(7))],reported_at:stamp,unavailable:[]});
for(const value of [f.bound.host,f.bound.source_host,...f.ids])if(!validHostID(value))throw Error('Invalid exact synthetic selector');
if(!validCustomName(f.hostLabel)||parseJSON(stringifyJSON({n:wide})).n!==wide)throw Error('Synthetic input or exact codec refused');
f.refresh=()=>{const c=f.connections.at(-1);if(!c||c.socket.readyState!==1)throw Error('Synthetic connection unavailable');c.options.onEvent({sequence:++f.serial,type:'host.metadata.changed',entity:f.bound.host,at:stamp,data:{detail:'Synthetic observation changed'}});};
f.release=()=>{f.readState='ready';f.pendingOps=[];for(const p of f.pending.splice(0))p.resolve();};
f.disconnect=()=>{f.holdReconnect=true;f.connections.at(-1).socket.close();};
f.reconnect=()=>{f.holdReconnect=false;f.resolveReconnect?.();f.resolveReconnect=null;};
f.exact=value=>stringifyJSON(value);
f.capture=async()=>{f.release();f.reconnect();await new Promise(r=>setTimeout(r,40));if(f.active||f.pending.length)throw Error('Synthetic reads still active');return{calls:f.calls,completed:f.completed,forbidden:f.forbidden,activeReads:f.active,pendingReads:f.pending.length,clipboardAttempts:window.__clipboardAttempts,connections:f.connections.length};};
export class Backend{
 constructor(token,options){if(token!==seed.token)throw Error('Unexpected synthetic bootstrap selector');this.options=options;this.socket={readyState:1,close:()=>{if(this.socket.readyState===3)return;this.socket.readyState=3;options.onClose({code:1006});}};this.ready=f.holdReconnect?new Promise(resolve=>f.resolveReconnect=resolve):Promise.resolve();f.connections.push(this);}
 disconnect(){this.socket.readyState=3;}
 async call(operation,params={}){const entry={operation,params:copy(params),mode:f.mode,permission:f.permission,context:copy(f.bound)};f.calls.push(entry);f.active++;
  try{let valid=['node_context','status','node_metadata'].includes(operation)&&exact(params,[]);if(operation==='inspect')valid=exact(params,['id'])&&validHostID(params.id)&&f.ids.includes(params.id);
   if(!valid){f.forbidden.push(entry);throw Error('Refused operation or unexpected exact read arguments: '+operation);}
   const inspectMode=f.inspectMode,requestValue=operation==='node_context'?f.contextReport():operation==='status'?f.status():operation==='node_metadata'?f.metadata():f.lease(params.id);
   if(operation==='inspect'&&(f.permission==='observe-host'||f.permission==='observe-own'&&requestValue.request.origin_peer!==f.bound.source_host))throw Object.assign(Error('Synthetic destination refuses another source under observe-own'),{code:'forbidden'});
   if(f.readState==='pending'&&f.pendingOps.includes(operation))await new Promise(resolve=>f.pending.push({operation,resolve}));
   if(f.readState==='error'&&f.pendingOps.includes(operation))throw Error('Synthetic destination read unavailable; no local view substituted');
   if(operation==='node_context')return requestValue;
   if(operation==='status'){f.lastStatus=copy(requestValue);return requestValue;}
   if(operation==='node_metadata'){f.lastMetadata=copy(requestValue);return requestValue;}
   if(inspectMode==='error')throw Error('Synthetic exact lease read unavailable');
   if(inspectMode==='wrong-id')requestValue.id=f.foreign;
   if(inspectMode==='wrong-origin')requestValue.request.origin_peer=f.foreign;
   f.lastLease=copy(requestValue);return requestValue;
  }finally{f.active--;f.completed.push({operation,mode:entry.mode,permission:entry.permission});}
 }
}
