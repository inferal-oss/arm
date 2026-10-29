// Synthetic published status, node registry, pool view and custom-name documents.
// Read allowlist only; no socket, discovery, mutation or real backend.
import {validCustomName,validNameSnapshot} from '../../internal/server/web/resource-names.mjs';
const host='c'.repeat(32),stamp='2026-10-29T12:00:00.000Z',revision=9223372036854775806n;
const id=n=>(9007199254740993n+BigInt(n)).toString(16).padStart(32,'0');
const label=(kind,n)=>`${kind}-${n}-`+'longvalidlabel'.repeat(10).slice(0,128-`${kind}-${n}-`.length);
const f=window.sharedDialogFixture={host,revision,cursor:9007199254740993n,mode:new URL(location.href).searchParams.get('fixture')||'dense',readState:'ready',calls:[],forbidden:[],connections:[],pending:[],holdReconnect:false};
const ref=(kind,authority_host,id,name,active=true,binding_lease='')=>({kind,authority_host,id,binding_lease,name,active});
f.data=()=>{
 const empty=f.mode==='empty',leases=empty?[]:Array.from({length:24},(_,i)=>({id:id(i),host,owner:'1000',owner_username:'synthetic',display_name:label('Lease',i),host_display_name:label('Host',0),state:'released',request:{host,reason:'Synthetic reason '+i+' '+ 'reason'.repeat(20),agent:'synthetic-agent',resources:{cpu:1,memory_bytes:0,disk_bytes:0},ttl_ns:60000000000,idempotency_key:'synthetic-'+i,queue:false},created_at:stamp,started_at:null,expires_at:null,ended_at:stamp,revision:1,capabilities:[]}));
 const resources=empty?[]:['cpu','memory','disk'].map(kind=>({id:host+'/'+kind,host,pool:kind,kind,unit:kind==='cpu'?'cores':'bytes',budget_total:8,reserved:0,available:8,accounting:'cooperative',observed:null,users:[],display_name:label('Resource-'+kind,0),host_display_name:label('Host',0)}));
 const nodes=empty?[]:Array.from({length:12},(_,i)=>({host:id(100+i),name:label('Node',i),display_name:label('Node',i),local:false,trust:{execution_account:{uid:'1000',username:'synthetic',source:'paired-identity-snapshot'},state:'active',address:'192.0.2.'+(i+1)+':443',pair:'synthetic-pair-'+i,revision:1,certificate_fingerprint:'a'.repeat(64),spki_sha256:'b'.repeat(64),expires_at:'2099-01-01T00:00:00Z',inbound_grant:{permissions:[],limits:{cpu:0,memory_bytes:0,disk_bytes:0},max_ttl_ns:60000000000,contact_ids:[],operator_inbox:false}},metadata:null,outbound_scope:null,metadata_freshness:{known:false,stale:false,received_at:null,age_ns:0,stale_after_ns:10000000000},scope_freshness:{known:false,stale:false,received_at:null,age_ns:0,stale_after_ns:10000000000},health:'unobserved'}));
 const pools=empty?[]:Array.from({length:24},(_,i)=>({pool:{id:'pool_'+id(200+i),name:label('Pool',i),scope:'host',host,authority:host,owner:'1000',description:'Synthetic pool description '+ 'description'.repeat(20),unit:'permits',capacity:9007199254740993n,revision:revision-BigInt(i),state:'active',accounting:'cooperative',expiry_policy:'lease',created_at:stamp,updated_at:stamp},review:null,reserved:0,available:9007199254740993n,deficit:0,users:[],queued:[],display_name:label('Pool',i)}));
 const messages=empty?[]:Array.from({length:24},(_,i)=>({id:id(300+i),sender:'synthetic-sender',audience:'operator',body:'Synthetic message '+i+' '+ 'body'.repeat(60),created_at:stamp,deliveries:{}}));
 const entries=empty?[]:[ref('host',host,host,label('Host',0)),...leases.map((l,i)=>ref('lease',host,l.id,label('Lease',i))),...resources.map((r,i)=>ref('resource',host,r.id,label('Resource-'+r.kind,0))),...nodes.map((n,i)=>ref('host',n.host,n.host,label('Node',i))),...pools.map((p,i)=>ref('pool',host,p.pool.id,label('Pool',i))),...Array.from({length:12},(_,i)=>ref('lease',host,id(400+i),label('Retained',i),false))];
 const names={version:1,owner:'1000',host,revision:f.nameRevision??revision,entries};
 if(!validNameSnapshot(names,host)||!entries.every(e=>validCustomName(e.name)))throw Error('Synthetic naming contract invalid');
 return{leases,resources,nodes,pools,messages,names};
};
f.refresh=()=>f.connections.at(-1).options.onEvent({sequence:++f.cursor,type:'host.metadata.changed',entity:host,at:stamp,data:{detail:'Synthetic read refresh'}});
f.release=()=>{f.readState='ready';for(const p of f.pending.splice(0))p.resolve();};
f.disconnect=()=>{f.holdReconnect=true;f.connections.at(-1).socket.close();};
f.reconnect=()=>{f.holdReconnect=false;f.resolveReconnect?.();f.resolveReconnect=null;};
export class Backend{
 constructor(token,options){this.options=options;this.socket={readyState:1,close:()=>{if(this.socket.readyState===3)return;this.socket.readyState=3;options.onClose({code:1006});}};this.ready=f.holdReconnect?new Promise(r=>f.resolveReconnect=r):Promise.resolve();f.connections.push(this);}
 disconnect(){this.socket.readyState=3;}
 async call(operation,params={}){
  f.calls.push({operation,params});
  const read=['status','events','messages','agents','node_list','pool_list','name_list','monitor_subscribe','subscribe','mesh_operator_watch'];
  if(!read.includes(operation)){f.forbidden.push(operation);throw Error('Refused shared-dialog inventory operation: '+operation);}
  const expected=['pool_list','name_list'].includes(operation)?['all']:['subscribe','events'].includes(operation)?['after']:operation==='monitor_subscribe'?['after','epoch']:[];
  let valid=params&&typeof params==='object'&&!Array.isArray(params)&&Object.keys(params).sort().join(',')===expected.sort().join(',');
  if('all'in params)valid&&=params.all===true;
  if('after'in params){try{valid&&=BigInt(params.after)>=0n&&BigInt(params.after)<=9223372036854775807n;}catch{valid=false;}}
  if('epoch'in params)valid&&=typeof params.epoch==='string'&&params.epoch.length<=64;
  if(!valid){f.forbidden.push(operation+':unexpected-read-arguments');throw Error('Refused unbounded synthetic read arguments');}

  if(['node_list','pool_list','name_list'].includes(operation)){
   if(f.readState==='pending')await new Promise(resolve=>f.pending.push({operation,resolve}));
   if(f.readState==='error'){
    if(operation==='name_list')return{version:1,owner:'1000',host,revision:0,entries:[],naming_diagnostic:{code:'name_metadata_unavailable',message:'Custom names are unavailable; exact IDs remain available.'}};
    throw Object.assign(Error('Synthetic inventory read unavailable'),{code:'inventory_unavailable'});
   }
  }
  const data=f.data();
  if(operation==='status')return{host,owner:'1000',owner_username:'synthetic',display_name:label('Host',0),draining:false,capacity:{cpu:8,memory_bytes:34359738368,disk_bytes:549755813888},reserved:{cpu:0,memory_bytes:0,disk_bytes:0},leases:data.leases,cursor:f.cursor,capabilities:['durable-events','custom-resource-pools'],resources:data.resources,infrastructure_ports:[],cpu_budget:null,memory_budget:null,disk_budget:null};
  if(operation==='node_list')return{source_host:host,mesh_state:'active',nodes:data.nodes,registry_at:stamp};
  if(operation==='pool_list')return data.pools;
  if(operation==='name_list')return structuredClone(data.names);
  if(operation==='messages')return data.messages;
  if(operation==='agents'||operation==='events')return[];
  if(operation==='monitor_subscribe')return{host,epoch:'e'.repeat(32),cursor:0,oldest:0,gap:false,cadence_ns:2000000000,filesystem_cadence_ns:5000000000,stale_after_ns:10000000000,age_ns:0,stale:false,latest:null,history:[]};
  return{subscription:'synthetic-read-only'};
 }
}
