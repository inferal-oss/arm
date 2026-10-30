// Published read DTOs only. Synthetic names/endpoints/observations are not authority.
import {stringifyJSON} from '../../internal/server/web/core.mjs';
import {validHostID} from '../../internal/server/web/mesh-model.mjs';
import {validCustomName} from '../../internal/server/web/resource-names.mjs';
import {availabilityFacts} from '../../internal/server/web/semblance/node-availability.mjs';
import {formatKubeQuantity} from '../../internal/server/web/semblance/kube-quantity.mjs';
const source='c'.repeat(32),stamp=new Date().toISOString(),oldStamp=new Date(Date.now()-120000).toISOString(),int64=9223372036854775807n;
const id=n=>(9007199254740993n+BigInt(n)).toString(16).padStart(32,'0'),digest=n=>BigInt(n+1).toString(16).padStart(64,'0'),uid=n=>'00000000-0000-4000-8000-'+n.toString(16).padStart(12,'0');
const custom=(kind,n)=>(kind+'-'+n+'-'+ 'longvalidlabel'.repeat(12)).slice(0,128),dns=(kind,n)=>(kind+'-'+n+'-'+ 'longvalidlabel'.repeat(6)).slice(0,63).replace(/-$/,'x');
const f=window.nodeInventoryFixture={source,stamp,mode:'dense',kubeMode:'dense',readState:'ready',pendingOps:[],calls:[],forbidden:[],connections:[],pending:[],active:0,idle:[],holdReconnect:false,cursor:9007199254740993n};
f.exact=value=>stringifyJSON(value);
f.hosts=[source,...Array.from({length:7},(_,i)=>id(100+i))];f.target=f.hosts[1];
const grant=()=>({permissions:['observe-host','observe-own','lease-own','run','message','contact-read'],limits:{cpu:8,memory_bytes:68719476736,disk_bytes:549755813888},max_ttl_ns:600000000000,contact_ids:Array.from({length:12},(_,i)=>'synthetic-contact-'+i),operator_inbox:true});
const freshness=(stale=false,known=true)=>({known,stale,received_at:known?(stale?oldStamp:stamp):null,age_ns:stale?120000000000:0,stale_after_ns:30000000000});
f.node=host=>{
 if(!f.hosts.includes(host))throw Error('Unknown synthetic exact host');const n=f.hosts.indexOf(host),local=host===source,missing=['unverified','unavailable'].includes(f.mode),stale=f.mode==='stale'||f.mode==='unavailable';
 const metadata=missing?null:{schema_version:1,host,hostname:dns('reported-host',n)+'.'+dns('domain',n)+'.example',platform:{os:'linux',kernel_release:'synthetic-kernel-'+ 'release'.repeat(12),process_architecture:n%2?'arm64':'amd64'},build:{arm_version:'synthetic-development-'+ 'revision'.repeat(12),go_version:'synthetic-go-version'},inventory:{logical_cpus:64,host_memory:{total_bytes:9007199254740993n,observed_at:stale?oldStamp:stamp,known:true,stale,age_ns:stale?120000000000:0,stale_after_ns:30000000000},state_filesystem:{total_bytes:549755813888,observed_at:stale?oldStamp:stamp,known:true,stale,age_ns:stale?120000000000:0,stale_after_ns:30000000000}},capabilities:['node-availability-v1','host-monitor-stream','durable-events','synthetic-future-'+ 'capability'.repeat(16)],reported_at:stale?oldStamp:stamp,unavailable:[]};
 const entry={host,name:dns('synthetic-node',n),display_name:custom('Synthetic-node',n),local,trust:{state:local?'local':f.mode==='unverified'?'pending':'active',execution_account:{uid:'1000',username:'synthetic_'+ 'account'.repeat(20),source:local?'local-identity':'paired-identity-snapshot'},address:dns('endpoint',n)+'.'+dns('zone',n)+'.example:42224',pair:digest(n),revision:42,certificate_fingerprint:digest(20+n),spki_sha256:digest(40+n),expires_at:'2099-01-01T00:00:00Z',inbound_grant:local?null:grant()},metadata,outbound_scope:local||missing?null:{source_host:source,target_host:host,pair:digest(n),revision:42,grant:grant(),observed_at:stale?oldStamp:stamp},metadata_freshness:freshness(stale,!missing),scope_freshness:freshness(stale,!missing),health:local?'local':f.mode==='unverified'?'pending':f.mode==='unavailable'?'unavailable':stale?'stale':'reachable',...(f.mode==='unavailable'?{error:{code:'node_read_unavailable',message:'Current destination information is unavailable; cached observations do not authorize actions.'}}:{})};
 if(!validHostID(entry.host)||!validCustomName(entry.display_name)||entry.metadata&&entry.metadata.host!==entry.host)throw Error('Invalid synthetic node binding');return entry;
};
f.report=host=>({schema:1,host,owner_declared:{profile:'stable',revision:9007199254740991,updated_at:stamp},observed_at:stamp});
f.inspection=host=>({schema:1,host,...(!['unverified','unavailable'].includes(f.mode)?{owner_declared:f.report(host).owner_declared,last_success_at:f.mode==='stale'?oldStamp:stamp}:{}),declaration_state:['unverified','unavailable'].includes(f.mode)?'unsupported':f.mode==='stale'?'stale':'reported',connection_state:f.mode==='unverified'?'identity_unavailable':f.mode==='unavailable'?'offline':'reachable',resource_freshness:{cpu:f.mode==='stale'?'stale':'unknown',memory:f.mode==='stale'?'stale':'unknown',disk:f.mode==='stale'?'stale':'unknown'}});
if(!availabilityFacts({host:f.target,report:f.report(f.target),profileState:'reported',connection:'reachable',resourceState:'unknown'})[0][1].includes('Stable'))throw Error('Synthetic availability report violates current UI validation');
f.contexts=Array.from({length:4},(_,i)=>({name:'synthetic-context-'+i+'-'+ 'segment-'.repeat(16)+'inventory',cluster_name:'synthetic-cluster-'+i+'-'+ 'segment-'.repeat(12)+'inventory',auth_info_name:'synthetic-read-only-identity-'+i,namespace:dns('synthetic-namespace',i),current:i===0}));
const kubeNode=i=>({name:dns('worker',i)+'.'+dns('zone',i)+'.example',uid:uid(i+1),os:'linux',arch:i%2?'amd64':'arm64',ready:i!==4,unschedulable:i===5,taints:i===3?['dedicated=other:NoSchedule']:[],allocatable_cpu:'180000000000n',allocatable_memory:'535328752Ki',allocatable_ephemeral:'20Gi',capacity:{cpu:'192',memory:'512Gi','ephemeral-storage':'25Gi','vendor.example/exact-units':'9007199254740993'},allocatable:{cpu:'180000000000n',memory:'535328752Ki','ephemeral-storage':'20Gi','vendor.example/exact-units':'9007199254740993',...Object.fromEntries(Array.from({length:66},(_,n)=>['vendor.example/resource-'+n,String(n+1)]))},zone:dns('synthetic-zone',i),instance_type:dns('synthetic-machine',i),conditions:['Ready='+(i===4?'False':'True'),'MemoryPressure=False','DiskPressure=False']});
const namespaceUIDs=new Map();
const namespaceUID=(context,name)=>{const key=context+'\0'+name;if(!namespaceUIDs.has(key))namespaceUIDs.set(key,uid(100+namespaceUIDs.size));return namespaceUIDs.get(key);};
f.kube=params=>{
 const choice=f.contexts.find(c=>c.name===params.context);if(!choice)throw Error('Unknown synthetic context');const n=f.contexts.indexOf(choice),nodes=f.kubeMode==='empty'?[]:Array.from({length:6},(_,i)=>kubeNode(i));
 const query=params.query.trim().toLowerCase(),matches=nodes.sort((a,b)=>a.name.localeCompare(b.name)).filter(x=>(x.name+' '+x.arch+' '+x.zone+' '+x.instance_type).toLowerCase().includes(query));
 return{cluster:{context:choice.name,server:'https://'+dns('synthetic-control',n)+'.example:6443',ca_sha256:digest(80+n),auth_subject:'system:serviceaccount:'+choice.namespace+':'+dns('inventory-reader',n),auth_groups:['system:serviceaccounts','system:authenticated']},namespace:{name:params.namespace||choice.namespace,uid:namespaceUID(choice.name,params.namespace||choice.namespace)},nodes:matches.slice(0,params.limit),inventory:{total:nodes.length,matched:matches.length,truncated:matches.length>params.limit}};
};
if(formatKubeQuantity('180000000000n','cpu')!=='180 cores')throw Error('Synthetic quantity rejected by current formatter');
f.refresh=()=>f.connections.at(-1).options.onEvent({sequence:++f.cursor,type:'host.metadata.changed',entity:source,at:stamp,data:{detail:'Synthetic registry read refresh'}});
f.release=()=>{f.readState='ready';f.pendingOps=[];for(const p of f.pending.splice(0))p.resolve();};
f.disconnect=()=>{f.holdReconnect=true;f.connections.at(-1).socket.close();};f.reconnect=()=>{f.holdReconnect=false;f.resolveReconnect?.();f.resolveReconnect=null;};
f.capture=async()=>{f.release();f.reconnect();await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));if(f.active!==0||f.pending.length)throw Error('Synthetic reads are still active');return{calls:f.calls,forbidden:f.forbidden,pendingReads:f.pending.length,activeReads:f.active,clipboardAttempts:window.__clipboardAttempts};};
const exact=(q,keys)=>q!==null&&typeof q==='object'&&!Array.isArray(q)&&Object.keys(q).sort().join(',')===[...keys].sort().join(',');
const cursor=x=>typeof x==='bigint'?x>=0n&&x<=int64:Number.isSafeInteger(x)&&x>=0;
export class Backend{
 constructor(token,options){this.options=options;this.socket={readyState:1,close:()=>{if(this.socket.readyState===3)return;this.socket.readyState=3;options.onClose({code:1006});}};this.ready=f.holdReconnect?new Promise(r=>f.resolveReconnect=r):Promise.resolve();f.connections.push(this);}
 disconnect(){this.socket.readyState=3;}
 async call(operation,params={}){
  f.calls.push({operation,params});f.active++;
  try{
   let valid=false;
   if(['status','messages','agents','node_list','mesh_operator_list','kube_contexts','mesh_operator_watch'].includes(operation))valid=exact(params,[]);
   if(operation==='pool_list')valid=exact(params,['all'])&&params.all===true;
   if(['subscribe','events'].includes(operation))valid=exact(params,['after'])&&cursor(params.after);
   if(operation==='monitor_subscribe')valid=exact(params,['epoch','after'])&&typeof params.epoch==='string'&&params.epoch.length<=64&&cursor(params.after);
   if(['node_inspect','node_availability_inspect_v1','node_availability_read_v1'].includes(operation))valid=exact(params,['host'])&&validHostID(params.host)&&f.hosts.includes(params.host);
   if(operation==='kube_nodes')valid=exact(params,['context','namespace','query','limit'])&&typeof params.context==='string'&&params.context.length>0&&params.context.length<=253&&f.contexts.some(c=>c.name===params.context)&&typeof params.namespace==='string'&&(params.namespace===''||params.namespace.length<=63&&/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(params.namespace))&&typeof params.query==='string'&&new TextEncoder().encode(params.query).length<=128&&params.limit===50;
   if(!valid){f.forbidden.push({operation,params});throw Error('Refused operation or unexpected read arguments: '+operation);}
   if(f.readState==='pending'&&f.pendingOps.includes(operation))await new Promise(resolve=>f.pending.push({operation,params:structuredClone(params),resolve}));
   if(f.readState==='error'&&f.pendingOps.includes(operation))throw Error(operation.startsWith('kube_')?'Kubernetes inventory unavailable; no empty inventory was established':'Current destination information is unavailable; no local data is substituted.');
   if(operation==='status')return{host:source,owner:'1000',owner_username:'synthetic',display_name:'synthetic-source',cursor:f.cursor,draining:false,capacity:{cpu:8,memory_bytes:34359738368,disk_bytes:549755813888},reserved:{cpu:0,memory_bytes:0,disk_bytes:0},leases:[],resources:[],capabilities:['durable-events','node-availability-v1'],infrastructure_ports:[],cpu_budget:null,memory_budget:null,disk_budget:null};
   if(operation==='node_list')return{source_host:source,mesh_state:'active',registry_at:stamp,nodes:f.mode==='empty'?[]:f.hosts.map(f.node)};
   if(operation==='node_inspect'){const value=f.node(params.host);f.lastNode=structuredClone(value);return value;}
   if(operation==='node_availability_inspect_v1')return f.inspection(params.host);
   if(operation==='node_availability_read_v1'){if(['unverified','unavailable'].includes(f.mode))throw Error('Current report unavailable');return f.report(params.host);}
   if(operation==='kube_contexts')return f.kubeMode==='no-contexts'?[]:structuredClone(f.contexts);
   if(operation==='kube_nodes'){const value=f.kube(params);f.lastKube=structuredClone(value);return value;}
   if(operation==='monitor_subscribe')return{host:source,epoch:'e'.repeat(32),cursor:0,oldest:0,gap:false,cadence_ns:2000000000,filesystem_cadence_ns:5000000000,stale_after_ns:10000000000,age_ns:0,stale:false,latest:null,history:[]};
   if(['subscribe','mesh_operator_watch'].includes(operation))return{subscription:'synthetic-read-only'};
   return[];
  }finally{f.active--;}
 }
}
