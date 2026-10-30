// Synthetic historical reads only. Local preparation never authorizes a listener.
import {forwardSourceDraft} from '../../internal/server/web/mesh-forward-console.mjs';
const hex=bytes=>[...bytes].map(x=>x.toString(16).padStart(2,'0')).join('');
const freshID=()=>hex(crypto.getRandomValues(new Uint8Array(16))),now=new Date().toISOString(),maxInt64=9223372036854775807n;
const f=window.forwardInventoryFixture={source:freshID(),scene:'dense',readState:'ready',pendingOps:[],pending:[],active:0,calls:[],forbidden:[],connections:[],holdReconnect:false,cursor:9007199254740993n};
f.draft={workspace:freshID(),target:freshID(),service:'service_'+ 'a'.repeat(56),revision:'9007199254740991',bind:'[::1]:0',minutes:'15',reason:('Synthetic reservation reason '+ 'x'.repeat(90)+' ').repeat(40).slice(0,4096)};
const validated=forwardSourceDraft(f.draft);if(validated.reason.length!==4096||validated.grant_revision!==Number.MAX_SAFE_INTEGER)throw Error('Source fixture draft was not admitted by actual local validator');
f.rows=['active','ending','needs_attention','terminal'].map((state,i)=>({key:freshID(),lease:freshID(),state,...(state==='needs_attention'?{outcome:'unknown',failure:('Original listener ownership unavailable; port/resource charge retained; application delivery unknown. '+ 'synthetic-observation-'.repeat(80)).slice(0,1024),recovery_reason:f.draft.reason}:{}),workspace:f.draft.workspace,target:freshID(),service:f.draft.service,grant_revision:maxInt64,bind:i%2?'[::1]:65534':'127.0.0.1:65535',expires_at:new Date(Date.now()+600000).toISOString(),active_connections:state==='active'?3:0,original_owner:state==='active'||state==='ending'}));
f.keys=new Set(f.rows.map(r=>r.key));
f.release=()=>{f.readState='ready';f.pendingOps=[];for(const p of f.pending.splice(0))p.resolve();};
f.refresh=()=>f.connections.at(-1).options.onEvent({sequence:++f.cursor,type:'host.metadata.changed',entity:f.source,at:now,data:{detail:'Synthetic read refresh'}});
f.disconnect=()=>{f.holdReconnect=true;f.connections.at(-1).socket.close();};f.reconnect=()=>{f.holdReconnect=false;f.resolveReconnect?.();f.resolveReconnect=null;};
f.capture=async()=>{f.release();f.reconnect();await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));if(f.active||f.pending.length)throw Error('Synthetic reads remain active');return{calls:f.calls,forbidden:f.forbidden,activeReads:f.active,pendingReads:f.pending.length,clipboardAttempts:window.__clipboardAttempts};};
const exact=(q,keys)=>q!==null&&typeof q==='object'&&!Array.isArray(q)&&Object.keys(q).sort().join(',')===[...keys].sort().join(',');
const cursor=x=>typeof x==='bigint'?x>=0n&&x<=maxInt64:Number.isSafeInteger(x)&&x>=0;
export class Backend{
 constructor(token,options){this.options=options;this.socket={readyState:1,close:()=>{if(this.socket.readyState===3)return;this.socket.readyState=3;options.onClose({code:1006});}};this.ready=f.holdReconnect?new Promise(r=>f.resolveReconnect=r):Promise.resolve();f.connections.push(this);}
 disconnect(){this.socket.readyState=3;}
 async call(operation,params={}){f.calls.push({operation,params});f.active++;try{
  let valid=false;
  if(['status','messages','agents','node_list','mesh_operator_list','mesh_operator_watch','mesh_forward_list'].includes(operation))valid=exact(params,[]);
  if(operation==='pool_list')valid=exact(params,['all'])&&params.all===true;
  if(['subscribe','events'].includes(operation))valid=exact(params,['after'])&&cursor(params.after);
  if(operation==='monitor_subscribe')valid=exact(params,['epoch','after'])&&['','e'.repeat(32)].includes(params.epoch)&&cursor(params.after);
  if(operation==='mesh_forward_status')valid=exact(params,['key'])&&typeof params.key==='string'&&/^[a-f0-9]{32}$/.test(params.key)&&f.keys.has(params.key);
  if(!valid){f.forbidden.push({operation,params});throw Error('Refused operation or unexpected read arguments: '+operation);}
  if(f.readState==='pending'&&f.pendingOps.includes(operation))await new Promise(resolve=>f.pending.push({operation,params:structuredClone(params),resolve}));
  if(f.readState==='error'&&f.pendingOps.includes(operation))throw Error('Synthetic source inventory unavailable; existing charges and delivery remain unknown.');
  if(operation==='status')return{host:f.source,owner:'1000',owner_username:'synthetic',display_name:'synthetic-source',cursor:f.cursor,draining:false,capacity:{cpu:8,memory_bytes:34359738368,disk_bytes:549755813888},reserved:{cpu:0,memory_bytes:0,disk_bytes:0},leases:[],resources:[],capabilities:['durable-events'],infrastructure_ports:[],cpu_budget:null,memory_budget:null,disk_budget:null};
  if(operation==='node_list')return{source_host:f.source,mesh_state:'disabled',registry_at:now,nodes:[]};
  if(operation==='mesh_forward_list')return f.scene==='empty'?[]:structuredClone(f.rows);
  if(operation==='mesh_forward_status')return structuredClone(f.rows.find(r=>r.key===params.key));
  if(operation==='monitor_subscribe')return{host:f.source,epoch:'e'.repeat(32),cursor:0,oldest:0,gap:false,cadence_ns:2000000000,filesystem_cadence_ns:5000000000,stale_after_ns:10000000000,age_ns:0,stale:false,latest:null,history:[]};
  if(['subscribe','mesh_operator_watch'].includes(operation))return{subscription:'synthetic-read-only'};
  return[];
 }finally{f.active--;}}
}
