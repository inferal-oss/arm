// Synthetic published Event/Status/PortBinding shapes; no transport or discovery.
import {stringifyJSON} from '../../internal/server/web/core.mjs';
const f=window.activityHostFixture={calls:[],forbidden:[],connections:[],state:'fresh',holdReconnect:false};
const base=9007199254740993n,host='c'.repeat(32),lease='d'.repeat(32),stamp='2026-10-29T12:00:00.000Z';
const variant=new URL(location.href).searchParams.get('fixture')||'dense';
const known=['live-cpu-admission','live-memory-admission','live-disk-admission','custom-resource-pools','counted-resource-subpools','local-owner-policy-budgets','cooperative-reservation','process-group-supervision','local-lease-reviews-v1','observed-usage-policy','draining-shutdown','owned-gradle-services','automatic-mesh-lifecycle','sender-aware-agent-messaging','workspace-published-agents-poll-v1','port-reservation','native-port-inventory','socket-activation','durable-events','host-monitor-stream','reported-node-metadata','database-inspection-backup','browser-reviewed-lease-controls'];
f.host=host;f.cursor=base-1n;f.events=[];f.revision=0;
f.event=(sequence=base)=>({sequence,type:'lease.released',entity:f.eventEntity??lease,at:stamp,data:{reason:'Synthetic operation first paragraph.\n\nSecond paragraph. '+ 'longreason'.repeat(40)+' END-REASON',exact_count:9223372036854775807n,detail:variant==='truncated'?'payload'.repeat(37000)+' END-PAYLOAD':f.contextLabel??'Synthetic event detail'}});
f.status=()=>({host:f.host,display_name:f.state==='fresh'?(f.displayName??'synthetic-host-'+ 'node'.repeat(24)):'',owner:'1000',...(f.state==='fresh'?{owner_username:'synthetic_'+ 'account'.repeat(24)}:{}),draining:f.state==='draining',cursor:f.cursor,
 capacity:{cpu:8,memory_bytes:34359738368,disk_bytes:549755813888},reserved:{cpu:0,memory_bytes:0,disk_bytes:0},leases:[],resources:[],cpu_budget:null,memory_budget:null,disk_budget:null,
 capabilities:f.state==='unknown'?null:f.state==='empty'?[]:[...known,...Array.from({length:3},(_,i)=>'future-extension-'+i+'-'+ 'capability'.repeat(16))],
 infrastructure_ports:f.state==='unknown'?null:f.state==='empty'?[]:Array.from({length:8},(_,i)=>({name:'synthetic-port-'+i,network:'tcp4',address:'192.0.2.'+(i+1),port:42000+i,mode:'hold',namespace:'synthetic',state:'infrastructure',detail:variant==='truncated'?'metadata'.repeat(33000)+' END-INFRASTRUCTURE':'Synthetic port description.\n\n'+ 'portdetail'.repeat(24)}))});
f.exact=event=>stringifyJSON(event);
f.append=()=>{const e=f.event(++f.cursor);f.events.push(e);f.events=f.events.slice(-256);f.connections.at(-1).options.onEvent(structuredClone(e));return String(e.sequence);};
f.repeat=sequence=>{const event=f.events.find(e=>String(e.sequence)===String(sequence));if(!event)throw Error('Synthetic event not found');f.connections.at(-1).options.onEvent(structuredClone(event));};
f.refresh=state=>{f.state=state;f.revision++;const e={sequence:++f.cursor,type:'host.metadata.changed',entity:f.host,at:stamp,data:{detail:'Synthetic status refresh '+f.revision}};f.events.push(e);f.connections.at(-1).options.onEvent(e);};
f.disconnect=()=>{f.holdReconnect=true;f.connections.at(-1).socket.close();};
f.switchSource=()=>{if(!f.holdReconnect)throw Error('Synthetic source switch requires held reconnect');f.host='f'.repeat(32);f.eventEntity='e'.repeat(32);f.contextLabel='Synthetic replacement source';f.events=f.events.map(e=>f.event(e.sequence));};
f.releaseReconnect=()=>{f.holdReconnect=false;f.resolveReconnect?.();f.resolveReconnect=null;};
export class Backend{
 constructor(token,options){this.options=options;this.socket={readyState:1,close:()=>{if(this.socket.readyState===3)return;this.socket.readyState=3;options.onClose({code:1006});}};this.ready=f.holdReconnect?new Promise(r=>f.resolveReconnect=r):Promise.resolve();f.connections.push(this);}
 disconnect(){this.socket.readyState=3;}
 async call(operation,params={}){
  f.calls.push({operation,params});
  if(operation==='status')return structuredClone(f.status());
  if(operation==='events'){
   // Journal entries arrive between the initial status snapshot and catch-up.
   // The manager's published read limit is256; the UI retains its latest200.
   if(!f.initialized){f.initialized=true;f.events=variant==='empty'?[]:Array.from({length:variant==='truncated'?1:200},(_,i)=>f.event(base+BigInt(i)));f.cursor=f.events.at(-1)?.sequence??base-1n;}
   return structuredClone(f.events.filter(e=>e.sequence>BigInt(params.after??0)).slice(0,256));
  }
  if(operation==='monitor_subscribe')return{host:f.host,epoch:'e'.repeat(32),cursor:0,oldest:0,gap:false,cadence_ns:2000000000,filesystem_cadence_ns:5000000000,stale_after_ns:10000000000,age_ns:0,stale:false,latest:null,history:[]};
  if(['subscribe','mesh_operator_watch'].includes(operation))return{subscription:'synthetic-read-only'};
  if(operation==='node_list')return{nodes:[]};
  if(['messages','agents','pool_list'].includes(operation))return[];
  f.forbidden.push(operation);throw Error('Refused Activity/Host inventory operation: '+operation);
 }
}
